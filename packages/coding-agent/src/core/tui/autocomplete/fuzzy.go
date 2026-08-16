package autocomplete

import "strings"

// fuzzyMatch reports whether query matches text as a case-folded subsequence,
// approximating pi-tui's fuzzyFilter for autocomplete use. An empty query
// matches everything (so typing just "/" lists all commands). This mirrors
// components.FuzzyMatch but lives here to keep the autocomplete package
// independent of the components package.
func fuzzyMatch(text, query string) bool {
	if query == "" {
		return true
	}
	t := strings.ToLower(text)
	q := strings.ToLower(query)
	ti := 0
	for qi := 0; qi < len(q); qi++ {
		matched := false
		for ; ti < len(t); ti++ {
			if t[ti] == q[qi] {
				matched = true
				ti++
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
