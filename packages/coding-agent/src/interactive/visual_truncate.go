package interactive

// visual_truncate.go ports reference components/visual-truncate.ts (verified —
// see mapping/12-tools.md). Built on top of tui.WrapTextWithAnsi, which already
// preserves ANSI codes across wrapped line breaks.
//
// The reference wraps via `new Text(text, paddingX, 0).render(width)`; we mirror
// Go Text.Render's no-bgFn path exactly (normalize tabs to 3 spaces, wrap, then
// pad each line left+right and fill to full width) so the result is
// interchangeable with a NewText sibling in the same Container.

import (
	"strings"

	"ycode/packages/tui"
)

// VisualTruncateResult is the output of TruncateToVisualLines.
type VisualTruncateResult struct {
	// VisualLines is the last N visual lines (already padded to width).
	VisualLines []string
	// SkippedCount is how many visual lines were dropped from the front.
	SkippedCount int
}

// TruncateToVisualLines truncates text to the last maxVisualLines visual lines
// (accounting for word-wrap at the given width). Port of reference
// components/visual-truncate.ts.
//
// paddingX mirrors the Text component's horizontal padding — use 0 when the
// result will be placed in a Box (Box adds its own padding), 1 when placed in a
// plain Container. Returned lines are pre-padded and width-justified so they
// drop straight into a Container next to NewText siblings.
func TruncateToVisualLines(text string, maxVisualLines, width, paddingX int) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{}
	}
	if paddingX < 0 {
		paddingX = 0
	}
	if maxVisualLines < 0 {
		maxVisualLines = 0
	}
	contentWidth := width - paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}

	// Mirror Text.Render: tabs → 3 spaces, then wrap preserving ANSI codes.
	normalized := strings.ReplaceAll(text, "\t", "   ")
	wrapped := tui.WrapTextWithAnsi(normalized, contentWidth)

	pad := strings.Repeat(" ", paddingX)
	all := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		padded := pad + line + pad
		if extra := width - tui.VisibleWidth(padded); extra > 0 {
			padded += strings.Repeat(" ", extra)
		}
		all = append(all, padded)
	}

	if len(all) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: all, SkippedCount: 0}
	}
	skipped := len(all) - maxVisualLines
	last := append([]string(nil), all[len(all)-maxVisualLines:]...)
	return VisualTruncateResult{VisualLines: last, SkippedCount: skipped}
}

// visualTruncateText is a Component that renders its input via
// TruncateToVisualLines, caching by (text, width). It mirrors the inline
// {render, invalidate} component the reference bash-execution.ts uses for its
// collapsed preview.
type visualTruncateText struct {
	text           string
	maxVisualLines int
	paddingX       int

	cachedText  string
	cachedWidth int
	cachedLines []string
}

// newVisualTruncateText builds a Component that returns the last
// maxVisualLines visual lines of text, padded with paddingX, recomputed
// lazily on width changes.
func newVisualTruncateText(text string, maxVisualLines, paddingX int) *visualTruncateText {
	return &visualTruncateText{text: text, maxVisualLines: maxVisualLines, paddingX: paddingX}
}

// SetText updates the input and invalidates the cache.
func (v *visualTruncateText) SetText(text string) {
	v.text = text
	v.invalidate()
}

// Render returns the truncated visual lines, recomputing only when text or
// width changes.
func (v *visualTruncateText) Render(width int) []string {
	if v.cachedLines != nil && v.cachedText == v.text && v.cachedWidth == width {
		return v.cachedLines
	}
	result := TruncateToVisualLines(v.text, v.maxVisualLines, width, v.paddingX)
	v.cachedText = v.text
	v.cachedWidth = width
	v.cachedLines = result.VisualLines
	return result.VisualLines
}

// Invalidate clears the cache (satisfies tui.Invalidater).
func (v *visualTruncateText) Invalidate() { v.invalidate() }

func (v *visualTruncateText) invalidate() {
	v.cachedText = ""
	v.cachedWidth = 0
	v.cachedLines = nil
}
