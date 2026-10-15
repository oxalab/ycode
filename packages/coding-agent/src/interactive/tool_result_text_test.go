package interactive

// Audit A9: the edit diff must reach the tool box. toolResultText swaps the
// success line for Details.Diff (reference formatEditResult), editDiffFromDetails
// unwraps the typed/map/*any shapes, and toolBg tints errors red even while
// still partial (reference getEditHeaderBg).

import (
	"strings"
	"testing"

	"ycode/packages/coding-agent/src/core/tools"
	llm "ycode/packages/llm/src"
)

func textResult(s string) []llm.ContentBlock {
	return []llm.ContentBlock{&llm.TextContent{Type: "text", Text: s}}
}

func TestToolResultTextPrefersEditDiff(t *testing.T) {
	details := &tools.EditToolDetails{Diff: " 1 aaa\n-2 bbb\n+2 BBB\n 3 ccc"}
	got := toolResultText(textResult("Successfully replaced 1 block(s) in f.txt."), details, false)
	if got != details.Diff {
		t.Errorf("diff should replace the success line, got %q", got)
	}
}

func TestToolResultTextErrorKeepsErrorText(t *testing.T) {
	details := &tools.EditToolDetails{Diff: "-1 x\n+1 y"}
	got := toolResultText(textResult("Could not edit file: f.txt. boom."), details, true)
	if got != "Could not edit file: f.txt. boom." {
		t.Errorf("error text should win over details, got %q", got)
	}
}

func TestToolResultTextPlainWithoutDetails(t *testing.T) {
	got := toolResultText(textResult("plain output"), nil, false)
	if got != "plain output" {
		t.Errorf("plain results keep their text, got %q", got)
	}
	empty := toolResultText(textResult("plain output"), &tools.EditToolDetails{}, false)
	if empty != "plain output" {
		t.Errorf("empty diff must not replace text, got %q", empty)
	}
}

func TestEditDiffFromDetailsShapes(t *testing.T) {
	typed := &tools.EditToolDetails{Diff: "typed"}
	var wrappedAny any = typed
	decoded := any(map[string]any{"diff": "decoded", "firstChangedLine": 2})

	if got := editDiffFromDetails(typed); got != "typed" {
		t.Errorf("typed details: got %q", got)
	}
	if got := editDiffFromDetails(&wrappedAny); got != "typed" {
		t.Errorf("*any-wrapped typed details: got %q", got)
	}
	if got := editDiffFromDetails(decoded); got != "decoded" {
		t.Errorf("decoded map details: got %q", got)
	}
	if got := editDiffFromDetails(nil); got != "" {
		t.Errorf("nil details: got %q", got)
	}
	// Decoded session entries without a details key carry a typed-nil *any —
	// this must not dereference (panic in TestRebuildTranscript before the guard).
	var nilAny *any
	if got := editDiffFromDetails(nilAny); got != "" {
		t.Errorf("typed-nil *any: got %q", got)
	}
}

func TestToolBgErrorBeatsPartial(t *testing.T) {
	th := diffTheme(t)
	errPartial := toolBg(th, true, true)("x")
	if errPartial != toolBg(th, false, true)("x") {
		t.Error("an error while partial must use the error background")
	}
	if errPartial == toolBg(th, true, false)("x") {
		t.Error("error background must differ from pending background")
	}
}

// Audit A10: the live write preview (reference formatWriteCall) — interior
// blanks kept, 10-line collapse with the expand hint when collapsed, chroma
// highlighting by file path, and the success line suppressed (reference
// formatWriteResult renders nothing on success).

var writePreviewArgs = map[string]any{
	"path":    "notes.txt",
	"content": "one\n\ntwo\n\nthree\n4\n5\n6\n7\n8\n9\n10\n11\n12\n",
}

func TestWritePreviewCollapsedShowsHint(t *testing.T) {
	th := diffTheme(t)
	body := writePreviewBody(writePreviewArgs, "notes.txt", false, th)
	visible := visibleText(body)
	if !strings.Contains(visible, "one\n\ntwo") {
		t.Errorf("interior blank line must survive, got:\n%s", visible)
	}
	// 14 display lines: 3 text + 2 interior blanks + 9 numbered (interior blanks
	// count — that's the fix). Collapsed shows 10, so 4 remain behind the hint.
	if !strings.Contains(visible, "... (4 more lines, 14 total,") {
		t.Errorf("collapsed preview must carry the expand hint, got:\n%s", visible)
	}
}

func TestWritePreviewExpandedShowsAll(t *testing.T) {
	th := diffTheme(t)
	body := writePreviewBody(writePreviewArgs, "notes.txt", true, th)
	visible := visibleText(body)
	if !strings.Contains(visible, "12") || strings.Contains(visible, "more lines") {
		t.Errorf("expanded preview must show every line without the hint, got:\n%s", visible)
	}
}

func TestWritePreviewHighlightsByPath(t *testing.T) {
	th := diffTheme(t)
	body := writePreviewBody(map[string]any{"content": "func main() {}"}, "main.go", true, th)
	if !strings.Contains(body, "\x1b[") {
		t.Error("a .go path should produce chroma-highlighted (ANSI) output")
	}
}

func TestFormatHidesWriteSuccessLine(t *testing.T) {
	th := diffTheme(t)
	tc := NewToolExecution("write", writePreviewArgs, th)
	tc.UpdateResult("Successfully wrote 60 bytes to notes.txt", false, false)
	out := visibleText(tc.format())
	if strings.Contains(out, "Successfully wrote") {
		t.Errorf("write success line must not render (reference formatWriteResult), got:\n%s", out)
	}
	if !strings.Contains(out, "one") {
		t.Errorf("preview body missing, got:\n%s", out)
	}

	tcErr := NewToolExecution("write", map[string]any{"path": "x.txt", "content": "y"}, th)
	tcErr.UpdateResult("failed to write file: boom", true, false)
	if !strings.Contains(visibleText(tcErr.format()), "failed to write file: boom") {
		t.Error("write error result must still render")
	}
}
