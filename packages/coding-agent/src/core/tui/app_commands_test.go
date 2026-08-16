package tui

import (
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// testTheme returns an initialized theme for formatSessionStats tests. InitTheme
// is idempotent; the dark theme is the loader's fallback so it always loads.
func testTheme() *theme.Theme {
	theme.InitTheme("dark")
	return theme.GetTheme()
}

// ============================================================================
// parseSlashCommand
// ============================================================================

func TestParseSlashCommand(t *testing.T) {
	cases := []struct {
		in            string
		wantName      string
		wantRest      string
		desc          string
	}{
		{"/model", "/model", "", "bare command"},
		{"  /model  ", "/model", "", "trimmed"},
		{"/model gpt-4o", "/model", "gpt-4o", "command + arg"},
		{"/model  two  spaces", "/model", "two  spaces", "internal spaces preserved"},
		{"/Model GPT", "/model", "GPT", "name lowercased, arg case preserved"},
		{"/compact   summarize now", "/compact", "summarize now", "multi-space after command"},
		{"/export '/path with space.jsonl'", "/export", "'/path with space.jsonl'", "quoted arg kept raw (parsePathArg decodes)"},
		{"/", "", "", "bare slash → not a command"},
		{"hello", "", "", "non-slash → not a command"},
		{"", "", "", "empty"},
	}
	for _, c := range cases {
		name, rest := parseSlashCommand(c.in)
		if name != c.wantName || rest != c.wantRest {
			t.Errorf("%q (%s): got (%q, %q), want (%q, %q)", c.in, c.desc, name, rest, c.wantName, c.wantRest)
		}
	}
}

// ============================================================================
// findExactModelMatch
// ============================================================================

func TestFindExactModelMatch(t *testing.T) {
	models := []*llm.Model[llm.Api]{
		{ID: "gpt-4o", Provider: "openai"},
		{ID: "claude-opus-4", Provider: "anthropic"},
	}
	cases := []struct {
		term string
		want string // matched ID, or "" for nil
	}{
		{"gpt-4o", "gpt-4o"},                         // bare ID
		{"openai/gpt-4o", "gpt-4o"},                  // provider/id
		{"anthropic/claude-opus-4", "claude-opus-4"}, // provider/id
		{"claude-opus-4", "claude-opus-4"},           // bare ID
		{"nonexistent", ""},                          // no match
		{"", ""},                                     // empty term
		{"openai/claude-opus-4", ""},                 // mismatched provider/id
	}
	for _, c := range cases {
		m := findExactModelMatch(c.term, models)
		got := ""
		if m != nil {
			got = m.ID
		}
		if got != c.want {
			t.Errorf("findExactModelMatch(%q): got %q, want %q", c.term, got, c.want)
		}
	}

	if findExactModelMatch("gpt-4o", nil) != nil {
		t.Error("nil models → should return nil")
	}
	if findExactModelMatch("gpt-4o", []*llm.Model[llm.Api]{nil}) != nil {
		t.Error("nil element should be skipped")
	}
}

// ============================================================================
// countMessages
// ============================================================================

func TestCountMessages(t *testing.T) {
	entries := []*compaction.SessionEntry{
		{Type: compaction.EntryTypeMessage},
		{Type: compaction.EntryTypeCompaction},
		{Type: compaction.EntryTypeMessage},
		nil, // nil entry must be skipped, not panic
		{Type: compaction.EntryTypeBranchSummary},
		{Type: compaction.EntryTypeMessage},
	}
	if n := countMessages(entries); n != 3 {
		t.Errorf("countMessages = %d, want 3", n)
	}
	if n := countMessages(nil); n != 0 {
		t.Errorf("countMessages(nil) = %d, want 0", n)
	}
	// Fewer than 2 messages is the compaction gate boundary.
	if countMessages([]*compaction.SessionEntry{{Type: compaction.EntryTypeMessage}}) != 1 {
		t.Error("single message should count as 1")
	}
}

// ============================================================================
// parsePathArg
// ============================================================================

func TestParsePathArg(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"foo.jsonl", "foo.jsonl"},
		{"  spaced.jsonl  ", "spaced.jsonl"},
		{"a b c", "a"},                   // first token only
		{`"my file.jsonl"`, "my file.jsonl"},     // double-quoted
		{`'my file.jsonl'`, "my file.jsonl"},     // single-quoted
		{`"/a/b c.jsonl" trailing`, "/a/b c.jsonl"}, // quoted + trailing junk
		{`'unterminated`, "'unterminated"},        // no closing quote → first token
	}
	for _, c := range cases {
		if got := parsePathArg(c.in); got != c.want {
			t.Errorf("parsePathArg(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ============================================================================
// formatSessionStats
// ============================================================================

func TestFormatSessionStats(t *testing.T) {
	tk := testTheme()

	// Named session with file, cache tokens, and cost.
	full := core.SessionStats{
		SessionFile:       "/tmp/s.jsonl",
		SessionID:         "abc123",
		UserMessages:      3,
		AssistantMessages: 2,
		ToolCalls:         5,
		ToolResults:       5,
		TotalMessages:     10,
		Tokens: core.TokenStats{Input: 100, Output: 50, CacheRead: 20, CacheWrite: 10, Total: 180},
		Cost:              0.0123,
	}
	out := formatSessionStats(full, "my-session", tk)
	mustContain(t, out, "Session Info", "my-session", "/tmp/s.jsonl", "abc123",
		"Messages", "User:", "Assistant:", "Tool Calls:", "Tool Results:", "Total:",
		"Tokens", "Input:", "Output:", "Cache Read:", "Cache Write:", "Cost:")
	mustNotContain(t, out, "In-memory")

	// In-memory session, no cache, no cost, no name.
	empty := core.SessionStats{SessionID: "mem"}
	out2 := formatSessionStats(empty, "", tk)
	mustContain(t, out2, "In-memory", "mem", "Session Info")
	mustNotContain(t, out2, "Cache Read", "Cache Write", "Cost:", "Name:")
}

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("output missing %q\n--- output ---\n%s", sub, s)
		}
	}
}

func mustNotContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("output unexpectedly contains %q\n--- output ---\n%s", sub, s)
		}
	}
}

// ============================================================================
// lastAssistantText
// ============================================================================

func TestLastAssistantText(t *testing.T) {
	if got := lastAssistantText(nil); got != "" {
		t.Errorf("nil → %q, want empty", got)
	}

	// Only the LAST assistant message is used, scanning backward over a user msg.
	msgs := []llm.Message{
		llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: "old"}}},
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: "q"}}},
		llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			llm.TextContent{Type: "text", Text: "new "},
			llm.TextContent{Type: "text", Text: "answer"},
		}},
	}
	if got := lastAssistantText(msgs); got != "new answer" {
		t.Errorf("got %q, want 'new answer'", got)
	}

	// No assistant message → "".
	onlyUser := []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: "hi"}}}}
	if got := lastAssistantText(onlyUser); got != "" {
		t.Errorf("no assistant → %q, want empty", got)
	}
}
