package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	core "ycode/packages/coding-agent/src/core"
)

// TestLoadSessionPools_NilManager verifies the nil-safety contract: a nil manager
// yields empty pools (no nil-pointer dereference).
func TestLoadSessionPools_NilManager(t *testing.T) {
	current, all, currentPath := loadSessionPools(nil)
	if current != nil || all != nil || currentPath != "" {
		t.Errorf("loadSessionPools(nil) = %v, %v, %q; want nil, nil, empty", current, all, currentPath)
	}
}

// TestLoadSessionPools_FromManager verifies the current-folder pool reflects the
// session files on disk and that the active-session path round-trips through the
// manager's accessors. The `all` pool is intentionally NOT asserted:
// ListAllSessions reads a global/env sessions dir and is non-hermetic across
// machines.
func TestLoadSessionPools_FromManager(t *testing.T) {
	dir := t.TempDir()

	// Build a valid session header via json.Marshal so cwd is escaped correctly
	// (avoids raw-string pitfalls on Windows paths).
	headerJSON, err := json.Marshal(core.SessionHeader{
		Type:      "session",
		Version:   3,
		ID:        "abc123",
		Timestamp: "2026-06-18T00:00:00.000Z",
		Cwd:       dir,
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	msg := `{"type":"message","id":"m1","parentId":null,"timestamp":"2026-06-18T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`
	sessionFile := filepath.Join(dir, "2026-06-18T00-00-00-000Z_abc123.jsonl")
	if err := os.WriteFile(sessionFile, []byte(string(headerJSON)+"\n"+msg+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	// newSession() sets a session file path but does not write it (lazy write on
	// first assistant message), so dir still holds only the file above.
	sm := core.NewSessionManager(dir, dir)
	current, _, currentPath := loadSessionPools(sm)

	if len(current) != 1 {
		t.Fatalf("current pool = %d sessions, want 1: %+v", len(current), current)
	}
	if current[0].ID != "abc123" {
		t.Errorf("current[0].ID = %q, want abc123", current[0].ID)
	}
	if current[0].FirstMessage != "hello" {
		t.Errorf("current[0].FirstMessage = %q, want hello", current[0].FirstMessage)
	}
	if currentPath != sm.GetSessionFile() {
		t.Errorf("currentPath = %q, want %q", currentPath, sm.GetSessionFile())
	}
}

// TestSessionPickerCallbacks_SelectThenCancel verifies the core picker wiring:
// OnSelect records the path and quits; OnCancel clears it and quits.
func TestSessionPickerCallbacks_SelectThenCancel(t *testing.T) {
	var selected string
	cb := sessionPickerCallbacks(&selected)

	// OnSelect/OnCancel return tea.Quit (a func). Func values can only be
	// compared to nil, so assert a non-nil cmd whose invocation yields a non-nil
	// Msg — the cmd is well-formed and terminates the program. (The impl returns
	// the literal tea.Quit; we are not re-testing bubbletea.)
	cmd := cb.OnSelect("/sessions/abc.jsonl")
	if cmd == nil || cmd() == nil {
		t.Errorf("OnSelect returned %v, want a non-nil quit cmd producing a Msg", cmd)
	}
	if selected != "/sessions/abc.jsonl" {
		t.Errorf("after OnSelect, selected = %q, want the path", selected)
	}

	cmd = cb.OnCancel()
	if cmd == nil || cmd() == nil {
		t.Errorf("OnCancel returned %v, want a non-nil quit cmd producing a Msg", cmd)
	}
	if selected != "" {
		t.Errorf("after OnCancel, selected = %q, want empty", selected)
	}
}

// TestSessionPickerCallbacks_NilPointer verifies a defensive nil selected pointer
// does not panic; the quit cmd is still returned.
func TestSessionPickerCallbacks_NilPointer(t *testing.T) {
	cb := sessionPickerCallbacks(nil)
	if cmd := cb.OnSelect("x"); cmd == nil || cmd() == nil {
		t.Errorf("OnSelect(nil) = %v, want a non-nil quit cmd", cmd)
	}
	if cmd := cb.OnCancel(); cmd == nil || cmd() == nil {
		t.Errorf("OnCancel(nil) = %v, want a non-nil quit cmd", cmd)
	}
}

// TestNewSessionPickerModel_Renders verifies the model wires the component,
// renders a non-empty picker with the search placeholder, and handles a
// WindowSizeMsg without panicking — all without driving a real tea.Program.
func TestNewSessionPickerModel_Renders(t *testing.T) {
	t2, err := defaultTheme()
	if err != nil {
		t.Fatalf("defaultTheme: %v", err)
	}
	var selected string
	m := newSessionPickerModel(t2, nil, nil, "", &selected)

	if m.Init() != nil {
		t.Error("Init should return a nil cmd")
	}
	view := m.View()
	if view == "" {
		t.Fatal("View should render a non-empty picker")
	}
	// The search input placeholder ("Search sessions...") is a stable substring.
	if !strings.Contains(view, "Search") {
		t.Errorf("view should contain the search placeholder:\n%s", view)
	}

	// A WindowSizeMsg must resize without panic and stay the same model type.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if _, ok := next.(sessionPickerModel); !ok {
		t.Errorf("Update(WindowSizeMsg) returned %T, want sessionPickerModel", next)
	}
}
