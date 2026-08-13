package exporthtml

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ANSI escape code to HTML converter.
// Direct port of reference/coding-agent/src/core/export-html/ansi-to-html.ts.
//
// Supports:
//   - Standard foreground colors (30-37) and bright variants (90-97)
//   - Standard background colors (40-47) and bright variants (100-107)
//   - 256-color palette (38;5;N and 48;5;N)
//   - RGB true color (38;2;R;G;B and 48;2;R;G;B)
//   - Text styles: bold (1), dim (2), italic (3), underline (4)
//   - Reset (0)

// Standard ANSI color palette (0-15)
var ansiColors = [16]string{
	"#000000", // 0: black
	"#800000", // 1: red
	"#008000", // 2: green
	"#808000", // 3: yellow
	"#000080", // 4: blue
	"#800080", // 5: magenta
	"#008080", // 6: cyan
	"#c0c0c0", // 7: white
	"#808080", // 8: bright black
	"#ff0000", // 9: bright red
	"#00ff00", // 10: bright green
	"#ffff00", // 11: bright yellow
	"#0000ff", // 12: bright blue
	"#ff00ff", // 13: bright magenta
	"#00ffff", // 14: bright cyan
	"#ffffff", // 15: bright white
}

// color256ToHex converts a 256-color index to a hex color string.
func color256ToHex(index int) string {
	if index < 16 {
		return ansiColors[index]
	}

	// Color cube (16-231): 6x6x6 = 216 colors
	if index < 232 {
		cubeIndex := index - 16
		r := cubeIndex / 36
		g := (cubeIndex % 36) / 6
		b := cubeIndex % 6
		toComponent := func(n int) int {
			if n == 0 {
				return 0
			}
			return 55 + n*40
		}
		return fmt.Sprintf("#%02x%02x%02x", toComponent(r), toComponent(g), toComponent(b))
	}

	// Grayscale (232-255): 24 shades
	gray := 8 + (index-232)*10
	return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
}

// escapeHtml escapes HTML special characters.
func escapeHtml(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	text = strings.ReplaceAll(text, "'", "&#039;")
	return text
}

// textStyle tracks active ANSI styling.
type textStyle struct {
	fg        string
	bg        string
	bold      bool
	dim       bool
	italic    bool
	underline bool
}

func createEmptyStyle() textStyle {
	return textStyle{}
}

func styleToInlineCSS(s textStyle) string {
	var parts []string
	if s.fg != "" {
		parts = append(parts, "color:"+s.fg)
	}
	if s.bg != "" {
		parts = append(parts, "background-color:"+s.bg)
	}
	if s.bold {
		parts = append(parts, "font-weight:bold")
	}
	if s.dim {
		parts = append(parts, "opacity:0.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

func hasStyle(s textStyle) bool {
	return s.fg != "" || s.bg != "" || s.bold || s.dim || s.italic || s.underline
}

// applySgrCode parses ANSI SGR (Select Graphic Rendition) codes and updates style.
func applySgrCode(params []int, s *textStyle) {
	i := 0
	for i < len(params) {
		code := params[i]

		switch {
		case code == 0:
			// Reset all
			*s = createEmptyStyle()
		case code == 1:
			s.bold = true
		case code == 2:
			s.dim = true
		case code == 3:
			s.italic = true
		case code == 4:
			s.underline = true
		case code == 22:
			s.bold = false
			s.dim = false
		case code == 23:
			s.italic = false
		case code == 24:
			s.underline = false
		case code >= 30 && code <= 37:
			// Standard foreground colors
			s.fg = ansiColors[code-30]
		case code == 38:
			// Extended foreground color
			if i+1 < len(params) && params[i+1] == 5 && i+2 < len(params) {
				// 256-color: 38;5;N
				s.fg = color256ToHex(params[i+2])
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 && i+4 < len(params) {
				// RGB: 38;2;R;G;B
				s.fg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 39:
			s.fg = ""
		case code >= 40 && code <= 47:
			// Standard background colors
			s.bg = ansiColors[code-40]
		case code == 48:
			// Extended background color
			if i+1 < len(params) && params[i+1] == 5 && i+2 < len(params) {
				// 256-color: 48;5;N
				s.bg = color256ToHex(params[i+2])
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 && i+4 < len(params) {
				// RGB: 48;2;R;G;B
				s.bg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 49:
			s.bg = ""
		case code >= 90 && code <= 97:
			// Bright foreground colors
			s.fg = ansiColors[code-90+8]
		case code >= 100 && code <= 107:
			// Bright background colors
			s.bg = ansiColors[code-100+8]
		}
		// Ignore unrecognized codes

		i++
	}
}

// Match ANSI escape sequences: ESC[ followed by params and ending with 'm'
var ansiRegex = regexp.MustCompile(`\x1b\[([\d;]*)m`)

// AnsiToHtml converts ANSI-escaped text to HTML with inline styles.
// Mirrors TS ansiToHtml() (ansi-to-html.ts:198-250).
func AnsiToHtml(text string) string {
	style := createEmptyStyle()
	var result strings.Builder
	lastIndex := 0
	inSpan := false

	matches := ansiRegex.FindAllStringSubmatchIndex(text, -1)

	for _, match := range matches {
		fullStart, fullEnd := match[0], match[1]
		groupStart, groupEnd := match[2], match[3]

		// Add text before this escape sequence
		if fullStart > lastIndex {
			result.WriteString(escapeHtml(text[lastIndex:fullStart]))
		}

		// Close existing span if we have one
		if inSpan {
			result.WriteString("</span>")
			inSpan = false
		}

		// Parse SGR parameters
		paramStr := text[groupStart:groupEnd]
		var params []int
		if paramStr != "" {
			for _, p := range strings.Split(paramStr, ";") {
				n, _ := strconv.Atoi(p)
				params = append(params, n)
			}
		} else {
			params = []int{0}
		}

		// Apply the codes
		applySgrCode(params, &style)

		// Open new span if we have any styling
		if hasStyle(style) {
			result.WriteString(fmt.Sprintf(`<span style="%s">`, styleToInlineCSS(style)))
			inSpan = true
		}

		lastIndex = fullEnd
	}

	// Add remaining text
	if lastIndex < len(text) {
		result.WriteString(escapeHtml(text[lastIndex:]))
	}

	// Close any open span
	if inSpan {
		result.WriteString("</span>")
	}

	return result.String()
}

// AnsiLinesToHtml converts a slice of ANSI-escaped lines to HTML.
// Each line is wrapped in a div element.
// Mirrors TS ansiLinesToHtml() (ansi-to-html.ts:256-258).
func AnsiLinesToHtml(lines []string) string {
	var result strings.Builder
	for _, line := range lines {
		html := AnsiToHtml(line)
		if html == "" {
			html = "&nbsp;"
		}
		result.WriteString(`<div class="ansi-line">`)
		result.WriteString(html)
		result.WriteString("</div>")
	}
	return result.String()
}
