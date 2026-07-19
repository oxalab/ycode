package tui

import (
	"strings"
	"testing"
)

// ---- VisibleWidth + NormalizeTerminalOutput ----

func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"\t\x1b[31m界\x1b[0m", 5}, // tab=3 + 界=2, ANSI ignored
		{"🇨", 2},                    // lone regional indicator
		{"🇨🇳", 2},                   // flag pair
		{"ำ", 1},                    // Thai AM
		{"ຳ", 1},                    // Lao AM
		{"กำ", 2},                   // Thai + AM
		{"ກຳ", 2},                   // Lao + AM
		{"👍", 2},
		{"✅", 2},
		{"⚡", 2},
		{"⚡️", 2}, // lightning + VS16
		{"👨‍💻", 2}, // man ZWJ computer
		{"🏳️‍🌈", 2}, // rainbow flag
		{"\x1b]133;A\x07hello\x1b]133;B\x07", 5},   // OSC 133 BEL stripped
		{"\x1b]133;A\x1b\\hello\x1b]133;B\x1b\\", 5}, // OSC 133 ST stripped
	}
	for _, c := range cases {
		if got := VisibleWidth(c.in); got != c.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAllRegionalIndicatorsWidth2(t *testing.T) {
	for cp := rune(0x1f1e6); cp <= 0x1f1ff; cp++ {
		if got := VisibleWidth(string(cp)); got != 2 {
			t.Errorf("regional indicator U+%X width %d, want 2", cp, got)
		}
	}
}

func TestNormalizeTerminalOutput(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ำ", "ํา"},
		{"ຳ", "ໍາ"},
	}
	for _, c := range cases {
		if got := NormalizeTerminalOutput(c.in); got != c.want {
			t.Errorf("NormalizeTerminalOutput(%q)=%q want %q", c.in, got, c.want)
		}
	}
	// Normalized AM keeps the same visible width.
	if VisibleWidth(NormalizeTerminalOutput("ำabc")) != VisibleWidth("ำabc") {
		t.Error("normalization changed width")
	}
}

// ---- WrapTextWithAnsi ----

func TestWrapTextWithAnsiPlain(t *testing.T) {
	wrapped := WrapTextWithAnsi("hello world this is a test", 10)
	if len(wrapped) < 2 {
		t.Fatalf("expected multiple lines, got %v", wrapped)
	}
	for i, l := range wrapped {
		if w := VisibleWidth(l); w > 10 {
			t.Errorf("line %d width %d > 10: %q", i, w, l)
		}
	}
}

func TestWrapTextWithAnsiCJKBreak(t *testing.T) {
	text := "This is an example 中文汉字测试段落内容中文汉字测试段落内容."
	wrapped := WrapTextWithAnsi(text, 40)
	want := []string{
		"This is an example 中文汉字测试段落内容",
		"中文汉字测试段落内容.",
	}
	if len(wrapped) != len(want) {
		t.Fatalf("got %d lines %v, want %d", len(wrapped), wrapped, len(want))
	}
	for i := range want {
		if wrapped[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, wrapped[i], want[i])
		}
	}
}

func TestWrapTextWithAnsiPreservesColor(t *testing.T) {
	text := "\x1b[31mThis is an example 中文汉字测试段落内容中文汉字测试段落内容.\x1b[0m"
	wrapped := WrapTextWithAnsi(text, 40)
	if len(wrapped) != 2 {
		t.Fatalf("got %v", wrapped)
	}
	if wrapped[0] != "\x1b[31mThis is an example 中文汉字测试段落内容" {
		t.Errorf("line0=%q", wrapped[0])
	}
	if wrapped[1] != "\x1b[31m中文汉字测试段落内容.\x1b[0m" {
		t.Errorf("line1=%q", wrapped[1])
	}
}

func TestWrapUnderlineNotBeforeStyledText(t *testing.T) {
	underlineOn := "\x1b[4m"
	url := "https://example.com/very/long/path/that/will/wrap"
	text := "read this thread " + underlineOn + url + "\x1b[24m"
	wrapped := WrapTextWithAnsi(text, 40)
	if wrapped[0] != "read this thread" {
		t.Fatalf("line0=%q want no underline", wrapped[0])
	}
	if !strings.HasPrefix(wrapped[1], underlineOn) || !strings.Contains(wrapped[1], "https://") {
		t.Fatalf("line1=%q should start with underline", wrapped[1])
	}
}

