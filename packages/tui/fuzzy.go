package tui

import (
	"regexp"
	"sort"
	"strings"
)

// FuzzyMatchResult is the outcome of FuzzyMatch.
type FuzzyMatchResult struct {
	Matches bool
	Score   float64
}

// FuzzyMatch reports whether every character of query appears in text in order
// (case-insensitive), with a Score where lower is better. Matches reference
// fuzzy.ts fuzzyMatch.
func FuzzyMatch(query, text string) FuzzyMatchResult {
	queryLower := strings.ToLower(query)

	match := func(q string) FuzzyMatchResult {
		if len(q) == 0 {
			return FuzzyMatchResult{Matches: true, Score: 0}
		}
		textLower := strings.ToLower(text)
		runes := []rune(textLower)
		qRunes := []rune(q)
		if len(qRunes) > len(runes) {
			return FuzzyMatchResult{Matches: false}
		}
		qi := 0
		var score float64
		lastMatch := -1
		consecutive := 0
		for i := 0; i < len(runes) && qi < len(qRunes); i++ {
			if runes[i] != qRunes[qi] {
				continue
			}
			isBoundary := i == 0 || isFuzzyWordBoundary(runes[i-1])
			if lastMatch == i-1 {
				consecutive++
				score -= float64(consecutive) * 5
			} else {
				consecutive = 0
				if lastMatch >= 0 {
					score += float64(i-lastMatch-1) * 2
				}
			}
			if isBoundary {
				score -= 10
			}
			score += float64(i) * 0.1
			lastMatch = i
			qi++
		}
		if qi < len(qRunes) {
			return FuzzyMatchResult{Matches: false}
		}
		if q == textLower {
			score -= 100
		}
		return FuzzyMatchResult{Matches: true, Score: score}
	}

	primary := match(queryLower)
	if primary.Matches {
		return primary
	}

	// Alpha-numeric swap fallback: "codex52" can match text containing "52codex".
	swapped := swapAlphaNumeric(queryLower)
	if swapped == "" {
		return primary
	}
	sw := match(swapped)
	if !sw.Matches {
		return primary
	}
	return FuzzyMatchResult{Matches: true, Score: sw.Score + 5}
}

func isFuzzyWordBoundary(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '-', '_', '.', '/', ':':
		return true
	}
	return false
}

var (
	alphaThenDigitRe = regexp.MustCompile(`^([a-z]+)([0-9]+)$`)
	digitThenAlphaRe = regexp.MustCompile(`^([0-9]+)([a-z]+)$`)
)

func swapAlphaNumeric(q string) string {
	if m := alphaThenDigitRe.FindStringSubmatch(q); m != nil {
		return m[2] + m[1]
	}
	if m := digitThenAlphaRe.FindStringSubmatch(q); m != nil {
		return m[1] + m[2]
	}
	return ""
}

// FuzzyFilter returns items matching every whitespace/slash-separated token of
// query (all tokens must match), sorted best-first. Matches reference fuzzy.ts
// fuzzyFilter.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	q := strings.TrimSpace(query)
	if q == "" {
		return items
	}
	tokens := strings.FieldsFunc(q, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '/'
	})
	if len(tokens) == 0 {
		return items
	}

	type scored struct {
		item  T
		total float64
	}
	var results []scored
	for _, it := range items {
		text := getText(it)
		total := 0.0
		all := true
		for _, tok := range tokens {
			m := FuzzyMatch(tok, text)
			if !m.Matches {
				all = false
				break
			}
			total += m.Score
		}
		if all {
			results = append(results, scored{it, total})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].total < results[j].total
	})
	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}
