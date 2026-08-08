package interactive

// visual_truncate_test.go locks in TruncateToVisualLines behavior against the
// reference components/visual-truncate.ts contract. Each case asserts both the
// returned lines (with padding/width-fill applied) and the skipped count.

import (
	"strings"
	"testing"

	"ycode/packages/tui"
)

func TestTruncateToVisualLinesEmpty(t *testing.T) {
	got := TruncateToVisualLines("", 5, 10, 0)
	if len(got.VisualLines) != 0 || got.SkippedCount != 0 {
		t.Errorf("TruncateToVisualLines('', 5, 10, 0) = %+v, want empty", got)
	}
}

func TestTruncateToVisualLinesShortSingleLine(t *testing.T) {
	// One line shorter than maxVisualLines — returned padded to width.
	got := TruncateToVisualLines("hi", 5, 10, 0)
	if got.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d, want 0", got.SkippedCount)
	}
	if len(got.VisualLines) != 1 || got.VisualLines[0] != "hi        " {
		t.Errorf("VisualLines = %#v, want [\"hi        \"]", got.VisualLines)
	}
}

func TestTruncateToVisualLinesFitsAll(t *testing.T) {
	text := "line1\nline2\nline3"
	got := TruncateToVisualLines(text, 5, 10, 0)
	if got.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d, want 0", got.SkippedCount)
	}
	if len(got.VisualLines) != 3 {
		t.Fatalf("VisualLines len = %d, want 3", len(got.VisualLines))
	}
	// Each line is padded to width 10.
	want0 := "line1     "
	if got.VisualLines[0] != want0 {
		t.Errorf("VisualLines[0] = %q, want %q", got.VisualLines[0], want0)
	}
}

func TestTruncateToVisualLinesTruncates(t *testing.T) {
	text := "line1\nline2\nline3\nline4\nline5"
	got := TruncateToVisualLines(text, 2, 10, 0)
	if got.SkippedCount != 3 {
		t.Errorf("SkippedCount = %d, want 3", got.SkippedCount)
	}
	if len(got.VisualLines) != 2 {
		t.Fatalf("VisualLines len = %d, want 2", len(got.VisualLines))
	}
	// Last 2 visual lines.
	if got.VisualLines[0] != "line4     " {
		t.Errorf("VisualLines[0] = %q, want \"line4     \"", got.VisualLines[0])
	}
	if got.VisualLines[1] != "line5     " {
		t.Errorf("VisualLines[1] = %q, want \"line5     \"", got.VisualLines[1])
	}
}

func TestTruncateToVisualLinesWrapsLongWords(t *testing.T) {
	// "abcdefghij" at width 5 must wrap to 2 visual lines (force-break), then
	// truncate to the last 1.
	got := TruncateToVisualLines("abcdefghij", 1, 5, 0)
	if got.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", got.SkippedCount)
	}
	if len(got.VisualLines) != 1 {
		t.Fatalf("VisualLines len = %d, want 1", len(got.VisualLines))
	}
	// Last wrapped segment, padded to width 5.
	if got.VisualLines[0] != "fghij" {
		t.Errorf("VisualLines[0] = %q, want \"fghij\"", got.VisualLines[0])
	}
}

func TestTruncateToVisualLinesPaddingX(t *testing.T) {
	// paddingX=2 consumes 4 columns; content area is 6. Result is padded left
	// and right, then filled to full width.
	got := TruncateToVisualLines("hi", 5, 10, 2)
	if len(got.VisualLines) != 1 {
		t.Fatalf("VisualLines len = %d, want 1", len(got.VisualLines))
	}
	want := "  hi      " // 2 left-pad + "hi" + 2 right-pad + 4 fill = 10
	if got.VisualLines[0] != want {
		t.Errorf("VisualLines[0] = %q, want %q", got.VisualLines[0], want)
	}
	if w := tui.VisibleWidth(got.VisualLines[0]); w != 10 {
		t.Errorf("VisibleWidth(line) = %d, want 10", w)
	}
}

func TestTruncateToVisualLinesPreservesANSI(t *testing.T) {
	// ANSI color codes must survive wrapping and not count toward visible
	// width. Use a long string so wrapping kicks in.
	red := "\x1b[31m" + strings.Repeat("a", 20) + "\x1b[0m"
	got := TruncateToVisualLines(red, 1, 5, 0)
	if len(got.VisualLines) != 1 {
		t.Fatalf("VisualLines len = %d, want 1", len(got.VisualLines))
	}
	// Last segment must contain the reset code so subsequent output isn't
	// colored, and visible width must equal the render width.
	if !strings.Contains(got.VisualLines[0], "\x1b[0m") {
		t.Errorf("ANSI reset missing from %q", got.VisualLines[0])
	}
	if w := tui.VisibleWidth(got.VisualLines[0]); w != 5 {
		t.Errorf("VisibleWidth = %d, want 5", w)
	}
}

func TestTruncateToVisualLinesEveryLineWidthBounded(t *testing.T) {
	// Random-ish multi-line input at a narrow width — every returned line must
	// be exactly `width` visible columns (padding-fill invariant).
	text := "the quick brown fox\njumps over the lazy dog\nline three here"
	got := TruncateToVisualLines(text, 3, 8, 0)
	for i, l := range got.VisualLines {
		if w := tui.VisibleWidth(l); w != 8 {
			t.Errorf("VisualLines[%d] width = %d, want 8 (line=%q)", i, w, l)
		}
	}
}

// --- Component wrapper ---

func TestVisualTruncateTextCachesByWidth(t *testing.T) {
	v := newVisualTruncateText("hello\nworld", 5, 0)
	first := v.Render(10)
	if len(first) != 2 {
		t.Fatalf("first Render len = %d, want 2", len(first))
	}
	// Same width → cache hit, same slice identity (no recompute).
	second := v.Render(10)
	if &first[0] != &second[0] {
		t.Errorf("Render(10) did not return cached slice")
	}
	// Different width → recompute; lines differ.
	third := v.Render(20)
	if len(third) != 2 {
		t.Fatalf("third Render len = %d, want 2", len(third))
	}
	if third[0] == first[0] {
		t.Errorf("Render(20) returned same content as Render(10) — expected recompute")
	}
}

func TestVisualTruncateTextSetTextInvalidates(t *testing.T) {
	v := newVisualTruncateText("alpha", 5, 0)
	first := v.Render(10)
	if !strings.HasPrefix(first[0], "alpha") {
		t.Fatalf("first Render = %#v, want prefix \"alpha\"", first)
	}
	v.SetText("beta")
	second := v.Render(10)
	if !strings.HasPrefix(second[0], "beta") {
		t.Errorf("after SetText(\"beta\"), Render = %#v, want prefix \"beta\"", second)
	}
}

func TestVisualTruncateTextInvalidate(t *testing.T) {
	v := newVisualTruncateText("hi", 5, 0)
	_ = v.Render(10)
	if v.cachedLines == nil {
		t.Fatal("expected cache populated after Render")
	}
	v.Invalidate()
	if v.cachedLines != nil {
		t.Error("expected cache cleared after Invalidate()")
	}
}
