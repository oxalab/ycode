package components

import (
	"regexp"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

// RenderDiffOptions controls diff rendering. Currently a placeholder for
// future options (e.g., filePath-based hunk headers).
type RenderDiffOptions struct {
	// FilePath is reserved for API compatibility with the TS version.
	FilePath string
}

// diffLineParts holds the parsed components of a diff line.
type diffLineParts struct {
	prefix   string // "+", "-", or " "
	lineNum  string
	content  string
}

// parseDiffLine parses a diff line into prefix, line number, and content.
// Format: "+123 content" / "-123 content" / " 123 content" / "     ..."
func parseDiffLine(line string) *diffLineParts {
	// Match: prefix char ([+-\s]), optional line number (\s*\d*), space, then content
	m := diffLineRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	return &diffLineParts{
		prefix:  m[1],
		lineNum: m[2],
		content: m[3],
	}
}

var diffLineRegex = regexp.MustCompile(`^([+\-\s])(\s*\d*)\s(.*)$`)

// replaceTabs replaces tabs with 3 spaces for consistent rendering.
func replaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

// wordDiffPart represents a segment of a word-level diff.
type wordDiffPart struct {
	value   string
	removed bool
	added   bool
}

// diffWords computes a simple word-level diff between old and new strings.
// Groups whitespace with adjacent words for cleaner highlighting.
// This is a simplified LCS-based word diff approximating the TS "diff" library.
func diffWords(oldText, newText string) []wordDiffPart {
	oldWords := splitWordsWithWhitespace(oldText)
	newWords := splitWordsWithWhitespace(newText)

	// Build LCS table
	m, n := len(oldWords), len(newWords)
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldWords[i] == newWords[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var parts []wordDiffPart
	i, j := 0, 0
	for i < m && j < n {
		if oldWords[i] == newWords[j] {
			parts = append(parts, wordDiffPart{value: oldWords[i]})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			parts = append(parts, wordDiffPart{value: oldWords[i], removed: true})
			i++
		} else {
			parts = append(parts, wordDiffPart{value: newWords[j], added: true})
			j++
		}
	}
	for i < m {
		parts = append(parts, wordDiffPart{value: oldWords[i], removed: true})
		i++
	}
	for j < n {
		parts = append(parts, wordDiffPart{value: newWords[j], added: true})
		j++
	}
	return parts
}

// splitWordsWithWhitespace splits a string into tokens where each token is
// either a run of word characters or a run of whitespace.
func splitWordsWithWhitespace(s string) []string {
	wordRe := regexp.MustCompile(`(\s+|\S+)`)
	matches := wordRe.FindAllString(s, -1)
	return matches
}

// renderIntraLineDiff computes word-level diff between old and new content,
// applying inverse styling to the changed parts. Leading whitespace is
// preserved (not inverse-highlighted).
func renderIntraLineDiff(t *theme.Theme, oldContent, newContent string) (removedLine, addedLine string) {
	parts := diffWords(oldContent, newContent)

	isFirstRemoved := true
	isFirstAdded := true

	for _, part := range parts {
		if part.removed {
			value := part.value
			if isFirstRemoved {
				value, removedLine = stripLeadingWhitespace(value, removedLine)
				isFirstRemoved = false
			}
			if value != "" {
				removedLine += t.Inverse(value)
			}
		} else if part.added {
			value := part.value
			if isFirstAdded {
				value, addedLine = stripLeadingWhitespace(value, addedLine)
				isFirstAdded = false
			}
			if value != "" {
				addedLine += t.Inverse(value)
			}
		} else {
			removedLine += part.value
			addedLine += part.value
		}
	}
	return removedLine, addedLine
}

// stripLeadingWhitespace removes leading whitespace from value and appends it
// to the accumulator, returning the stripped value and updated accumulator.
func stripLeadingWhitespace(value, acc string) (string, string) {
	leadingWsRe := regexp.MustCompile(`^(\s*)`)
	leading := leadingWsRe.FindString(value)
	if leading != "" {
		value = value[len(leading):]
		acc += leading
	}
	return value, acc
}

// RenderDiff renders a diff string with colored lines and intra-line change
// highlighting.
//   - Context lines: toolDiffContext color
//   - Removed lines: toolDiffRemoved color, with inverse on changed tokens
//   - Added lines: toolDiffAdded color, with inverse on changed tokens
//
// Port of reference/coding-agent/src/modes/interactive/components/diff.ts
func RenderDiff(diffText string, opts RenderDiffOptions, t *theme.Theme) string {
	lines := strings.Split(diffText, "\n")
	var result []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		parsed := parseDiffLine(line)

		if parsed == nil {
			result = append(result, t.Fg("toolDiffContext", line))
			i++
			continue
		}

		switch parsed.prefix {
		case "-":
			// Collect consecutive removed lines
			var removedLines []diffLineParts
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "-" {
					break
				}
				removedLines = append(removedLines, *p)
				i++
			}

			// Collect consecutive added lines
			var addedLines []diffLineParts
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "+" {
					break
				}
				addedLines = append(addedLines, *p)
				i++
			}

			// Only do intra-line diffing for single-line modifications
			if len(removedLines) == 1 && len(addedLines) == 1 {
				removed := removedLines[0]
				added := addedLines[0]
				removedLine, addedLine := renderIntraLineDiff(
					t,
					replaceTabs(removed.content),
					replaceTabs(added.content),
				)
				result = append(result, t.Fg("toolDiffRemoved", "-"+removed.lineNum+" "+removedLine))
				result = append(result, t.Fg("toolDiffAdded", "+"+added.lineNum+" "+addedLine))
			} else {
				for _, removed := range removedLines {
					result = append(result, t.Fg("toolDiffRemoved", "-"+removed.lineNum+" "+replaceTabs(removed.content)))
				}
				for _, added := range addedLines {
					result = append(result, t.Fg("toolDiffAdded", "+"+added.lineNum+" "+replaceTabs(added.content)))
				}
			}

		case "+":
			// Standalone added line
			result = append(result, t.Fg("toolDiffAdded", "+"+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++

		default:
			// Context line
			result = append(result, t.Fg("toolDiffContext", " "+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		}
	}

	return strings.Join(result, "\n")
}
