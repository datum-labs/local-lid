// Local Lid: a dashboard companion for the Datum Connect daemon (datum-connectd).
//
// Installed as the datumctl plugin binary `datumctl-local-lid`, it also runs
// standalone. It talks to the daemon only through its loopback /v1 API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/datum-labs/local-lid/internal/config"
	"github.com/datum-labs/local-lid/internal/connectd"
	"github.com/datum-labs/local-lid/internal/inspector"
	"github.com/datum-labs/local-lid/internal/localstore"
	"github.com/datum-labs/local-lid/internal/server"
	"github.com/datum-labs/local-lid/web"
)

var version = "0.0.0-dev"

const usage = `Local Lid: dashboard for the Datum Connect daemon.

Usage:
  local-lid init  --project <id> [--project <id>...] [--setup-token-file F] [--daemon-url U]
  local-lid serve   start the dashboard on 127.0.0.1:47790
  local-lid open    serve and open it in a browser
  local-lid inspect enable|disable <service-id|endpoint> [--project P]
  local-lid inspect list
  local-lid replay <service-id> <exchange-id>
  local-lid note set service|dial|network <selector> <text...>
  local-lid note clear service|dial|network <selector>
  local-lid note list
  local-lid logs add <name> <path> | logs remove <name> | logs list
  local-lid audit [--daemon] [--project P] [-n N]
  local-lid version

inspect puts an HTTP inspector in front of a private TCP service by
re-creating it (brief outage; its service id changes). Stopping
"local-lid serve" normally puts every original service back.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:], false)
	case "open":
		err = cmdServe(os.Args[2:], true)
	case "inspect":
		err = cmdInspect(os.Args[2:])
	case "replay":
		err = cmdReplay(os.Args[2:])
	case "note", "notes":
		err = cmdNote(os.Args[2:])
	case "logs", "log":
		err = cmdLogs(os.Args[2:])
	case "audit":
		err = cmdAudit(os.Args[2:])
	case "version", "--version":
		fmt.Println("local-lid", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "local-lid:", err)
		os.Exit(1)
	}
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// cmdInit mints a Viewer and a project-scoped Operate token per project with
// the Connect daemon's setup token, and stores them in Local Lid's config.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var projects multiFlag
	fs.Var(&projects, "project", "Datum project id (repeatable)")
	tokenFile := fs.String("setup-token-file", "", "Connect daemon setup token (default: found automatically)")
	daemonURL := fs.String("daemon-url", "", "daemon URL (default "+connectd.DefaultURL+")")
	portal := fs.String("portal-url", "", "Datum portal base URL for links")
	_ = fs.Parse(args)
	if len(projects) == 0 {
		return errors.New("at least one --project is required (the Connect daemon has no list-projects endpoint)")
	}

	setup, path, err := readSetupToken(*tokenFile)
	if err != nil {
		return err
	}
	fmt.Println("using setup token from", path)

	cfg := config.LoadOrDefault()
	cfg.SetupTokenFile = path
	if *daemonURL != "" {
		cfg.DaemonURL = *daemonURL
	}
	if *portal != "" {
		cfg.PortalBaseURL = *portal
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := connectd.New(cfg.DaemonURL, setup)
	if err := c.Health(ctx); err != nil {
		return fmt.Errorf("is datum-connectd running? %w", err)
	}

	for _, id := range projects {
		viewer, err := c.MintToken(ctx, id, connectd.MintRequest{Role: "viewer"})
		if err != nil {
			return fmt.Errorf("mint viewer token for %s: %w", id, err)
		}
		// No Operate token is stored: writes mint a short-lived one each time.
		cfg.UpsertProject(config.Project{ID: id, ViewerTokenID: viewer.TokenID, ViewerToken: viewer.Bearer})
		cfg.TokenNames[viewer.TokenID] = "Local Lid (viewer)"
		fmt.Printf("project %s: viewer token %s\n", id, viewer.TokenID)
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	p, _ := config.Path()
	fmt.Println("saved", p)
	return nil
}

// readSetupToken finds the Connect daemon's setup token the same way the datumctl connect plugin
// does: DATUM_CONNECT_DIR, then the per-user daemon dir, then the system one.
func readSetupToken(explicit string) (string, string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if d := os.Getenv("DATUM_CONNECT_DIR"); d != "" {
			candidates = append(candidates, filepath.Join(d, "daemon_auth", "setup.token"))
		}
		candidates = append(candidates, filepath.Join(userStateDir(), "datumctl", "connect", "daemon", "daemon_auth", "setup.token"))
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err == nil {
			return strings.TrimSpace(string(raw)), p, nil
		}
	}
	return "", "", fmt.Errorf("no setup token found (tried %s); pass --setup-token-file", strings.Join(candidates, ", "))
}

func userStateDir() string {
	switch runtime.GOOS {
	case "windows":
		return os.Getenv("LOCALAPPDATA")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support")
	default:
		if x := os.Getenv("XDG_STATE_HOME"); x != "" {
			return x
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "state")
	}
}

func cmdServe(args []string, open bool) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "", "address (default "+config.DefaultListen+")")
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	addr := cfg.Listen
	if *listen != "" {
		addr = *listen
	}
	if addr == "" {
		addr = config.DefaultListen
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("refusing to listen on %s: Local Lid is loopback-only", addr)
	}
	changed := cfg.EnsureControlSecret()
	rctx, rcancel := context.WithTimeout(context.Background(), 15*time.Second)
	if server.RetireStoredOperateTokens(rctx, cfg) {
		changed = true
	}
	rcancel()
	if changed {
		if err := cfg.Save(); err != nil {
			return err
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	cfgPath, _ := config.Path()
	srvState := server.New(cfg, web.FS(), version, inspector.StatePath(cfgPath), localstore.New(cfgPath))

	// A previous run that crashed may have left services pointing at an
	// inspector that no longer exists: put the originals back first.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	srvState.Inspect.RestoreLeftovers(ctx)
	cancel()

	url := "http://" + ln.Addr().String()
	log.Printf("Local Lid %s on %s", version, url)
	if open {
		openBrowser(url)
	}
	srv := &http.Server{Handler: srvState.Handler(), ReadHeaderTimeout: 10 * time.Second}

	stop, stopCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopCancel()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-stop.Done():
	}
	log.Printf("shutting down: restoring inspected services")
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	srvState.Inspect.RestoreAll(ctx)
	return srv.Shutdown(ctx)
}

// controlCall posts to the running Local Lid's CLI-only control routes.
func controlCall(path string, body any, out any) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.ControlSecret == "" {
		return errors.New("Local Lid has no control secret yet; start it once with local-lid serve")
	}
	listen := cfg.Listen
	if listen == "" {
		listen = config.DefaultListen
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://"+listen+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Local-Lid-Control", cfg.ControlSecret)
	resp, err := (&http.Client{Timeout: 6 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("is local-lid serve running? %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func cmdInspect(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: local-lid inspect enable|disable <service-id|endpoint> | inspect list")
	}
	action := args[0]
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	project := fs.String("project", "", "project id (default: the only configured project)")
	_ = fs.Parse(args[1:])
	switch action {
	case "list":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		listen := cfg.Listen
		if listen == "" {
			listen = config.DefaultListen
		}
		resp, err := http.Get("http://" + listen + "/api/inspect")
		if err != nil {
			return fmt.Errorf("is local-lid serve running? %w", err)
		}
		defer resp.Body.Close()
		var views []inspector.View
		if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
			return err
		}
		if len(views) == 0 {
			fmt.Println("No services are inspected.")
		}
		for _, v := range views {
			fmt.Printf("%s  %s -> inspector %s -> %s  (%d exchanges)\n", v.ServiceID, v.Original.Endpoint, v.Listen, v.Target, v.Exchanges)
		}
		return nil
	case "enable", "disable":
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: local-lid inspect %s <service-id|endpoint>", action)
		}
		var rec inspector.Record
		if err := controlCall("/control/inspect/"+action, map[string]string{"project": *project, "service": fs.Arg(0)}, &rec); err != nil {
			return err
		}
		if action == "enable" {
			fmt.Printf("Inspecting %s: peers now reach it through %s. The service was re-created, so its id is now %s.\n", rec.Original.Endpoint, rec.Listen, rec.ServiceID)
		} else {
			fmt.Printf("Restored %s (inspector stopped).\n", rec.Original.Endpoint)
		}
		return nil
	}
	return fmt.Errorf("unknown inspect action %q", action)
}

func cmdReplay(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: local-lid replay <service-id> <exchange-id>")
	}
	var res inspector.ReplayResult
	if err := controlCall("/control/replay", map[string]string{"service": args[0], "exchange": args[1]}, &res); err != nil {
		return err
	}
	fmt.Printf("HTTP %d\n", res.Status)
	for _, h := range res.Headers {
		fmt.Printf("%s: %s\n", h[0], h[1])
	}
	fmt.Printf("\n%s\n", res.Body)
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		if _, err := exec.LookPath("wslview"); err == nil {
			cmd = exec.Command("wslview", url)
		} else {
			cmd = exec.Command("xdg-open", url)
		}
	}
	if err := cmd.Start(); err != nil {
		log.Printf("open a browser at %s (%v)", url, err)
	}
}

func localStore() (*localstore.Store, error) {
	p, err := config.Path()
	if err != nil {
		return nil, err
	}
	return localstore.New(p), nil
}

// serviceNoteKey resolves a service selector (endpoint or service id) to its
// note key. Ids are looked up in the daemon's status, and an inspected service maps
// back to its original endpoint, so notes survive inspector toggles.
func serviceNoteKey(selector, project string) (string, error) {
	if !strings.HasPrefix(selector, "service-") {
		return localstore.ServiceKey(selector), nil
	}
	cfgPath, _ := config.Path()
	var recs []inspector.Record
	if raw, err := os.ReadFile(inspector.StatePath(cfgPath)); err == nil {
		_ = json.Unmarshal(raw, &recs)
	}
	for _, r := range recs {
		if r.ServiceID == selector || r.OriginalID == selector {
			return localstore.ServiceKey(r.Original.Endpoint), nil
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if project == "" {
		project = cfg.DefaultProject()
	}
	p := cfg.Project(project)
	if p == nil {
		return "", fmt.Errorf("project %q isn't configured; pass --project", project)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := connectd.New(cfg.DaemonURL, p.ViewerToken).Status(ctx, project)
	if err != nil {
		return "", err
	}
	for _, sv := range st.Services {
		if sv.ID == selector {
			return localstore.ServiceKey(sv.Endpoint), nil
		}
	}
	return "", fmt.Errorf("no service %s in project %s", selector, project)
}

func cmdNote(args []string) error {
	if len(args) >= 1 && args[0] == "list" {
		ls, err := localStore()
		if err != nil {
			return err
		}
		notes, err := ls.Notes()
		if err != nil {
			return err
		}
		if len(notes) == 0 {
			fmt.Println("No notes.")
		}
		for k, n := range notes {
			fmt.Printf("%-32s %s\n", k, n.Text)
		}
		return nil
	}
	fs := flag.NewFlagSet("note", flag.ExitOnError)
	project := fs.String("project", "", "project id (for service ids)")
	if len(args) < 3 || (args[0] != "set" && args[0] != "clear") {
		return errors.New("usage: local-lid note set|clear service|dial|network <selector> [text...]")
	}
	action, kind, selector := args[0], args[1], args[2]
	_ = fs.Parse(args[3:])
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if action == "set" && text == "" {
		return errors.New("note text is empty; use note clear to remove a note")
	}
	if action == "clear" {
		text = ""
	}
	var key string
	switch kind {
	case "service":
		k, err := serviceNoteKey(selector, *project)
		if err != nil {
			return err
		}
		key = k
	case "dial":
		key = localstore.DialKey(selector)
	case "network":
		key = localstore.NetworkKey(selector)
	default:
		return fmt.Errorf("kind must be service, dial or network, not %q", kind)
	}
	ls, err := localStore()
	if err != nil {
		return err
	}
	if err := ls.SetNote(key, text); err != nil {
		return err
	}
	ls.Audit("note_"+action, key, text)
	if action == "set" {
		fmt.Printf("Note set on %s.\n", key)
	} else {
		fmt.Printf("Note cleared on %s.\n", key)
	}
	return nil
}

func cmdLogs(args []string) error {
	ls, err := localStore()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 3 && args[0] == "add":
		abs, err := ls.AddLog(args[1], args[2])
		if err != nil {
			return err
		}
		ls.Audit("log_add", args[1], abs)
		fmt.Printf("Registered log %s -> %s. The dashboard can now tail it.\n", args[1], abs)
		return nil
	case len(args) == 2 && args[0] == "remove":
		if err := ls.RemoveLog(args[1]); err != nil {
			return err
		}
		ls.Audit("log_remove", args[1], "")
		fmt.Printf("Removed log %s.\n", args[1])
		return nil
	case len(args) == 1 && args[0] == "list":
		srcs, err := ls.Logs()
		if err != nil {
			return err
		}
		if len(srcs) == 0 {
			fmt.Println("No logs registered.")
		}
		for _, src := range srcs {
			state := "missing"
			if src.Exists {
				state = fmt.Sprintf("%d bytes", *src.SizeBytes)
			}
			fmt.Printf("%-20s %s (%s)\n", src.Name, src.Path, state)
		}
		return nil
	}
	return errors.New("usage: local-lid logs add <name> <path> | logs remove <name> | logs list")
}

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	withDaemon := fs.Bool("daemon", false, "also show the Connect daemon's audit (needs its setup token)")
	project := fs.String("project", "", "project for the daemon's audit (default: the only configured project)")
	n := fs.Int("n", 50, "how many entries")
	_ = fs.Parse(args)
	ls, err := localStore()
	if err != nil {
		return err
	}
	entries, err := ls.AuditEntries(*n)
	if err != nil {
		return err
	}
	fmt.Println("Local Lid actions:")
	if len(entries) == 0 {
		fmt.Println("  (none)")
	}
	for _, e := range entries {
		fmt.Printf("  %s  %-24s %s  %s\n", time.UnixMilli(e.TsMs).Format("2006-01-02 15:04:05"), e.Action, e.Target, e.Detail)
	}
	if !*withDaemon {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *project == "" {
		*project = cfg.DefaultProject()
	}
	setup, _, err := readSetupToken(cfg.SetupTokenFile)
	if err != nil {
		return err
	}
	var rows []struct {
		TsMs     int64   `json:"ts_unix_ms"`
		Event    string  `json:"event"`
		Resource *string `json:"resource"`
		Actor    string  `json:"actor"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base := cfg.DaemonURL
	if base == "" {
		base = connectd.DefaultURL
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/audit?project="+*project, nil)
	req.Header.Set("Authorization", "Bearer "+setup)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Connect daemon audit: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return err
	}
	if len(rows) > *n {
		rows = rows[len(rows)-*n:]
	}
	names := ls.TokenNames()
	for id, n := range cfg.TokenNames {
		names[id] = n
	}
	fmt.Printf("\nConnect daemon (%s):\n", *project)
	for _, r := range rows {
		res := ""
		if r.Resource != nil {
			res = *r.Resource
		}
		actor := r.Actor
		if id := strings.TrimPrefix(strings.TrimPrefix(actor, "operate:"), "viewer:"); names[id] != "" {
			actor += " (" + names[id] + ")"
		}
		fmt.Printf("  %s  %-28s %-24s %s\n", time.UnixMilli(r.TsMs).Format("2006-01-02 15:04:05"), r.Event, res, actor)
	}
	return nil
}
