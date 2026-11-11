// Package localstore holds Local Lid's purely local data, kept next to its
// config: notes, registered log files, and an audit log of its own actions.
// The CLI writes these files; the server only reads them, so the dashboard
// stays read-only.
package localstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(configPath string) *Store { return &Store{dir: filepath.Dir(configPath)} }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- Notes ----

// Note keys: "service:<endpoint>", "dial:<port>", "network:<name>".
// Services are keyed by endpoint, not id: the Connect daemon mints a new id
// every time a service is re-created, including inspector toggles.
type Note struct {
	Text    string `json:"text"`
	SetAtMs int64  `json:"set_at_unix_ms"`
}

func ServiceKey(endpoint string) string { return "service:" + endpoint }
func DialKey(port string) string        { return "dial:" + port }
func NetworkKey(name string) string     { return "network:" + name }

func (s *Store) Notes() (map[string]Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notes := map[string]Note{}
	return notes, readJSON(s.path("notes.json"), &notes)
}

func (s *Store) SetNote(key, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	notes := map[string]Note{}
	if err := readJSON(s.path("notes.json"), &notes); err != nil {
		return err
	}
	if text == "" {
		delete(notes, key)
	} else {
		notes[key] = Note{Text: text, SetAtMs: time.Now().UnixMilli()}
	}
	return writeJSON(s.path("notes.json"), notes)
}

// ---- Logs ----

// MaxTailLines caps how much of a log the dashboard can pull at once.
const MaxTailLines = 1000

type LogSource struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	SizeBytes *int64 `json:"size_bytes"`
}

func (s *Store) logMap() (map[string]string, error) {
	m := map[string]string{}
	return m, readJSON(s.path("logs.json"), &m)
}

func validLogName(name string) error {
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "/\\ ") {
		return fmt.Errorf("log name %q must be 1-64 characters with no spaces or slashes", name)
	}
	return nil
}

// AddLog registers a file by name. Only registered names can be tailed.
func (s *Store) AddLog(name, path string) (string, error) {
	if err := validLogName(name); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.logMap()
	if err != nil {
		return "", err
	}
	m[name] = abs
	return abs, writeJSON(s.path("logs.json"), m)
}

func (s *Store) RemoveLog(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.logMap()
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return fmt.Errorf("no log named %q", name)
	}
	delete(m, name)
	return writeJSON(s.path("logs.json"), m)
}

func (s *Store) Logs() ([]LogSource, error) {
	s.mu.Lock()
	m, err := s.logMap()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]LogSource, 0, len(m))
	for name, p := range m {
		src := LogSource{Name: name, Path: p}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			size := fi.Size()
			src.Exists, src.SizeBytes = true, &size
		}
		out = append(out, src)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Tail returns the last n lines of a registered log, read from the end of
// the file so big logs stay cheap.
func (s *Store) Tail(name string, n int) ([]string, error) {
	if n <= 0 || n > MaxTailLines {
		n = MaxTailLines
	}
	s.mu.Lock()
	m, err := s.logMap()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	p, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("no log named %q (register it with local-lid logs add)", name)
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return tailLines(f, n)
}

func tailLines(f *os.File, n int) ([]string, error) {
	const chunk = 64 << 10
	const maxRead = 8 << 20 // never read more than 8 MiB back
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	end := fi.Size()
	var buf []byte
	for pos := end; pos > 0 && end-pos < maxRead; {
		size := int64(chunk)
		if pos < size {
			size = pos
		}
		pos -= size
		b := make([]byte, size)
		if _, err := f.ReadAt(b, pos); err != nil && err != io.EOF {
			return nil, err
		}
		buf = append(b, buf...)
		if strings.Count(string(buf), "\n") > n {
			break
		}
	}
	text := strings.TrimRight(string(buf), "\n")
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// ---- Audit ----

type AuditEntry struct {
	TsMs   int64  `json:"ts_unix_ms"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail,omitempty"`
}

func (s *Store) Audit(action, target, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(s.dir, 0o700)
	f, err := os.OpenFile(s.path("audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	raw, _ := json.Marshal(AuditEntry{TsMs: time.Now().UnixMilli(), Action: action, Target: target, Detail: detail})
	_, _ = f.Write(append(raw, '\n'))
}

// AuditEntries returns the newest n entries, oldest first.
func (s *Store) AuditEntries(n int) ([]AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path("audit.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []AuditEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, sc.Err()
}

// ---- Token names ----

// MaxTokenNames bounds token-names.json; short-lived tokens pile up otherwise.
const MaxTokenNames = 500

type tokenName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NameToken remembers a token id Local Lid minted, so the daemon's
// "operate:<token-id>" last_actor can be shown by name.
func (s *Store) NameToken(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []tokenName
	_ = readJSON(s.path("token-names.json"), &list)
	list = append(list, tokenName{ID: id, Name: name})
	if len(list) > MaxTokenNames {
		list = list[len(list)-MaxTokenNames:]
	}
	_ = writeJSON(s.path("token-names.json"), list)
}

func (s *Store) TokenNames() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []tokenName
	_ = readJSON(s.path("token-names.json"), &list)
	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.ID] = t.Name
	}
	return out
}
