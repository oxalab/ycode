package components

import "strings"

// VisualTruncateResult holds the result of truncating text to visual lines.
type VisualTruncateResult struct {
	// VisualLines is the list of rendered visual lines to display.
	VisualLines []string
	// SkippedCount is the number of visual lines that were dropped (hidden).
	SkippedCount int
}

// TruncateToVisualLines truncates text to a maximum number of visual lines,
// taking the last N lines (tail). It accounts for:
//   - Line wrapping based on terminal width
//   - ANSI escape codes (which don't consume visual width)
//   - Horizontal padding (paddingX)
//
// Parameters:
//   - text:         the text content (may contain newlines and ANSI codes)
//   - maxVisualLines: maximum number of visual lines to keep
//   - width:        terminal/render width in cells
//   - paddingX:     horizontal padding (use 0 for Box, 1 for plain Container)
//
// Port of reference/coding-agent/src/modes/interactive/components/visual-truncate.ts
func TruncateToVisualLines(text string, maxVisualLines, width, paddingX int) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{VisualLines: []string{}, SkippedCount: 0}
	}

	allVisualLines := computeVisualLines(text, width, paddingX)

	if len(allVisualLines) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: allVisualLines, SkippedCount: 0}
	}

	// Take the last N visual lines
	start := len(allVisualLines) - maxVisualLines
	truncated := allVisualLines[start:]
	return VisualTruncateResult{
		VisualLines:  truncated,
		SkippedCount: len(allVisualLines) - maxVisualLines,
	}
}

// computeVisualLines breaks text into visual lines, wrapping at the given width
// after accounting for horizontal padding and ANSI escape sequences.
func computeVisualLines(text string, width, paddingX int) []string {
	if width <= paddingX {
		// Degenerate width — can't wrap meaningfully, just split on newlines
		return strings.Split(text, "\n")
	}
	usableWidth := width - paddingX

	// First split into logical lines (on explicit newlines)
	logicalLines := strings.Split(text, "\n")

	var visualLines []string
	for _, logical := range logicalLines {
		wrapped := wrapVisualLine(logical, usableWidth)
		visualLines = append(visualLines, wrapped...)
	}
	return visualLines
}

// wrapVisualLine wraps a single logical line (which may contain ANSI codes)
// into visual lines no wider than maxWidth cells.
func wrapVisualLine(line string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{line}
	}

	var result []string
	var current strings.Builder
	currentWidth := 0

	// Walk the string, handling ANSI escape sequences as zero-width.
	i := 0
	for i < len(line) {
		// Check for ANSI escape sequence
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			// Find the end of the CSI sequence (letter in 0x40-0x7E)
			j := i + 2
			for j < len(line) {
				c := line[j]
				if c >= 0x40 && c <= 0x7E {
					j++
					break
				}
				j++
			}
			// Append the escape sequence verbatim (zero visual width)
			current.WriteString(line[i:j])
			i = j
			continue
		}

		// Regular rune — decode it to measure width
		r, size := decodeRune(line[i:])
		runeWidth := runeVisualWidth(r)

		// If this rune would overflow, flush the current line
		if currentWidth+runeWidth > maxWidth && current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
			currentWidth = 0
		}

		current.WriteString(line[i : i+size])
		currentWidth += runeWidth
		i += size
	}

	// Flush remaining
	result = append(result, current.String())
	return result
}

// decodeRune decodes a single UTF-8 rune from the start of s, returning the
// rune and its byte length. Falls back to a single byte for invalid UTF-8.
func decodeRune(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	// Fast path for ASCII
	if s[0] < 0x80 {
		return rune(s[0]), 1
	}
	r, size := decodeUTF8(s)
	if size == 0 {
		// Invalid byte — treat as replacement, consume 1 byte
		return 0xFFFD, 1
	}
	return r, size
}

// decodeUTF8 decodes a UTF-8 sequence. Returns the rune and byte length,
// or (0, 0) if the sequence is invalid.
func decodeUTF8(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	b0 := s[0]
	var expectedLen int
	switch {
	case b0 < 0x80:
		return rune(b0), 1
	case b0 < 0xC0:
		return 0, 0 // continuation byte at start — invalid
	case b0 < 0xE0:
		expectedLen = 2
	case b0 < 0xF0:
		expectedLen = 3
	default:
		expectedLen = 4
	}
	if len(s) < expectedLen {
		return 0, 0
	}
	// Validate continuation bytes
	for k := 1; k < expectedLen; k++ {
		if s[k]&0xC0 != 0x80 {
			return 0, 0
		}
	}
	var r rune
	switch expectedLen {
	case 2:
		r = rune(b0&0x1F)<<6 | rune(s[1]&0x3F)
	case 3:
		r = rune(b0&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F)
	case 4:
		r = rune(b0&0x07)<<18 | rune(s[1]&0x3F)<<12 | rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F)
	}
	return r, expectedLen
}

// runeVisualWidth returns the display width of a rune in terminal cells.
// East Asian wide/full characters take 2 cells; most others take 1.
// Control characters take 0 cells.
func runeVisualWidth(r rune) int {
	if r == 0 {
		return 0
	}
	if r < 0x20 || (r >= 0x7F && r < 0xA0) {
		// Control characters
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// isWideRune reports whether a rune is East Asian Wide or Fullwidth,
// taking 2 cells in a monospace terminal. This covers CJK, emoji ranges, etc.
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return true
	case r >= 0x2E80 && r <= 0x303E: // CJK Radicals, Kangxi
		return true
	case r >= 0x3041 && r <= 0x33FF: // Hiragana, Katakana, CJK symbols
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Ext A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified
		return true
	case r >= 0xA000 && r <= 0xA4CF: // Yi
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul Syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility
		return true
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility Forms
		return true
	case r >= 0xFF00 && r <= 0xFF60: // Fullwidth Forms
		return true
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth Signs
		return true
	case r >= 0x1F300 && r <= 0x1FAFF: // Emoji & symbols
		return true
	case r >= 0x20000 && r <= 0x3FFFD: // CJK Ext B-F
		return true
	}
	return false
}
