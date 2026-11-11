package inspector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/datum-labs/local-lid/internal/connectd"
)

// Spec is everything needed to recreate a service exactly as it was.
type Spec struct {
	Endpoint string   `json:"endpoint"`
	Protocol string   `json:"protocol"`
	Public   bool     `json:"public"`
	Hostname *string  `json:"hostname,omitempty"`
	Allow    []string `json:"allow"`
}

func specOf(s connectd.Service) Spec {
	return Spec{Endpoint: s.Endpoint, Protocol: s.Protocol, Public: s.Public, Hostname: s.Hostname, Allow: s.Allow}
}

func (s Spec) request(endpoint string) connectd.ServiceRequest {
	return connectd.ServiceRequest{Endpoint: endpoint, Protocol: s.Protocol, Public: s.Public, Hostname: s.Hostname, Allow: s.Allow}
}

// Record is one inspected service, persisted so the original can be put
// back even after a crash.
type Record struct {
	Project    string `json:"project"`
	OriginalID string `json:"original_id"`
	Original   Spec   `json:"original"`
	ServiceID  string `json:"service_id"` // the service currently pointing at the hop
	Listen     string `json:"listen"`
	Target     string `json:"target"`
	EnabledAt  int64  `json:"enabled_at_unix_ms"`
}

// View is what the dashboard sees.
type View struct {
	Record
	Exchanges int  `json:"exchanges"`
	Running   bool `json:"running"`
}

type Manager struct {
	// OnRestore, if set, is told about restores that didn't come from an
	// explicit disable (normal shutdown, or startup after a crash).
	OnRestore func(endpoint, why string)
	mu        sync.Mutex
	path      string
	// client returns an Operate-token client for one operation, and a
	// release func that revokes that token when the operation is done.
	client  func(ctx context.Context, project string) (*connectd.Client, func(), error)
	records map[string]*Record // by ServiceID
	hops    map[string]*Hop    // by ServiceID
}

func NewManager(statePath string, client func(ctx context.Context, project string) (*connectd.Client, func(), error)) *Manager {
	return &Manager{path: statePath, client: client, records: map[string]*Record{}, hops: map[string]*Hop{}}
}

// StatePath is <dir of config>/inspect.json.
func StatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "inspect.json")
}

func (m *Manager) load() ([]*Record, error) {
	raw, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []*Record
	return recs, json.Unmarshal(raw, &recs)
}

func (m *Manager) saveLocked() error {
	recs := make([]*Record, 0, len(m.records))
	for _, r := range m.records {
		recs = append(recs, r)
	}
	raw, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Manager) Views() []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]View, 0, len(m.records))
	for id, r := range m.records {
		v := View{Record: *r}
		if h := m.hops[id]; h != nil {
			v.Exchanges = h.Store.Len()
			v.Running = true
		}
		out = append(out, v)
	}
	return out
}

// Hop returns the running hop for an inspected service id.
func (m *Manager) Hop(serviceID string) (*Hop, *Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hops[serviceID], m.records[serviceID]
}

// recreate retries a create while the Connect daemon still reserves the port
// (409) for the service that was just deleted.
func recreate(ctx context.Context, c *connectd.Client, project string, req connectd.ServiceRequest) (*connectd.Service, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		s, err := c.CreateService(ctx, project, req)
		if err == nil {
			return s, nil
		}
		if !connectd.IsStatus(err, http.StatusConflict) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// statusNow reads the daemon's current state on a fresh context, so the check
// still works after the operation's own context has run out.
func statusNow(c *connectd.Client, project string) (*connectd.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connectd.ReadTimeout)
	defer cancel()
	return c.Status(ctx, project)
}

func byID(st *connectd.Status, id string) *connectd.Service {
	if st == nil {
		return nil
	}
	for i := range st.Services {
		if st.Services[i].ID == id {
			return &st.Services[i]
		}
	}
	return nil
}

func byEndpoint(st *connectd.Status, endpoint string) *connectd.Service {
	if st == nil {
		return nil
	}
	for i := range st.Services {
		if st.Services[i].Endpoint == endpoint {
			return &st.Services[i]
		}
	}
	return nil
}

// stillTime returns ctx, or a fresh one if ctx has already run out, so a
// slow earlier step can't stop the swap from finishing or being undone.
func stillTime(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), 2*connectd.WriteTimeout)
}

