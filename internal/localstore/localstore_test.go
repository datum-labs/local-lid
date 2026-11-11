package localstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotesSetAndClear(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "config.json"))
	if err := s.SetNote(ServiceKey("127.0.0.1:8080"), "why it exists"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Notes()
	if n["service:127.0.0.1:8080"].Text != "why it exists" {
		t.Fatalf("got %+v", n)
	}
	_ = s.SetNote(ServiceKey("127.0.0.1:8080"), "")
	if n, _ = s.Notes(); len(n) != 0 {
		t.Fatalf("clear left %+v", n)
	}
}

func TestTailOnlyRegisteredNames(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "config.json"))
	logPath := filepath.Join(dir, "app.log")
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	_ = os.WriteFile(logPath, []byte(b.String()), 0o600)
	if _, err := s.AddLog("app", logPath); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Tail("app", 3)
	if err != nil || strings.Join(lines, ",") != "line 4998,line 4999,line 5000" {
		t.Fatalf("tail: %v %v", lines, err)
	}
	if lines, _ = s.Tail("app", 0); len(lines) != MaxTailLines || lines[0] != "line 4001" {
		t.Fatalf("capped tail: len=%d first=%q", len(lines), lines[0])
	}
	if _, err := s.Tail("../../etc/passwd", 3); err == nil {
		t.Fatal("unregistered name must be refused")
	}
	if _, err := s.AddLog("bad/name", logPath); err == nil {
		t.Fatal("slash in name must be refused")
	}
}

func TestAuditNewestN(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "config.json"))
	for i := 0; i < 5; i++ {
		s.Audit("note_set", fmt.Sprintf("t%d", i), "")
	}
	e, err := s.AuditEntries(2)
	if err != nil || len(e) != 2 || e[1].Target != "t4" {
		t.Fatalf("%+v %v", e, err)
	}
}
