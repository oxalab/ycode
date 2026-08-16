package components

import (
	"fmt"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// BranchSummaryComponent renders a branch summary message with
// collapsed/expanded state. Uses the same background color as custom
// messages (customMessageBg) for visual consistency.
//
// Port of reference/coding-agent/src/modes/interactive/components/branch-summary-message.ts
type BranchSummaryComponent struct {
	theme    *theme.Theme
	message  core.BranchSummaryMessage
	width    int
	expanded bool
}

// NewBranchSummaryComponent creates a new branch summary component.
func NewBranchSummaryComponent(msg core.BranchSummaryMessage, t *theme.Theme) *BranchSummaryComponent {
	return &BranchSummaryComponent{
		theme:   t,
		message: msg,
	}
}

// SetWidth updates the available width.
func (b *BranchSummaryComponent) SetWidth(width int) {
	b.width = width
}

// SetExpanded toggles the expanded state.
func (b *BranchSummaryComponent) SetExpanded(expanded bool) {
	b.expanded = expanded
}

// View renders the branch summary.
func (b BranchSummaryComponent) View() string {
	width := b.width
	if width <= 0 {
		width = 80
	}

	style := lipgloss.NewStyle().
		Padding(0, 1).
		Width(width - 2)

	// Label: "[branch]" in customMessageLabel color, bold
	label := b.theme.Fg("customMessageLabel", "\x1b[1m[branch]\x1b[22m")

	var body string
	if b.expanded {
		// Show full summary with header
		header := "**Branch Summary**\n\n"
		rendered := RenderMarkdown(header+b.message.Summary, b.theme)
		body = label + "\n" + b.theme.Fg("customMessageText", rendered)
	} else {
		// Collapsed: hint to expand
		hint := b.theme.Fg("customMessageText", "Branch summary (") +
			b.theme.Fg("dim", "ctrl+o") +
			b.theme.Fg("customMessageText", " to expand)")
		body = label + "\n" + hint
	}

	rendered := style.Render(body)

	// Apply customMessageBg background
	rendered = b.theme.Bg("customMessageBg", rendered)

	return rendered
}

// String returns a debug representation.
func (b BranchSummaryComponent) String() string {
	return fmt.Sprintf("BranchSummary{fromID: %s, expanded: %v}", b.message.FromID, b.expanded)
}
