package interactive

// selectors_test.go verifies the P5a selector wiring (preselect, callback fire,
// render structure) without a live *core.AgentSession. The SelectList's own
// input→OnSelect path is covered by the pi-tui package; here we exercise our
// closures (Value→ThinkingLevel conversion, live-preview, cancel) + the shell
// render (borders + list).

import (
	"testing"

	"ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// ensureTheme initializes the global theme for tests (deterministic "dark", no
// terminal-bg detection). Idempotent.
func ensureTheme() { theme.InitTheme("dark") }

func TestThinkingSelectorWiring(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	current := sdk.ThinkingMedium
	available := []sdk.ThinkingLevel{sdk.ThinkingOff, sdk.ThinkingLow, sdk.ThinkingMedium, sdk.ThinkingHigh}

	var selected sdk.ThinkingLevel
	var cancelled bool
	sel := newThinkingSelector(current, available, th,
		func(l sdk.ThinkingLevel) { selected = l },
		func() { cancelled = true })

	// Current level is preselected.
	item, ok := sel.list.GetSelectedItem()
	if !ok || item.Value != string(sdk.ThinkingMedium) {
		t.Errorf("preselected = %q, want %q", item.Value, sdk.ThinkingMedium)
	}
	// Descriptions mirror thinking-selector.ts:11-18.
	if item.Description != "Moderate reasoning (~8k tokens)" {
		t.Errorf("medium description = %q", item.Description)
	}

	// OnSelect closure converts Value → ThinkingLevel.
	sel.list.OnSelect(item)
	if selected != sdk.ThinkingMedium {
		t.Errorf("OnSelect fired with %q, want %q", selected, sdk.ThinkingMedium)
	}

	// OnCancel fires.
	sel.list.OnCancel()
	if !cancelled {
		t.Error("OnCancel did not fire")
	}

	// Shell renders borders + the list (>= 3 lines: top border, item(s), bottom).
	lines := sel.Render(80)
	if len(lines) < 3 {
		t.Errorf("thinking selector rendered %d lines, want >= 3", len(lines))
	}
}

func TestThemeSelectorWiring(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	current := theme.GetCurrentThemeName()

	var previewed string
	var selected string
	var cancelled bool
	sel := newThemeSelector(current, th,
		func(name string) { selected = name },
		func() { cancelled = true },
		func(name string) { previewed = name }, // onPreview (live)
	)

	// Current theme is preselected and marked "(current)".
	item, ok := sel.list.GetSelectedItem()
	if !ok || item.Value != current {
		t.Errorf("preselected = %q, want %q", item.Value, current)
	}
	if item.Description != "(current)" {
		t.Errorf("current description = %q, want (current)", item.Description)
	}

	// Live preview fires on selection change.
	sel.list.OnSelectionChange(item)
	if previewed != current {
		t.Errorf("onPreview fired with %q, want %q", previewed, current)
	}

	// Select keeps the theme.
	sel.list.OnSelect(item)
	if selected != current {
		t.Errorf("OnSelect fired with %q, want %q", selected, current)
	}

	// Cancel fires.
	sel.list.OnCancel()
	if !cancelled {
		t.Error("OnCancel did not fire")
	}

	if len(sel.Render(80)) < 3 {
		t.Error("theme selector did not render borders + list")
	}
}

func TestRouteCommandUnknown(t *testing.T) {
	// Unknown commands must return false WITHOUT touching the session/chat
	// (the Orchestrator here has nil session/chat). Known commands open
	// selectors / mutate state and are exercised with a real session elsewhere.
	o := &Orchestrator{}
	for _, in := range []string{"/foobar", "hello", "", "/clear"} {
		if o.routeCommand(in) {
			t.Errorf("routeCommand(%q) = true, want false (unknown command)", in)
		}
	}
}
