package components

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	llm "ycode/packages/llm/src"
)

// SettingsConfig is a snapshot of every setting the settings selector exposes.
// The App builds it from the SettingsManager and routes changes back via
// SettingsCallbacks. Kept decoupled from SettingsManager because not every
// field has a dedicated getter/setter yet.
//
// Mirrors SettingsConfig in reference settings-selector.ts.
type SettingsConfig struct {
	AutoCompact              bool
	ShowImages               bool
	ImageWidthCells          int
	AutoResizeImages         bool
	BlockImages              bool
	EnableSkillCommands      bool
	SteeringMode             string // "all" | "one-at-a-time"
	FollowUpMode             string // "all" | "one-at-a-time"
	Transport                llm.Transport
	ThinkingLevel            theme.ThinkingLevel
	AvailableThinkingLevels  []theme.ThinkingLevel
	CurrentTheme             string
	AvailableThemes          []string
	HideThinkingBlock        bool
	CollapseChangelog        bool
	EnableInstallTelemetry   bool
	DoubleEscapeAction       string // "fork" | "tree" | "none"
	TreeFilterMode           string // "default" | "no-tools" | "user-only" | "labeled-only" | "all"
	ShowHardwareCursor       bool
	EditorPaddingX           int
	AutocompleteMaxVisible   int
	QuietStartup             bool
	ClearOnShrink            bool
	ShowTerminalProgress     bool
	WarningsAnthropicExtra   bool
}

// SettingsCallbacks reports setting changes to the App. OnChange carries the
// setting id and the new value as a string (the component converts typed
// values); submenu items (thinking/theme) also fire OnChange on confirm.
type SettingsCallbacks struct {
	// OnChange is fired for every value-cycling setting with its id + new value.
	OnChange func(id string, value string) tea.Cmd
	// OnWarningsChange fires when the warnings submenu toggles a warning.
	OnWarningsChange func(anthropicExtraUsage bool) tea.Cmd
	// OnThemePreview fires as the user moves through the theme submenu.
	OnThemePreview func(themeName string) tea.Cmd
	// OnCancel fires when the user closes the settings list.
	OnCancel func() tea.Cmd
}

// settingsItem is one row in the settings list.
type settingsItem struct {
	id           string
	label        string
	description  string
	values       []string // value options for cycling (empty for submenu items)
	currentIdx   int      // index into values
	isSubmenu    bool
	submenuKind  string // "thinking" | "theme" | "warnings"
	displayValue string // shown for submenu items (no values list)
}

// submenuModel is the common interface the three submenus satisfy.
type submenuModel interface {
	SetWidth(int)
	Update(tea.Msg) (tea.Model, tea.Cmd)
	View() string
}

// SettingsSelectorComponent renders a searchable list of settings. Most rows
// cycle through a fixed value list (left/right or enter); the thinking-level,
// theme, and warnings rows open a nested submenu.
//
// Port of reference/coding-agent/src/modes/interactive/components/settings-selector.ts
type SettingsSelectorComponent struct {
	theme   *theme.Theme
	width   int
	config  SettingsConfig
	cb      SettingsCallbacks

	items    []settingsItem
	filtered []int // indices into items
	cursor   int

	search searchInput

	submenu submenuModel // non-nil while a submenu is open
}

// searchInput is a tiny search field used to filter the settings list.
// (Kept local to avoid coupling to the generic Selector, which owns its own
// lifecycle around a single choice.)
type searchInput struct {
	value string
}

func (si *searchInput) handleKey(str string) bool {
	// Very small inline editor: printable chars append, backspace deletes.
	if str == "backspace" {
		if len(si.value) > 0 {
			// Strip one rune.
			r := []rune(si.value)
			si.value = string(r[:len(r)-1])
		}
		return true
	}
	if str == "ctrl+u" {
		si.value = ""
		return true
	}
	if len(str) == 1 {
		c := str[0]
		if c >= 0x20 && c != 0x7f {
			si.value += str
			return true
		}
	}
	return false
}

// NewSettingsSelectorComponent creates a new settings selector.
func NewSettingsSelectorComponent(
	t *theme.Theme,
	config SettingsConfig,
	callbacks SettingsCallbacks,
) *SettingsSelectorComponent {
	s := &SettingsSelectorComponent{
		theme:  t,
		config: config,
		cb:     callbacks,
	}
	s.items = s.buildItems()
	s.applyFilter()
	return s
}

// SetWidth updates the available width and forwards to an open submenu.
func (s *SettingsSelectorComponent) SetWidth(width int) {
	s.width = width
	if s.submenu != nil {
		s.submenu.SetWidth(width)
	}
}

