package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalhostOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := localhostOnly(ok)
	for host, want := range map[string]int{
		"127.0.0.1:47790":       200,
		"localhost:47790":       200,
		"[::1]:47790":           200,
		"localhost":             200,
		"evil.example:47790":    403, // DNS rebinding
		"192.168.1.5:47790":     403,
		"127.0.0.1.nip.io:4779": 403,
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/info", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %s: got %d, want %d", host, w.Code, want)
		}
	}
}
