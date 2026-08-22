package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
)

// TestResolveSessionPath verifies path-like args pass through, ID-prefix matches
// resolve locally, and unknown args resolve to not-found. (The global branch is
// non-hermetic — ListAllSessions reads a machine-global dir — so it is not
// asserted here.)
func TestResolveSessionPath(t *testing.T) {
	dir := t.TempDir()
	writeTestSession(t, dir, "abc123")

	// Path-like (contains a separator) → used as-is.
	if r := resolveSessionPath("/sessions/x.jsonl", dir, dir); r.kind != rsPath || r.path != "/sessions/x.jsonl" {
		t.Errorf("path-like → %+v, want {rsPath /sessions/x.jsonl}", r)
	}
	// Backslash also counts as path-like.
	if r := resolveSessionPath(`C:\s\y.jsonl`, dir, dir); r.kind != rsPath {
		t.Errorf("backslash path → %+v, want rsPath", r)
	}
	// .jsonl suffix without a separator is still path-like.
	if r := resolveSessionPath("plain.jsonl", dir, dir); r.kind != rsPath {
		t.Errorf("bare .jsonl → %+v, want rsPath", r)
	}

	// ID prefix → local match.
	r := resolveSessionPath("abc", dir, dir)
	if r.kind != rsLocal {
		t.Errorf("id-prefix → kind %v, want rsLocal", r.kind)
	}
	if !strings.HasSuffix(r.path, "abc123.jsonl") {
		t.Errorf("id-prefix → path %q, want to end in abc123.jsonl", r.path)
	}

	// Unknown id → not found.
	r = resolveSessionPath("zzz", dir, dir)
	if r.kind != rsNotFound || r.arg != "zzz" {
		t.Errorf("unknown → %+v, want {rsNotFound zzz}", r)
	}
}

// TestSessionCwdAdapter verifies the adapter bridges *SessionManager to the
// (typo'd) SessionCwdSource interface: GetCwd + GetSesionFile (missing 's').
func TestSessionCwdAdapter(t *testing.T) {
	dir := t.TempDir()
	sm := core.NewSessionManager(dir, dir)

	var src interface {
		GetCwd() string
		GetSesionFile() string
	} = sessionCwdAdapter{sm}

	if got := src.GetCwd(); got != dir {
		t.Errorf("GetCwd() = %q, want %q", got, dir)
	}
	// GetSesionFile must exist (the typo'd method SessionCwdSource requires) and
	// return the manager's session file path.
	want := sm.GetSessionFile()
	if got := src.GetSesionFile(); got != want {
		t.Errorf("GetSesionFile() = %q, want %q (manager's GetSessionFile)", got, want)
	}
}

// writeTestSession writes a minimal valid session jsonl into dir with the given
// ID, so ListSessions can discover it. Mirrors the fixture in
// cli/session_picker_test.go.
func writeTestSession(t *testing.T, dir, id string) {
	t.Helper()
	headerJSON, err := json.Marshal(core.SessionHeader{
		Type:      "session",
		Version:   3,
		ID:        id,
		Timestamp: "2026-06-18T00:00:00.000Z",
		Cwd:       dir,
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	msg := `{"type":"message","id":"m1","parentId":null,"timestamp":"2026-06-18T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`
	sessionFile := filepath.Join(dir, "2026-06-18T00-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(sessionFile, []byte(string(headerJSON)+"\n"+msg+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
}
