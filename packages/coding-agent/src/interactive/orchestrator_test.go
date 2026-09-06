package interactive

// orchestrator_test.go covers the app.clear ctrl+c handler (audit #9): first
// press clears the editor, a second press within 500ms exits, and the window
// expiry resets to clear-again.

import (
	"testing"
	"time"

	"ycode/packages/coding-agent/src/core/tui/theme"
	"ycode/packages/tui/components"
)

func newTestOrchestratorEditor(t *testing.T) *Orchestrator {
	t.Helper()
	ensureTheme()
	base := components.NewEditor(nil, buildEditorTheme(theme.GetTheme()))
	return &Orchestrator{editor: &CustomEditor{Editor: base}, th: theme.GetTheme()}
}

func TestCtrlCClearsThenExits(t *testing.T) {
	o := newTestOrchestratorEditor(t)
	o.editor.SetText("hello")

	if o.ctrlC() {
		t.Fatal("first press must not exit")
	}
	if got := o.editor.GetText(); got != "" {
		t.Fatalf("first press must clear the editor, got %q", got)
	}

	if !o.ctrlC() {
		t.Fatal("second press within 500ms must exit")
	}
}

func TestCtrlCEmptyEditorExitsOnDoublePress(t *testing.T) {
	o := newTestOrchestratorEditor(t)

	// First press stamps the window even on an empty editor (reference
	// clearEditor runs unconditionally).
	if o.ctrlC() {
		t.Fatal("first press must not exit")
	}
	if !o.ctrlC() {
		t.Fatal("double-press on an empty editor must exit")
	}
}

func TestCtrlCWindowExpiry(t *testing.T) {
	o := newTestOrchestratorEditor(t)
	o.editor.SetText("again")

	if o.ctrlC() {
		t.Fatal("first press must not exit")
	}
	// Simulate the 500ms window lapsing, then edit again.
	o.lastCtrlC = time.Now().Add(-501 * time.Millisecond)
	o.editor.SetText("again")

	if o.ctrlC() {
		t.Fatal("press after window expiry must not exit")
	}
	if got := o.editor.GetText(); got != "" {
		t.Fatalf("press after window expiry must clear the editor, got %q", got)
	}
}
