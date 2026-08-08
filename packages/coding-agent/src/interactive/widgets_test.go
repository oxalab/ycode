package interactive

// widgets_test.go covers the P7 widgets: CountdownTimer (immediate tick, dispose,
// zero-expiry, live countdown+expire), retryStatusText formatting, and the
// extension widgets' input routing (submit/cancel).

import (
	"sync"
	"testing"
	"time"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

func TestCountdownTimerImmediateAndDispose(t *testing.T) {
	var mu sync.Mutex
	var ticks []int
	timer := NewCountdownTimer(2500, nil, func(s int) {
		mu.Lock()
		ticks = append(ticks, s)
		mu.Unlock()
	}, nil)
	mu.Lock()
	if len(ticks) != 1 || ticks[0] != 3 { // ceil(2500/1000) = 3
		t.Errorf("immediate tick = %v, want [3]", ticks)
	}
	mu.Unlock()
	timer.Dispose()
	time.Sleep(1200 * time.Millisecond) // would tick once if not disposed
	mu.Lock()
	if len(ticks) != 1 {
		t.Errorf("ticked after Dispose: %v", ticks)
	}
	mu.Unlock()
}

func TestCountdownTimerZeroExpiresImmediately(t *testing.T) {
	expired := false
	var ticks []int
	NewCountdownTimer(0, nil, func(s int) { ticks = append(ticks, s) }, func() { expired = true })
	if len(ticks) != 1 || ticks[0] != 0 {
		t.Errorf("tick = %v, want [0]", ticks)
	}
	if !expired {
		t.Error("zero timeout did not expire immediately")
	}
}

func TestCountdownTimerCountsDownAndExpires(t *testing.T) {
	var mu sync.Mutex
	var ticks []int
	expired := false
	NewCountdownTimer(100, nil, // ceil(100/1000) = 1
		func(s int) { mu.Lock(); ticks = append(ticks, s); mu.Unlock() },
		func() { mu.Lock(); expired = true; mu.Unlock() })
	time.Sleep(1500 * time.Millisecond) // immediate(1) → tick(0) → expire
	mu.Lock()
	defer mu.Unlock()
	if len(ticks) < 2 {
		t.Errorf("ticks = %v, want >= 2 (immediate + at least one countdown)", ticks)
	}
	if !expired {
		t.Error("did not expire after counting to 0")
	}
}

func TestRetryStatusText(t *testing.T) {
	ensureTheme()
	o := &Orchestrator{th: theme.GetTheme()}
	got := o.retryStatusText(7, 2, 3)
	if got == "" {
		t.Error("retryStatusText empty")
	}
	// Negative remaining clamps to 0 (no negative in the text).
	if o.retryStatusText(-5, 1, 3) == "" {
		t.Error("negative remaining produced empty text")
	}
}

func TestExtensionInputSubmitAndCancel(t *testing.T) {
	ensureTheme()
	installAppKeybindings()
	th := theme.GetTheme()

	submitted := ""
	cancelled := false
	e := NewExtensionInput(nil, "Enter value", "", 0, th,
		func(v string) { submitted = v },
		func() { cancelled = true })

	e.HandleInput("a")
	e.HandleInput("b")
	e.HandleInput("\n") // submit (enter/CR)
	if submitted != "ab" {
		t.Errorf("submit = %q, want ab", submitted)
	}

	e.HandleInput("\x1b") // escape → cancel
	if !cancelled {
		t.Error("escape did not cancel")
	}
}

func TestExtensionEditorCancel(t *testing.T) {
	ensureTheme()
	installAppKeybindings()
	th := theme.GetTheme()

	cancelled := false
	e := NewExtensionEditor(nil, nil, "Edit text", "", th,
		nil, func() { cancelled = true })
	e.HandleInput("\x1b") // escape → cancel (intercepted before the editor)
	if !cancelled {
		t.Error("escape did not cancel the extension editor")
	}
}

func TestExtensionEditorPrefill(t *testing.T) {
	ensureTheme()
	installAppKeybindings()
	th := theme.GetTheme()

	// Prefill populates the underlying editor — this is the unique Editor
	// feature vs Input, so verify it round-trips through NewExtensionEditor.
	e := NewExtensionEditor(nil, nil, "Edit text", "pre-filled content", th, nil, nil)
	if got := e.editor.GetText(); got != "pre-filled content" {
		t.Errorf("editor prefill = %q, want %q", got, "pre-filled content")
	}
}
