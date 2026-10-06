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
