package components

import "strings"

// DynamicBorder renders a horizontal separator line across the full width,
// applying an optional color function to style it.
//
// This is used as a visual divider between sections (above/below tool executions,
// bash commands, compaction summaries, etc.).
//
// The colorFunc parameter applies ANSI styling to the border characters.
// Example: theme.Fg("border", str) for themed borders, or theme.Fg("dim", str)
// for muted borders.
type DynamicBorder struct {
	width     int
	colorFunc func(string) string
	char      string
}

// NewDynamicBorder creates a new DynamicBorder.
// colorFunc is applied to the border line — pass nil for no styling.
func NewDynamicBorder(colorFunc func(string) string) DynamicBorder {
	return DynamicBorder{
		colorFunc: colorFunc,
		char:      "─",
	}
}

// SetWidth updates the border's rendered width.
func (d *DynamicBorder) SetWidth(width int) {
	d.width = width
}

// SetChar changes the border character (default: "─").
func (d *DynamicBorder) SetChar(char string) {
	d.char = char
}

// View renders the border as a single line string.
func (d DynamicBorder) View() string {
	if d.width <= 0 {
		return ""
	}

	line := strings.Repeat(d.char, d.width)

	if d.colorFunc != nil {
		return d.colorFunc(line)
	}
	return line
}

// Render is a convenience function that renders a border with the given
// width and optional color function in one call.
func Render(width int, colorFunc func(string) string) string {
	d := DynamicBorder{width: width, colorFunc: colorFunc, char: "─"}
	return d.View()
}
