package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Tools re-audit B2: TruncateHead's loop was missing the `i < maxLines`
// bound, so files with more than MaxLines short lines (under the byte limit)
// were returned untruncated while the footer claimed truncation happened.
func TestTruncateHeadRespectsMaxLines(t *testing.T) {
	const total = 5000
	lines := make([]string, total)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%04d", i) // ~9 bytes/line → ~45KB < 50KB
	}
	content := strings.Join(lines, "\n")

	trunc := TruncateHead(content, TruncationOptions{})

	if !trunc.Truncated {
		t.Fatal("expected Truncated=true for 5000 lines")
	}
	if trunc.TruncatedBy == nil || *trunc.TruncatedBy != "lines" {
		t.Errorf("TruncatedBy = %v, want %q", trunc.TruncatedBy, "lines")
	}
	if trunc.OutputLines != DefaultMaxLines {
		t.Errorf("OutputLines = %d, want %d", trunc.OutputLines, DefaultMaxLines)
	}
	gotLines := strings.Count(trunc.Content, "\n") + 1
	if gotLines != DefaultMaxLines {
		t.Errorf("Content has %d lines, want %d", gotLines, DefaultMaxLines)
	}
	if !strings.Contains(trunc.Content, "line-1999") {
		t.Errorf("Content should include the last allowed line, missing line-1999")
	}
	if strings.Contains(trunc.Content, "line-2000") {
		t.Errorf("Content must not include lines beyond the limit (line-2000 present)")
	}
}

func TestTruncateHeadByteLimitStillWorks(t *testing.T) {
	// One huge line exceeds the byte budget → the dedicated first-line path:
	// nothing kept, flagged FirstLineExceedsLimit (same as TS).
	big := strings.Repeat("x", 100*1024)
	trunc := TruncateHead(big, TruncationOptions{})
	if !trunc.Truncated || trunc.TruncatedBy == nil || *trunc.TruncatedBy != "bytes" {
		t.Fatalf("expected bytes truncation, got Truncated=%v TruncatedBy=%v", trunc.Truncated, trunc.TruncatedBy)
	}
	if !trunc.FirstLineExceedsLimit {
		t.Error("FirstLineExceedsLimit should be set when the first line alone exceeds maxBytes")
	}
	if trunc.OutputLines != 0 || trunc.Content != "" {
		t.Errorf("expected empty output, got %d lines: %q", trunc.OutputLines, trunc.Content)
	}
}

func TestTruncateHeadByteLimitMidFile(t *testing.T) {
	// Byte budget runs out mid-file (first line fits) → truncated by bytes
	// with the lines that fit, not the whole file.
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString(fmt.Sprintf("row-%04d %s\n", i, strings.Repeat("y", 40))) // ~51B/line → ~154KB total
	}
	trunc := TruncateHead(b.String(), TruncationOptions{})
	if !trunc.Truncated || trunc.TruncatedBy == nil || *trunc.TruncatedBy != "bytes" {
		t.Fatalf("expected bytes truncation, got Truncated=%v TruncatedBy=%v", trunc.Truncated, trunc.TruncatedBy)
	}
	if trunc.OutputLines >= 3000 || trunc.OutputBytes > DefaultMaxBytes {
		t.Errorf("byte limit not respected: %d lines, %d bytes", trunc.OutputLines, trunc.OutputBytes)
	}
}

func TestTruncateHeadUnderLimitsUnchanged(t *testing.T) {
	content := "a\nb\nc"
	trunc := TruncateHead(content, TruncationOptions{})
	if trunc.Truncated {
		t.Fatal("small content must not be truncated")
	}
	if trunc.Content != content || trunc.OutputLines != 3 {
		t.Errorf("under-limits content changed: %q (lines %d)", trunc.Content, trunc.OutputLines)
	}
	if trunc.TruncatedBy != nil {
		t.Errorf("TruncatedBy should be nil when not truncated, got %q", *trunc.TruncatedBy)
	}
}

// Tools re-audit B9: TruncatedBy serialized as "" (and the whole struct as
// PascalCase) where the reference emits null / camelCase. TruncatedBy is now
// a *string, so a non-truncated result must marshal "truncatedBy":null.
func TestTruncationResultMarshalsCamelCaseWithNull(t *testing.T) {
	trunc := TruncateHead("a\nb\nc", TruncationOptions{})
	data, err := json.Marshal(trunc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"truncatedBy":null`) {
		t.Errorf(`expected "truncatedBy":null in %s`, got)
	}
	if strings.Contains(got, "TruncatedBy") || strings.Contains(got, "TotalLines") {
		t.Errorf("PascalCase keys leaked into marshal: %s", got)
	}
	if !strings.Contains(got, `"totalLines":3`) {
		t.Errorf("expected camelCase totalLines in %s", got)
	}
}

// Tools re-audit B9, accepted divergence: TruncateLine counts Unicode code
// points and always cuts at a rune boundary, where the TS reference counts
// UTF-16 code units and can split a surrogate pair. Pinned here so the
// rune-boundary behavior is a deliberate, tested choice.
func TestTruncateLineCutsAtRuneBoundary(t *testing.T) {
	line := strings.Repeat("\U0001F600", 10) // 😀 U+1F600, 4 bytes / 1 rune each

	got, truncated := TruncateLine(line, 5)
	if !truncated {
		t.Fatal("expected truncation for 10-rune line at 5")
	}
	want := strings.Repeat("\U0001F600", 5) + "... [truncated]"
	if got != want {
		t.Errorf("got %q, want %q (clean rune cut)", got, want)
	}

	if _, truncated := TruncateLine("short", GrepMaxLineLength); truncated {
		t.Error("short line must not be flagged truncated")
	}
}
