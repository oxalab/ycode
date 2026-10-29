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
	// Fallback: partial-json style prefix parse (mirrors the reference's
	// partialParse chain), re-marshalled into T.
	if m := parsePartialJSONObject(s); len(m) > 0 {
		if b, err := json.Marshal(m); err == nil {
			var out T
			if err := json.Unmarshal(b, &out); err == nil {
				return out
			}
		}
	}
	return zero
}

// parsePartialJSONObject parses a prefix-closed partial JSON document the way
// the reference's partial-json fallback does: values that completed are kept,
// an unterminated string keeps its partial text, and incomplete keys or
// literals are dropped. All open containers are implicitly closed.
func parsePartialJSONObject(s string) map[string]interface{} {
	if strings.TrimSpace(s) == "" {
		return map[string]interface{}{}
	}
	// Parse the ORIGINAL string: trailing whitespace can be meaningful string
	// content mid-stream (the reference only trims for its emptiness check).
	v, _, _ := parsePartialJSONValue(s)
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

// parsePartialJSONValue parses one JSON value from s[i:], returning the value,
// whether it exists at all (absent values are dropped by the caller), and
// whether it was fully terminated. A partial string or container is present
// (kept) but not complete.
func parsePartialJSONValue(s string) (interface{}, bool, bool) {
	i := 0
	n := len(s)
	skipWS := func() {
		for i < n {
			switch s[i] {
			case ' ', '\t', '\n', '\r':
				i++
			default:
				return
			}
		}
	}

	// parseString scans a JSON string starting at s[i] == '"'. An
	// unterminated string still yields its partial text (present, incomplete).
	parseString := func() (string, bool, bool) {
		start := i
		i++
		for i < n {
			if s[i] == '\\' {
				i += 2 // skip the escaped char; overshooting past n ends the loop
				continue
			}
			if s[i] == '"' {
				raw := s[start : i+1]
				i++
				var out string
				if err := json.Unmarshal([]byte(RepairJSON(raw)), &out); err == nil {
					return out, true, true
				}
				return raw[1 : len(raw)-1], true, true
			}
			i++
		}
		frag := s[start+1:]
		if strings.HasSuffix(frag, "\\") {
			frag = frag[:len(frag)-1] // dangling escape
		}
		var out string
		// The fragment is string CONTENT (repair expects to start outside a
		// string), so wrap it in quotes before repairing.
		if err := json.Unmarshal([]byte(RepairJSON(`"`+frag+`"`)), &out); err == nil {
			return out, true, false
		}
		return frag, true, false
	}

	parseNumber := func() (interface{}, bool, bool) {
		start := i
		i++
		for i < n {
			c := s[i]
			if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
				i++
				continue
			}
			break
		}
		var f float64
		if err := json.Unmarshal([]byte(s[start:i]), &f); err != nil {
			return nil, false, false // incomplete number ("-1e") — drop, next delta completes it
		}
		return f, true, true
	}

	var value func() (interface{}, bool, bool)
	value = func() (interface{}, bool, bool) {
		skipWS()
		if i >= n {
			return nil, false, false
		}
		switch c := s[i]; {
		case c == '{':
			i++
			obj := map[string]interface{}{}
			for {
				skipWS()
				if i >= n {
					return obj, true, false
				}
				switch s[i] {
				case '}':
					i++
					return obj, true, true
				case ',':
					i++
					continue
				}
				if s[i] != '"' {
					return obj, true, false // unexpected token — keep what completed
				}
				key, keyPresent, keyComplete := parseString()
				if !keyPresent || !keyComplete {
					return obj, true, false // unterminated key — drop it
				}
				skipWS()
				if i >= n || s[i] != ':' {
					return obj, true, false // key without a value yet — drop it
				}
				i++
				val, present, _ := value()
				if !present {
					return obj, true, false // incomplete value — drop the member
				}
				obj[key] = val
			}
		case c == '[':
			i++
			arr := []interface{}{}
			for {
				skipWS()
				if i >= n {
					return arr, true, false
				}
				switch s[i] {
				case ']':
					i++
					return arr, true, true
				case ',':
					i++
					continue
				}
				val, present, _ := value()
				if !present {
					return arr, true, false
				}
				arr = append(arr, val)
			}
		case c == '"':
			return parseString()
		case c == 't':
			if i+4 <= n && s[i:i+4] == "true" {
				i += 4
				return true, true, true
			}
			return nil, false, false
		case c == 'f':
			if i+5 <= n && s[i:i+5] == "false" {
				i += 5
				return false, true, true
			}
			return nil, false, false
		case c == 'n':
			if i+4 <= n && s[i:i+4] == "null" {
				i += 4
				return nil, true, true
			}
			return nil, false, false
		default:
			return parseNumber()
		}
	}

	return value()
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