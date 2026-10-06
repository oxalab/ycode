package tools

import (
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
	if trunc.TruncatedBy != "lines" {
		t.Errorf("TruncatedBy = %q, want %q", trunc.TruncatedBy, "lines")
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
	if !trunc.Truncated || trunc.TruncatedBy != "bytes" {
		t.Fatalf("expected bytes truncation, got Truncated=%v TruncatedBy=%q", trunc.Truncated, trunc.TruncatedBy)
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
	if !trunc.Truncated || trunc.TruncatedBy != "bytes" {
		t.Fatalf("expected bytes truncation, got Truncated=%v TruncatedBy=%q", trunc.Truncated, trunc.TruncatedBy)
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
	if trunc.TruncatedBy != "" {
		t.Errorf("TruncatedBy should be empty when not truncated, got %q", trunc.TruncatedBy)
	}
}
