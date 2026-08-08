package interactive

// settings_selector.go ports reference components/settings-selector.ts
// (verified — see mapping/13-selectors.md). It wraps the pi-tui SettingsList
// (value cycling + submenus + search) in DynamicBorders. The reference lists
// ~25 settings; this Go port covers the subset whose setters actually exist in
// core.SettingsManager (auto-compact, steering/follow-up mode, transport,
// thinking level, theme) — the image/hide-thinking/quiet/etc. settings are
// read-only in Go (getters only, no setters), so toggling them would be a lie.
// Adding more is one SettingItem + one onChange case each.
//
// Like the other selectors it swaps into editorContainer via swapInSelector.

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// settingsSubmenuLayout mirrors SETTINGS_SUBMENU_SELECT_LIST_LAYOUT (:19-22).
var settingsSubmenuLayout = components.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// settingsSelector wraps a SettingsList with borders (Focusable + InputHandler).
type settingsSelector struct {
	*tui.Container
	list    *components.SettingsList
	focused bool
}

func (s *settingsSelector) HandleInput(data string) { s.list.HandleInput(data) }
func (s *settingsSelector) Focused() bool          { return s.focused }
func (s *settingsSelector) SetFocused(f bool)      { s.focused = f }

// selectSubmenu is the SettingsList submenu for picking one value from a list
// (reference SelectSubmenu, settings-selector.ts:134-196): title + description
// + SelectList + hint. Implements tui.Component (Render via Container) +
// InputHandler (forwards to the list) so SettingsList can drive it.
type selectSubmenu struct {
	*tui.Container
	list *components.SelectList
}

func newSelectSubmenu(title, description string, options []components.SelectItem, current string,
	th *theme.Theme, onSelect func(string), onCancel func(), onSelectionChange func(string)) *selectSubmenu {

	c := &tui.Container{}
	c.AddChild(components.NewText(th.Bold(th.Fg("accent", title)), 0, 0, nil))
	if description != "" {
		c.AddChild(components.NewSpacer(1))
		c.AddChild(components.NewText(th.Fg("muted", description), 0, 0, nil))
	}
	c.AddChild(components.NewSpacer(1))

	list := components.NewSelectList(options, min(len(options), 10), buildSelectListTheme(th), settingsSubmenuLayout)
	for i, o := range options {
		if o.Value == current {
			list.SetSelectedIndex(i)
			break
		}
	}
	list.OnSelect = func(item components.SelectItem) { onSelect(item.Value) }
	list.OnCancel = onCancel
	if onSelectionChange != nil {
		list.OnSelectionChange = func(item components.SelectItem) { onSelectionChange(item.Value) }
	}
	c.AddChild(list)
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText(th.Fg("dim", "  Enter to select · Esc to go back"), 0, 0, nil))
	return &selectSubmenu{Container: c, list: list}
}

func (s *selectSubmenu) HandleInput(data string) { s.list.HandleInput(data) }

// SettingsSelectorConfig is the displayed settings state (reference SettingsConfig,
// pruned to the Go-supported subset).
type SettingsSelectorConfig struct {
	AutoCompact            bool
	SteeringMode           string
	FollowUpMode           string
	Transport              llm.Transport
	ThinkingLevel          sdk.ThinkingLevel
	AvailableThinkingLevels []sdk.ThinkingLevel
	Theme                  string
	AvailableThemes        []string
}

// SettingsSelectorCallbacks fires on each change. Each maps to a SettingsManager
// setter (+ live UI refresh where relevant).
type SettingsSelectorCallbacks struct {
	OnAutoCompactChange   func(bool)
	OnSteeringModeChange  func(string)
	OnFollowUpModeChange  func(string)
	OnTransportChange     func(llm.Transport)
	OnThinkingLevelChange func(sdk.ThinkingLevel)
	OnThemeChange         func(string)
	OnThemePreview        func(string)
	OnCancel              func()
}

// newSettingsSelector builds the settings panel.
func newSettingsSelector(cfg SettingsSelectorConfig, cb SettingsSelectorCallbacks, th *theme.Theme) *settingsSelector {
	boolVal := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}

	items := []components.SettingItem{
		{
			ID:           "autocompact",
			Label:        "Auto-compact",
			Description:  "Automatically compact context when it gets too large",
			CurrentValue: boolVal(cfg.AutoCompact),
			Values:       []string{"true", "false"},
		},
		{
			ID:           "steering-mode",
			Label:        "Steering mode",
			Description:  "Enter while streaming queues steering messages. 'one-at-a-time': deliver one, wait. 'all': deliver all at once.",
			CurrentValue: cfg.SteeringMode,
			Values:       []string{"one-at-a-time", "all"},
		},
		{
			ID:           "follow-up-mode",
			Label:        "Follow-up mode",
			Description:  "Queues follow-up messages until the agent stops. 'one-at-a-time': deliver one, wait. 'all': deliver all at once.",
			CurrentValue: cfg.FollowUpMode,
			Values:       []string{"one-at-a-time", "all"},
		},
		{
			ID:           "transport",
			Label:        "Transport",
			Description:  "Preferred transport for providers that support multiple transports",
			CurrentValue: string(cfg.Transport),
			Values:       []string{"sse", "websocket", "websocket-cached", "auto"},
		},
	}

	// Thinking level submenu.
	thinkingOpts := make([]components.SelectItem, len(cfg.AvailableThinkingLevels))
	for i, lv := range cfg.AvailableThinkingLevels {
		thinkingOpts[i] = components.SelectItem{
			Value:       string(lv),
			Label:       string(lv),
			Description: thinkingLevelDescriptions[lv],
		}
	}
	items = append(items, components.SettingItem{
		ID:           "thinking",
		Label:        "Thinking level",
		Description:  "Reasoning depth for thinking-capable models",
		CurrentValue: string(cfg.ThinkingLevel),
		Submenu: func(currentValue string, done func(selected string)) tui.Component {
			return newSelectSubmenu("Thinking Level",
				"Select reasoning depth for thinking-capable models",
				thinkingOpts, currentValue, th,
				func(value string) { cb.OnThinkingLevelChange(sdk.ThinkingLevel(value)); done(value) },
				func() { done("") },
				nil)
		},
	})

	// Theme submenu (with live preview).
	themeOpts := make([]components.SelectItem, len(cfg.AvailableThemes))
	for i, name := range cfg.AvailableThemes {
		themeOpts[i] = components.SelectItem{Value: name, Label: name}
	}
	items = append(items, components.SettingItem{
		ID:           "theme",
		Label:        "Theme",
		Description:  "Color theme for the interface",
		CurrentValue: cfg.Theme,
		Submenu: func(currentValue string, done func(selected string)) tui.Component {
			return newSelectSubmenu("Theme", "Select color theme",
				themeOpts, currentValue, th,
				func(value string) { cb.OnThemeChange(value); done(value) },
				func() { cb.OnThemePreview(currentValue); done("") }, // cancel → restore
				func(value string) { cb.OnThemePreview(value) })      // preview
		},
	})

	list := components.NewSettingsList(items, 10, buildSettingsListTheme(th),
		func(id, newValue string) {
			switch id {
			case "autocompact":
				cb.OnAutoCompactChange(newValue == "true")
			case "steering-mode":
				cb.OnSteeringModeChange(newValue)
			case "follow-up-mode":
				cb.OnFollowUpModeChange(newValue)
			case "transport":
				cb.OnTransportChange(llm.Transport(newValue))
			}
		},
		cb.OnCancel,
		components.SettingsListOptions{EnableSearch: true},
	)

	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(list)
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	return &settingsSelector{Container: c, list: list}
}
