package modes

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// --- SerializeJSONLine ---

// TestSerializeJSONLine_RoundTrip verifies the output is valid JSON terminated
// by exactly one LF and that it round-trips through json.Unmarshal.
func TestSerializeJSONLine_RoundTrip(t *testing.T) {
	type widget struct {
		Type string `json:"type"`
		ID   int    `json:"id"`
	}
	line, err := SerializeJSONLine(widget{Type: "x", ID: 7})
	if err != nil {
		t.Fatalf("SerializeJSONLine: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("output not LF-terminated: %q", line)
	}
	if c := strings.Count(line, "\n"); c != 1 {
		t.Errorf("output has %d LF bytes, want exactly 1 (the terminator): %q", c, line)
	}

	var got widget
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &got); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if got.Type != "x" || got.ID != 7 {
		t.Errorf("round-trip = %+v, want {x 7}", got)
	}
}

// TestSerializeJSONLine_StrictSingleLine verifies embedded newlines and U+2028/
// U+2029 in payload strings never produce extra LF bytes (the single-record
// guarantee), because encoding/json escapes them.
func TestSerializeJSONLine_StrictSingleLine(t *testing.T) {
	payloads := []map[string]any{
		{"k": "a\nb"},       // embedded LF in string
		{"k": "a b"},   // LINE SEPARATOR
		{"k": "a b"},   // PARAGRAPH SEPARATOR
		{"k": "a\rb"},       // CR
	}
	for _, p := range payloads {
		line, err := SerializeJSONLine(p)
		if err != nil {
			t.Fatalf("SerializeJSONLine(%+v): %v", p, err)
		}
		if c := strings.Count(line, "\n"); c != 1 {
			t.Errorf("payload %+v → %d LFs, want 1: %q", p, c, line)
		}
		if strings.Contains(line, " ") || strings.Contains(line, " ") {
			t.Errorf("payload %+v left a raw U+2028/2029 in the line: %q", p, line)
		}
		// Round-trips back to the original string.
		var got map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &got); err != nil {
			t.Errorf("unmarshal failed for %+v: %v", p, err)
		}
	}
}

// TestSerializeJSONLine_Error verifies a non-marshalable value returns an error
// and an empty string.
func TestSerializeJSONLine_Error(t *testing.T) {
	line, err := SerializeJSONLine(make(chan int))
	if err == nil {
		t.Fatal("chan value → nil error, want a marshal error")
	}
	if line != "" {
		t.Errorf("error path → %q, want empty string", line)
	}
}

// --- AttachJSONLLineReader ---

// collectLines runs the reader over s and returns the framed lines + the error.
func collectLines(s string) ([]string, error) {
	var lines []string
	err := AttachJSONLLineReader(strings.NewReader(s), func(line string) {
		lines = append(lines, line)
	})
	return lines, err
}

// TestAttachJSONLLineReader_LF verifies basic LF framing, including a final
// line with no trailing newline.
func TestAttachJSONLLineReader_LF(t *testing.T) {
	lines, err := collectLines("a\nb\nc")
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if len(lines) != 3 || lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("lines = %v, want [a b c]", lines)
	}
}

// TestAttachJSONLLineReader_CRLF verifies "\r\n" framing strips the "\r".
func TestAttachJSONLLineReader_CRLF(t *testing.T) {
	lines, err := collectLines("a\r\nb\r\n")
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Errorf("lines = %v, want [a b] (no CR)", lines)
	}
}

// TestAttachJSONLLineReader_EmptyLines verifies empty lines between records are
// emitted (the caller decides to skip them), and that a single trailing newline
// does NOT produce a spurious empty line.
func TestAttachJSONLLineReader_EmptyLines(t *testing.T) {
	// "a\n\nb\n" → a, "", b (the blank between); trailing "\n" is the record
	// terminator for b, not a new empty record.
	lines, err := collectLines("a\n\nb\n")
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	want := []string{"a", "", "b"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}

	// Single trailing newline → no spurious empty line.
	only, err := collectLines("cmd\n")
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if len(only) != 1 || only[0] != "cmd" {
		t.Errorf("trailing-newline → %v, want [cmd]", only)
	}
}

// TestAttachJSONLLineReader_KeepsUnicodeSeparators verifies U+2028/U+2029 inside
// a record are NOT treated as line breaks (strict LF-only framing — the core
// contract this reader exists to uphold).
func TestAttachJSONLLineReader_KeepsUnicodeSeparators(t *testing.T) {
	lines, err := collectLines("abc def ghi\n")
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want exactly 1 (U+2028/2029 must not split)", lines)
	}
	if lines[0] != "abc def ghi" {
		t.Errorf("line = %q, want the separators kept in-content", lines[0])
	}
}

// TestAttachJSONLLineReader_ReadError verifies a non-EOF read error is surfaced
// (after emitting any complete lines read before it).
func TestAttachJSONLLineReader_ReadError(t *testing.T) {
	r := &errReader{data: "x\n", err: io.ErrUnexpectedEOF}
	var lines []string
	err := AttachJSONLLineReader(r, func(line string) { lines = append(lines, line) })
	if err != io.ErrUnexpectedEOF {
		t.Errorf("error = %v, want io.ErrUnexpectedEOF", err)
	}
	if len(lines) != 1 || lines[0] != "x" {
		t.Errorf("lines = %v, want [x] (the line read before the error)", lines)
	}
}

// errReader serves data then a fixed error (to simulate a mid-stream read failure).
type errReader struct {
	data string
	n    int
	err  error
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.n >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.n:])
	r.n += n
	return n, nil
}