func TestWrapBackgroundPreservedNoFullReset(t *testing.T) {
	bgBlue := "\x1b[44m"
	text := bgBlue + "hello world this is blue background text" + "\x1b[0m"
	wrapped := WrapTextWithAnsi(text, 15)
	for i, l := range wrapped {
		if !strings.Contains(l, bgBlue) {
			t.Errorf("line %d missing bg: %q", i, l)
		}
	}
	for i := 0; i < len(wrapped)-1; i++ {
		if strings.HasSuffix(wrapped[i], "\x1b[0m") {
			t.Errorf("middle line %d ends with full reset: %q", i, wrapped[i])
		}
	}
}

func TestWrapTrimsTrailingWhitespaceOverflow(t *testing.T) {
	wrapped := WrapTextWithAnsi("  ", 1)
	if VisibleWidth(wrapped[0]) > 1 {
		t.Errorf("got %q width %d", wrapped[0], VisibleWidth(wrapped[0]))
	}
}

// ---- WrapTextWithAnsi OSC 8 hyperlinks ----

func TestWrapOsc8ReopenOnContinuation(t *testing.T) {
	url := "https://example.com"
	input := "\x1b]8;;" + url + "\x1b\\0123456789\x1b]8;;\x1b\\"
	lines := WrapTextWithAnsi(input, 6)
	for _, line := range lines {
		stripped := strings.ReplaceAll(line, "\x1b]8;;"+url+"\x1b\\", "")
		stripped = stripSgr(stripped)
		if strings.TrimSpace(stripped) != "" {
			if !strings.HasPrefix(line, "\x1b]8;;"+url+"\x1b\\") && !strings.Contains(line, "\x1b]8;;"+url+"\x1b\\") {
				t.Errorf("line with text missing OSC8 reopen: %q", line)
			}
		}
	}
}

func TestWrapOsc8ClosesBeforeLineBreak(t *testing.T) {
	url := "https://example.com"
	input := "\x1b]8;;" + url + "\x1b\\0123456789\x1b]8;;\x1b\\"
	lines := WrapTextWithAnsi(input, 6)
	for i := 0; i < len(lines)-1; i++ {
		if strings.Contains(lines[i], "\x1b]8;;"+url+"\x1b\\") {
			if !strings.HasSuffix(lines[i], "\x1b]8;;\x1b\\") {
				t.Errorf("non-final line not closed: %q", lines[i])
			}
		}
	}
}

func TestWrapOsc8PreservesBELTerminator(t *testing.T) {
	url := "https://example.com/oauth/" + strings.Repeat("a", 32)
	input := "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
	lines := WrapTextWithAnsi(input, 20)
	if len(lines) < 2 {
		t.Fatalf("expected wrap, got %v", lines)
	}
	for _, l := range lines {
		if !strings.Contains(l, "\x1b]8;;"+url+"\x07") {
			t.Errorf("line missing BEL reopen: %q", l)
		}
		if strings.Contains(l, "\x1b]8;;"+url+"\x1b\\") {
			t.Errorf("line reopened with ST instead of BEL: %q", l)
		}
	}
}

func TestWrapOsc8SingleLineOneOpenOneClose(t *testing.T) {
	url := "https://example.com"
	input := "before \x1b]8;;" + url + "\x1b\\link\x1b]8;;\x1b\\ after"
	lines := WrapTextWithAnsi(input, 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %v", lines)
	}
	opens := strings.Count(lines[0], "\x1b]8;;"+url+"\x1b\\")
	closes := strings.Count(lines[0], "\x1b]8;;\x1b\\")
	if opens != 1 || closes != 1 {
		t.Errorf("opens=%d closes=%d on %q", opens, closes, lines[0])
	}
}

// ---- TruncateToWidth ----

func TestTruncateHugeUnicode(t *testing.T) {
	text := strings.Repeat("🙂界", 100000)
	got := TruncateToWidth(text, 40, "…", false)
	if w := VisibleWidth(got); w > 40 {
		t.Errorf("width %d > 40", w)
	}
	if !strings.HasSuffix(got, "…\x1b[0m") {
		t.Errorf("suffix wrong: %q", got[len(got)-10:])
	}
}

