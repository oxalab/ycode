package utils

import (
	"strings"
	"unicode/utf8"
)

// SanitizeSurrogates: Removes unpaired Unicode surrogate code points from a string.
// Valid surrogate pairs (used for characters outsid the BMP like emoji) are preserved.
func SanitizeSurrogates(s string) string {
	if !strings.ContainsAny(s, "\xed\xa0\x80\xed\xb0\x80"){
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if isSurrogate(r) {
			// Check if its a valid pair
			if isHighSurrogate(r) && i+size < len(s) {
				r2, size2 := utf8.DecodeRuneInString(s[i+size:])
				if isLowSurrogate(r2){
					b.WriteString(s[i : i+size+size2])
					i += size + size2
					continue
				}
			}
			// Unpaired surrogate - skip it
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func isHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func isLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }
func isSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }