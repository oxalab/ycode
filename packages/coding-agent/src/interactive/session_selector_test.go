package interactive

// session_selector_test.go verifies the sessionSelector's key routing —
// delete/rename interception in list mode, the delete-confirm state
// (enter/commit/cancel/ignore, active-session refusal), and rename mode's
// key ownership.

import (
	"testing"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

func newTestSessionSelector(t *testing.T) *sessionSelector {
	t.Helper()
	return newTestSessionSelectorWithItems(t, []sessionItem{
		{path: "/path/a.jsonl", label: "Session A", desc: "3 msgs"},
		{path: "/path/b.jsonl", label: "Session B", desc: "5 msgs"},
		{path: "/path/c.jsonl", label: "Session C", desc: "1 msg"},
	})
}

func newTestSessionSelectorWithItems(t *testing.T, items []sessionItem) *sessionSelector {
	t.Helper()
	ensureTheme()
	installAppKeybindings() // ensures tui.GetKeybindings() returns the merged manager
	s := newSessionSelector(theme.GetTheme())
	s.refresh(func() []sessionItem { return items })
	return s
}

func TestSessionSelector_DeleteKeyEntersConfirmMode(t *testing.T) {
	s := newTestSessionSelector(t)
	var fired bool
	s.onDelete = func(path, label string) { fired = true }

	// Default selection is the first row.
	s.HandleInput("\x04") // Ctrl+D

	if s.mode != sessionModeConfirmDelete {
		t.Fatalf("mode = %q, want %q", s.mode, sessionModeConfirmDelete)
	}
	if s.confirmDeleteTarget.path != "/path/a.jsonl" {
		t.Errorf("confirmDeleteTarget = %q, want /path/a.jsonl", s.confirmDeleteTarget.path)
	}
	if fired {
		t.Error("onDelete must not fire until confirmed")
	}
}

func TestSessionSelector_ConfirmModeSubmitFiresOnDelete(t *testing.T) {
	s := newTestSessionSelector(t)
	var gotPath, gotLabel string
	s.onDelete = func(path, label string) {
		gotPath, gotLabel = path, label
	}

	s.HandleInput("\x04") // Ctrl+D → confirm mode
	s.HandleInput("\r")   // Enter → commit

	if gotPath != "/path/a.jsonl" {
		t.Errorf("onDelete path = %q, want /path/a.jsonl", gotPath)
	}
	if gotLabel != "Session A" {
		t.Errorf("onDelete label = %q, want \"Session A\"", gotLabel)
	}
	if s.mode != sessionModeList {
		t.Errorf("after submit, mode = %q, want \"list\"", s.mode)
	}
}

func TestSessionSelector_ConfirmModeEscapeCancels(t *testing.T) {
	s := newTestSessionSelector(t)
	var fired bool
	s.onDelete = func(path, label string) { fired = true }

	s.HandleInput("\x04") // Ctrl+D → confirm mode
	s.HandleInput("\x1b") // Esc → cancel

	if s.mode != sessionModeList {
		t.Errorf("after Esc, mode = %q, want \"list\"", s.mode)
	}
	if fired {
		t.Error("onDelete must not fire on cancel")
	}
}

func TestSessionSelector_ConfirmModeIgnoresOtherKeys(t *testing.T) {
	s := newTestSessionSelector(t)
	var fired bool
	s.onDelete = func(path, label string) { fired = true }

	s.HandleInput("\x04") // Ctrl+D → confirm mode
	s.HandleInput("x")    // regular key — must be swallowed
	s.HandleInput("\x04") // even another Ctrl+D

	if s.mode != sessionModeConfirmDelete {
		t.Errorf("mode = %q, want still %q", s.mode, sessionModeConfirmDelete)
	}
	if fired {
		t.Error("onDelete must not fire on ignored keys")
	}
}

func TestSessionSelector_DeleteCurrentSessionRefused(t *testing.T) {
	s := newTestSessionSelectorWithItems(t, []sessionItem{
		{path: "/path/current.jsonl", label: "Current", desc: "", current: true},
		{path: "/path/other.jsonl", label: "Other", desc: ""},
	})
	var fired bool
	s.onDelete = func(path, label string) { fired = true }
	var errMsg string
	s.onError = func(msg string) { errMsg = msg }

	s.HandleInput("\x04") // Ctrl+D on the current row

	if fired {
		t.Error("deleting the active session must be refused")
	}
	if errMsg != "Cannot delete the currently active session" {
		t.Errorf("onError = %q, want refusal message", errMsg)
	}
	if s.mode != sessionModeList {
		t.Errorf("mode = %q, want \"list\" (no confirm state)", s.mode)
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
	// Down should move selection to second row; then Ctrl+D + Enter should
	// delete that row, not the first.
	s := newTestSessionSelector(t)
	var gotPath string
	s.onDelete = func(path, label string) { gotPath = path }
	s.HandleInput("\x1b[B") // Down
	s.HandleInput("\x04")   // Ctrl+D → confirm mode
	s.HandleInput("\r")     // Enter → commit
	if gotPath != "/path/b.jsonl" {
		t.Errorf("after Down+Ctrl+D+Enter, onDelete path = %q, want /path/b.jsonl", gotPath)
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
