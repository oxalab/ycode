package interactive

// login_dialog_test.go verifies the dialog's content builders + the input flow
// (typing → submit delivers to promptCh; escape cancels) WITHOUT a real
// AuthStorage.Login flow (engine=nil; cancelFn stubbed). The full OAuth device
// flow is exercised end-to-end only with a configured provider on a real
// terminal. Same package → we can set promptActive/cancelFn directly.

import (
	"strings"
	"testing"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

func TestLoginDialogShowAndPrompt(t *testing.T) {
	ensureTheme()
	installAppKeybindings()
	th := theme.GetTheme()

	d := NewLoginDialog(nil, "test-provider", "TestProvider", th, func(bool, string) {})
	d.cancelFn = func() {} // stub: avoid nil-panic if escape fires before Start

	// Render: borders + title + initial waiting state.
	joined := strings.Join(d.Render(80), "\n")
	if !strings.Contains(joined, "Login to TestProvider") {
		t.Error("title missing from render")
	}
	if !strings.Contains(joined, "Starting login...") {
		t.Error("initial waiting message missing")
	}

	// showAuth adds the OSC8-hyperlinked URL + click hint.
	d.showAuth("https://example/auth", "Open this URL")
	joined = strings.Join(d.Render(80), "\n")
	if !strings.Contains(joined, "https://example/auth") {
		t.Error("auth URL missing after showAuth")
	}
	if !strings.Contains(joined, "Ctrl+click to open") {
		t.Error("click hint missing")
	}

	// Prompt flow: showPrompt lays out the input; typing + submit delivers the
	// value to promptCh (what the blocking OnPrompt waits on).
	d.showPrompt("Enter code:", "123456")
	d.promptActive = true
	d.HandleInput("a")
	d.HandleInput("b")
	d.HandleInput("c")
	d.HandleInput("\r") // submit (enter/CR)
	select {
	case v := <-d.promptCh:
		if v != "abc" {
			t.Errorf("prompt input = %q, want abc", v)
		}
	default:
		t.Error("submit did not deliver input to promptCh")
	}

	// Escape cancels (fires cancelFn).
	cancelled := false
	d.cancelFn = func() { cancelled = true }
	d.HandleInput("\x1b") // escape
	if !cancelled {
		t.Error("escape did not fire cancel")
	}
}
