// Package server is Local Lid's loopback HTTP server: the embedded React
// dashboard plus a small read-only /api the dashboard polls.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/datum-labs/local-lid/internal/config"
	"github.com/datum-labs/local-lid/internal/connectd"
	"github.com/datum-labs/local-lid/internal/inspector"
	"github.com/datum-labs/local-lid/internal/localstore"
)

type Server struct {
	Inspect *inspector.Manager
	Local   *localstore.Store
	cfg     *config.Config
	daemon  *connectd.Client
	web     fs.FS
	version string
	started time.Time
}

func New(cfg *config.Config, web fs.FS, version, inspectState string, local *localstore.Store) *Server {
	base := connectd.New(cfg.DaemonURL, "")
	// No Operate token is stored. Each write mints one with the daemon's
	// setup token, valid for 5 minutes (longer than the slowest swap), and revokes it when done. A stored
	// project-wide Operate token could rewrite or delete every service.
	operate := func(ctx context.Context, project string) (*connectd.Client, func(), error) {
		setup, err := readSetup(cfg.SetupTokenFile)
		if err != nil {
			return nil, nil, err
		}
		admin := base.WithToken(setup)
		ttl := uint64(300)
		tok, err := admin.MintToken(ctx, project, connectd.MintRequest{Role: "operate", Scopes: []string{"project"}, TTLSeconds: &ttl})
		if err != nil {
			return nil, nil, fmt.Errorf("mint a short-lived operate token: %w", err)
		}
		local.NameToken(tok.TokenID, "Local Lid")
		release := func() {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := admin.RevokeToken(rctx, project, tok.TokenID); err != nil {
				log.Printf("revoke operate token %s: %v (it expires in 5 minutes anyway)", tok.TokenID, err)
			}
		}
		return base.WithToken(tok.Bearer), release, nil
	}
	mgr := inspector.NewManager(inspectState, operate)
	mgr.OnRestore = func(endpoint, why string) { local.Audit("inspect_restore", endpoint, why) }
	return &Server{
		Inspect: mgr,
		Local:   local,
		cfg:     cfg,
		daemon:  base,
		web:     web,
		version: version,
		started: time.Now(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/status", s.status)
	// Inspector: reads for the dashboard; replay only when replay_enabled.
	mux.HandleFunc("GET /api/inspect", s.inspectList)
	mux.HandleFunc("GET /api/inspect/{service}/traffic", s.traffic)
	mux.HandleFunc("GET /api/inspect/{service}/traffic/{ex}", s.exchange)
	mux.HandleFunc("POST /api/inspect/{service}/traffic/{ex}/replay", s.browserReplay)
	// Notes and logs are set by the CLI; the dashboard only reads them.
	mux.HandleFunc("GET /api/notes", s.notes)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("GET /api/logs/{name}/tail", s.tail)
	// CLI control: needs the control secret from Local Lid's private config.
	mux.HandleFunc("POST /control/inspect/enable", s.control(s.inspectEnable))
	mux.HandleFunc("POST /control/inspect/disable", s.control(s.inspectDisable))
	mux.HandleFunc("POST /control/replay", s.control(s.cliReplay))
	mux.Handle("GET /", http.FileServerFS(s.web))
	return localhostOnly(mux)
}

// localhostOnly refuses requests not addressed to localhost by name or
// loopback IP. Binding loopback stops other machines; this stops DNS
// rebinding, where a web page makes the browser send requests to
// 127.0.0.1 under an attacker-controlled Host name.
func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" {
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "Local Lid only answers requests addressed to localhost"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// control guards CLI-only routes. Browsers don't have the secret, and any
// request carrying an Origin header (every browser POST does) is refused
// outright.
func (s *Server) control(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Local-Lid-Control")
		if r.Header.Get("Origin") != "" || s.cfg.ControlSecret == "" ||
			subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.ControlSecret)) != 1 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "control routes are for the local-lid CLI only"})
			return
		}
		h(w, r)
	}
}

type controlRequest struct {
	Project  string `json:"project"`
	Service  string `json:"service"`
	Exchange string `json:"exchange"`
}

func readControl(r *http.Request) (controlRequest, error) {
	var req controlRequest
	err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096)).Decode(&req)
	return req, err
}

