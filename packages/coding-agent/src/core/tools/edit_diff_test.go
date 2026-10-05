package tools

import (
	"strings"
	"testing"
)

// Regression tests for the tools re-audit A1-A4 criticals in edit_diff.go
// (docs/port-divergence-audit.md, "Tools re-audit findings").

// A4: DetectLineEnding must mirror TS detectLineEnding (edit-diff.ts:11-17):
// no LF → "\n"; LF but no CRLF → "\n"; CRLF before first lone LF → "\r\n";
// lone LF before first CRLF → "\n". The old Go code inverted the middle
// cases, rewriting LF-only files as CRLF on every edit.
func TestDetectLineEnding(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"no line endings", "plain text", "\n"},
		{"lf only", "a\nb\nc\n", "\n"},
		{"crlf first", "a\r\nb\nc\n", "\r\n"},
		{"lone lf before crlf", "a\nb\r\nc\r\n", "\n"},
		{"crlf only", "a\r\nb\r\n", "\r\n"},
	}
	for _, tc := range cases {
		if got := DetectLineEnding(tc.content); got != tc.want {
			t.Errorf("%s: DetectLineEnding(%q) = %q, want %q", tc.name, tc.content, got, tc.want)
		}
	}
}

// A2: NormalizeToLF must convert CRLF and lone CR to LF — not to the literal
// letter "n" (the old bug corrupted every CRLF file).
func TestNormalizeToLF(t *testing.T) {
	if got := NormalizeToLF("a\r\nb\r\nc"); got != "a\nb\nc" {
		t.Errorf("CRLF: got %q, want %q", got, "a\nb\nc")
	}
	if got := NormalizeToLF("a\rb\nc"); got != "a\nb\nc" {
		t.Errorf("lone CR: got %q, want %q", got, "a\nb\nc")
	}
	if strings.Contains(NormalizeToLF("x\r\ny"), "n\n") || strings.HasPrefix(NormalizeToLF("x\r\ny"), "xn") {
		t.Errorf("literal-n corruption regression: %q", NormalizeToLF("x\r\ny"))
	}
}

// A3: the apply splice must skip matchIndex+matchLength, not
// matchIndex+matchIndex. This edit has different index and length, so the old
// bug mangled the tail (or panicked).
func TestApplyEditsToNormalizedContent_Splice(t *testing.T) {
	content := "aaa\nTARGETXX\nzzz\n"
	result, err := ApplyEditsToNormalizedContent(content, []Edit{
		{OldText: "TARGETXX", NewText: "REPLACED"},
	}, "test.txt")
	if err != nil {
		t.Fatalf("ApplyEditsToNormalizedContent error: %v", err)
	}
	want := "aaa\nREPLACED\nzzz\n"
	if result.NewContent != want {
		t.Fatalf("NewContent = %q, want %q", result.NewContent, want)
	}
	if result.BaseContent != content {
		t.Fatalf("BaseContent = %q, want %q", result.BaseContent, content)
	}
}

// A1: GenerateDiffString panicked with index-out-of-range on ANY non-empty
// diff (inner loop condition used i instead of j). It must now return a sane
// unified diff.
func TestGenerateDiffString_NoPanicAndOutput(t *testing.T) {
	old := "line1\nline2\nline3\nline4\nline5\n"
	new := "line1\nline2-changed\nline3\nline4\nline5\n"

	var diff string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("GenerateDiffString panicked: %v", r)
			}
		}()
		diff, _ = GenerateDiffString(old, new, 4)
	}()

	if !strings.Contains(diff, "line2") {
		t.Errorf("diff missing changed line, got %q", diff)
	}
	if strings.Contains(diff, "line2\n") && !strings.Contains(diff, "line2-changed") {
		t.Errorf("diff should mention the replacement, got %q", diff)
	}
	if strings.Count(diff, "\n") == 0 {
		t.Errorf("diff unexpectedly empty")
	}
}

// A2+A4 end-to-end: an edit round trip must preserve the file's original line
// endings (LF stays LF, CRLF stays CRLF) and never inject corruption.
func TestEditRoundTrip_PreservesLineEndings(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"lf file", "aaa\nTARGET\nzzz\n"},
		{"crlf file", "aaa\r\nTARGET\r\nzzz\r\n"},
	}
	for _, tc := range cases {
		ending := DetectLineEnding(tc.content)
		normalized := NormalizeToLF(tc.content)

		result, err := ApplyEditsToNormalizedContent(normalized, []Edit{
			{OldText: "TARGET", NewText: "REPLACED"},
		}, "test.txt")
		if err != nil {
			t.Fatalf("%s: apply error: %v", tc.name, err)
		}

		final := RestoreLineEndings(result.NewContent, ending)
		if strings.Contains(final, "REPLACED") == false {
			t.Errorf("%s: replacement missing: %q", tc.name, final)
		}
		if ending == "\n" && strings.Contains(final, "\r") {
			t.Errorf("%s: LF file must stay LF, got CR: %q", tc.name, final)
		}
		if ending == "\r\n" && strings.Count(final, "\r\n") != 3 {
			t.Errorf("%s: CRLF file must keep CRLF endings, got %q", tc.name, final)
		}
		if strings.Contains(final, "TARGET") {
			t.Errorf("%s: old text should be gone: %q", tc.name, final)
		}
	}
}
