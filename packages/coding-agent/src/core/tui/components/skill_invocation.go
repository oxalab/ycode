package components

import (
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// SkillInvocationComponent renders a skill invocation message with
// collapsed/expanded state. Uses the same background color as custom
// messages (customMessageBg) for visual consistency.
//
// Only renders the skill block itself — the user message is rendered separately.
//
// Port of reference/coding-agent/src/modes/interactive/components/skill-invocation-message.ts
type SkillInvocationComponent struct {
	theme      *theme.Theme
	skillBlock core.ParsedSkillBlock
	width      int
	expanded   bool
}

// NewSkillInvocationComponent creates a new skill invocation component.
func NewSkillInvocationComponent(block core.ParsedSkillBlock, t *theme.Theme) *SkillInvocationComponent {
	return &SkillInvocationComponent{
		theme:      t,
		skillBlock: block,
	}
}

// SetWidth updates the available width.
func (s *SkillInvocationComponent) SetWidth(width int) {
	s.width = width
}

// SetExpanded toggles the expanded state.
func (s *SkillInvocationComponent) SetExpanded(expanded bool) {
	s.expanded = expanded
}

// View renders the skill invocation.
func (s SkillInvocationComponent) View() string {
	width := s.width
	if width <= 0 {
		width = 80
	}

	style := lipgloss.NewStyle().
		Padding(0, 1).
		Width(width - 2)

	var body string
	if s.expanded {
		// Expanded: label + skill name header + full content
		label := s.theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m")
		header := "**" + s.skillBlock.Name + "**\n\n"
		rendered := RenderMarkdown(header+s.skillBlock.Content, s.theme)
		body = label + "\n" + s.theme.Fg("customMessageText", rendered)
	} else {
		// Collapsed: single line - [skill] name (hint to expand)
		line := s.theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m ") +
			s.theme.Fg("customMessageText", s.skillBlock.Name) +
			s.theme.Fg("dim", " (ctrl+o to expand)")
		body = line
	}

	rendered := style.Render(body)

	// Apply customMessageBg background
	rendered = s.theme.Bg("customMessageBg", rendered)

	return rendered
}
