package components

import (
	"strings"

	"ycode/packages/tui"
)

// TruncatedText renders a single line of text (first line only), truncated to fit
// the viewport width with optional padding. Every output line is exactly width
// visible columns.
type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

// NewTruncatedText returns a TruncatedText.
func NewTruncatedText(text string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{text: text, paddingX: paddingX, paddingY: paddingY}
}

// SetText updates the text.
func (t *TruncatedText) SetText(text string) { t.text = text }

// Invalidate is a no-op.
func (t *TruncatedText) Invalidate() {}

// Render truncates to width.
func (t *TruncatedText) Render(width int) []string {
	var out []string
	empty := strings.Repeat(" ", width)
	for i := 0; i < t.paddingY; i++ {
		out = append(out, empty)
	}

	available := width - t.paddingX*2
	if available < 1 {
		available = 1
	}

	single := t.text
	if idx := strings.IndexByte(single, '\n'); idx >= 0 {
		single = single[:idx]
	}
	display := tui.TruncateToWidth(single, available, "...", false)

	left := strings.Repeat(" ", t.paddingX)
	right := strings.Repeat(" ", t.paddingX)
	line := left + display + right
	if pad := width - tui.VisibleWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	out = append(out, line)

	for i := 0; i < t.paddingY; i++ {
		out = append(out, empty)
	}
	return out
}
