package interactive

// diff.go ports reference/coding-agent/src/modes/interactive/components/diff.ts
// (verified — see mapping/12-tools.md): a pure function that colors a unified
// diff and highlights intra-line word changes with inverse video. The Go
// coding-agent already has a faithful port at core/tui/components/diff.go, but
// importing that package would pull in charmbracelet (the BT TUI), so the logic
// is reproduced here against the shared theme.Theme.
//
// Word-level diffing uses github.com/sergi/go-diff/diffmatchpatch (the mapping
// note's recommended substitute for the TS "diff" npm package's diffWords):
// words are token-encoded into runes, DiffMainRunes diffs the encoded
// sequences, and the tokens are decoded back — the same trick dmp itself uses
// for line-mode diffing (DiffLinesToRunes).

import (
	"regexp"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

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

// wordRuneBase is the first rune used for token encoding. Words map to
// wordRuneBase + index in the vocabulary. Kept clear of the surrogate range
// and beyond typical text so collisions with real content are impossible in
// the encoded domain (words never appear there — only vocab indices do).
const wordRuneBase = 0x100000

// diffWords computes a word-level diff via diffmatchpatch, approximating the
// TS diff library's diffWords (whitespace groups with the adjacent word).
//
// Token encoding: build a vocabulary of unique whitespace-or-word tokens from
// both inputs, map each token to a rune (wordRuneBase + index), and run
// DiffMainRunes over the two rune slices. Because each token is a single rune,
// dmp treats every word as an atomic unit — matching how diffWords behaves.
// checklines=false keeps dmp from doing its own line-mode pre-pass, which
// would be meaningless over token-encoded input.
func diffWords(oldText, newText string) []wordDiffPart {
	oldTokens := splitWordsWithWhitespace(oldText)
	newTokens := splitWordsWithWhitespace(newText)

	vocab := make(map[string]rune, len(oldTokens)+len(newTokens))
	reverseVocab := make(map[rune]string)
	encode := func(tokens []string) []rune {
		out := make([]rune, 0, len(tokens))
		for _, tok := range tokens {
			r, ok := vocab[tok]
			if !ok {
				r = wordRuneBase + rune(len(vocab))
				vocab[tok] = r
				reverseVocab[r] = tok
			}
			out = append(out, r)
		}
		return out
	}
	oldRunes := encode(oldTokens)
	newRunes := encode(newTokens)

	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMainRunes(oldRunes, newRunes, false)
	// Merge fragmented adjacent ops so changed words render as one inverse
	// span per side (matches diffWords' merged part output). In the encoded
	// domain every rune is a whole token, so merging only ever happens at
	// word boundaries — a word is never split mid-token.
	diffs = dmp.DiffCleanupMerge(diffs)

	// Decode each diff chunk's text back into its tokens. A chunk's Text is a
	// run of encoded runes; each rune expands to its full token.
	decode := func(encoded string) string {
		var b strings.Builder
		for _, r := range encoded {
			b.WriteString(reverseVocab[r])
		}
		return b.String()
	}

	parts := make([]wordDiffPart, 0, len(diffs))
	for _, d := range diffs {
		part := wordDiffPart{value: decode(d.Text)}
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			part.removed = true
		case diffmatchpatch.DiffInsert:
			part.added = true
		}
		parts = append(parts, part)
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
