package interactive

// settings_selector_test.go verifies the settings panel mechanics — render,
// value cycling, and submenu open/escape-back — without a live session
// (callbacks record into locals). The underlying SettingsList/SelectList input
// paths are covered by the pi-tui package.

import (
	"strings"
	"testing"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

func TestSettingsSelectorCycleAndSubmenu(t *testing.T) {
	ensureTheme()
	installAppKeybindings()
	th := theme.GetTheme()

	var autoCompact = true // start true; expect cycling to false
	var thinkingLevel string
	cb := SettingsSelectorCallbacks{
		OnAutoCompactChange:   func(b bool) { autoCompact = b },
		OnThinkingLevelChange: func(l sdk.ThinkingLevel) { thinkingLevel = string(l) },
		OnSteeringModeChange:  func(string) {},
		OnFollowUpModeChange:  func(string) {},
		OnTransportChange:     func(llm.Transport) {},
		OnThemeChange:         func(string) {},
		OnThemePreview:        func(string) {},
	}
	cfg := SettingsSelectorConfig{
		AutoCompact:             true,
		SteeringMode:            "all",
		FollowUpMode:            "all",
		Transport:               "sse",
		ThinkingLevel:           sdk.ThinkingMedium,
		AvailableThinkingLevels: []sdk.ThinkingLevel{sdk.ThinkingOff, sdk.ThinkingMedium, sdk.ThinkingHigh},
		Theme:                   "dark",
		AvailableThemes:         []string{"dark", "light"},
	}
	sel := newSettingsSelector(cfg, cb, th)

	// Render: borders + the settings list.
	lines := sel.Render(80)
	if len(lines) < 3 {
		t.Fatalf("settings selector rendered %d lines, want >= 3", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Auto-compact") {
		t.Error("Auto-compact row missing from render")
	}

	// Index 0 = Auto-compact (true). Confirm cycles true → false.
	sel.HandleInput("\r")
	if autoCompact != false {
		t.Errorf("autocompact cycle: got %v, want false", autoCompact)
	}

	// Navigate to Thinking level (index 4) and open its submenu.
	for i := 0; i < 4; i++ {
		sel.HandleInput("\x1b[B") // down
	}
	sel.HandleInput("\r") // open submenu
	if sub := strings.Join(sel.Render(80), "\n"); !strings.Contains(sub, "Thinking Level") {
		t.Errorf("thinking submenu did not open:\n%s", sub)
	}

	// Escape returns to the main list.
	sel.HandleInput("\x1b") // escape
	if main := strings.Join(sel.Render(80), "\n"); !strings.Contains(main, "Auto-compact") {
		t.Errorf("escape did not return to main settings list:\n%s", main)
	}
	_ = thinkingLevel
}
