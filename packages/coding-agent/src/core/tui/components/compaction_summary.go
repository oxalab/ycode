package components

import (
	"fmt"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// CompactionSummaryComponent renders a compaction summary message with
// collapsed/expanded state. Uses the same background color as custom
// messages (customMessageBg) for visual consistency.
//
// Port of reference/coding-agent/src/modes/interactive/components/compaction-summary-message.ts
type CompactionSummaryComponent struct {
	theme    *theme.Theme
	message  core.CompactionSummaryMessage
	width    int
	expanded bool
}

// NewCompactionSummaryComponent creates a new compaction summary component.
func NewCompactionSummaryComponent(msg core.CompactionSummaryMessage, t *theme.Theme) *CompactionSummaryComponent {
	return &CompactionSummaryComponent{
		theme:   t,
		message: msg,
	}
}

// SetWidth updates the available width.
func (c *CompactionSummaryComponent) SetWidth(width int) {
	c.width = width
}

// SetExpanded toggles the expanded state.
func (c *CompactionSummaryComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
}

// View renders the compaction summary.
func (c CompactionSummaryComponent) View() string {
	width := c.width
	if width <= 0 {
		width = 80
	}

	style := lipgloss.NewStyle().
		Padding(0, 1).
		Width(width - 2)

	// Label: "[compaction]" in customMessageLabel color, bold
	label := c.theme.Fg("customMessageLabel", "\x1b[1m[compaction]\x1b[22m")

	tokenStr := formatThousands(c.message.TokensBefore)

	var body string
	if c.expanded {
		// Show full summary with header
		header := fmt.Sprintf("**Compacted from %s tokens**\n\n", tokenStr)
		rendered := RenderMarkdown(header+c.message.Summary, c.theme)
		body = label + "\n" + c.theme.Fg("customMessageText", rendered)
	} else {
		// Collapsed: hint to expand
		hint := c.theme.Fg("customMessageText", fmt.Sprintf("Compacted from %s tokens (", tokenStr)) +
			c.theme.Fg("dim", "ctrl+o") +
			c.theme.Fg("customMessageText", " to expand)")
		body = label + "\n" + hint
	}

	rendered := style.Render(body)

	// Apply customMessageBg background
	rendered = c.theme.Bg("customMessageBg", rendered)

	return rendered
}

// formatThousands formats an integer with thousands separators (e.g., 1234567 → "1,234,567").
func formatThousands(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	count := 0
	for n > 0 {
		if count > 0 && count%3 == 0 {
			digits = append([]byte{','}, digits...)
		}
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
		count++
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
