package inspector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Hop is one running inspector: it listens on Listen and forwards every
// HTTP request to Target, recording it in Store.
type Hop struct {
	Listen string // e.g. "[::1]:8080"
	Target string // e.g. "127.0.0.1:8080"
	Store  *Store
	srv    *http.Server
	ln     net.Listener
}

// HopAddresses picks where the hop listens and where it forwards for a
// service endpoint. The hop must use the same port (remote peers dial the
// port), so it takes whichever loopback address the target doesn't.
// "localhost" is pinned to 127.0.0.1 so the hop can never forward to itself.
func HopAddresses(endpoint string) (listen, target string, err error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("endpoint %q is not host:port: %w", endpoint, err)
	}
	switch host {
	case "localhost", "127.0.0.1":
		return net.JoinHostPort("::1", port), net.JoinHostPort("127.0.0.1", port), nil
	case "::1":
		return net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", "", fmt.Errorf("endpoint host %q is a name; inspect needs an IP address or localhost so the hop can't loop", host)
	}
	if ip.IsUnspecified() {
		return "", "", fmt.Errorf("endpoint %q is a wildcard address; point the service at a concrete address first", endpoint)
	}
	// A non-loopback target (e.g. a LAN host) leaves both loopback
	// addresses free; use 127.0.0.1.
	return net.JoinHostPort("127.0.0.1", port), endpoint, nil
}

// Start binds the listener first, so a port clash fails before any service
// is touched.
func (h *Hop) Start() error {
	ln, err := net.Listen("tcp", h.Listen)
	if err != nil {
		return fmt.Errorf("inspector can't listen on %s (is something else, maybe a wildcard listener, already on that port?): %w", h.Listen, err)
	}
	h.ln = ln
	if h.Store == nil {
		h.Store = &Store{}
	}
	target := &url.URL{Scheme: "http", Host: h.Target}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // keep the caller's Host header
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if ex, ok := r.Context().Value(exKey{}).(*Exchange); ok {
				h.Store.update(func() { ex.Error = err.Error() })
			}
			http.Error(w, "Local Lid inspector: target unreachable: "+err.Error(), http.StatusBadGateway)
		},
		ModifyResponse: func(resp *http.Response) error {
			ex, ok := resp.Request.Context().Value(exKey{}).(*Exchange)
			if !ok {
				return nil
			}
			reqCap, _ := resp.Request.Context().Value(reqCapKey{}).(*capBuffer)
			started, _ := resp.Request.Context().Value(startKey{}).(time.Time)
			status := resp.StatusCode
			respCap := &capBuffer{}
			headers := headersOf(resp.Header)
			h.Store.update(func() { ex.ResponseStatus = &status })
			resp.Body = &teeBody{ReadCloser: resp.Body, cap: respCap, done: func() {
				h.Store.update(func() {
					ex.Response = &Message{Headers: headers, Body: respCap.buf.String(), BodyTruncated: respCap.truncated}
					if reqCap != nil {
						ex.Request.Body = reqCap.buf.String()
						ex.Request.BodyTruncated = reqCap.truncated
					}
					d := time.Since(started).Milliseconds()
					ex.DurationMs = &d
				})
			}}
			return nil
		},
	}
	h.srv = &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.serve(proxy, w, r) }),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		if err := h.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("inspector %s: %v", h.Listen, err)
		}
	}()
	return nil
}

type exKey struct{}
type reqCapKey struct{}
type startKey struct{}

func (h *Hop) serve(proxy *httputil.ReverseProxy, w http.ResponseWriter, r *http.Request) {
	ex := &Exchange{
		Summary: Summary{TimestampMs: now(), Method: r.Method, Path: r.URL.RequestURI()},
		Host:    r.Host,
		Request: Message{Headers: headersOf(r.Header)},
	}
	h.Store.add(ex)
	reqCap := &capBuffer{}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = &teeBody{ReadCloser: r.Body, cap: reqCap}
	}
	ctx := context.WithValue(r.Context(), exKey{}, ex)
	ctx = context.WithValue(ctx, reqCapKey{}, reqCap)
	ctx = context.WithValue(ctx, startKey{}, time.Now())
	proxy.ServeHTTP(w, r.WithContext(ctx))
	// Requests that ended without a response body close (errors) still get
	// their request body recorded.
	h.Store.update(func() {
		if ex.Request.Body == "" && reqCap.buf.Len() > 0 {
			ex.Request.Body = reqCap.buf.String()
			ex.Request.BodyTruncated = reqCap.truncated
		}
		if ex.ResponseStatus == nil && ex.Error != "" {
			s := http.StatusBadGateway
			ex.ResponseStatus = &s
		}
	})
}

func (h *Hop) Stop() {
	if h.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.srv.Shutdown(ctx)
}

// ReplayResult mirrors the old dashboard's replay response.
type ReplayResult struct {
	Status  int      `json:"status"`
	Headers []Header `json:"headers"`
	Body    string   `json:"body"`
}

var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Content-Length": true,
}

// Replay sends a recorded request again, straight to the real target (not
// through the hop, so the replay isn't recorded as new traffic).
func Replay(ctx context.Context, target string, ex Exchange) (*ReplayResult, error) {
	if ex.Request.BodyTruncated {
		return nil, errors.New("the request body was truncated when captured, so it can't be replayed faithfully")
	}
	// The path came from a peer. Build the URL from parts so it can never
	// change the host: joining strings let "@other-host/x" (recorded from a
	// request line like "GET http:@other-host/x") become target@other-host.
	p, err := url.ParseRequestURI(ex.Path)
	if err != nil || p.Opaque != "" || p.Host != "" || !strings.HasPrefix(ex.Path, "/") || strings.HasPrefix(ex.Path, "//") {
		return nil, fmt.Errorf("can't replay request target %q: only /path?query forms are replayed", ex.Path)
	}
	u := &url.URL{Scheme: "http", Host: target, Path: p.Path, RawPath: p.RawPath, RawQuery: p.RawQuery}
	req, err := http.NewRequestWithContext(ctx, ex.Method, u.String(), strings.NewReader(ex.Request.Body))
	if err != nil {
		return nil, err
	}
	if req.URL.Host != target {
		return nil, fmt.Errorf("replay URL host %q isn't the target %q", req.URL.Host, target)
	}
	for _, h := range ex.Request.Headers {
		if !hopByHop[http.CanonicalHeaderKey(h[0])] && h[1] != Redacted {
			req.Header.Add(h[0], h[1])
		}
	}
	req.Host = ex.Host
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c := &capBuffer{}
	_, _ = io.Copy(c, resp.Body)
	body := c.buf.String()
	if c.truncated {
		body += "\n…(truncated)"
	}
	return &ReplayResult{Status: resp.StatusCode, Headers: headersOf(resp.Header), Body: body}, nil
}