// Init satisfies tea.Model.
func (s *SettingsSelectorComponent) Init() tea.Cmd { return nil }

// Update routes messages: submenu first (if open), then list navigation and
// search.
func (s *SettingsSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Submenu takes all input while open.
	if s.submenu != nil {
		model, cmd := s.submenu.Update(msg)
		if sm, ok := model.(submenuModel); ok {
			s.submenu = sm
		}
		return s, cmd
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	str := keyMsg.String()

	switch str {
	case "up", "k":
		s.moveCursor(-1)
		return s, nil
	case "down", "j":
		s.moveCursor(1)
		return s, nil
	case "left", "[", "h":
		return s, s.cycleFocused(-1)
	case "right", "]", "l":
		return s, s.cycleFocused(1)
	case "enter":
		return s, s.handleEnter()
	case "escape", "ctrl+c":
		if s.cb.OnCancel != nil {
			return s, s.cb.OnCancel()
		}
		return s, nil
	}

	// Typing filters the list.
	if s.search.handleKey(str) {
		s.applyFilter()
	}
	return s, nil
}

// View renders either the open submenu or the settings list.
func (s SettingsSelectorComponent) View() string {
	width := s.width
	if width <= 0 {
		width = 70
	}

	if s.submenu != nil {
		return s.submenu.View()
	}

	var b strings.Builder
	b.WriteString(s.theme.Bold("Settings") + "\n")
	b.WriteString(s.theme.Dim("  Search: ") + s.search.value + "\n\n")

	if len(s.filtered) == 0 {
		b.WriteString(s.theme.Dim("  No matching settings"))
		return b.String()
	}

	n := len(s.filtered)
	start := s.cursor - 10/2
	if start < 0 {
		start = 0
	}
	end := start + 10
	if end > n {
		end = n
		start = end - 10
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < end; i++ {
		b.WriteString(s.renderRow(s.filtered[i], i == s.cursor))
		b.WriteString("\n")
	}
	if start > 0 || end < n {
		b.WriteString(s.theme.Dim("  (" + itoa(s.cursor+1) + "/" + itoa(n) + ")"))
	}
	return b.String()
}

// --- Internal: item building ---

// buildItems constructs the ordered settings list from the current config,
// mirroring the TS item ordering.
func (s *SettingsSelectorComponent) buildItems() []settingsItem {
	cfg := s.config
	items := []settingsItem{
		s.boolItem("autocompact", "Auto-compact", "Automatically compact context when it gets too large", cfg.AutoCompact),
		s.boolItem("auto-resize-images", "Auto-resize images", "Resize large images to 2000x2000 max for better model compatibility", cfg.AutoResizeImages),
		s.boolItem("block-images", "Block images", "Prevent images from being sent to LLM providers", cfg.BlockImages),
		s.boolItem("skill-commands", "Skill commands", "Register skills as /skill:name commands", cfg.EnableSkillCommands),
		s.cycleItem("steering-mode", "Steering mode", "Enter while streaming queues steering messages", []string{"one-at-a-time", "all"}, cfg.SteeringMode),
		s.cycleItem("follow-up-mode", "Follow-up mode", "Queue follow-up messages until agent stops", []string{"one-at-a-time", "all"}, cfg.FollowUpMode),
		s.cycleItem("transport", "Transport", "Preferred transport for multi-transport providers", transportValues(), string(cfg.Transport)),
		s.boolItem("hide-thinking", "Hide thinking", "Hide thinking blocks in assistant responses", cfg.HideThinkingBlock),
		s.boolItem("collapse-changelog", "Collapse changelog", "Show condensed changelog after updates", cfg.CollapseChangelog),
		s.boolItem("quiet-startup", "Quiet startup", "Disable verbose printing at startup", cfg.QuietStartup),
		s.boolItem("install-telemetry", "Install telemetry", "Send an anonymous version/update ping after updates", cfg.EnableInstallTelemetry),
		s.cycleItem("double-escape-action", "Double-escape action", "Action when pressing Escape twice with empty editor", []string{"tree", "fork", "none"}, cfg.DoubleEscapeAction),
		s.cycleItem("tree-filter-mode", "Tree filter mode", "Default filter when opening /tree", []string{"default", "no-tools", "user-only", "labeled-only", "all"}, cfg.TreeFilterMode),
		s.boolItem("show-hardware-cursor", "Show hardware cursor", "Show the terminal cursor while positioning it for IME", cfg.ShowHardwareCursor),
		s.cycleItem("editor-padding", "Editor padding", "Horizontal padding for input editor (0-3)", []string{"0", "1", "2", "3"}, itoa(cfg.EditorPaddingX)),
		s.cycleItem("autocomplete-max-visible", "Autocomplete max items", "Max visible items in autocomplete dropdown", []string{"3", "5", "7", "10", "15", "20"}, itoa(cfg.AutocompleteMaxVisible)),
		s.boolItem("clear-on-shrink", "Clear on shrink", "Clear empty rows when content shrinks (may flicker)", cfg.ClearOnShrink),
		s.boolItem("terminal-progress", "Terminal progress", "Show OSC 9;4 progress indicators in the tab bar", cfg.ShowTerminalProgress),
	}

	// Image toggles only when terminal supports images.
	if cfg.ShowImages || cfg.ImageWidthCells > 0 {
		items = append(items, s.boolItem("show-images", "Show images", "Render images inline in terminal", cfg.ShowImages))
		items = append(items, s.cycleItem("image-width-cells", "Image width", "Preferred inline image width in terminal cells", []string{"60", "80", "120"}, itoa(cfg.ImageWidthCells)))
	}

	// Submenu items.
	items = append(items, settingsItem{
		id: "warnings", label: "Warnings", description: "Enable or disable individual warnings",
		isSubmenu: true, submenuKind: "warnings", displayValue: "configure",
	})
	items = append(items, settingsItem{
		id: "thinking", label: "Thinking level", description: "Reasoning depth for thinking-capable models",
		isSubmenu: true, submenuKind: "thinking", displayValue: string(cfg.ThinkingLevel),
	})
	items = append(items, settingsItem{
		id: "theme", label: "Theme", description: "Color theme for the interface",
		isSubmenu: true, submenuKind: "theme", displayValue: cfg.CurrentTheme,
	})

	return items
}

// boolItem is a true/false cycling item.
func (s *SettingsSelectorComponent) boolItem(id, label, desc string, val bool) settingsItem {
	idx := 1 // "false"
	if val {
		idx = 0 // "true" first
	}
	return settingsItem{
		id: id, label: label, description: desc,
		values: []string{"true", "false"}, currentIdx: idx,
	}
}

// cycleItem picks the index of currentValue within values (0 if absent).
func (s *SettingsSelectorComponent) cycleItem(id, label, desc string, values []string, currentValue string) settingsItem {
	idx := 0
	for i, v := range values {
		if v == currentValue {
			idx = i
			break
		}
	}
	return settingsItem{id: id, label: label, description: desc, values: values, currentIdx: idx}
}

// transportValues returns the ordered transport options.
func transportValues() []string {
	return []string{"sse", "websocket", "websocket-cached", "auto"}
}

// --- Internal: navigation ---

func (s *SettingsSelectorComponent) moveCursor(delta int) {
	n := len(s.filtered)
	if n == 0 {
		s.cursor = 0
		return
	}
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= n {
		s.cursor = n - 1
	}
}

// cycleFocused cycles the focused item's value by delta (+1/-1). Submenu items
// are cycled via handleEnter instead.
func (s *SettingsSelectorComponent) cycleFocused(delta int) tea.Cmd {
	if s.cursor < 0 || s.cursor >= len(s.filtered) {
		return nil
	}
	item := &s.items[s.filtered[s.cursor]]
	if len(item.values) == 0 {
		return nil
	}
	n := len(item.values)
	item.currentIdx = ((item.currentIdx+delta)%n + n) % n
	if s.cb.OnChange != nil {
		return s.cb.OnChange(item.id, item.values[item.currentIdx])
	}
	return nil
}

// handleEnter cycles a value item or opens a submenu for submenu items.
func (s *SettingsSelectorComponent) handleEnter() tea.Cmd {
	if s.cursor < 0 || s.cursor >= len(s.filtered) {
		return nil
	}
	item := &s.items[s.filtered[s.cursor]]
	if item.isSubmenu {
		return s.openSubmenu(item)
	}
	return s.cycleFocused(1)
}

// openSubmenu builds and installs the submenu for the given item.
func (s *SettingsSelectorComponent) openSubmenu(item *settingsItem) tea.Cmd {
	switch item.submenuKind {
	case "thinking":
		s.submenu = NewThinkingSelectorComponent(
			s.theme, s.config.ThinkingLevel, s.config.AvailableThinkingLevels,
			ThinkingSelectorCallbacks{
				OnSelect: func(level theme.ThinkingLevel) tea.Cmd {
					s.config.ThinkingLevel = level
					item.displayValue = string(level)
					s.submenu = nil
					if s.cb.OnChange != nil {
						return s.cb.OnChange("thinking", string(level))
					}
					return nil
				},
				OnCancel: func() tea.Cmd { s.submenu = nil; return nil },
			},
		)
	case "theme":
		s.submenu = NewThemeSelectorComponent(
			s.theme, s.config.CurrentTheme,
			ThemeSelectorCallbacks{
				OnSelect: func(name string) tea.Cmd {
					s.config.CurrentTheme = name
					item.displayValue = name
					s.submenu = nil
					if s.cb.OnChange != nil {
						return s.cb.OnChange("theme", name)
					}
					return nil
				},
				OnCancel: func() tea.Cmd {
					// Restore original theme preview before closing.
					var cmd tea.Cmd
					if s.cb.OnThemePreview != nil {
						cmd = s.cb.OnThemePreview(s.config.CurrentTheme)
					}
					s.submenu = nil
					return cmd
				},
				OnPreview: func(name string) {
					if s.cb.OnThemePreview != nil {
						s.cb.OnThemePreview(name)
					}
				},
			},
		)
	case "warnings":
		s.submenu = s.newWarningsSubmenu()
	}
	if s.submenu != nil {
		s.submenu.SetWidth(s.width)
	}
	return nil
}

// newWarningsSubmenu builds a tiny selector for the warnings toggle.
func (s *SettingsSelectorComponent) newWarningsSubmenu() submenuModel {
	cur := "false"
	if s.config.WarningsAnthropicExtra {
		cur = "true"
	}
	sel := NewSelector[string](
		[]string{"true", "false"},
		s.theme,
		func(v, q string) bool { return FuzzyMatch(v, q) },
		func(v string, selected bool) string {
			label := "Anthropic extra usage: " + v
			if selected {
				return s.theme.Fg("accent", label)
			}
			return label
		},
		func(v string) tea.Cmd {
			s.config.WarningsAnthropicExtra = v == "true"
			s.submenu = nil
			if s.cb.OnWarningsChange != nil {
				return s.cb.OnWarningsChange(v == "true")
			}
			return nil
		},
		func() tea.Cmd { s.submenu = nil; return nil },
		SelectorOptions[string]{
			Title:          "Warnings",
			EmptyText:      "No options",
			PreselectIndex: boolStrIndex(cur),
		},
	)
	return &warningsSubmenuAdapter{sel: sel}
}

// warningsSubmenuAdapter adapts Selector[string] to the submenuModel interface.
// (Selector.Update returns a value Selector, not a pointer, so it cannot
// satisfy submenuModel directly.)
type warningsSubmenuAdapter struct {
	sel Selector[string]
}

func (w *warningsSubmenuAdapter) SetWidth(width int)        { w.sel.SetWidth(width) }
func (w *warningsSubmenuAdapter) Init() tea.Cmd             { return w.sel.Init() }
func (w *warningsSubmenuAdapter) View() string              { return w.sel.View() }
func (w *warningsSubmenuAdapter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	w.sel, cmd = w.sel.Update(msg)
	return w, cmd
}

// boolStrIndex returns the Selector index for a "true"/"false" preselect.
func boolStrIndex(v string) int {
	if v == "false" {
		return 1
	}
	return 0
}

// --- Internal: rendering ---

// renderRow renders one settings row: cursor marker, label (dim desc), value.
func (s SettingsSelectorComponent) renderRow(itemIdx int, selected bool) string {
	item := s.items[itemIdx]

	marker := "  "
	if selected {
		marker = s.theme.Fg("accent", "› ")
	}

	label := item.label
	desc := item.description
	if selected {
		label = s.theme.Bold(label)
	} else {
		desc = s.theme.Dim(desc)
	}

	// Value to show on the right.
	value := ""
	if len(item.values) > 0 {
		value = item.values[item.currentIdx]
	} else {
		value = item.displayValue
	}
	valueText := s.theme.Fg("warning", value)

	return marker + label + "\n" + s.theme.Dim("      "+item.description) + "  " + valueText
}

// --- Internal: search ---

// applyFilter filters items by the search value (fuzzy over label + description).
func (s *SettingsSelectorComponent) applyFilter() {
	q := strings.ToLower(s.search.value)
	s.filtered = s.filtered[:0]
	for i, item := range s.items {
		if q == "" || FuzzyMatchAny(q, strings.ToLower(item.label), strings.ToLower(item.description)) {
			s.filtered = append(s.filtered, i)
		}
	}
	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}
