package utils

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
	"fmt"
)

var validJSONEscapes = map[byte]bool {
	'"': true, '\\': true, '/': true,
	'b': true, 'f': true, 'n': true, 'r': true, 't': true, 'u': true,
}

func isControlCharacter(r rune) bool {
	return r >= 0x00 && r <= 0x1f
}

func escapeControlCharacter(r rune) string {
	switch r {
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	default:
		return fmt.Sprintf(`\u%04x`, r)
	}
}

// RepairJSON: Repairs malformed JSON string literals by escaping raw control
// characted inside strings and doubling backslashes before invalid escape chars.
func RepairJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !inString{
			b.WriteRune(r)
			if r == '"' {
				inString = true
			}
			i += size
			continue
		}
		if r == '"' {
			b.WriteRune(r)
			inString = false
			i += size
			continue
		}
		if r == '\\' {
			if i+1 >= len(s) {
				b.WriteString(`\\`)
				i += size
				continue
			}
			next := s[i+1]
			if next == 'u' {
				if i+5 < len(s) && isHex4(s[i+2:i+6]){
					b.WriteString(s[i : i+6])
					i += 6
					continue
				}
			}
			if validJSONEscapes[next] {
				b.WriteByte('\\')
				b.WriteByte(next)
				i += 2
				continue
			}
			b.WriteString(`\\`)
			i += size
			continue
		}
		if isControlCharacter(r) {
			b.WriteString(escapeControlCharacter(r))
		}else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// ParseJSONWithRepair: Tries standard JSON parsing first, then retries with repair
// if the first attempt fails
func ParseJSONWithRepair[T any](s string) (T, error) {
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		repaired := RepairJSON(s)
		if repaired != s {
			return v, json.Unmarshal([]byte(repaired), &v)
		}
		return v, err
	}
	return v, nil
}

// ParseStreamingJSON attempts to parse potentially incomplete JSON during streaming.
// Always returns a valid valude, even if the JSON is incomplete
func ParseStreamingJSON[T any](s string)  T {
	var zero T
	if strings.TrimSpace(s) == "" {
		return zero
	}
	if v, err := ParseJSONWithRepair[T](s); err == nil {
		return v
	}
	// Fallback: try partial-json style parsing.
	// You'll need a Go partial JSON library or implement a simple one
	// For now, return zero value on failure
	return zero
}

func isHex4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')){
			return false
		}
	}
	return true
}