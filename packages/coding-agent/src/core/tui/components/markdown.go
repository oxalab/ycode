package components

import (
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
)

// markdownRenderer is a lazily-initialized glamour renderer tied to a theme.
// Create one per theme change and reuse for all markdown rendering calls.
type markdownRenderer struct {
	renderer *glamour.TermRenderer
	theme    *theme.Theme
}

// globalRenderer caches the current renderer so we don't recreate on every call.
var globalRenderer *markdownRenderer

// RenderMarkdown renders a markdown string using the current theme colors.
// It wraps glamour with custom style configuration derived from the project theme.
//
// If the theme is nil or rendering fails, returns the raw text as a fallback.
func RenderMarkdown(text string, t *theme.Theme) string {
	if t == nil || text == "" {
		return text
	}

	r := getRenderer(t)
	if r == nil {
		return text
	}

	out, err := r.renderer.Render(text)
	if err != nil {
		return text
	}
	return out
}

// getRenderer returns a cached glamour renderer, creating one if the theme changed.
func getRenderer(t *theme.Theme) *markdownRenderer {
	if globalRenderer != nil && globalRenderer.theme == t {
		return globalRenderer
	}

	styles := buildGlamourStyles(t)
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(styles),
		glamour.WithWordWrap(0), // we handle wrapping at the viewport level
	)
	if err != nil {
		return nil
	}

	globalRenderer = &markdownRenderer{
		renderer: r,
		theme:    t,
	}
	return globalRenderer
}

// buildGlamourStyles maps the project theme colors to a glamour ansi.StyleConfig.
func buildGlamourStyles(t *theme.Theme) ansi.StyleConfig {
	c := t.GetResolvedColors()
	if c == nil {
		return ansi.StyleConfig{}
	}

	// Helper to convert a hex color string to an ansi.StylePrimitive.
	// Empty string means "no override" (use terminal default).
	sp := func(hex string) ansi.StylePrimitive {
		if hex == "" {
			return ansi.StylePrimitive{}
		}
		return ansi.StylePrimitive{
			Color: &hex,
		}
	}

	// Helper for a block with foreground color.
	sblock := func(hex string) ansi.StyleBlock {
		return ansi.StyleBlock{
			StylePrimitive: sp(hex),
		}
	}

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				// Base text color from theme
				Color: ptrStr(resolveHex(c.Text, t)),
			},
		},
		H1:            sblock(resolveHex(c.MdHeading, t)),
		H2:            sblock(resolveHex(c.MdHeading, t)),
		H3:            sblock(resolveHex(c.MdHeading, t)),
		H4:            sblock(resolveHex(c.MdHeading, t)),
		H5:            sblock(resolveHex(c.MdHeading, t)),
		H6:            sblock(resolveHex(c.MdHeading, t)),
		Strikethrough: sp(resolveHex(c.Dim, t)),
		Emph:          sp(resolveHex(c.Text, t)),
		Strong:        sp(resolveHex(c.Text, t)),
		HorizontalRule: ansi.StylePrimitive{
			Color: ptrStr(resolveHex(c.MdHr, t)),
		},
		Item: ansi.StylePrimitive{
			Color: ptrStr(resolveHex(c.MdListBullet, t)),
		},
		Enumeration: ansi.StylePrimitive{
			Color: ptrStr(resolveHex(c.MdListBullet, t)),
		},
		Link:     sp(resolveHex(c.MdLink, t)),
		LinkText: sp(resolveHex(c.MdLink, t)),
		Code: sblock(resolveHex(c.MdCode, t)),
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: sblock(resolveHex(c.MdCodeBlock, t)),
		},
		BlockQuote: sblock(resolveHex(c.MdQuote, t)),
		Paragraph: ansi.StyleBlock{
			StylePrimitive: sp(resolveHex(c.Text, t)),
		},
		Text: sp(resolveHex(c.Text, t)),
	}
}

// resolveHex returns the hex color string for a theme color value.
// Theme color values may be empty (default), a variable reference, or a hex.
// Since the theme has already resolved variables, we return as-is.
func resolveHex(colorVal string, t *theme.Theme) string {
	if colorVal == "" {
		return ""
	}
	// Theme colors are already resolved at theme creation time.
	// If it looks like a hex color, return it directly.
	if len(colorVal) > 0 && colorVal[0] == '#' {
		return colorVal
	}
	// Otherwise it might be a CSS color name — glamour handles those.
	// But for ANSI output, convert known names to nothing (let terminal decide).
	return ""
}

// ptrStr returns a pointer to the string, or nil if empty.
func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