func TestTruncatePreservesAnsi(t *testing.T) {
	text := "\x1b[31m" + strings.Repeat("hello ", 1000) + "\x1b[0m"
	got := TruncateToWidth(text, 20, "…", false)
	if w := VisibleWidth(got); w > 20 {
		t.Errorf("width %d > 20", w)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Error("missing red code")
	}
	if !strings.HasSuffix(got, "\x1b[0m…\x1b[0m") {
		t.Errorf("suffix wrong: %q", got[len(got)-15:])
	}
}

func TestTruncateMalformedAnsiNoHang(t *testing.T) {
	text := "abc\x1bnot-ansi " + strings.Repeat("🙂", 1000)
	got := TruncateToWidth(text, 20, "…", false)
	if w := VisibleWidth(got); w > 20 {
		t.Errorf("width %d > 20: %q", w, got)
	}
}

func TestTruncateWideEllipsis(t *testing.T) {
	if got := TruncateToWidth("abcdef", 1, "🙂", false); got != "" {
		t.Errorf("width1 got %q", got)
	}
	got := TruncateToWidth("abcdef", 2, "🙂", false)
	if got != "\x1b[0m🙂\x1b[0m" {
		t.Errorf("width2 got %q", got)
	}
}

func TestTruncateReturnsOriginalWhenFits(t *testing.T) {
	if got := TruncateToWidth("a", 2, "🙂", false); got != "a" {
		t.Errorf("got %q", got)
	}
	if got := TruncateToWidth("界", 2, "🙂", false); got != "界" {
		t.Errorf("got %q", got)
	}
}

func TestTruncatePads(t *testing.T) {
	got := TruncateToWidth("🙂界🙂界🙂界", 8, "…", true)
	if w := VisibleWidth(got); w != 8 {
		t.Errorf("width %d != 8: %q", w, got)
	}
}

func TestTruncateNoEllipsisTrailingReset(t *testing.T) {
	got := TruncateToWidth("\x1b[31m"+strings.Repeat("hello", 100), 10, "", false)
	if w := VisibleWidth(got); w > 10 {
		t.Errorf("width %d > 10", w)
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("missing reset: %q", got[len(got)-5:])
	}
}

func TestTruncateContiguousPrefix(t *testing.T) {
	got := TruncateToWidth("🙂\t界 \x1b_abc\x07", 7, "…", true)
	want := "🙂\t\x1b[0m…\x1b[0m "
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// ---- sliceWithWidth / ExtractSegments tab accounting ----

func TestSliceWithWidthTab(t *testing.T) {
	text := "out 192M\t.pi/skill-tests/results-ha"
	s := sliceWithWidth(text, 0, 10, true)
	if s.text != "out 192M" || s.width != 8 {
		t.Errorf("got %+v", s)
	}
	if VisibleWidth(s.text) != s.width {
		t.Errorf("width mismatch")
	}
}

func TestExtractSegmentsTab(t *testing.T) {
	text := "out 192M\t.pi/skill-tests/results-ha"
	seg := ExtractSegments(text, 10, 13, 10, true)
	if seg.Before != "out 192M" || seg.BeforeWidth != 8 {
		t.Errorf("before=%q width=%d", seg.Before, seg.BeforeWidth)
	}
	seg2 := ExtractSegments(text, 11, 13, 10, true)
	if seg2.Before != "out 192M\t" || seg2.BeforeWidth != 11 {
		t.Errorf("before=%q width=%d", seg2.Before, seg2.BeforeWidth)
	}
}

// ---- regional indicator regression (wrap) ----

func TestWrapRegionalIndicatorListLine(t *testing.T) {
	wrapped := WrapTextWithAnsi("      - 🇨", 9)
	if len(wrapped) != 2 {
		t.Fatalf("got %v", wrapped)
	}
	if VisibleWidth(wrapped[0]) != 7 {
		t.Errorf("line0 width %d: %q", VisibleWidth(wrapped[0]), wrapped[0])
	}
	if VisibleWidth(wrapped[1]) != 2 {
		t.Errorf("line1 width %d: %q", VisibleWidth(wrapped[1]), wrapped[1])
	}
}

func stripSgr(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if code, length, ok := extractAnsiCodeAt(s, i); ok {
			_ = code
			i += length
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
