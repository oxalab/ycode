package tui

// This file is a Go port of reference/tui/src/word-navigation.ts.
//
// KNOWN DEVIATION: the reference uses Node's Intl.Segmenter (word granularity),
// which performs dictionary-based segmentation for CJK text (e.g. "你好世界" →
// "你好" + "世界"). Reproducing that requires a bundled Chinese/Japanese/Korean
// dictionary, which is out of scope for a self-contained port. Instead, CJK
// ideographs segment per-grapheme (a common editor fallback). Latin/digit runs,
// whitespace, and ASCII punctuation match the reference exactly.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// WordSegment is one token produced by a word segmenter.
type WordSegment struct {
	Text       string
	IsWordLike bool
}

// WordNavOptions controls word navigation. Segment overrides the default word
// segmenter; IsAtomic marks segments (e.g. paste markers) that move as one unit.
type WordNavOptions struct {
	Segment  func(string) []WordSegment
	IsAtomic func(string) bool
}

// SegmentWords is the default word segmenter: maximal runs of letters/digits are
// word segments, whitespace runs and punctuation runs are their own segments,
// and CJK ideographs segment per-grapheme.
func SegmentWords(s string) []WordSegment {
	var segs []WordSegment
	var cur strings.Builder
	curWordLike := false
	curActive := false

	flush := func() {
		if curActive {
			segs = append(segs, WordSegment{Text: cur.String(), IsWordLike: curWordLike})
			cur.Reset()
			curActive = false
		}
	}

	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		seg := gr.Str()
		r := firstRune(seg)
		switch {
		case unicode.IsSpace(r):
			flush()
			segs = append(segs, WordSegment{Text: seg, IsWordLike: false})
		case isCJKRune([]rune{r}):
			// CJK ideographs always segment per-grapheme.
			flush()
			segs = append(segs, WordSegment{Text: seg, IsWordLike: true})
		default:
			wordLike := unicode.IsLetter(r) || unicode.IsDigit(r)
			if curActive && curWordLike == wordLike {
				cur.WriteString(seg)
			} else {
				flush()
				cur.WriteString(seg)
				curWordLike = wordLike
				curActive = true
			}
		}
	}
	flush()
	return segs
}

// FindWordBackward returns the cursor position after moving one word backward.
// cursor is a rune index into text.
func FindWordBackward(text string, cursor int, opts *WordNavOptions) int {
	if cursor <= 0 {
		return 0
	}
	runes := []rune(text)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	before := string(runes[:cursor])

	segs, isAtomic := resolveSegmenter(before, opts)

	newCursor := cursor

	// Skip trailing whitespace segments.
	for len(segs) > 0 {
		last := segs[len(segs)-1]
		if (isAtomic != nil && isAtomic(last.Text)) || !isSpaceSegment(last) {
			break
		}
		newCursor -= utf8.RuneCountInString(last.Text)
		segs = segs[:len(segs)-1]
	}
	if len(segs) == 0 {
		return newCursor
	}

	last := segs[len(segs)-1]
	switch {
	case isAtomic != nil && isAtomic(last.Text):
		newCursor -= utf8.RuneCountInString(last.Text)
	case last.IsWordLike:
		end := lastPunctuationEnd(last.Text)
		if end < 0 {
			newCursor -= utf8.RuneCountInString(last.Text)
		} else {
			newCursor -= utf8.RuneCountInString(last.Text) - end
		}
	default:
		// Skip trailing non-word, non-whitespace (punctuation) run.
		for len(segs) > 0 {
			l := segs[len(segs)-1]
			if (isAtomic != nil && isAtomic(l.Text)) || l.IsWordLike || isSpaceSegment(l) {
				break
			}
			newCursor -= utf8.RuneCountInString(l.Text)
			segs = segs[:len(segs)-1]
		}
	}
	return newCursor
}

// FindWordForward returns the cursor position after moving one word forward.
// cursor is a rune index into text.
func FindWordForward(text string, cursor int, opts *WordNavOptions) int {
	runes := []rune(text)
	if cursor >= len(runes) {
		return len(runes)
	}
	after := string(runes[cursor:])

	segs, isAtomic := resolveSegmenter(after, opts)

	newCursor := cursor
	i := 0

	// Skip leading whitespace segments.
	for i < len(segs) {
		s := segs[i]
		if (isAtomic != nil && isAtomic(s.Text)) || !isSpaceSegment(s) {
			break
		}
		newCursor += utf8.RuneCountInString(s.Text)
		i++
	}
	if i >= len(segs) {
		return newCursor
	}

	s := segs[i]
	switch {
	case isAtomic != nil && isAtomic(s.Text):
		newCursor += utf8.RuneCountInString(s.Text)
	case s.IsWordLike:
		idx := firstPunctuationIndex(s.Text)
		if idx >= 0 {
			newCursor += idx
		} else {
			newCursor += utf8.RuneCountInString(s.Text)
		}
	default:
		// Skip non-word, non-whitespace (punctuation) run.
		for i < len(segs) {
			s := segs[i]
			if (isAtomic != nil && isAtomic(s.Text)) || s.IsWordLike || isSpaceSegment(s) {
				break
			}
			newCursor += utf8.RuneCountInString(s.Text)
			i++
		}
	}
	return newCursor
}

func resolveSegmenter(s string, opts *WordNavOptions) ([]WordSegment, func(string) bool) {
	if opts != nil && opts.Segment != nil {
		return opts.Segment(s), opts.IsAtomic
	}
	return SegmentWords(s), nil
}

func isSpaceSegment(s WordSegment) bool {
	if s.Text == "" {
		return false
	}
	return unicode.IsSpace(firstRune(s.Text))
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// firstPunctuationIndex returns the rune index of the first ASCII punctuation in
// s, or -1 if none.
func firstPunctuationIndex(s string) int {
	idx := 0
	for _, r := range s {
		if isPunctuationRune(r) {
			return idx
		}
		idx++
	}
	return -1
}

// lastPunctuationEnd returns the rune index just past the last ASCII punctuation
// in s, or -1 if none.
func lastPunctuationEnd(s string) int {
	end := -1
	idx := 0
	for _, r := range s {
		if isPunctuationRune(r) {
			end = idx + 1
		}
		idx++
	}
	return end
}
