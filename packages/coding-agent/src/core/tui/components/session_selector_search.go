package components

import (
	"regexp"
	"sort"
	"strings"

	core "ycode/packages/coding-agent/src/core"
)

// SessionSortMode re-exports the sort modes used by the session search helpers.
// (Defined as sessionSortMode in session_selector.go; aliased here for clarity.)
//
// Note: sessionSortThreaded / sessionSortRecent / sessionSortRelevance live in
// session_selector.go and are used directly below.

// searchTokenKind distinguishes fuzzy subsequence tokens from exact phrases.
type searchTokenKind int

const (
	searchTokenFuzzy searchTokenKind = iota
	searchTokenPhrase
)

// searchToken is one parsed unit of a search query.
type searchToken struct {
	kind  searchTokenKind
	value string
}

// parsedSearchQuery is the result of parsing a search string.
type parsedSearchQuery struct {
	mode  string // "tokens" | "regex"
	tokens []searchToken
	regex  *regexp.Regexp
	// error is set when parsing failed (e.g. invalid regex); the query is
	// then treated as non-matching.
	errorMsg string
}

// sessionMatchResult holds whether a session matched and a relevance score.
type sessionMatchResult struct {
	matches bool
	// score is meaningful only when matches == true; lower is better.
	score float64
}

// hasSessionName reports whether a session has a non-empty name.
func hasSessionNameSearch(sess *core.SessionInfo) bool {
	return sess != nil && strings.TrimSpace(sess.Name) != ""
}

// matchesNameFilterSearch reports whether a session passes the name filter.
func matchesNameFilterSearch(sess *core.SessionInfo, filter sessionNameFilter) bool {
	if filter == sessionNameAll {
		return true
	}
	return hasSessionNameSearch(sess)
}

// getSessionSearchText builds the haystack searched against a session.
func getSessionSearchText(sess *core.SessionInfo) string {
	return sess.ID + " " + sess.Name + " " + sess.AllMessagesText + " " + sess.Cwd
}

// parseSearchQuery parses a raw query string into either regex mode
// ("re:<pattern>") or token mode (fuzzy words + "quoted phrases").
//
// Examples:
//
//	foo bar            → [fuzzy:foo, fuzzy:bar]
//	foo "node cve"     → [fuzzy:foo, phrase:"node cve"]
//	re:^fix typo$      → regex mode
//	"unclosed          → falls back to whitespace fuzzy tokenization
func parseSearchQuery(query string) parsedSearchQuery {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return parsedSearchQuery{mode: "tokens"}
	}

	// Regex mode: re:<pattern>
	if strings.HasPrefix(trimmed, "re:") {
		pattern := strings.TrimSpace(trimmed[3:])
		if pattern == "" {
			return parsedSearchQuery{mode: "regex", errorMsg: "Empty regex"}
		}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return parsedSearchQuery{mode: "regex", errorMsg: err.Error()}
		}
		return parsedSearchQuery{mode: "regex", regex: re}
	}

	// Token mode with quote support.
	var tokens []searchToken
	var buf strings.Builder
	inQuote := false

	flush := func(kind searchTokenKind) {
		v := strings.TrimSpace(buf.String())
		buf.Reset()
		if v == "" {
			return
		}
		tokens = append(tokens, searchToken{kind: kind, value: v})
	}

	for _, ch := range trimmed {
		if ch == '"' {
			if inQuote {
				flush(searchTokenPhrase)
				inQuote = false
			} else {
				flush(searchTokenFuzzy)
				inQuote = true
			}
			continue
		}
		if !inQuote && (ch == ' ' || ch == '\t') {
			flush(searchTokenFuzzy)
			continue
		}
		buf.WriteRune(ch)
	}

	// Unclosed quote → fall back to plain whitespace tokenization.
	if inQuote {
		fields := strings.Fields(trimmed)
		tokens = tokens[:0]
		for _, f := range fields {
			tokens = append(tokens, searchToken{kind: searchTokenFuzzy, value: f})
		}
		return parsedSearchQuery{mode: "tokens", tokens: tokens}
	}

	flush(searchTokenFuzzy)
	return parsedSearchQuery{mode: "tokens", tokens: tokens}
}

