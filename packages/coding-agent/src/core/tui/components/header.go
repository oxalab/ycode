package components

import (
	"fmt"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// Header renders the app header bar: title + version + keybinding hints.
// It's a lightweight component — not a full tea.Model, just a renderer
// that produces styled strings. The parent App calls SetWidth on resize.
type Header struct {
	theme   *theme.Theme
	width   int
	version string
}

// NewHeader creates a new Header component.
func NewHeader(t *theme.Theme, version string) Header {
	return Header{
		theme:   t,
		version: version,
	}
}

// SetWidth updates the header's rendered width (called on resize).
func (h *Header) SetWidth(width int) {
	h.width = width
}

// View renders the header as a two-line string: title line + separator.
func (h Header) View() string {
	if h.theme == nil {
		return ""
	}

	// Title line: " ycode v0.1.0"
	title := h.theme.Bold(" ycode") + h.theme.Dim(fmt.Sprintf(" v%s", h.version))

	// Hints (right-aligned): "Ctrl+C quit • Esc cancel"
	hints := h.theme.Dim("Ctrl+C quit · Esc cancel")

	// Compose on one line if width allows
	if h.width > 0 {
		titleWidth := lipgloss.Width(title)
		hintsWidth := lipgloss.Width(hints)
		gap := h.width - titleWidth - hintsWidth
		if gap < 2 {
			gap = 2
		}
		titleLine := title + strings.Repeat(" ", gap) + hints

		return titleLine
	}

	return title
}
