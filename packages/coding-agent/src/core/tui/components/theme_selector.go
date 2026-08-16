package components

import (
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// ThemeSelectorCallbacks holds the result handlers supplied by the App.
//
// OnPreview fires as the highlighted theme changes (live preview) and carries
// no Cmd; OnSelect fires on confirm and may return a Cmd (e.g. persist + redraw).
type ThemeSelectorCallbacks struct {
	OnSelect  func(themeName string) tea.Cmd
	OnCancel  func() tea.Cmd
	OnPreview func(themeName string)
}

// ThemeSelectorComponent renders a picker over the available theme names.
// The current theme is preselected, checkmarked, and previewed live as the
// cursor moves. Built on the generic Selector.
//
// Port of reference/coding-agent/src/modes/interactive/components/theme-selector.ts
type ThemeSelectorComponent struct {
	theme *theme.Theme
	width int

	currentTheme string
	themes       []string

	selector Selector[string]

	onPreview     func(string)
	lastPreviewed string
}

// NewThemeSelectorComponent creates a new theme selector. The current theme
// is preselected; OnPreview is wired to fire on every cursor change.
func NewThemeSelectorComponent(
	t *theme.Theme,
	currentTheme string,
	callbacks ThemeSelectorCallbacks,
) *ThemeSelectorComponent {
	themes := theme.GetAvailableThemes()
	if len(themes) == 0 {
		themes = []string{"dark", "light"}
	}

	preselect := 0
	for i, name := range themes {
		if name == currentTheme {
			preselect = i
			break
		}
	}

	ts := &ThemeSelectorComponent{
		theme:       t,
		currentTheme: currentTheme,
		themes:      themes,
		onPreview:   callbacks.OnPreview,
	}

	ts.selector = NewSelector[string](
		themes,
		t,
		ts.filterItem,
		ts.renderItem,
		func(name string) tea.Cmd {
			if callbacks.OnSelect != nil {
				return callbacks.OnSelect(name)
			}
			return nil
		},
		callbacks.OnCancel,
		SelectorOptions[string]{
			Title:         "Select Theme",
			EmptyText:     "No matching themes",
			PreselectIndex: preselect,
		},
	)
	// Mark the current theme with a checkmark in its row.
	ts.selector.SetIsSelectedFn(ts.isCurrent)

	return ts
}

// SetWidth updates the available width.
func (ts *ThemeSelectorComponent) SetWidth(width int) {
	ts.width = width
	ts.selector.SetWidth(width)
}

// Init satisfies tea.Model.
func (ts *ThemeSelectorComponent) Init() tea.Cmd { return ts.selector.Init() }

// Update forwards messages to the selector, then fires OnPreview when the
// highlighted theme changes (mirrors the TS onSelectionChange handler).
func (ts *ThemeSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	ts.selector, cmd = ts.selector.Update(msg)

	// Live preview: fire whenever the highlighted item changes.
	if ts.onPreview != nil {
		if name, ok := ts.selector.SelectedItem(); ok && name != ts.lastPreviewed {
			ts.lastPreviewed = name
			ts.onPreview(name)
		}
	}

	return ts, cmd
}

// View renders the selector.
func (ts ThemeSelectorComponent) View() string {
	return ts.selector.View()
}

// --- Internal ---

// isCurrent reports whether an item is the currently-active theme.
func (ts *ThemeSelectorComponent) isCurrent(name string) bool {
	return name == ts.currentTheme
}

// filterItem matches a theme name against the query (fuzzy).
func (ts *ThemeSelectorComponent) filterItem(name string, query string) bool {
	return FuzzyMatch(name, query)
}

// renderItem renders one theme row. The current theme is drawn in accent;
// the selector's own row logic adds the selection marker and checkmark.
func (ts *ThemeSelectorComponent) renderItem(name string, selected bool) string {
	if name == ts.currentTheme {
		return ts.theme.Fg("accent", name)
	}
	return name
}
