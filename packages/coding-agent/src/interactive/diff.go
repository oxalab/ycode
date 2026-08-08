package interactive

// diff.go ports reference/coding-agent/src/modes/interactive/components/diff.ts
// (verified — see mapping/12-tools.md): a pure function that colors a unified
// diff and highlights intra-line word changes with inverse video. The Go
// coding-agent already has a faithful port at core/tui/components/diff.go, but
// importing that package would pull in charmbracelet (the BT TUI), so the logic
// is reproduced here against the shared theme.Theme (no charmbracelet, no
// sergi/go-diff — the word-diff is a self-contained LCS).

import (
	"regexp"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

type diffLineParts struct {
	prefix  string // "+", "-", or " "
	lineNum string
	content string
}

var diffLineRegex = regexp.MustCompile(`^([+\-\s])(\s*\d*)\s(.*)$`)

func parseDiffLine(line string) *diffLineParts {
	m := diffLineRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	return &diffLineParts{prefix: m[1], lineNum: m[2], content: m[3]}
}

func replaceTabs(text string) string { return strings.ReplaceAll(text, "\t", "   ") }

type wordDiffPart struct {
	value   string
	removed bool
	added   bool
}

// diffWords computes a simple LCS-based word diff (approximates the TS "diff"
// library's diffWords; groups whitespace with adjacent words).
func diffWords(oldText, newText string) []wordDiffPart {
	oldWords := splitWordsWithWhitespace(oldText)
	newWords := splitWordsWithWhitespace(newText)
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

var wordTokenRe = regexp.MustCompile(`(\s+|\S+)`)

func splitWordsWithWhitespace(s string) []string { return wordTokenRe.FindAllString(s, -1) }

var leadingWsRe = regexp.MustCompile(`^(\s*)`)

func stripLeadingWhitespace(value, acc string) (string, string) {
	leading := leadingWsRe.FindString(value)
	if leading != "" {
		value = value[len(leading):]
		acc += leading
	}
	return value, acc
}

// renderIntraLineDiff applies inverse styling to the changed words of a single
// removed→added line pair (leading whitespace preserved).
func renderIntraLineDiff(t *theme.Theme, oldContent, newContent string) (removedLine, addedLine string) {
	parts := diffWords(oldContent, newContent)
	isFirstRemoved, isFirstAdded := true, true
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

// renderDiff colors a unified diff string: context → toolDiffContext, removed →
// toolDiffRemoved, added → toolDiffAdded, with intra-line inverse highlighting
// for single-line modifications. (Reference diff.ts:79-147.)
func renderDiff(diffText string, t *theme.Theme) string {
	lines := strings.Split(diffText, "\n")
	var result []string
	i := 0
	for i < len(lines) {
		parsed := parseDiffLine(lines[i])
		if parsed == nil {
			result = append(result, t.Fg("toolDiffContext", lines[i]))
			i++
			continue
		}
		switch parsed.prefix {
		case "-":
			var removed []diffLineParts
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "-" {
					break
				}
				removed = append(removed, *p)
				i++
			}
			var added []diffLineParts
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "+" {
					break
				}
				added = append(added, *p)
				i++
			}
			if len(removed) == 1 && len(added) == 1 {
				rl, al := renderIntraLineDiff(t, replaceTabs(removed[0].content), replaceTabs(added[0].content))
				result = append(result, t.Fg("toolDiffRemoved", "-"+removed[0].lineNum+" "+rl))
				result = append(result, t.Fg("toolDiffAdded", "+"+added[0].lineNum+" "+al))
			} else {
				for _, r := range removed {
					result = append(result, t.Fg("toolDiffRemoved", "-"+r.lineNum+" "+replaceTabs(r.content)))
				}
				for _, a := range added {
					result = append(result, t.Fg("toolDiffAdded", "+"+a.lineNum+" "+replaceTabs(a.content)))
				}
			}
		case "+":
			result = append(result, t.Fg("toolDiffAdded", "+"+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		default:
			result = append(result, t.Fg("toolDiffContext", " "+parsed.lineNum+" "+replaceTabs(parsed.content)))
			i++
		}
	}
	return strings.Join(result, "\n")
}

// diffDetectRe matches a unified-diff added/removed line with a line number.
var diffDetectRe = regexp.MustCompile(`(?m)^[\+\-]\s*\d+\s`)

// isDiffLike reports whether text contains unified-diff added/removed lines
// (used to decide whether to color a tool result as a diff).
func isDiffLike(s string) bool { return diffDetectRe.MatchString(s) }
