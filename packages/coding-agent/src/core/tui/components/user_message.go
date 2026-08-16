package components

import (
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// UserMessageComponent renders a user message as a styled bubble with
// the theme's userMessageBg background and userMessageText foreground.
//
// Port of reference/coding-agent/src/modes/interactive/components/user-message.ts
type UserMessageComponent struct {
	theme  *theme.Theme
	text   string
	width  int
}

// NewUserMessageComponent creates a new user message bubble.
func NewUserMessageComponent(text string, t *theme.Theme) UserMessageComponent {
	return UserMessageComponent{
		theme: t,
		text:  text,
	}
}

// SetWidth updates the available width for rendering.
func (u *UserMessageComponent) SetWidth(width int) {
	u.width = width
}

// View renders the user message as a styled bubble.
func (u UserMessageComponent) View() string {
	if u.text == "" {
		return ""
	}

	width := u.width
	if width <= 0 {
		width = 80
	}

	// Render text with padding
	style := lipgloss.NewStyle().
		Padding(0, 1).
		Width(width - 2)

	content := u.text

	// Apply userMessageText foreground color
	if u.theme != nil {
		content = u.theme.Fg("userMessageText", content)
	}

	rendered := style.Render(content)

	// Apply userMessageBg background to the entire rendered block
	if u.theme != nil {
		rendered = u.theme.Bg("userMessageBg", rendered)
	}

	// Wrap with OSC 133 zone markers
	return osc133ZoneStart + rendered + osc133ZoneEnd + osc133ZoneFinal
}