// rollback puts the original service back after a failed swap. A
// half-created inspector service would still hold the port and turn the
// re-create into a 409, so it's removed first.
func rollback(c *connectd.Client, project string, orig Spec, listen string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*connectd.WriteTimeout)
	defer cancel()
	if st, err := c.Status(ctx, project); err == nil {
		if s := byEndpoint(st, orig.Endpoint); s != nil && s.DesiredActive {
			return nil // already back
		}
		if s := byEndpoint(st, listen); s != nil {
			if err := c.DeleteService(ctx, project, s.ID); err != nil && !connectd.IsStatus(err, http.StatusNotFound) {
				return fmt.Errorf("remove the half-created inspector service %s: %w", s.ID, err)
			}
		}
	}
	if _, err := recreate(ctx, c, project, orig.request(orig.Endpoint)); err != nil {
		if st, sErr := statusNow(c, project); sErr == nil && byEndpoint(st, orig.Endpoint) != nil {
			return nil // saved after all
		}
		return err
	}
	return nil
}

// Enable puts an inspector in front of a service. selector is a service id
// or its endpoint.
func (m *Manager) Enable(ctx context.Context, project, selector string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Checked before the daemon lookup: once inspected, the service's
	// endpoint is the hop's, so the original endpoint no longer matches.
	if r := m.findLocked(selector); r != nil {
		return nil, fmt.Errorf("%s is already inspected (service %s, through %s)", r.Original.Endpoint, r.ServiceID, r.Listen)
	}
	c, release, err := m.client(ctx, project)
	if err != nil {
		return nil, err
	}
	defer release()
	st, err := c.Status(ctx, project)
	if err != nil {
		return nil, err
	}
	if !st.Running {
		return nil, fmt.Errorf("project %s isn't running (%s); not touching its services", project, deref(st.LastError, "no error reported"))
	}
	var svc *connectd.Service
	for i := range st.Services {
		s := &st.Services[i]
		if s.ID == selector || s.Endpoint == selector {
			svc = s
			break
		}
	}
	if svc == nil {
		return nil, fmt.Errorf("no service %q in project %s", selector, project)
	}
	if r := m.records[svc.ID]; r != nil {
		return nil, fmt.Errorf("service %s is already inspected (original endpoint %s)", svc.ID, r.Original.Endpoint)
	}
	if svc.Public {
		return nil, errors.New("inspecting public services isn't supported yet: staging can't create public services, so the swap can't be verified (see SPIKE.md)")
	}
	if svc.Protocol == "udp" {
		return nil, errors.New("the inspector is HTTP-only; UDP services can't be inspected")
	}
	listen, target, err := HopAddresses(svc.Endpoint)
	if err != nil {
		return nil, err
	}

	// 1. Bind the hop before touching the service: a port clash fails here,
	//    with nothing changed.
	hop := &Hop{Listen: listen, Target: target}
	if err := hop.Start(); err != nil {
		return nil, err
	}
	orig := specOf(*svc)
	rec := &Record{Project: project, OriginalID: svc.ID, Original: orig, Listen: listen, Target: target, EnabledAt: time.Now().UnixMilli()}

	// 2. Delete, then create at the hop. Create-first is a 409: one service
	//    per port.
	//
	// A failed or timed-out call doesn't mean the daemon did nothing: it
	// saves intent early and may finish (or half-finish) after we give up.
	// So after any error, read its status and act on what actually happened.
	if err := c.DeleteService(ctx, project, svc.ID); err != nil && !connectd.IsStatus(err, http.StatusNotFound) {
		st2, sErr := statusNow(c, project)
		cur := byID(st2, svc.ID)
		switch {
		case sErr != nil:
			hop.Stop()
			return nil, fmt.Errorf("delete %s failed (%v), and the daemon can't be read to see what happened (%v); check datumctl connect status", svc.ID, err, sErr)
		case cur != nil && cur.DesiredActive:
			hop.Stop()
			return nil, fmt.Errorf("delete %s: %w (nothing changed)", svc.ID, err)
		case cur != nil:
			// Half-deleted: still saved, but stopped. Put it back as it was.
			rctx, cancel := context.WithTimeout(context.Background(), connectd.WriteTimeout)
			rErr := c.ResumeService(rctx, project, svc.ID)
			cancel()
			hop.Stop()
			if rErr != nil {
				return nil, fmt.Errorf("delete %s failed part-way (%v) and left it stopped; resuming it also failed (%v); check datumctl connect status", svc.ID, err, rErr)
			}
			return nil, fmt.Errorf("delete %s failed part-way (%v); the service was resumed, so nothing changed", svc.ID, err)
		}
		// Gone: the delete went through after all. Carry on.
	}
	cctx, ccancel := stillTime(ctx)
	created, err := recreate(cctx, c, project, orig.request(listen))
	ccancel()
	if err != nil {
		// The daemon may have saved the inspector service before failing.
		if st2, sErr := statusNow(c, project); sErr == nil {
			if s := byEndpoint(st2, listen); s != nil {
				created, err = s, nil
			}
		}
	}
	if err != nil {
		// 3. Roll back to the original.
		rbErr := rollback(c, project, orig, listen)
		hop.Stop()
		if rbErr != nil {
			return nil, fmt.Errorf("create at inspector failed (%v) AND rollback failed (%v); check datumctl connect status for %s", err, rbErr, orig.Endpoint)
		}
		return nil, fmt.Errorf("create at inspector failed, original service restored: %w", err)
	}
	rec.ServiceID = created.ID
	m.records[created.ID] = rec
	m.hops[created.ID] = hop
	if err := m.saveLocked(); err != nil {
		log.Printf("inspector: couldn't save state (restore on restart may not work): %v", err)
	}
	return rec, nil
}

