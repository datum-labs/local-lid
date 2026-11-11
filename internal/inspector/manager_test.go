package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datum-labs/local-lid/internal/connectd"
)

// fakeDaemon is a tiny stand-in for the Connect daemon's /v1 service API,
// with switches for the slow and half-finished behaviour the real daemon
// can show (it saves intent early, then does control-plane work).
type fakeDaemon struct {
	mu       sync.Mutex
	next     int
	services []*connectd.Service

	deleteLateReply time.Duration // delete happens at once, the reply comes this much later
	deletePartial   bool          // delete only stops the service, then fails
	createSaveFail  bool          // create saves the service, then fails
	createFail      bool          // create fails without saving
	createFailOnce  bool          // like createFail, for the first create only
}

func (f *fakeDaemon) find(id string) int {
	for i, s := range f.services {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func port(endpoint string) string { _, p, _ := net.SplitHostPort(endpoint); return p }

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, msg string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v1/status":
		f.mu.Lock()
		svcs := make([]connectd.Service, 0, len(f.services))
		for _, s := range f.services {
			svcs = append(svcs, *s)
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"running": true, "services": svcs, "dials": []any{}})

	case r.Method == http.MethodPost && path == "/v1/services":
		var req connectd.ServiceRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		if f.createFail || f.createFailOnce {
			f.createFailOnce = false
			f.mu.Unlock()
			fail(http.StatusBadGateway, "control plane unavailable")
			return
		}
		for _, s := range f.services {
			if port(s.Endpoint) == port(req.Endpoint) {
				f.mu.Unlock()
				fail(http.StatusConflict, "port in use")
				return
			}
		}
		f.next++
		s := &connectd.Service{ID: fmt.Sprintf("service-%d", f.next), Endpoint: req.Endpoint, Protocol: "tcp", Allow: req.Allow, DesiredActive: true, Running: true, Ready: true}
		f.services = append(f.services, s)
		saveFail := f.createSaveFail
		f.mu.Unlock()
		if saveFail {
			fail(http.StatusBadGateway, "saved, then reconcile failed")
			return
		}
		_ = json.NewEncoder(w).Encode(s)

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/v1/services/"):
		id := strings.TrimPrefix(path, "/v1/services/")
		f.mu.Lock()
		i := f.find(id)
		if i < 0 {
			f.mu.Unlock()
			fail(http.StatusNotFound, "no such service")
			return
		}
		if f.deletePartial {
			f.services[i].DesiredActive = false
			f.mu.Unlock()
			fail(http.StatusBadGateway, "cloud delete failed")
			return
		}
		f.services = append(f.services[:i], f.services[i+1:]...)
		late := f.deleteLateReply
		f.mu.Unlock()
		time.Sleep(late)
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/resume"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/services/"), "/resume")
		f.mu.Lock()
		defer f.mu.Unlock()
		if i := f.find(id); i >= 0 {
			f.services[i].DesiredActive = true
			_ = json.NewEncoder(w).Encode(f.services[i])
			return
		}
		fail(http.StatusNotFound, "no such service")
	default:
		fail(http.StatusNotFound, "unknown route")
	}
}

func (f *fakeDaemon) at(endpoint string) *connectd.Service {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.services {
		if s.Endpoint == endpoint {
			cp := *s
			return &cp
		}
	}
	return nil
}

// freeDualPort finds a port free on both 127.0.0.1 and [::1].
func freeDualPort(t *testing.T) string {
	for i := 0; i < 20; i++ {
		l4, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skip("no IPv4 loopback")
		}
		p := port(l4.Addr().String())
		l6, err := net.Listen("tcp", net.JoinHostPort("::1", p))
		l4.Close()
		if err == nil {
			l6.Close()
			return p
		}
	}
	t.Skip("no port free on both 127.0.0.1 and [::1] (no IPv6 loopback?)")
	return ""
}

type rig struct {
	f      *fakeDaemon
	m      *Manager
	orig   string
	listen string
}

