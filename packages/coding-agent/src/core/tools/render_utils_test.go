package tools

import (
	"strings"
	"testing"

	llm "ycode/packages/llm/src"
)

// noImageTerminal pins GetCapabilities to a non-image terminal so the
// fallback-text path in GetTextOutput is deterministic in tests.
func noImageTerminal(t *testing.T) {
	t.Helper()
	t.Setenv("TERM_PROGRAM", "plain-term")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERMKIT", "")
}

// Tools re-audit B1: GetTextOutput type-switched on Go-local
// TextContentBlock/ImageContentBlock structs that no tool produces — it
// returned "" for every real tool result. It must read *llm.TextContent /
// *llm.ImageContent (what tools store) and map shapes (what app_update puts
// on the error path).
func TestGetTextOutputReadsLLMContentBlocks(t *testing.T) {
	noImageTerminal(t)

	r := &ToolResult{Content: []any{
		&llm.TextContent{Type: "text", Text: "HELLO_OUTPUT"},
	}}
	if got := GetTextOutput(r, false); got != "HELLO_OUTPUT" {
		t.Errorf("text block: got %q, want %q", got, "HELLO_OUTPUT")
	}

	r = &ToolResult{Content: []any{
		llm.TextContent{Type: "text", Text: "value-not-pointer"},
	}}
	if got := GetTextOutput(r, false); got != "value-not-pointer" {
		t.Errorf("value text block: got %q, want %q", got, "value-not-pointer")
	}
}

func TestGetTextOutputImageFallback(t *testing.T) {
	noImageTerminal(t)

	r := &ToolResult{Content: []any{
		&llm.TextContent{Type: "text", Text: "before"},
		&llm.ImageContent{Type: "image", Data: "", MimeType: "image/png"},
	}}
	got := GetTextOutput(r, false)
	if !strings.Contains(got, "before") {
		t.Errorf("text part missing: %q", got)
	}
	if !strings.Contains(got, "[Image: image/png]") {
		t.Errorf("image fallback missing: %q", got)
	}

	// Image-only result: just the fallback.
	r = &ToolResult{Content: []any{
		&llm.ImageContent{Type: "image", Data: "", MimeType: "image/jpeg"},
	}}
	if got := GetTextOutput(r, false); got != "[Image: image/jpeg]" {
		t.Errorf("image-only: got %q, want fallback text", got)
	}
}

func TestGetTextOutputMapShapes(t *testing.T) {
	noImageTerminal(t)

	// app_update.go error path stores map[string]string.
	r := &ToolResult{Content: []any{
		map[string]string{"type": "text", "text": "ERR_MESSAGE"},
	}}
	if got := GetTextOutput(r, false); got != "ERR_MESSAGE" {
		t.Errorf("map[string]string: got %q, want %q", got, "ERR_MESSAGE")
	}

	r = &ToolResult{Content: []any{
		map[string]any{"type": "text", "text": "ANY_MESSAGE"},
	}}
	if got := GetTextOutput(r, false); got != "ANY_MESSAGE" {
		t.Errorf("map[string]any: got %q, want %q", got, "ANY_MESSAGE")
	}
}

func TestGetTextOutputNilAndEmpty(t *testing.T) {
	noImageTerminal(t)
	if got := GetTextOutput(nil, false); got != "" {
		t.Errorf("nil result: got %q, want empty", got)
	}
	if got := GetTextOutput(&ToolResult{}, false); got != "" {
		t.Errorf("empty result: got %q, want empty", got)
	}
}

// Tools re-audit B8: StripAnsi only matched plain CSI (ESC[...letter), so OSC
// sequences (the app's own OSC 133 among them), private-mode CSI, colon SGR
// params and C1 introducers leaked into tool output. It must match the npm
// strip-ansi@7.2.0 / ansi-regex@6.2.2 pattern the reference uses.
func TestStripAnsi(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain sgr", "\x1b[31mred\x1b[0m", "red"},
		{"erase line", "a\x1b[2Kb", "ab"},
		{"private mode", "\x1b[?25hcursor", "cursor"},
		{"osc bel", "\x1b]0;title\x07after", "after"},
		{"osc st hyperlink pair", "\x1b]8;;http://e\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"colon sgr params", "\x1b[38:5:196mX\x1b[0m", "X"},
		{"c1 csi introducer", "\u009bKafter", "after"},
		{"mixed sgr and osc", "a\x1b[31mb\x1b]0;t\x07c", "abc"},
		{"no escape passthrough", "plain text", "plain text"},
	}
	for _, tc := range cases {
		if got := StripAnsi(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// B8 leak path: OSC 133 (emitted by the app itself) must not survive the
// GetTextOutput text pipeline.
func TestGetTextOutputStripsOSC(t *testing.T) {
	noImageTerminal(t)

	r := &ToolResult{Content: []any{
		&llm.TextContent{Type: "text", Text: "\x1b]133;A\x07visible"},
	}}
	got := GetTextOutput(r, false)
	if strings.Contains(got, "\x1b") || strings.Contains(got, "133;A") {
		t.Errorf("OSC leaked through GetTextOutput: %q", got)
	}
	if !strings.Contains(got, "visible") {
		t.Errorf("text content lost: %q", got)
	}
}
