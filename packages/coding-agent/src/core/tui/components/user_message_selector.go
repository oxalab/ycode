package components

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// UserMessageItem is one row in the user-message selector: the session entry
// id plus the message text. The App builds these from session history.
type UserMessageItem struct {
	ID        string
	Text      string
	Timestamp string // optional, shown if present
}

// UserMessageSelectorCallbacks holds the result handlers supplied by the App.
type UserMessageSelectorCallbacks struct {
	OnSelect func(entryID string) tea.Cmd
	OnCancel func() tea.Cmd
}

// UserMessageSelectorComponent renders a list of user messages for "fork from
// message" branching. Each message occupies two rendered lines (text +
// position metadata) plus a blank separator, with wrapping cursor navigation.
//
// Port of reference/coding-agent/src/modes/interactive/components/user-message-selector.ts
type UserMessageSelectorComponent struct {
	theme      *theme.Theme
	width      int
	maxVisible int

	messages []UserMessageItem
	cursor   int

	cb UserMessageSelectorCallbacks
}

// NewUserMessageSelectorComponent creates a new user-message selector.
// initialSelectedID preselects a message; otherwise the most recent is selected.
func NewUserMessageSelectorComponent(
	t *theme.Theme,
	messages []UserMessageItem,
	initialSelectedID string,
	callbacks UserMessageSelectorCallbacks,
) *UserMessageSelectorComponent {
	s := &UserMessageSelectorComponent{
		theme:      t,
		messages:   messages,
		maxVisible: 10,
		cb:         callbacks,
	}

	idx := -1
	if initialSelectedID != "" {
		for i, m := range messages {
			if m.ID == initialSelectedID {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		idx = len(messages) - 1 // most recent
	}
	if idx < 0 {
		idx = 0
	}
	s.cursor = idx

	return s
}

// SetWidth updates the available width.
func (s *UserMessageSelectorComponent) SetWidth(width int) { s.width = width }

// Init satisfies tea.Model.
func (s *UserMessageSelectorComponent) Init() tea.Cmd { return nil }

// Update handles navigation (with wrap), confirm, and cancel.
func (s *UserMessageSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	n := len(s.messages)

	switch keyMsg.String() {
	case "up", "k":
		if n > 0 {
			if s.cursor == 0 {
				s.cursor = n - 1
			} else {
				s.cursor--
			}
		}
		return s, nil
	case "down", "j":
		if n > 0 {
			if s.cursor == n-1 {
				s.cursor = 0
			} else {
				s.cursor++
			}
		}
		return s, nil
	case "enter":
		if s.cursor >= 0 && s.cursor < n {
			if s.cb.OnSelect != nil {
				return s, s.cb.OnSelect(s.messages[s.cursor].ID)
			}
		}
		return s, nil
	case "escape", "ctrl+c":
		if s.cb.OnCancel != nil {
			return s, s.cb.OnCancel()
		}
		return s, nil
	}
	return s, nil
}

// View renders the header + message list + scroll indicator.
func (s UserMessageSelectorComponent) View() string {
	var b strings.Builder
	b.WriteString(s.theme.Bold("Fork from Message") + "\n")
	b.WriteString(s.theme.Fg("muted", "Select a user message to copy the active path up to that point into a new session") + "\n\n")

	if len(s.messages) == 0 {
		b.WriteString(s.theme.Fg("muted", "  No user messages found"))
		return b.String()
	}

	width := s.width
	if width <= 0 {
		width = 70
	}

	n := len(s.messages)
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

	for i := start; i < end; i++ {
		msg := s.messages[i]
		selected := i == s.cursor

		// Normalize to a single line and truncate to the available width.
		normalized := strings.TrimSpace(strings.ReplaceAll(msg.Text, "\n", " "))
		maxWidth := width - 2 // account for the 2-char cursor
		text := truncateVisual(normalized, maxWidth)

		cursor := "  "
		if selected {
			cursor = s.theme.Fg("accent", "› ")
			text = s.theme.Bold(text)
		}
		b.WriteString(cursor + text + "\n")

		// Metadata line: position in history (+ optional timestamp).
		meta := "Message " + itoa(i+1) + " of " + itoa(n)
		if msg.Timestamp != "" {
			meta = msg.Timestamp + " · " + meta
		}
		b.WriteString(s.theme.Fg("muted", "  "+meta) + "\n\n")
	}

	if start > 0 || end < n {
		b.WriteString(s.theme.Fg("muted", "  ("+itoa(s.cursor+1)+"/"+itoa(n)+")"))
	}
	return b.String()
}

// truncateVisual shortens s to max visual cells, appending an ellipsis when cut.
// Uses rune count as a simple width approximation (good enough for previews).
func truncateVisual(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