func newRig(t *testing.T) *rig {
	f := &fakeDaemon{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	m := NewManager(filepath.Join(t.TempDir(), "inspect.json"), func(context.Context, string) (*connectd.Client, func(), error) {
		return connectd.New(srv.URL, "test"), func() {}, nil
	})
	p := freeDualPort(t)
	r := &rig{f: f, m: m, orig: "127.0.0.1:" + p, listen: "[::1]:" + p}
	f.services = []*connectd.Service{{ID: "service-orig", Endpoint: r.orig, Protocol: "tcp", Allow: []string{"peer-key"}, DesiredActive: true, Running: true, Ready: true}}
	t.Cleanup(func() {
		for _, h := range m.hops {
			h.Stop()
		}
	})
	return r
}

// consistent is the property every scenario must end in: either the service
// is inspected and Local Lid has a record of it, or the original is back and
// active and nothing is recorded. Never a half-state.
func (r *rig) consistent(t *testing.T) (inspected bool) {
	t.Helper()
	views := r.m.Views()
	hopSvc, origSvc := r.f.at(r.listen), r.f.at(r.orig)
	switch {
	case len(views) == 1 && hopSvc != nil && hopSvc.DesiredActive && origSvc == nil && views[0].ServiceID == hopSvc.ID:
		return true
	case len(views) == 0 && origSvc != nil && origSvc.DesiredActive && hopSvc == nil:
		return false
	}
	t.Fatalf("inconsistent: records=%d hopService=%+v originalService=%+v", len(views), hopSvc, origSvc)
	return false
}

func TestEnableHappyPath(t *testing.T) {
	r := newRig(t)
	if _, err := r.m.Enable(context.Background(), "p", r.orig); err != nil {
		t.Fatal(err)
	}
	if !r.consistent(t) {
		t.Fatal("expected inspected")
	}
	if _, err := r.m.Disable(context.Background(), r.orig); err != nil {
		t.Fatal(err)
	}
	if r.consistent(t) {
		t.Fatal("expected restored")
	}
}

// The daemon deletes, but replies after the request has given up. Before the
// fix this said "nothing changed" while the service was actually gone.
func TestEnableDeleteRepliesLate(t *testing.T) {
	r := newRig(t)
	r.f.deleteLateReply = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := r.m.Enable(ctx, "p", r.orig)
	if err != nil {
		t.Logf("Enable: %v", err)
	}
	r.consistent(t)
}

// Delete stops the service but fails before removing it: it must be resumed.
func TestEnableDeleteHalfDone(t *testing.T) {
	r := newRig(t)
	r.f.deletePartial = true
	_, err := r.m.Enable(context.Background(), "p", r.orig)
	if err == nil || !strings.Contains(err.Error(), "resumed") {
		t.Fatalf("want a 'resumed' error, got %v", err)
	}
	if r.consistent(t) {
		t.Fatal("expected the original, active")
	}
}

// Create saves the inspector service, then fails. Before the fix the rollback
// hit a 409 and left the service pointing at a stopped inspector.
func TestEnableCreateSavedThenFailed(t *testing.T) {
	r := newRig(t)
	r.f.createSaveFail = true
	if _, err := r.m.Enable(context.Background(), "p", r.orig); err != nil {
		t.Fatalf("a create the daemon saved should count as done: %v", err)
	}
	if !r.consistent(t) {
		t.Fatal("expected inspected")
	}
}

// Create fails without saving: the original must come back.
func TestEnableCreateFailedRollsBack(t *testing.T) {
	r := newRig(t)
	r.f.createFailOnce = true
	_, err := r.m.Enable(context.Background(), "p", r.orig)
	if err == nil || !strings.Contains(err.Error(), "original service restored") {
		t.Fatalf("want rollback, got %v", err)
	}
	if r.consistent(t) {
		t.Fatal("expected the original back")
	}
}

// Restore's delete replies late: the original must still come back.
func TestDisableDeleteRepliesLate(t *testing.T) {
	r := newRig(t)
	if _, err := r.m.Enable(context.Background(), "p", r.orig); err != nil {
		t.Fatal(err)
	}
	r.f.deleteLateReply = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := r.m.Disable(ctx, r.orig); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if r.consistent(t) {
		t.Fatal("expected restored")
	}
}
