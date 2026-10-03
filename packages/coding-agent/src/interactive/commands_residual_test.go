package interactive

// commands_residual_test.go locks in the pure helpers behind the post-P8
// residual commands (/copy, /session, /export, /hotkeys). The command handlers
// need a live session (verified end-to-end on a real terminal); these table
// tests cover the error-prone, dependency-free pieces so the faithful formatting
// can't silently regress.

import (
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

func TestLastAssistantText(t *testing.T) {
	// Scans backward for the most recent assistant message; concatenates TextContent.
	msgs := []llm.Message{
		llm.UserMessage{Content: []llm.ContentBlock{llm.TextContent{Text: "hi"}}},
		llm.AssistantMessage{Content: []llm.ContentBlock{
			llm.TextContent{Text: "first "},
			&llm.TextContent{Text: "answer"},
		}},
		llm.UserMessage{Content: []llm.ContentBlock{llm.TextContent{Text: "again"}}},
		llm.AssistantMessage{Content: []llm.ContentBlock{llm.TextContent{Text: "second"}}},
	}
	if got := lastAssistantText(msgs); got != "second" {
		t.Errorf("lastAssistantText = %q, want %q", got, "second")
	}
	// Pointer-form TextContent is concatenated too.
	if got := lastAssistantText(msgs[:2]); got != "first answer" {
		t.Errorf("lastAssistantText (first) = %q, want %q", got, "first answer")
	}
	// No assistant → empty.
	only := []llm.Message{llm.UserMessage{Content: []llm.ContentBlock{llm.TextContent{Text: "x"}}}}
	if got := lastAssistantText(only); got != "" {
		t.Errorf("lastAssistantText (none) = %q, want empty", got)
	}
}

func TestParsePathArg(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"out.html":     "out.html",
		"a b.jsonl":    "a",
		`"my file.html"`: "my file.html", // strips one matching quote pair
		`'q'`:          "q",
		"  spaced.md ": "spaced.md",
	}
	for in, want := range cases {
		if got := parsePathArg(in); got != want {
			t.Errorf("parsePathArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSessionStats(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	stats := core.SessionStats{
		SessionFile:        "/tmp/s.json",
		SessionID:          "abc123",
		UserMessages:       3,
		AssistantMessages:  2,
		ToolCalls:          5,
		ToolResults:        4,
		TotalMessages:      9,
		Tokens:             core.TokenStats{Input: 100, Output: 50, CacheRead: 7, CacheWrite: 3},
		Cost:               0.0012,
	}
	out := stripANSI(formatSessionStats(stats, "my-session", th))
	// Faithful labels must appear (Fg wraps the label; values are plain — strip
	// ANSI first so label+value is a contiguous substring).
	for _, want := range []string{"Session Info", "Name: my-session", "File: /tmp/s.json", "ID: abc123",
		"Messages", "User: 3", "Assistant: 2", "Tool Calls: 5", "Tool Results: 4", "Total: 9",
		"Tokens", "Input: 100", "Output: 50", "Cache Read: 7", "Cache Write: 3", "Cost: $0.0012"} {
		if !strings.Contains(out, want) {
			t.Errorf("formatSessionStats missing %q\ngot:\n%s", want, out)
		}
	}
	// Empty file → "In-memory"; zero cache/cost blocks are omitted.
	out2 := stripANSI(formatSessionStats(core.SessionStats{SessionID: "x"}, "", th))
	if !strings.Contains(out2, "File: In-memory") {
		t.Errorf("expected In-memory fallback, got:\n%s", out2)
	}
	if strings.Contains(out2, "Cache") {
		t.Errorf("zero cache should be omitted, got:\n%s", out2)
	}
}

func TestFormatHotkeys(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	out := stripANSI(formatHotkeys(core.DefaultKeybindings(), th))
	if !strings.Contains(out, "Keybindings") {
		t.Errorf("formatHotkeys missing title, got:\n%s", out)
	}
	// Submit + description appear; empty-key actions are skipped.
	if !strings.Contains(out, "Submit the current message") {
		t.Errorf("formatHotkeys missing a description, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+z") { // Undo default key
		t.Errorf("formatHotkeys missing undo key, got:\n%s", out)
	}
}

// stripANSI removes CSI escape sequences so label (Fg/Bold-wrapped) + plain value
// can be matched as one contiguous substring in format* tests.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

