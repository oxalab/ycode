package interactive

// orchestrator_test.go covers the app.clear ctrl+c handler (audit #9): first
// press clears the editor, a second press within 500ms exits, and the window
// expiry resets to clear-again.

import (
	"strings"
	"testing"
	"time"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	"ycode/packages/tui"
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

// ============================================================================
// Auto-retry chrome (audit #17)
// ============================================================================

// nullTerminal is a do-nothing tui.Terminal for engine construction in tests.
type nullTerminal struct{}

func (nullTerminal) Start(onInput func(string), onResize func()) error { return nil }
func (nullTerminal) Stop() error                                       { return nil }
func (nullTerminal) DrainInput(maxMs, idleMs int) error                { return nil }
func (nullTerminal) Write(data string)                                 {}
func (nullTerminal) Columns() int                                      { return 80 }
func (nullTerminal) Rows() int                                         { return 24 }
func (nullTerminal) KittyProtocolActive() bool                         { return false }
func (nullTerminal) MoveBy(lines int)                                  {}
func (nullTerminal) HideCursor()                                       {}
func (nullTerminal) ShowCursor()                                       {}
func (nullTerminal) ClearLine()                                        {}
func (nullTerminal) ClearFromCursor()                                  {}
func (nullTerminal) ClearScreen()                                      {}
func (nullTerminal) SetTitle(title string)                             {}
func (nullTerminal) SetProgress(active bool)                           {}

// newTestOrchestratorEngine builds an orchestrator with a real (unstarted)
// engine so Mutate/status/loader code paths can run in tests.
func newTestOrchestratorEngine(t *testing.T) *Orchestrator {
	t.Helper()
	ensureTheme()
	base := components.NewEditor(nil, buildEditorTheme(theme.GetTheme()))
	return &Orchestrator{
		editor: &CustomEditor{Editor: base},
		engine: tui.NewTUI(nullTerminal{}),
		status: &tui.Container{},
		chat:   &tui.Container{},
		th:     theme.GetTheme(),
	}
}

func TestRetryLifecycleLoaderEscapeAndFailureNotice(t *testing.T) {
	installAppKeybindings()
	o := newTestOrchestratorEngine(t)

	// Start: loader in the status container, escape swapped to abort.
	o.handleRetryStart(core.AgentSessionEvent{EventType: "auto_retry_start", RetryAttempt: 1, RetryMaxAttempt: 5, RetryDelayMs: 3000})
	if o.retryLoader == nil {
		t.Fatal("retryLoader not set on auto_retry_start")
	}
	if o.retryEscapeHandler != nil || o.editor.OnEscape == nil {
		t.Fatal("OnEscape not swapped: saved=nil, handler=nil")
	}
	if len(o.status.Children) == 0 {
		t.Error("status container empty after retry start")
	}

	// End (failure): loader cleared, escape restored, failure notice appended.
	o.handleRetryEnd(core.AgentSessionEvent{EventType: "auto_retry_end", RetrySuccess: false, RetryAttempt: 3, RetryError: "boom"})
	if o.retryLoader != nil || o.retryTimer != nil {
		t.Error("retry loader/timer not cleared on auto_retry_end")
	}
	if o.editor.OnEscape != nil || o.retryEscapeHandler != nil {
		t.Error("escape handler not restored on auto_retry_end")
	}
	chatText := containerText(o.chat)
	if !strings.Contains(chatText, "Retry failed after 3 attempt(s): boom") {
		t.Errorf("chat missing failure notice, got %q", chatText)
	}

	// End (success): no failure notice.
	o.chat = &tui.Container{}
	o.handleRetryStart(core.AgentSessionEvent{EventType: "auto_retry_start", RetryAttempt: 4, RetryMaxAttempt: 5, RetryDelayMs: 1000})
	o.handleRetryEnd(core.AgentSessionEvent{EventType: "auto_retry_end", RetrySuccess: true, RetryAttempt: 4})
	if text := containerText(o.chat); text != "" {
		t.Errorf("success path appended a notice: %q", text)
	}

	// End (user-cancelled): "Retry cancelled" is its own notice path — no
	// duplicate failure line.
	o.chat = &tui.Container{}
	o.handleRetryStart(core.AgentSessionEvent{EventType: "auto_retry_start", RetryAttempt: 2, RetryMaxAttempt: 5, RetryDelayMs: 1000})
	o.handleRetryEnd(core.AgentSessionEvent{EventType: "auto_retry_end", RetrySuccess: false, RetryAttempt: 2, RetryError: "Retry cancelled"})
	if text := containerText(o.chat); text != "" {
		t.Errorf("cancelled path appended a failure notice: %q", text)
	}
}

// containerText flattens a container's immediate Text children (test helper):
// renders each at a wide width and strips ANSI styling.
func containerText(c *tui.Container) string {
	var b strings.Builder
	for _, child := range c.Children {
		txt, ok := child.(*components.Text)
		if !ok {
			continue
		}
		for _, line := range txt.Render(200) {
			b.WriteString(stripANSI(line))
		}
	}
	return b.String()
}
