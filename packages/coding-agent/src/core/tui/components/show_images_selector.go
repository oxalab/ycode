package components

import (
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// showImagesItem is one row in the show-images selector: value + label + desc.
type showImagesItem struct {
	value       string // "yes" | "no"
	label       string
	description string
}

// showImagesOptions is the fixed two-option list (Yes first = enabled).
var showImagesOptions = []showImagesItem{
	{value: "yes", label: "Yes", description: "Show images inline in terminal"},
	{value: "no", label: "No", description: "Show text placeholder instead"},
}

// ShowImagesSelectorCallbacks holds the result handlers supplied by the App.
type ShowImagesSelectorCallbacks struct {
	OnSelect func(show bool) tea.Cmd
	OnCancel func() tea.Cmd
}

// ShowImagesSelectorComponent renders a Yes/No picker for inline images. The
// current value is preselected and checkmarked. Built on the generic Selector.
//
// Port of reference/coding-agent/src/modes/interactive/components/show-images-selector.ts
type ShowImagesSelectorComponent struct {
	theme *theme.Theme
	width int

	selector Selector[showImagesItem]
}

// NewShowImagesSelectorComponent creates a new show-images selector.
func NewShowImagesSelectorComponent(
	t *theme.Theme,
	currentValue bool,
	callbacks ShowImagesSelectorCallbacks,
) *ShowImagesSelectorComponent {
	// Yes = index 0; No = index 1.
	preselect := 1
	if currentValue {
		preselect = 0
	}
	selected := showImagesOptions[preselect]

	s := &ShowImagesSelectorComponent{theme: t}

	s.selector = NewSelector[showImagesItem](
		showImagesOptions,
		t,
		s.filterItem,
		s.renderItem,
		func(item showImagesItem) tea.Cmd {
			if callbacks.OnSelect != nil {
				return callbacks.OnSelect(item.value == "yes")
			}
			return nil
		},
		callbacks.OnCancel,
		SelectorOptions[showImagesItem]{
			Title:          "Show Images",
			EmptyText:      "No matching options",
			PreselectIndex: preselect,
			MaxVisible:     5,
		},
	)
	// Mark the current value with a checkmark.
	s.selector.SetIsSelectedFn(func(item showImagesItem) bool {
		return item.value == selected.value
	})

	return s
}

// SetWidth updates the available width.
func (s *ShowImagesSelectorComponent) SetWidth(width int) {
	s.width = width
	s.selector.SetWidth(width)
}

// Init satisfies tea.Model.
func (s *ShowImagesSelectorComponent) Init() tea.Cmd { return s.selector.Init() }

// Update forwards messages to the selector.
func (s *ShowImagesSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	s.selector, cmd = s.selector.Update(msg)
	return s, cmd
}

// View renders the selector.
func (s ShowImagesSelectorComponent) View() string {
	return s.selector.View()
}

// --- Internal ---

// filterItem matches an option against the query across label + description.
func (s *ShowImagesSelectorComponent) filterItem(item showImagesItem, query string) bool {
	return FuzzyMatchAny(query, item.label, item.description, item.value)
}

// renderItem renders one option row: "label — description".
func (s *ShowImagesSelectorComponent) renderItem(item showImagesItem, selected bool) string {
	label := item.label
	desc := item.description
	if selected {
		label = s.theme.Fg("accent", item.label)
	} else {
		desc = s.theme.Dim(item.description)
	}
	return label + " — " + desc
}