// matchSession evaluates a session against a parsed query.
func matchSession(sess *core.SessionInfo, parsed parsedSearchQuery) sessionMatchResult {
	text := getSessionSearchText(sess)

	// Regex mode.
	if parsed.mode == "regex" {
		if parsed.regex == nil {
			return sessionMatchResult{}
		}
		loc := parsed.regex.FindStringIndex(text)
		if loc == nil {
			return sessionMatchResult{}
		}
		return sessionMatchResult{matches: true, score: float64(loc[0]) * 0.1}
	}

	// Token mode: all tokens must match.
	if len(parsed.tokens) == 0 {
		return sessionMatchResult{matches: true}
	}

	var totalScore float64
	var normalizedText string
	normalizedReady := false

	for _, token := range parsed.tokens {
		if token.kind == searchTokenPhrase {
			if !normalizedReady {
				normalizedText = normalizeWhitespaceLower(text)
				normalizedReady = true
			}
			phrase := normalizeWhitespaceLower(token.value)
			if phrase == "" {
				continue
			}
			idx := strings.Index(normalizedText, phrase)
			if idx < 0 {
				return sessionMatchResult{}
			}
			totalScore += float64(idx) * 0.1
			continue
		}

		// Fuzzy subsequence match with a position-based score.
		m := fuzzyMatchScored(token.value, text)
		if !m.matches {
			return sessionMatchResult{}
		}
		totalScore += m.score
	}

	return sessionMatchResult{matches: true, score: totalScore}
}

// fuzzyMatchScored reports whether query matches text as a subsequence
// (case-insensitive) and returns a score: earlier/closer matches score lower
// (better). Mirrors pi-tui's fuzzyMatch scoring approximation.
type fuzzyScoredResult struct {
	matches bool
	score   float64
}

func fuzzyMatchScored(query, text string) fuzzyScoredResult {
	if query == "" {
		return fuzzyScoredResult{matches: true}
	}
	q := strings.ToLower(query)
	t := strings.ToLower(text)

	ti := 0
	totalPos := 0
	matched := 0
	for qi := 0; qi < len(q); qi++ {
		found := false
		for ; ti < len(t); ti++ {
			if t[ti] == q[qi] {
				totalPos += ti
				matched++
				ti++
				found = true
				break
			}
		}
		if !found {
			return fuzzyScoredResult{}
		}
	}
	// Lower average position → better (lower) score.
	avg := float64(totalPos)
	if matched > 0 {
		avg /= float64(matched)
	}
	return fuzzyScoredResult{matches: true, score: avg}
}

// normalizeWhitespaceLower collapses whitespace and lowercases text.
func normalizeWhitespaceLower(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// filterAndSortSessions filters sessions by name filter + query and sorts by
// the given mode. Recent mode keeps incoming order; relevance sorts by score.
func filterAndSortSessions(
	sessions []*core.SessionInfo,
	query string,
	sortMode sessionSortMode,
	nameFilter sessionNameFilter,
) []*core.SessionInfo {
	// Name filter.
	var nameFiltered []*core.SessionInfo
	for _, s := range sessions {
		if matchesNameFilterSearch(s, nameFilter) {
			nameFiltered = append(nameFiltered, s)
		}
	}

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nameFiltered
	}

	parsed := parseSearchQuery(query)
	if parsed.errorMsg != "" {
		return nil
	}

	// Recent mode: filter only, keep order.
	if sortMode == sessionSortRecent {
		var filtered []*core.SessionInfo
		for _, s := range nameFiltered {
			if matchSession(s, parsed).matches {
				filtered = append(filtered, s)
			}
		}
		return filtered
	}

	// Relevance mode: sort by score, tie-break by modified desc.
	type scored struct {
		session *core.SessionInfo
		score   float64
	}
	var scoredList []scored
	for _, s := range nameFiltered {
		res := matchSession(s, parsed)
		if !res.matches {
			continue
		}
		scoredList = append(scoredList, scored{session: s, score: res.score})
	}
	sort.SliceStable(scoredList, func(a, b int) bool {
		if scoredList[a].score != scoredList[b].score {
			return scoredList[a].score < scoredList[b].score
		}
		return scoredList[a].session.Modified.After(scoredList[b].session.Modified)
	})

	out := make([]*core.SessionInfo, len(scoredList))
	for i, s := range scoredList {
		out[i] = s.session
	}
	return out
}
