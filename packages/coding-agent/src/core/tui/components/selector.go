package components

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SelectorOptions configures a Selector at creation time.
type SelectorOptions[T any] struct {
	// Title is an optional header rendered above the search input.
	Title string
	// Prompt is the search input prompt (default "> ").
	Prompt string
	// MaxVisible is the number of items shown before scrolling (default 10).
	MaxVisible int
	// EmptyText is shown when the filtered list is empty (default "No matches").
	EmptyText string
	// PreselectIndex sets the initial cursor position.
	PreselectIndex int
	// FooterFn optionally renders extra lines appended after the list (and
	// scroll indicator), mirroring the TS components' list-container tail.
	// It receives the filtered items and cursor so footers can follow the
	// current selection. Called even when the list is empty.
	FooterFn func(filtered []T, cursor int) []string
}

// Selector is a generic, reusable fuzzy-search list used by every picker
// component (model, session, theme, thinking, settings, etc.). It renders:
//
//	┌── border ──┐
//	│ <search input>
//	│ → selected item
//	│   other item
//	│   ...
//	│ (n/total)        ← scroll indicator when list overflows
//	└── border ──┘
//
// Callers supply the item type T plus a filter function and a render function;
// navigation, filtering, scrolling, and selection/cancel handling are built in.
//
// Port of the shared selector pattern from the TS components.
type Selector[T any] struct {
	theme    *theme.Theme
	width    int

	items    []T
	filtered []T
	cursor   int

	search textinput.Model

	maxVisible int
	emptyText  string
	title      string

	// filterFn decides whether an item matches the current query.
	filterFn func(item T, query string) bool
	// renderFn renders a single item; selected is true for the cursor row.
	renderFn func(item T, selected bool) string
	// isSelectedFn optionally marks an item as "current" (e.g. active model)
	// so the renderer can show a checkmark. May be nil.
	isSelectedFn func(item T) bool
	// footerFn optionally renders trailing lines after the list. May be nil.
	footerFn func(filtered []T, cursor int) []string

	// Callbacks invoked on confirm/cancel. They return optional tea.Cmd.
	onSelect func(item T) tea.Cmd
	onCancel func() tea.Cmd

	focused bool
}

// NewSelector creates a new Selector with the given items and callbacks.
func NewSelector[T any](
	items []T,
	t *theme.Theme,
	filterFn func(T, string) bool,
	renderFn func(T, bool) string,
	onSelect func(T) tea.Cmd,
	onCancel func() tea.Cmd,
	opts SelectorOptions[T],
) Selector[T] {
	prompt := opts.Prompt
	if prompt == "" {
		prompt = "> "
	}
	maxVisible := opts.MaxVisible
	if maxVisible <= 0 {
		maxVisible = 10
	}
	emptyText := opts.EmptyText
	if emptyText == "" {
		emptyText = "No matches"
	}

	ti := textinput.New()
	ti.Prompt = prompt
	ti.Placeholder = "Search..."
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 40

	s := Selector[T]{
		theme:     t,
		items:     items,
		filterFn:  filterFn,
		renderFn:  renderFn,
		onSelect:  onSelect,
		onCancel:  onCancel,
		maxVisible: maxVisible,
		emptyText:  emptyText,
		title:      opts.Title,
		footerFn:   opts.FooterFn,
		focused:    true,
		search:     ti,
	}
	s.filtered = items
	if opts.PreselectIndex >= 0 && opts.PreselectIndex < len(s.filtered) {
		s.cursor = opts.PreselectIndex
	}
	return s
}

// SetIsSelectedFn installs a predicate that marks the "current" item.
func (s *Selector[T]) SetIsSelectedFn(fn func(T) bool) {
	s.isSelectedFn = fn
}

// SetWidth updates the available width.
func (s *Selector[T]) SetWidth(width int) {
	s.width = width
	if width > 2 {
		s.search.Width = width - 2
	}
}

// SetItems replaces the item list and re-runs the filter.
func (s *Selector[T]) SetItems(items []T) {
	s.items = items
	s.applyFilter()
}

// SetTitle updates the title rendered above the search input. Useful for
// wrappers that need a live title (e.g. a countdown suffix).
func (s *Selector[T]) SetTitle(title string) {
	s.title = title
}

// SelectedItem returns the item under the cursor, or the zero value if empty.
func (s Selector[T]) SelectedItem() (T, bool) {
	if s.cursor >= 0 && s.cursor < len(s.filtered) {
		return s.filtered[s.cursor], true
	}
	var zero T
	return zero, false
}

// SetFocus toggles input focus (for overlay management).
func (s *Selector[T]) SetFocus(focused bool) {
	s.focused = focused
	if focused {
		s.search.Focus()
	} else {
		s.search.Blur()
	}
}

// Init satisfies tea.Model.
func (s Selector[T]) Init() tea.Cmd { return nil }

