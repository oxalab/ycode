package interactive

// dynamic_border.go ports reference DynamicBorder (components/dynamic-border.ts,
// verified — see mapping/10-chrome.md): a horizontal rule that fills the viewport
// width in an optional color. Used by bash-execution (and later selectors).

import (
	"strings"

	"ycode/packages/tui"
)

// dynamicBorder renders one line of "─" repeated to width.
type dynamicBorder struct {
	color tui.StyleFunc
}

// newDynamicBorder builds a border; nil color → plain.
func newDynamicBorder(color tui.StyleFunc) dynamicBorder {
	return dynamicBorder{color: color}
}

func (d dynamicBorder) Render(width int) []string {
	color := d.color
	if color == nil {
		color = func(s string) string { return s }
	}
	return []string{color(strings.Repeat("─", max(1, width)))}
}

func (d dynamicBorder) Invalidate() {}
