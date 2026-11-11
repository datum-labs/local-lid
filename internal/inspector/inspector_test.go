package inspector

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHopAddresses(t *testing.T) {
	cases := []struct{ endpoint, listen, target, err string }{
		{"127.0.0.1:8080", "[::1]:8080", "127.0.0.1:8080", ""},
		{"localhost:8080", "[::1]:8080", "127.0.0.1:8080", ""}, // pinned, so the hop can't forward to itself
		{"[::1]:9000", "127.0.0.1:9000", "[::1]:9000", ""},
		{"192.168.1.20:80", "127.0.0.1:80", "192.168.1.20:80", ""},
		{"0.0.0.0:80", "", "", "wildcard"},
		{"myhost:80", "", "", "name"},
		{"nonsense", "", "", "host:port"},
	}
	for _, c := range cases {
		l, tg, err := HopAddresses(c.endpoint)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: want error containing %q, got %v", c.endpoint, c.err, err)
			}
			continue
		}
		if err != nil || l != c.listen || tg != c.target {
			t.Errorf("%s: got (%s, %s, %v), want (%s, %s)", c.endpoint, l, tg, err, c.listen, c.target)
		}
	}
}

func TestCapBufferKeepsFirstMaxBody(t *testing.T) {
	c := &capBuffer{}
	_, _ = c.Write([]byte(strings.Repeat("a", MaxBody-10)))
	_, _ = c.Write([]byte(strings.Repeat("b", 20)))
	if c.buf.Len() != MaxBody || !c.truncated {
		t.Fatalf("len=%d truncated=%v", c.buf.Len(), c.truncated)
	}
}

func TestStoreKeepsLastMax(t *testing.T) {
	s := &Store{}
	for i := 0; i < MaxExchanges+5; i++ {
		s.add(&Exchange{Summary: Summary{Method: "GET"}})
	}
	sum := s.Summaries()
	if len(sum) != MaxExchanges || sum[0].ID != "ex-6" {
		t.Fatalf("len=%d first=%s", len(sum), sum[0].ID)
	}
}

// End to end: a request through the hop reaches the target, is recorded
// with both bodies, and replays straight to the target.
func TestHopCapturesAndReplays(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Host", r.Host)
		fmt.Fprintf(w, "echo:%s:%s", r.URL.Path, b)
	}))
	defer target.Close()

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	h := &Hop{Listen: addr, Target: strings.TrimPrefix(target.URL, "http://")}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/path?q=1", strings.NewReader("hello"))
	req.Host = "app.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "echo:/path:hello" || resp.Header.Get("X-Seen-Host") != "app.example" {
		t.Fatalf("through hop: %q host=%q", body, resp.Header.Get("X-Seen-Host"))
	}

	// The exchange is finalized when the response body closes.
	var ex Exchange
	for i := 0; i < 50; i++ {
		if e, ok := h.Store.Get("ex-1"); ok && e.Response != nil {
			ex = e
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ex.Response == nil || ex.Request.Body != "hello" || ex.Response.Body != "echo:/path:hello" ||
		ex.Path != "/path?q=1" || ex.Host != "app.example" || *ex.ResponseStatus != 200 {
		t.Fatalf("recorded exchange wrong: %+v", ex)
	}

	res, err := Replay(context.Background(), h.Target, ex)
	if err != nil || res.Status != 200 || res.Body != "echo:/path:hello" {
		t.Fatalf("replay: %+v %v", res, err)
	}
	if hits.Load() != 2 || h.Store.Len() != 1 {
		t.Fatalf("hits=%d recorded=%d (replay must reach the target but not be recorded)", hits.Load(), h.Store.Len())
	}
}

func TestReplayRefusesTruncatedBody(t *testing.T) {
	ex := Exchange{Summary: Summary{Method: "POST", Path: "/"}, Request: Message{BodyTruncated: true}}
	if _, err := Replay(context.Background(), "127.0.0.1:1", ex); err == nil {
		t.Fatal("want error for truncated body")
	}
}

func TestCredentialHeadersRedactedAtCapture(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("Cookie", "session=abc")
	h.Set("X-Request-Id", "keep-me")
	got := map[string]string{}
	for _, p := range headersOf(h) {
		got[p[0]] = p[1]
	}
	if got["Authorization"] != Redacted || got["Cookie"] != Redacted || got["X-Request-Id"] != "keep-me" {
		t.Fatalf("redaction wrong: %v", got)
	}
}

// A peer controls the request-target. An opaque form like "http:@evil/x"
// is recorded as "@evil/x", which must not turn the replay URL's host into
// "evil" (target@evil). Replay may refuse, but must never reach another host.
func TestReplayCannotBeRedirectedByRequestTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { evilHits.Add(1) }))
	defer evil.Close()
	host := strings.TrimPrefix(target.URL, "http://")
	evilHost := strings.TrimPrefix(evil.URL, "http://")
	for _, p := range []string{"@" + evilHost + "/x", "http://" + evilHost + "/x", "//" + evilHost + "/x", "x@" + evilHost} {
		_, _ = Replay(context.Background(), host, Exchange{Summary: Summary{Method: "GET", Path: p}})
	}
	if n := evilHits.Load(); n != 0 {
		t.Fatalf("replay reached a host other than the target %d time(s)", n)
	}
}
