// Package inspector is Local Lid's L7 inspector: an HTTP hop that sits on
// the same port as a service's real target, at the other loopback address,
// and records each request/response pair so the dashboard can show and
// replay them.
package inspector

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// MaxExchanges kept per inspected service; oldest are dropped first.
	MaxExchanges = 200
	// MaxBody bytes captured per request or response body. Traffic beyond
	// this still flows; it just isn't recorded.
	MaxBody = 64 << 10
)

// Header pairs keep order and repeats, as the old dashboard expects.
type Header [2]string

type Message struct {
	Headers       []Header `json:"headers"`
	Body          string   `json:"body"`
	BodyTruncated bool     `json:"body_truncated"`
}

type Summary struct {
	ID             string `json:"id"`
	TimestampMs    int64  `json:"timestamp_unix_ms"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	ResponseStatus *int   `json:"response_status"`
	DurationMs     *int64 `json:"duration_ms"`
}

type Exchange struct {
	Summary
	Host     string   `json:"host"`
	Request  Message  `json:"request"`
	Response *Message `json:"response"`
	Error    string   `json:"error,omitempty"`
}

// Store is the in-memory ring of exchanges for one inspected service.
type Store struct {
	mu        sync.Mutex
	next      int
	exchanges []*Exchange
}

func (s *Store) add(e *Exchange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	e.ID = "ex-" + strconv.Itoa(s.next)
	s.exchanges = append(s.exchanges, e)
	if len(s.exchanges) > MaxExchanges {
		s.exchanges = s.exchanges[len(s.exchanges)-MaxExchanges:]
	}
}

func (s *Store) update(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func (s *Store) Summaries() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Summary, 0, len(s.exchanges))
	for _, e := range s.exchanges {
		out = append(out, e.Summary)
	}
	return out
}

func (s *Store) Get(id string) (Exchange, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.exchanges {
		if e.ID == id {
			cp := *e
			if e.Response != nil {
				r := *e.Response
				cp.Response = &r
			}
			return cp, true
		}
	}
	return Exchange{}, false
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.exchanges)
}

// Redacted replaces credential header values at capture time, so the real
// value is never stored, not even in memory (same list and reasoning as the
// old daemon's inspector). Replay leaves these headers out.
const Redacted = "[redacted]"

var sensitiveHeaders = map[string]bool{
	"Authorization": true, "Proxy-Authorization": true, "Cookie": true,
	"Set-Cookie": true, "X-Api-Key": true, "X-Auth-Token": true,
}

func headersOf(h http.Header) []Header {
	out := []Header{}
	for k, vs := range h {
		for _, v := range vs {
			if sensitiveHeaders[http.CanonicalHeaderKey(k)] {
				v = Redacted
			}
			out = append(out, Header{k, v})
		}
	}
	return out
}

// capBuffer keeps the first MaxBody bytes written to it and remembers
// whether more arrived.
type capBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	room := MaxBody - c.buf.Len()
	if room <= 0 {
		c.truncated = c.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// teeBody records what flows through a body while passing it on unchanged,
// and calls done once when the body is closed.
type teeBody struct {
	io.ReadCloser
	cap  *capBuffer
	once sync.Once
	done func()
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		_, _ = t.cap.Write(p[:n])
	}
	return n, err
}

func (t *teeBody) Close() error {
	err := t.ReadCloser.Close()
	t.once.Do(func() {
		if t.done != nil {
			t.done()
		}
	})
	return err
}

func now() int64 { return time.Now().UnixMilli() }
