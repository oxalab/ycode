package cli

import (
	"bytes"
	"strings"
	"testing"

	llm "ycode/packages/llm/src"
)

func TestFormatTokenCount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1K"},
		{2000, "2K"},
		{1500, "1.5K"},
		{200000, "200K"},
		{125000, "125K"},
		{1_000_000, "1M"},
		{2_000_000, "2M"},
		{1_500_000, "1.5M"},
	}
	for _, c := range cases {
		if got := formatTokenCount(c.in); got != c.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFuzzyMatch(t *testing.T) {
	if !fuzzyMatch("anthropic claude-sonnet", "") {
		t.Error("empty query should match everything")
	}
	if !fuzzyMatch("anthropic claude-sonnet", "anson") { // subsequence: a-n-s-o-n
		t.Error("'anson' should match 'anthropic claude-sonnet'")
	}
	if fuzzyMatch("anthropic claude-sonnet", "xyz") {
		t.Error("'xyz' should not match")
	}
	if !fuzzyMatch("OpenAI GPT", "oai") { // case-folded
		t.Error("'oai' should match 'OpenAI GPT' case-insensitively")
	}
}

func TestYesNo_HasImageInput(t *testing.T) {
	if yesNo(true) != "yes" || yesNo(false) != "no" {
		t.Errorf("yesNo(true)=%q yesNo(false)=%q", yesNo(true), yesNo(false))
	}
	if !hasImageInput([]llm.InputType{"text", "image"}) {
		t.Error("expected image input detected")
	}
	if hasImageInput([]llm.InputType{"text"}) {
		t.Error("did not expect image input")
	}
	if hasImageInput(nil) {
		t.Error("nil inputs should not report image")
	}
}

func TestRenderModelTable(t *testing.T) {
	// Deliberately unsorted; renderModelTable must sort by provider then id.
	models := []*llm.Model[llm.Api]{
		{ID: "sonnet", Provider: "zzz-last", ContextWindow: 200000, MaxTokens: 8000, Reasoning: true, Input: []llm.InputType{"text", "image"}},
		{ID: "gpt-4o", Provider: "openai", ContextWindow: 128000, MaxTokens: 16384, Reasoning: false, Input: []llm.InputType{"text"}},
	}
	var buf bytes.Buffer
	renderModelTable(models, &buf)
	out := buf.String()

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), out)
	}

	// Header contains all column labels.
	for _, h := range []string{"provider", "model", "context", "max-out", "thinking", "images"} {
		if !strings.Contains(lines[0], h) {
			t.Errorf("header %q missing from %q", h, lines[0])
		}
	}

	// Sorted: openai row before zzz-last row.
	if !strings.Contains(lines[1], "openai") {
		t.Errorf("first data row should be openai, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "zzz-last") {
		t.Errorf("second data row should be zzz-last, got %q", lines[2])
	}

	// Token formatting + yes/no present.
	if !strings.Contains(out, "200K") || !strings.Contains(out, "128K") {
		t.Errorf("expected formatted token counts in output:\n%s", out)
	}
	if !strings.Contains(lines[1], "no") { // gpt-4o: reasoning=false, images=no
		t.Errorf("expected 'no' for gpt-4o row, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "yes") { // sonnet: reasoning=true, images=yes
		t.Errorf("expected 'yes' for sonnet row, got %q", lines[2])
	}

	// Column alignment: the provider column width is the max provider length
	// (here "provider"/"zzz-last" = 8), so the model cell starts at width+2 in
	// every line — header included. Verify each line's model cell is aligned.
	providerWidth := 8 // max("provider", "openai", "zzz-last")
	modelCol := providerWidth + 2
	if len(lines[0]) <= modelCol || !strings.HasPrefix(lines[0][modelCol:], "model") {
		t.Errorf("header model cell not aligned at col %d:\n %q", modelCol, lines[0])
	}
	if len(lines[1]) <= modelCol || !strings.HasPrefix(lines[1][modelCol:], "gpt-4o") {
		t.Errorf("openai row model cell not aligned at col %d:\n %q", modelCol, lines[1])
	}
	if len(lines[2]) <= modelCol || !strings.HasPrefix(lines[2][modelCol:], "sonnet") {
		t.Errorf("sonnet row model cell not aligned at col %d:\n %q", modelCol, lines[2])
	}
}

func TestFormatNoModelsAvailableMessage(t *testing.T) {
	msg := formatNoModelsAvailableMessage()
	if !strings.HasPrefix(msg, "No models available.") {
		t.Errorf("message = %q, want prefix 'No models available.'", msg)
	}
	if !strings.Contains(msg, "/login") {
		t.Errorf("message should mention /login: %q", msg)
	}
}
