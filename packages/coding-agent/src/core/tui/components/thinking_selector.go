package components

import (
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// thinkingItem is one row in the thinking level selector: the level plus its
// human-readable description (shown alongside the label in the row).
type thinkingItem struct {
	level       theme.ThinkingLevel
	description string
}

// thinkingLevelDescriptions maps each thinking level to its description text,
// mirroring LEVEL_DESCRIPTIONS in the TS reference.
var thinkingLevelDescriptions = map[theme.ThinkingLevel]string{
	theme.ThinkingOff:     "No reasoning",
	theme.ThinkingMinimal: "Very brief reasoning (~1k tokens)",
	theme.ThinkingLow:     "Light reasoning (~2k tokens)",
	theme.ThinkingMedium:  "Moderate reasoning (~8k tokens)",
	theme.ThinkingHigh:    "Deep reasoning (~16k tokens)",
	theme.ThinkingXhigh:   "Maximum reasoning (~32k tokens)",
}

// allThinkingLevels is the canonical ordered list of thinking levels, from
// off (least) to xhigh (most).
var allThinkingLevels = []theme.ThinkingLevel{
	theme.ThinkingOff,
	theme.ThinkingMinimal,
	theme.ThinkingLow,
	theme.ThinkingMedium,
	theme.ThinkingHigh,
	theme.ThinkingXhigh,
}

// ThinkingSelectorCallbacks holds the result handlers supplied by the App.
type ThinkingSelectorCallbacks struct {
	OnSelect func(level theme.ThinkingLevel) tea.Cmd
	OnCancel func() tea.Cmd
}

// ThinkingSelectorComponent renders a picker over the available thinking
// levels, each shown with a token-budget description. The current level is
// preselected and checkmarked. Built on the generic Selector.
//
// Port of reference/coding-agent/src/modes/interactive/components/thinking-selector.ts
type ThinkingSelectorComponent struct {
	theme        *theme.Theme
	width        int
	currentLevel theme.ThinkingLevel

	selector Selector[thinkingItem]
}

// NewThinkingSelectorComponent creates a new thinking level selector.
// availableLevels filters which levels are shown (defaults to all when empty).
func NewThinkingSelectorComponent(
	t *theme.Theme,
	currentLevel theme.ThinkingLevel,
	availableLevels []theme.ThinkingLevel,
	callbacks ThinkingSelectorCallbacks,
) *ThinkingSelectorComponent {
	levels := availableLevels
	if len(levels) == 0 {
		levels = allThinkingLevels
	}

	items := make([]thinkingItem, 0, len(levels))
	for _, lvl := range levels {
		desc := thinkingLevelDescriptions[lvl]
		if desc == "" {
			desc = "Reasoning"
		}
		items = append(items, thinkingItem{level: lvl, description: desc})
	}

	preselect := 0
	for i, item := range items {
		if item.level == currentLevel {
			preselect = i
			break
		}
	}

	ts := &ThinkingSelectorComponent{
		theme:        t,
		currentLevel: currentLevel,
	}

	ts.selector = NewSelector[thinkingItem](
		items,
		t,
		ts.filterItem,
		ts.renderItem,
		func(item thinkingItem) tea.Cmd {
			if callbacks.OnSelect != nil {
				return callbacks.OnSelect(item.level)
			}
			return nil
		},
		callbacks.OnCancel,
		SelectorOptions[thinkingItem]{
			Title:          "Select Thinking Level",
			EmptyText:      "No matching levels",
			PreselectIndex: preselect,
			MaxVisible:     len(items),
		},
	)
	// Mark the current level with a checkmark.
	ts.selector.SetIsSelectedFn(ts.isCurrent)

	return ts
}

// SetWidth updates the available width.
func (ts *ThinkingSelectorComponent) SetWidth(width int) {
	ts.width = width
	ts.selector.SetWidth(width)
}

// Init satisfies tea.Model.
func (ts *ThinkingSelectorComponent) Init() tea.Cmd { return ts.selector.Init() }

// Update forwards messages to the selector.
func (ts *ThinkingSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	ts.selector, cmd = ts.selector.Update(msg)
	return ts, cmd
}

// View renders the selector.
func (ts ThinkingSelectorComponent) View() string {
	return ts.selector.View()
}

// --- Internal ---

// isCurrent reports whether an item is the currently-active thinking level.
func (ts *ThinkingSelectorComponent) isCurrent(item thinkingItem) bool {
	return item.level == ts.currentLevel
}

// filterItem matches an item against the query across level + description.
func (ts *ThinkingSelectorComponent) filterItem(item thinkingItem, query string) bool {
	return FuzzyMatchAny(query, string(item.level), item.description)
}

// renderItem renders one level row: "level — description".
func (ts *ThinkingSelectorComponent) renderItem(item thinkingItem, selected bool) string {
	levelText := string(item.level)
	desc := item.description
	if selected {
		levelText = ts.theme.Fg("accent", levelText)
	} else {
		desc = ts.theme.Dim(item.description)
	}
	return levelText + " — " + desc
}
