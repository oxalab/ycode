package utils

import (
	"encoding/json"
	"testing"
)

func partialMustEqual(t *testing.T, input string, want string) {
	t.Helper()
	got := parsePartialJSONObject(input)
	wantMap := map[string]interface{}{}
	if err := json.Unmarshal([]byte(want), &wantMap); err != nil {
		t.Fatalf("test bug: want is not JSON: %v", err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(wantMap)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("parsePartialJSONObject(%q) = %s, want %s", input, gotJSON, wantJSON)
	}
}

// The partial fallback mirrors the reference's partial-json chain: completed
// values survive, unterminated strings keep their text, incomplete keys or
// literals are dropped, and open containers are closed.
func TestParsePartialJSONObject(t *testing.T) {
	t.Run("complete object passes through", func(t *testing.T) {
		partialMustEqual(t, `{"command":"ls","timeout":5}`, `{"command":"ls","timeout":5}`)
	})
	t.Run("unterminated string keeps partial text", func(t *testing.T) {
		partialMustEqual(t, `{"command": "ls -la`, `{"command":"ls -la"}`)
	})
	t.Run("incomplete key dropped", func(t *testing.T) {
		partialMustEqual(t, `{"a": 1, "b`, `{"a":1}`)
	})
	t.Run("key without colon dropped", func(t *testing.T) {
		partialMustEqual(t, `{"a": 1, "b":`, `{"a":1}`)
	})
	t.Run("incomplete literal dropped", func(t *testing.T) {
		partialMustEqual(t, `{"a": 1, "b": tru`, `{"a":1}`)
	})
	t.Run("nested containers closed", func(t *testing.T) {
		partialMustEqual(t, `{"a": [1, 2, {"b": "c"}`, `{"a":[1,2,{"b":"c"}]}`)
	})
	t.Run("completed member after trailing comma kept", func(t *testing.T) {
		partialMustEqual(t, `{"a": 1,`, `{"a":1}`)
	})
	t.Run("escaped quote inside unterminated string", func(t *testing.T) {
		partialMustEqual(t, `{"cmd": "a\"b`, `{"cmd":"a\"b"}`)
	})
	t.Run("dangling backslash dropped", func(t *testing.T) {
		partialMustEqual(t, `{"a": "x\`, `{"a":"x"}`)
	})
	t.Run("truncated number finalized", func(t *testing.T) {
		partialMustEqual(t, `{"n": 12`, `{"n":12}`)
	})
	t.Run("malformed number dropped", func(t *testing.T) {
		partialMustEqual(t, `{"a": 1, "n": 1e`, `{"a":1}`)
	})
	t.Run("control character repaired", func(t *testing.T) {
		partialMustEqual(t, "{\"a\": \"x\ny\"}", `{"a":"x\ny"}`)
	})
}

// Streaming parity: progressive prefixes of a growing argument string must
// yield progressively complete maps (the value seen so far is always a prefix
// of the final text), with the final buffer parsing exactly.
func TestParseStreamingJSONProgressive(t *testing.T) {
	full := `{"command":"go test ./...","timeout":120}`
	openQuote := len(`{"command":"`)
	text := "go test ./..."
	var last map[string]interface{}
	for i := 1; i <= len(full); i++ {
		got := ParseStreamingJSON[map[string]interface{}](full[:i])
		if i > openQuote {
			want := text
			if i <= openQuote+len(text) {
				want = full[openQuote:i] // value still streaming: exact prefix
			}
			if got == nil {
				t.Fatalf("prefix %d: nil map", i)
			}
			if cmd, _ := got["command"].(string); cmd != want {
				t.Fatalf("prefix %d: command = %#v, want %q", i, cmd, want)
			}
		}
		last = got
	}
	if last == nil || last["command"] != text || last["timeout"] != float64(120) {
		t.Fatalf("final args = %v", last)
	}
}
