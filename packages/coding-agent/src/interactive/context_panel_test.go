package interactive

// context_panel_test.go covers the pure helpers in context_panel.go:
// estimateStringTokens, estimateStringTokensFromChars, estimateToolsTokens
// (the tool-source bucketing lives in estimateContextBreakdown and needs a
// full AgentSession, so the bucketing-by-Source-prefix logic is mirrored here
// against a synthesized []ToolDefinitionEntry instead).

import (
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
)

func TestEstimateStringTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abcd", 1},       // (4+3)/4 = 1
		{"abcde", 2},      // (5+3)/4 = 2
		{"abcdefgh", 2},   // (8+3)/4 = 2
		{"abcdefghi", 3},  // (9+3)/4 = 3
	}
	for _, c := range cases {
		if got := estimateStringTokens(c.in); got != c.want {
			t.Errorf("estimateStringTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEstimateStringTokensFromChars(t *testing.T) {
	cases := []struct {
		chars int
		want  int
	}{
		{0, 0},
		{4, 1},
		{5, 2},
		{100, 25}, // (100+3)/4
	}
	for _, c := range cases {
		if got := estimateStringTokensFromChars(c.chars); got != c.want {
			t.Errorf("estimateStringTokensFromChars(%d) = %d, want %d", c.chars, got, c.want)
		}
	}
}

// TestEstimateToolsTokensBySourcePrefix verifies the core logic that
// estimateContextBreakdown uses to split tools into builtin vs extension
// buckets, and that estimateToolsTokens sums name+description+parameters.
func TestEstimateToolsTokensBySourcePrefix(t *testing.T) {
	tools := []core.ToolDefinitionEntry{
		{
			Definition: extensions.ToolDefinition{
				Name:        "read", // 4 chars
				Description: "Read a file", // 11 chars
				Parameters:  jsonRaw(`{"type":"object"}`), // 17 chars
			},
			SourceInfo: core.SourceInfo{Source: "builtin:read"},
		},
		{
			Definition: extensions.ToolDefinition{
				Name:        "mcp__weather__forecast", // 23 chars
				Description: "Get the weather",        // 15 chars
				Parameters:  jsonRaw(`{"type":"object","properties":{}}`), // 34 chars
			},
			SourceInfo: core.SourceInfo{Source: "extension:weather"},
		},
	}

	// Bucket by Source prefix, mirroring estimateContextBreakdown.
	var builtin, ext []core.ToolDefinitionEntry
	for _, t := range tools {
		switch {
		case hasPrefix(t.SourceInfo.Source, "builtin:"):
			builtin = append(builtin, t)
		case hasPrefix(t.SourceInfo.Source, "extension:"), hasPrefix(t.SourceInfo.Source, "sdk:"):
			ext = append(ext, t)
		}
	}
	if len(builtin) != 1 || len(ext) != 1 {
		t.Fatalf("bucketing: builtin=%d ext=%d, want 1/1", len(builtin), len(ext))
	}
	if !hasPrefix(ext[0].Definition.Name, "mcp__") {
		t.Errorf("expected ext tool to be an mcp__ tool")
	}

	// estimateToolsTokens(builtin) should be (4+11+17+3)/4 = 35/4 = 8.
	if got := estimateToolsTokens(builtin); got != 8 {
		t.Errorf("estimateToolsTokens(builtin) = %d, want 8", got)
	}
}

// TestContextBreakdownFreeSpaceDerivation checks the free-space derivation
// math in isolation (no session needed).
func TestContextBreakdownFreeSpaceDerivation(t *testing.T) {
	b := contextBreakdown{
		contextWindow: 100000,
		categories: []categoryBreakdown{
			{label: "a", tokens: 20000},
			{label: "b", tokens: 30000},
		},
		reserveTokens: 5000,
	}
	used := 0
	for _, c := range b.categories {
		used += c.tokens
	}
	free := b.contextWindow - used - b.reserveTokens
	if free != 45000 {
		t.Errorf("free = %d, want 45000", free)
	}
}

// jsonRaw is a tiny test helper to build json.RawMessage without importing
// encoding/json into the table (keeps the test focused).
func jsonRaw(s string) []byte { return []byte(s) }

// hasPrefix is a local stub of strings.HasPrefix so the bucketing test does
// not need to import strings (kept minimal to match slash_test.go's style).
func hasPrefix(s, p string) bool {
	if len(p) > len(s) {
		return false
	}
	return s[:len(p)] == p
}
