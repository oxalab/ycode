package components

import (
	"strings"
	"testing"

	"ycode/packages/tui"
)

// ---- Spacer ----

func TestSpacer(t *testing.T) {
	s := NewSpacer(3)
	lines := s.Render(10)
	if len(lines) != 3 {
		t.Fatalf("got %d lines", len(lines))
	}
	for _, l := range lines {
		if l != "" {
			t.Errorf("spacer line should be empty: %q", l)
		}
	}
}

// ---- TruncatedText (ports truncated-text.test.ts) ----

func TestTruncatedTextPadsToWidth(t *testing.T) {
	tt := NewTruncatedText("Hello world", 1, 0)
	lines := tt.Render(50)
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
	if w := tui.VisibleWidth(lines[0]); w != 50 {
		t.Errorf("width=%d want 50", w)
	}
}

func TestTruncatedTextVerticalPadding(t *testing.T) {
	tt := NewTruncatedText("Hello", 0, 2)
	lines := tt.Render(40)
	if len(lines) != 5 {
		t.Fatalf("got %d lines want 5", len(lines))
	}
	for i, l := range lines {
		if w := tui.VisibleWidth(l); w != 40 {
			t.Errorf("line %d width=%d want 40", i, w)
		}
	}
}

func TestTruncatedTextTruncates(t *testing.T) {
	long := "This is a very long piece of text that will definitely exceed the available width"
	tt := NewTruncatedText(long, 1, 0)
	lines := tt.Render(30)
	if len(lines) != 1 || tui.VisibleWidth(lines[0]) != 30 {
		t.Fatalf("expected 1 line of width 30, got %d lines", len(lines))
	}
	if !strings.Contains(stripAnsiForTest(lines[0]), "...") {
		t.Errorf("should contain ellipsis: %q", lines[0])
	}
}

func TestTruncatedTextPreservesANSI(t *testing.T) {
	styled := "\x1b[31mHello\x1b[0m \x1b[34mworld\x1b[0m"
	tt := NewTruncatedText(styled, 1, 0)
	lines := tt.Render(40)
	if tui.VisibleWidth(lines[0]) != 40 {
		t.Errorf("width=%d", tui.VisibleWidth(lines[0]))
	}
	if !strings.Contains(lines[0], "\x1b[") {
		t.Error("should preserve ANSI codes")
	}
}

func TestTruncatedTextStyledResetBeforeEllipsis(t *testing.T) {
	long := "\x1b[31m" + "This is a very long red text that will be truncated" + "\x1b[0m"
	tt := NewTruncatedText(long, 1, 0)
	lines := tt.Render(20)
	if tui.VisibleWidth(lines[0]) != 20 {
		t.Errorf("width=%d", tui.VisibleWidth(lines[0]))
	}
	if !strings.Contains(lines[0], "\x1b[0m...") {
		t.Errorf("should have reset before ellipsis: %q", lines[0])
	}
}

func TestTruncatedTextExactFit(t *testing.T) {
	tt := NewTruncatedText("Hello world", 1, 0)
	lines := tt.Render(30)
	if strings.Contains(stripAnsiForTest(lines[0]), "...") {
		t.Errorf("should not truncate: %q", lines[0])
	}
}

func TestTruncatedTextEmpty(t *testing.T) {
	tt := NewTruncatedText("", 1, 0)
	lines := tt.Render(30)
	if len(lines) != 1 || tui.VisibleWidth(lines[0]) != 30 {
		t.Errorf("empty text: %d lines width %d", len(lines), tui.VisibleWidth(lines[0]))
	}
}

func TestTruncatedTextStopsAtNewline(t *testing.T) {
	tt := NewTruncatedText("First line\nSecond line\nThird line", 1, 0)
	lines := tt.Render(40)
	if len(lines) != 1 {
		t.Fatalf("got %d lines want 1", len(lines))
	}
	if !strings.Contains(stripAnsiForTest(lines[0]), "First line") {
		t.Errorf("should contain first line: %q", lines[0])
	}
	if strings.Contains(stripAnsiForTest(lines[0]), "Second line") {
		t.Errorf("should not contain second line: %q", lines[0])
	}
}

// ---- Text ----

func TestTextEmpty(t *testing.T) {
	tx := NewText("", 1, 1, nil)
	if lines := tx.Render(40); len(lines) != 0 {
		t.Errorf("empty text should render nothing, got %d lines", len(lines))
	}
}

func TestTextWrap(t *testing.T) {
	tx := NewText("hello world this is a test of wrapping", 1, 1, nil)
	lines := tx.Render(20)
	if len(lines) == 0 {
		t.Fatal("expected wrapped lines")
	}
	for i, l := range lines {
		if w := tui.VisibleWidth(l); w > 20 {
			t.Errorf("line %d width %d > 20", i, w)
		}
	}
	// Cache: second render returns the same result.
	lines2 := tx.Render(20)
	if len(lines2) != len(lines) {
		t.Error("cache mismatch")
	}
}

// ---- Box ----

func TestBoxPadding(t *testing.T) {
	b := NewBox(1, 1, nil)
	b.AddChild(NewText("hi", 0, 0, nil))
	lines := b.Render(20)
	if len(lines) == 0 {
		t.Fatal("expected padded lines")
	}
}

// ---- Loader ----

func TestLoaderRender(t *testing.T) {
	l := NewLoader(nil, nil, nil, "Working", nil)
	defer l.Stop()
	lines := l.Render(20)
	// Loader prepends a blank line: ["", message...]
	if len(lines) < 2 {
		t.Fatalf("expected blank + message, got %d lines: %v", len(lines), lines)
	}
	if lines[0] != "" {
		t.Errorf("first line should be blank: %q", lines[0])
	}
}

// ---- CancellableLoader ----

func TestCancellableLoaderAbort(t *testing.T) {
	c := NewCancellableLoader(nil, nil, nil, "")
	defer c.Dispose()
	if c.Aborted() {
		t.Fatal("should not be aborted initially")
	}
	c.HandleInput("\x1b") // escape → tui.select.cancel
	if !c.Aborted() {
		t.Error("should be aborted after escape")
	}
}

// ---- Image (fallback) ----

func TestImageFallback(t *testing.T) {
	tui.SetCapabilities(tui.TerminalCapabilities{Images: "", TrueColor: false, Hyperlinks: false})
	defer tui.ResetCapabilitiesCache()
	img := NewImage("AAAA", "image/png", ImageTheme{FallbackColor: nil}, ImageOptions{})
	lines := img.Render(40)
	if len(lines) != 1 {
		t.Fatalf("expected 1 fallback line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "[image/png]") {
		t.Errorf("fallback should mention mime: %q", lines[0])
	}
}

func stripAnsiForTest(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