func (s *Server) inspectEnable(w http.ResponseWriter, r *http.Request) {
	req, err := readControl(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Project == "" {
		req.Project = s.cfg.DefaultProject()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	rec, err := s.Inspect.Enable(ctx, req.Project, req.Service)
	if err != nil {
		s.Local.Audit("inspect_enable_refused", req.Service, err.Error())
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.Local.Audit("inspect_enable", rec.Original.Endpoint, rec.OriginalID+" -> "+rec.ServiceID+" via "+rec.Listen)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) inspectDisable(w http.ResponseWriter, r *http.Request) {
	req, err := readControl(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	rec, err := s.Inspect.Disable(ctx, req.Service)
	if err != nil {
		s.Local.Audit("inspect_disable_failed", req.Service, err.Error())
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.Local.Audit("inspect_disable", rec.Original.Endpoint, "restored")
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) inspectList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Inspect.Views())
}

func (s *Server) hopFor(w http.ResponseWriter, service string) (*inspector.Hop, *inspector.Record, bool) {
	hop, rec := s.Inspect.Hop(service)
	if hop == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service " + service + " isn't inspected"})
		return nil, nil, false
	}
	return hop, rec, true
}

func (s *Server) traffic(w http.ResponseWriter, r *http.Request) {
	if hop, _, ok := s.hopFor(w, r.PathValue("service")); ok {
		writeJSON(w, http.StatusOK, hop.Store.Summaries())
	}
}

func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	hop, _, ok := s.hopFor(w, r.PathValue("service"))
	if !ok {
		return
	}
	ex, found := hop.Store.Get(r.PathValue("ex"))
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "exchange not found (only the last 200 are kept)"})
		return
	}
	writeJSON(w, http.StatusOK, ex)
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request, service, exID string) {
	hop, _, ok := s.hopFor(w, service)
	if !ok {
		return
	}
	ex, found := hop.Store.Get(exID)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "exchange not found"})
		return
	}
	res, err := inspector.Replay(r.Context(), hop.Target, ex)
	s.Local.Audit("replay", service+"/"+exID, ex.Method+" "+ex.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// browserReplay is the dashboard's one write, behind the replay_enabled
// switch in Local Lid's config.
func (s *Server) browserReplay(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ReplayEnabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "replay is turned off (replay_enabled: false in Local Lid's config)"})
		return
	}
	s.replay(w, r, r.PathValue("service"), r.PathValue("ex"))
}

func (s *Server) cliReplay(w http.ResponseWriter, r *http.Request) {
	req, err := readControl(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.replay(w, r, req.Service, req.Exchange)
}

// Info is what the Device page needs that isn't in the daemon's status.
type Info struct {
	Hostname      string            `json:"hostname"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	Version       string            `json:"version"`
	StartedAtMs   int64             `json:"started_at_unix_ms"`
	DaemonURL     string            `json:"daemon_url"`
	DaemonUp      bool              `json:"daemon_up"`
	PortalBaseURL string            `json:"portal_base_url"`
	ReplayEnabled bool              `json:"replay_enabled"`
	Projects      []string          `json:"projects"`
	TokenNames    map[string]string `json:"token_names"`
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	projects := make([]string, 0, len(s.cfg.Projects))
	for _, p := range s.cfg.Projects {
		projects = append(projects, p.ID)
	}
	daemonURL := s.cfg.DaemonURL
	if daemonURL == "" {
		daemonURL = connectd.DefaultURL
	}
	writeJSON(w, http.StatusOK, Info{
		Hostname:      host,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Version:       s.version,
		StartedAtMs:   s.started.UnixMilli(),
		DaemonURL:     daemonURL,
		DaemonUp:      s.daemon.Health(r.Context()) == nil,
		PortalBaseURL: s.cfg.PortalBaseURL,
		ReplayEnabled: s.cfg.ReplayEnabled,
		Projects:      projects,
		TokenNames:    s.tokenNames(),
	})
}

// status passes the daemon's /v1/status through unchanged, using the project's
// stored Viewer token, so the browser never sees a token.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("project")
	p := s.cfg.Project(id)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not configured in Local Lid: " + id})
		return
	}
	raw, err := s.daemon.WithToken(p.ViewerToken).StatusRaw(r.Context(), id)
	if err != nil {
		code, kind := http.StatusBadGateway, "daemon_error"
		if connectd.IsUnreachable(err) {
			kind = "daemon_unreachable"
		}
		var apiErr *connectd.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			kind = "token_rejected"
		}
		writeJSON(w, code, map[string]string{"error": err.Error(), "kind": kind})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func (s *Server) notes(w http.ResponseWriter, _ *http.Request) {
	n, err := s.Local.Notes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) logs(w http.ResponseWriter, _ *http.Request) {
	l, err := s.Local.Logs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// tail serves only files registered by name through the CLI.
func (s *Server) tail(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	lines, err := s.Local.Tail(r.PathValue("name"), n)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": r.PathValue("name"), "lines": lines, "max_lines": localstore.MaxTailLines})
}

func (s *Server) tokenNames() map[string]string {
	names := s.Local.TokenNames()
	for id, n := range s.cfg.TokenNames {
		names[id] = n
	}
	return names
}

// readSetup reads the Connect daemon's setup token from the file init recorded.
func readSetup(path string) (string, error) {
	if path == "" {
		return "", errors.New("Local Lid doesn't know where the Connect daemon's setup token is; run local-lid init again")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the Connect daemon's setup token: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// RetireStoredOperateTokens revokes Operate tokens saved by Local Lid
// by older versions and removes them from the config.
func RetireStoredOperateTokens(ctx context.Context, cfg *config.Config) (changed bool) {
	setup, err := readSetup(cfg.SetupTokenFile)
	if err != nil {
		return false
	}
	admin := connectd.New(cfg.DaemonURL, setup)
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.OperateToken == "" {
			continue
		}
		if err := admin.RevokeToken(ctx, p.ID, p.OperateTokenID); err != nil && !connectd.IsStatus(err, http.StatusNotFound) {
			log.Printf("revoke stored operate token %s: %v (kept in config; retry next start)", p.OperateTokenID, err)
			continue
		}
		log.Printf("revoked the stored operate token %s for %s; Local Lid now mints short-lived ones", p.OperateTokenID, p.ID)
		p.OperateToken, p.OperateTokenID = "", ""
		changed = true
	}
	return changed
}
