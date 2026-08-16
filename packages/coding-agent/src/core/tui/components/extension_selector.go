package components

import (
	"time"

	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// extensionTickMsg advances the optional countdown timer by one second.
type extensionTickMsg struct{}

// ExtensionSelectorOptions configures an extension selector at creation time.
type ExtensionSelectorOptions struct {
	// TimeoutSeconds, if > 0, shows a "(Ns)" countdown in the title and
	// auto-cancels the dialog when it reaches zero.
	TimeoutSeconds int
}

// ExtensionSelectorCallbacks holds the result handlers supplied by the App.
type ExtensionSelectorCallbacks struct {
	OnSelect func(option string) tea.Cmd
	OnCancel func() tea.Cmd
}

// ExtensionSelectorComponent is the generic picker extensions use to ask the
// user to choose from a list of string options — e.g. the confirm "Yes/No"
// dialog or a branch-summary choice. Built on the generic Selector.
//
// An optional countdown timer auto-cancels the dialog after TimeoutSeconds,
// mirroring the timeout option in the TS reference.
//
// Port of reference/coding-agent/src/modes/interactive/components/extension-selector.ts
type ExtensionSelectorComponent struct {
	theme *theme.Theme
	width int

	baseTitle string
	options   []string

	selector Selector[string]
	cb       ExtensionSelectorCallbacks

	// countdown: seconds remaining, 0 = inactive.
	remaining int
}

// NewExtensionSelectorComponent creates a new extension selector. The first
// option is preselected, matching the TS default of index 0.
func NewExtensionSelectorComponent(
	t *theme.Theme,
	title string,
	options []string,
	callbacks ExtensionSelectorCallbacks,
	opts ExtensionSelectorOptions,
) *ExtensionSelectorComponent {
	e := &ExtensionSelectorComponent{
		theme:     t,
		baseTitle: title,
		options:   options,
		cb:        callbacks,
		remaining: opts.TimeoutSeconds,
	}

	displayTitle := title
	if opts.TimeoutSeconds > 0 {
		displayTitle = title + " (" + itoa(opts.TimeoutSeconds) + "s)"
	}

	e.selector = NewSelector[string](
		options,
		t,
		e.filterItem,
		e.renderItem,
		func(option string) tea.Cmd {
			if callbacks.OnSelect != nil {
				return callbacks.OnSelect(option)
			}
			return nil
		},
		callbacks.OnCancel,
		SelectorOptions[string]{
			Title:     displayTitle,
			EmptyText: "No options",
		},
	)

	return e
}

// SetWidth updates the available width.
func (e *ExtensionSelectorComponent) SetWidth(width int) {
	e.width = width
	e.selector.SetWidth(width)
}

// Init starts the countdown tick when a timeout is set.
func (e *ExtensionSelectorComponent) Init() tea.Cmd {
	if e.remaining > 0 {
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return extensionTickMsg{} })
	}
	return e.selector.Init()
}

// Update forwards messages to the selector and drives the countdown timer.
func (e *ExtensionSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Countdown tick: decrement, refresh the title, auto-cancel at zero.
	if _, ok := msg.(extensionTickMsg); ok {
		if e.remaining > 0 {
			e.remaining--
			if e.remaining <= 0 {
				e.remaining = 0
				if e.cb.OnCancel != nil {
					return e, e.cb.OnCancel()
				}
				return e, nil
			}
			e.selector.SetTitle(e.baseTitle + " (" + itoa(e.remaining) + "s)")
			return e, tea.Tick(time.Second, func(time.Time) tea.Msg { return extensionTickMsg{} })
		}
		return e, nil
	}

	var cmd tea.Cmd
	e.selector, cmd = e.selector.Update(msg)
	return e, cmd
}

// View renders the selector.
func (e ExtensionSelectorComponent) View() string {
	return e.selector.View()
}

// --- Internal ---

// filterItem matches an option against the query (fuzzy).
func (e *ExtensionSelectorComponent) filterItem(option string, query string) bool {
	return FuzzyMatch(option, query)
}

// renderItem renders one option row. Selected rows are drawn in accent; the
// Selector adds the "→ " marker itself.
func (e *ExtensionSelectorComponent) renderItem(option string, selected bool) string {
	if selected {
		return e.theme.Fg("accent", option)
	}
	return e.theme.Fg("text", option)
}
