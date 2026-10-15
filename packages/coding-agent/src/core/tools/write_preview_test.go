package tools

// Audit A10: the write tool's RenderCall preview must keep interior blank
// lines (only trailing ones are trimmed, reference trimTrailingEmptyLines),
// collapse to 10 lines with the expand hint, accept the file_path alias, and
// flag non-string content — matching reference formatWriteCall.

import (
	"fmt"
	"strings"
	"testing"
)

func TestWritePreviewKeepsInteriorBlankLines(t *testing.T) {
	def := CreateWriteToolDefinition(".", nil)
	rendered, ok := def.RenderCall(map[string]any{
		"path":    "notes.md",
		"content": "one\n\ntwo\n",
	}).(string)
	if !ok {
		t.Fatal("RenderCall did not return a string")
	}
	if !strings.Contains(rendered, "one\n\ntwo") {
		t.Errorf("interior blank line must be kept, got:\n%q", rendered)
	}
	if strings.Contains(rendered, "two\n\n") {
		t.Errorf("trailing blank lines must be trimmed, got:\n%q", rendered)
	}
}

func TestWritePreviewCollapsesWithExpandHint(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	def := CreateWriteToolDefinition(".", nil)
	rendered, _ := def.RenderCall(map[string]any{
		"path":    "big.txt",
		"content": b.String(),
	}).(string)

	if !strings.Contains(rendered, "line 10") || strings.Contains(rendered, "line 11\n") {
		t.Errorf("collapsed preview must show exactly 10 lines, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "... (2 more lines, 12 total, ctrl+o to expand)") {
		t.Errorf("expand hint missing or wrong, got:\n%s", rendered)
	}
}

func TestWritePreviewInvalidAndEmptyContent(t *testing.T) {
	def := CreateWriteToolDefinition(".", nil)

	rendered, _ := def.RenderCall(map[string]any{"path": "x.txt"}).(string)
	if !strings.Contains(rendered, "[invalid content arg - expected string]") {
		t.Errorf("non-string content should show the invalid-arg body, got:\n%q", rendered)
	}

	rendered, _ = def.RenderCall(map[string]any{"path": "x.txt", "content": ""}).(string)
	if strings.Contains(rendered, "\n\n") {
		t.Errorf("empty content should render header only, got:\n%q", rendered)
	}
}

func TestWritePreviewAcceptsFilePathAlias(t *testing.T) {
	def := CreateWriteToolDefinition(".", nil)
	rendered, _ := def.RenderCall(map[string]any{
		"file_path": "aliased.txt",
		"content":   "hi\n",
	}).(string)
	if !strings.Contains(rendered, "aliased.txt") {
		t.Errorf("file_path alias should feed the header, got:\n%q", rendered)
	}
	if !strings.Contains(rendered, "hi") {
		t.Errorf("content missing from preview, got:\n%q", rendered)
	}
}