// Disable puts the original service back. selector is the current service
// id, the original id, or the original endpoint.
func (m *Manager) Disable(ctx context.Context, selector string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.findLocked(selector)
	if rec == nil {
		return nil, fmt.Errorf("%q isn't inspected", selector)
	}
	if err := m.restoreLocked(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (m *Manager) findLocked(selector string) *Record {
	for _, r := range m.records {
		if r.ServiceID == selector || r.OriginalID == selector || r.Original.Endpoint == selector {
			return r
		}
	}
	return nil
}

func (m *Manager) restoreLocked(ctx context.Context, rec *Record) error {
	c, release, err := m.client(ctx, rec.Project)
	if err != nil {
		return err
	}
	defer release()
	if err := c.DeleteService(ctx, rec.Project, rec.ServiceID); err != nil && !connectd.IsStatus(err, http.StatusNotFound) {
		// Check what the daemon actually did before giving up.
		st, sErr := statusNow(c, rec.Project)
		cur := byID(st, rec.ServiceID)
		if sErr != nil || (cur != nil && cur.DesiredActive) {
			return fmt.Errorf("delete inspected service %s: %w (state kept; retry with local-lid inspect disable %s)", rec.ServiceID, err, rec.Original.Endpoint)
		}
		if cur != nil {
			// Stopped but still saved, so it still holds the port: try once more.
			dctx, cancel := context.WithTimeout(context.Background(), connectd.WriteTimeout)
			dErr := c.DeleteService(dctx, rec.Project, rec.ServiceID)
			cancel()
			if dErr != nil && !connectd.IsStatus(dErr, http.StatusNotFound) {
				return fmt.Errorf("delete inspected service %s: %w (state kept; retry with local-lid inspect disable %s)", rec.ServiceID, dErr, rec.Original.Endpoint)
			}
		}
	}
	rctx, rcancel := stillTime(ctx)
	_, err = recreate(rctx, c, rec.Project, rec.Original.request(rec.Original.Endpoint))
	rcancel()
	if err != nil {
		// The daemon may have saved it before failing.
		st, sErr := statusNow(c, rec.Project)
		if sErr != nil || byEndpoint(st, rec.Original.Endpoint) == nil {
			return fmt.Errorf("recreate original %s: %w (state kept; retry with local-lid inspect disable %s)", rec.Original.Endpoint, err, rec.Original.Endpoint)
		}
	}
	if h := m.hops[rec.ServiceID]; h != nil {
		h.Stop()
	}
	delete(m.hops, rec.ServiceID)
	delete(m.records, rec.ServiceID)
	return m.saveLocked()
}

// RestoreLeftovers runs at startup: any record still on disk means Local
// Lid stopped without restoring (a crash), so the service points at a hop
// that isn't running. Put the original back.
func (m *Manager) RestoreLeftovers(ctx context.Context) {
	recs, err := m.load()
	if err != nil {
		log.Printf("inspector: can't read %s: %v", m.path, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range recs {
		m.records[r.ServiceID] = r
	}
	for _, r := range recs {
		if err := m.restoreLocked(ctx, r); err != nil {
			log.Printf("inspector: restore %s failed: %v", r.Original.Endpoint, err)
		} else {
			log.Printf("inspector: restored %s (left inspected by a previous run)", r.Original.Endpoint)
			if m.OnRestore != nil {
				m.OnRestore(r.Original.Endpoint, "left inspected by a previous run (crash)")
			}
		}
	}
}

// RestoreAll runs on a normal shutdown, so stopping Local Lid never leaves
// a service pointing at a dead hop.
func (m *Manager) RestoreAll(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if err := m.restoreLocked(ctx, r); err != nil {
			log.Printf("inspector: restore %s failed: %v", r.Original.Endpoint, err)
		} else {
			log.Printf("inspector: restored %s", r.Original.Endpoint)
			if m.OnRestore != nil {
				m.OnRestore(r.Original.Endpoint, "Local Lid shutting down")
			}
		}
	}
}

func deref(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}
