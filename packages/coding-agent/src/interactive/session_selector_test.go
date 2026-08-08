package interactive

// session_selector_test.go verifies the sessionSelector's key routing —
// particularly that delete/rename keys are intercepted in list mode and that
// rename mode owns its keys.

import (
	"testing"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

func newTestSessionSelector(t *testing.T) *sessionSelector {
	t.Helper()
	ensureTheme()
	installAppKeybindings() // ensures tui.GetKeybindings() returns the merged manager
	th := theme.GetTheme()
	s := newSessionSelector(th)
	s.refresh(func() []sessionItem {
		return []sessionItem{
			{path: "/path/a.jsonl", label: "Session A", desc: "3 msgs"},
			{path: "/path/b.jsonl", label: "Session B", desc: "5 msgs"},
			{path: "/path/c.jsonl", label: "Session C", desc: "1 msg"},
		}
	})
	return s
}

func TestSessionSelector_DeleteKeyFiresOnDelete(t *testing.T) {
	s := newTestSessionSelector(t)
	var gotPath, gotLabel string
	s.onDelete = func(path, label string) {
		gotPath, gotLabel = path, label
	}

	// Default selection is the first row.
	s.HandleInput("\x04") // Ctrl+D

	if gotPath != "/path/a.jsonl" {
		t.Errorf("onDelete path = %q, want /path/a.jsonl", gotPath)
	}
	if gotLabel != "Session A" {
		t.Errorf("onDelete label = %q, want \"Session A\"", gotLabel)
	}
}

func TestSessionSelector_RenameKeyEntersRenameMode(t *testing.T) {
	s := newTestSessionSelector(t)
	s.HandleInput("\x12") // Ctrl+R

	if s.mode != sessionModeRename {
		t.Fatalf("mode = %q, want \"rename\"", s.mode)
	}
	if s.renameTarget.path != "/path/a.jsonl" {
		t.Errorf("renameTarget = %q, want /path/a.jsonl", s.renameTarget.path)
	}
	if s.renameInput == nil {
		t.Fatal("renameInput not initialized")
	}
	if got := s.renameInput.GetValue(); got != "Session A" {
		t.Errorf("renameInput prefilled with %q, want \"Session A\"", got)
	}
}

func TestSessionSelector_RenameModeEscapeCancels(t *testing.T) {
	s := newTestSessionSelector(t)
	s.HandleInput("\x12")        // Ctrl+R → rename
	s.HandleInput("\x1b")        // Esc → cancel

	if s.mode != sessionModeList {
		t.Errorf("after Esc, mode = %q, want \"list\"", s.mode)
	}
}

func TestSessionSelector_RenameModeSubmitFiresOnRename(t *testing.T) {
	s := newTestSessionSelector(t)
	var gotPath, gotCurrent, gotNew string
	s.onRename = func(path, currentName, newName string) {
		gotPath, gotCurrent, gotNew = path, currentName, newName
	}

	s.HandleInput("\x12") // Ctrl+R → rename
	// Replace the prefilled value with a fresh name.
	s.renameInput.SetValue("New Name")
	s.HandleInput("\r") // Enter → submit

	if gotPath != "/path/a.jsonl" {
		t.Errorf("onRename path = %q", gotPath)
	}
	if gotCurrent != "Session A" {
		t.Errorf("onRename currentName = %q", gotCurrent)
	}
	if gotNew != "New Name" {
		t.Errorf("onRename newName = %q, want \"New Name\"", gotNew)
	}
	if s.mode != sessionModeList {
		t.Errorf("after submit, mode = %q, want \"list\"", s.mode)
	}
}

func TestSessionSelector_ListModeNavigationStillWorks(t *testing.T) {
	// Down should move selection to second row; then Ctrl+D should fire on
	// that row, not the first.
	s := newTestSessionSelector(t)
	var gotPath string
	s.onDelete = func(path, label string) { gotPath = path }
	s.HandleInput("\x1b[B") // Down
	s.HandleInput("\x04")   // Ctrl+D
	if gotPath != "/path/b.jsonl" {
		t.Errorf("after Down+Ctrl+D, onDelete path = %q, want /path/b.jsonl", gotPath)
	}
}

func TestSessionSelector_NoCallbacksNoPanic(t *testing.T) {
	// Sanity: callbacks are nil → input handling must not panic.
	s := newTestSessionSelector(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on nil callbacks: %v", r)
		}
	}()
	s.HandleInput("\x04")  // Ctrl+D
	s.HandleInput("\x12")  // Ctrl+R
	s.HandleInput("\x1b")  // Esc
	s.HandleInput("\r")    // Enter
}