// Update handles messages: key navigation, search typing, confirm/cancel.
func (s Selector[T]) Update(msg tea.Msg) (Selector[T], tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			s.moveCursor(-1)
			return s, nil
		case "down", "j":
			s.moveCursor(1)
			return s, nil
		case "enter":
			if item, ok := s.SelectedItem(); ok {
				if s.onSelect != nil {
					return s, s.onSelect(item)
				}
			}
			return s, nil
		case "escape", "ctrl+c":
			if s.onCancel != nil {
				return s, s.onCancel()
			}
			return s, nil
		}
	}

	// All other input (typing) flows to the search field, then re-filter.
	var cmd tea.Cmd
	s.search, cmd = s.search.Update(msg)
	s.applyFilter()
	return s, cmd
}

// View renders the selector: optional title + search + list + scroll indicator.
func (s Selector[T]) View() string {
	width := s.width
	if width <= 0 {
		width = 60
	}

	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width - 2)

	var sections []string

	if s.title != "" {
		// Accent + bold, matching the TS selector title style
		// (theme.fg("accent", theme.bold(title))) used across the reference.
		sections = append(sections, s.theme.Fg("accent", s.theme.Bold(s.title)))
	}

	// Search input
	sections = append(sections, s.search.View())

	// List
	listLines := s.renderList()
	sections = append(sections, strings.Join(listLines, "\n"))

	content := strings.Join(sections, "\n")
	return border.Render(content)
}

// --- Internal ---

// moveCursor moves the cursor by delta, wrapping at the ends.
func (s *Selector[T]) moveCursor(delta int) {
	n := len(s.filtered)
	if n == 0 {
		s.cursor = 0
		return
	}
	s.cursor = ((s.cursor + delta) % n + n) % n
}

// applyFilter re-runs filterFn against the current search value and clamps
// the cursor into range.
func (s *Selector[T]) applyFilter() {
	query := strings.ToLower(s.search.Value())
	if query == "" || s.filterFn == nil {
		s.filtered = s.items
	} else {
		filtered := make([]T, 0, len(s.items))
		for _, item := range s.items {
			if s.filterFn(item, query) {
				filtered = append(filtered, item)
			}
		}
		s.filtered = filtered
	}
	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// renderList renders the visible window of filtered items plus scroll/empty
// indicators.
func (s Selector[T]) renderList() []string {
	n := len(s.filtered)

	// Empty state
	if n == 0 {
		lines := []string{s.theme.Dim("  " + s.emptyText)}
		lines = append(lines, s.renderFooter()...)
		return lines
	}

	// Compute visible window centered on the cursor.
	start := s.cursor - s.maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + s.maxVisible
	if end > n {
		end = n
		start = end - s.maxVisible
		if start < 0 {
			start = 0
		}
	}

	var lines []string
	for i := start; i < end; i++ {
		selected := i == s.cursor
		lines = append(lines, s.renderRow(s.filtered[i], selected))
	}

	// Scroll indicator when the list overflows.
	if start > 0 || end < n {
		lines = append(lines, s.theme.Dim("  ("+itoa(s.cursor+1)+"/"+itoa(n)+")"))
	}

	lines = append(lines, s.renderFooter()...)

	return lines
}

// renderFooter invokes the caller's footer renderer, if any.
func (s Selector[T]) renderFooter() []string {
	if s.footerFn == nil {
		return nil
	}
	return s.footerFn(s.filtered, s.cursor)
}

// renderRow renders one item, augmenting the caller's renderFn with a
// selection marker and current-item checkmark.
func (s Selector[T]) renderRow(item T, selected bool) string {
	// Caller-provided renderer produces the label.
	label := ""
	if s.renderFn != nil {
		label = s.renderFn(item, selected)
	}

	// Marker: "→ " when selected, "  " otherwise.
	marker := "  "
	if selected {
		marker = s.theme.Fg("accent", "→ ")
	}

	// Current-item checkmark.
	check := ""
	if s.isSelectedFn != nil && s.isSelectedFn(item) {
		check = s.theme.Fg("success", " ✓")
	}

	return marker + label + check
}

// ============================================================================
// Fuzzy matching helper
// ============================================================================

// FuzzyMatch reports whether query matches text as a subsequence (case-folded).
// This approximates pi-tui's fuzzyFilter for selector use. Empty query matches.
func FuzzyMatch(text, query string) bool {
	if query == "" {
		return true
	}
	t := strings.ToLower(text)
	q := strings.ToLower(query)
	ti := 0
	for qi := 0; qi < len(q); qi++ {
		found := false
		for ; ti < len(t); ti++ {
			if t[ti] == q[qi] {
				found = true
				ti++
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// FuzzyMatchAny reports whether query fuzzily matches any of the given strings.
func FuzzyMatchAny(query string, texts ...string) bool {
	for _, t := range texts {
		if FuzzyMatch(t, query) {
			return true
		}
	}
	return false
}

// itoa converts a non-negative int to its decimal string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
