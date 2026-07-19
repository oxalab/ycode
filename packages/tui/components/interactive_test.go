package components

import (
	"strings"
	"testing"

	"ycode/packages/tui"
)

// ---- SelectList tests (ports select-list.test.ts) ----

var selectTestTheme = SelectListTheme{
	SelectedPrefix: func(s string) string { return s },
	SelectedText:   func(s string) string { return s },
	Description:    func(s string) string { return s },
	ScrollInfo:     func(s string) string { return s },
	NoMatch:        func(s string) string { return s },
}

func visibleIndexOf(line, text string) int {
	idx := strings.Index(line, text)
	if idx < 0 {
		return -1
	}
	return tui.VisibleWidth(line[:idx])
}

func TestSelectListMultilineDescription(t *testing.T) {
	items := []SelectItem{
		{Value: "test", Label: "test", Description: "Line one\nLine two\nLine three"},
	}
	list := NewSelectList(items, 5, selectTestTheme)
	rendered := list.Render(100)
	if len(rendered) == 0 {
		t.Fatal("no output")
	}
	if strings.Contains(rendered[0], "\n") {
		t.Error("description should be single-line")
	}
	if !strings.Contains(rendered[0], "Line one Line two Line three") {
		t.Errorf("multiline desc not collapsed: %q", rendered[0])
	}
}

func TestSelectListDescriptionAlignment(t *testing.T) {
	items := []SelectItem{
		{Value: "short", Label: "short", Description: "short desc"},
		{Value: "very-long-name-needs-truncation", Label: "very-long-name-needs-truncation", Description: "long desc"},
	}
	list := NewSelectList(items, 5, selectTestTheme)
	rendered := list.Render(80)
	if len(rendered) < 2 {
		t.Fatalf("expected 2 rows, got %d", len(rendered))
	}
	col1 := visibleIndexOf(rendered[0], "short desc")
	col2 := visibleIndexOf(rendered[1], "long desc")
	if col1 < 0 || col2 < 0 {
		t.Fatalf("descriptions not found: %v", rendered)
	}
	if col1 != col2 {
		t.Errorf("description columns misaligned: %d vs %d", col1, col2)
	}
}

func TestSelectListMinColumnWidth(t *testing.T) {
	items := []SelectItem{
		{Value: "a", Label: "a", Description: "first"},
		{Value: "bb", Label: "bb", Description: "second"},
	}
	list := NewSelectList(items, 5, selectTestTheme, SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 20})
	rendered := list.Render(80)
	if col := visibleIndexOf(rendered[0], "first"); col != 14 {
		t.Errorf("min col: first at col %d want 14 (row=%q)", col, rendered[0])
	}
}

func TestSelectListMaxColumnWidth(t *testing.T) {
	items := []SelectItem{
		{Value: "very-long-name-needs-truncation", Label: "very-long-name-needs-truncation", Description: "first"},
		{Value: "short", Label: "short", Description: "second"},
	}
	list := NewSelectList(items, 5, selectTestTheme, SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 20})
	rendered := list.Render(80)
	col1 := visibleIndexOf(rendered[0], "first")
	if col1 != 22 {
		t.Errorf("max col: first at col %d want 22", col1)
	}
}

// ---- Input tests (ports key input.test.ts cases) ----

func TestInputSubmitBackslash(t *testing.T) {
	input := NewInput()
	var submitted string
	input.OnSubmit = func(v string) { submitted = v }
	for _, ch := range "hello\\" {
		input.HandleInput(string(ch))
	}
	input.HandleInput("\r")
	if submitted != "hello\\" {
		t.Errorf("submitted=%q want hello\\", submitted)
	}
}

func TestInputKillRingCWY(t *testing.T) {
	input := NewInput()
	input.SetValue("foo bar baz")
	input.HandleInput("\x05")       // Ctrl+E (end)
	input.HandleInput("\x17")       // Ctrl+W → deletes "baz"
	if input.GetValue() != "foo bar " {
		t.Fatalf("after CW: %q", input.GetValue())
	}
	input.HandleInput("\x01")       // Ctrl+A (start)
	input.HandleInput("\x19")       // Ctrl+Y (yank)
	if input.GetValue() != "bazfoo bar " {
		t.Errorf("after CY: %q", input.GetValue())
	}
}

func TestInputKillRingCU(t *testing.T) {
	input := NewInput()
	input.SetValue("hello world")
	input.HandleInput("\x01") // Ctrl+A
	for i := 0; i < 6; i++ {
		input.HandleInput("\x1b[C") // right 6
	}
	input.HandleInput("\x15") // Ctrl+U → deletes "hello "
	if input.GetValue() != "world" {
		t.Fatalf("after CU: %q", input.GetValue())
	}
	input.HandleInput("\x19") // Ctrl+Y
	if input.GetValue() != "hello world" {
		t.Errorf("after CY: %q", input.GetValue())
	}
}

func TestInputKillRingCK(t *testing.T) {
	input := NewInput()
	input.SetValue("hello world")
	input.HandleInput("\x01")  // Ctrl+A
	input.HandleInput("\x0b")  // Ctrl+K → deletes all
	if input.GetValue() != "" {
		t.Fatalf("after CK: %q", input.GetValue())
	}
	input.HandleInput("\x19") // Ctrl+Y
	if input.GetValue() != "hello world" {
		t.Errorf("after CY: %q", input.GetValue())
	}
}

func TestInputAltYCycles(t *testing.T) {
	input := NewInput()
	input.SetValue("first")
	input.HandleInput("\x05") // Ctrl+E
	input.HandleInput("\x17") // CW "first"
	input.SetValue("second")
	input.HandleInput("\x05")
	input.HandleInput("\x17") // CW "second"
	input.SetValue("third")
	input.HandleInput("\x05")
	input.HandleInput("\x17") // CW "third"

	if input.GetValue() != "" {
		t.Fatalf("expected empty: %q", input.GetValue())
	}
	input.HandleInput("\x19")  // Ctrl+Y → "third"
	if input.GetValue() != "third" {
		t.Fatalf("yank: %q", input.GetValue())
	}
	input.HandleInput("\x1by") // Alt+Y → "second"
	if input.GetValue() != "second" {
		t.Errorf("yankPop 1: %q", input.GetValue())
	}
	input.HandleInput("\x1by") // Alt+Y → "first"
	if input.GetValue() != "first" {
		t.Errorf("yankPop 2: %q", input.GetValue())
	}
	input.HandleInput("\x1by") // Alt+Y → "third" (wraps)
	if input.GetValue() != "third" {
		t.Errorf("yankPop 3: %q", input.GetValue())
	}
}

func TestInputConsecutiveCWAccumulates(t *testing.T) {
	input := NewInput()
	input.SetValue("one two three")
	input.HandleInput("\x05") // Ctrl+E
	input.HandleInput("\x17") // CW "three"
	input.HandleInput("\x17") // CW "two "
	input.HandleInput("\x17") // CW "one "
	if input.GetValue() != "" {
		t.Fatalf("after 3x CW: %q", input.GetValue())
	}
	input.HandleInput("\x19") // Ctrl+Y
	if input.GetValue() != "one two three" {
		t.Errorf("accumulated yank: %q", input.GetValue())
	}
}

func TestInputUndoCoalesces(t *testing.T) {
	input := NewInput()
	for _, ch := range "hello world" {
		input.HandleInput(string(ch))
	}
	input.HandleInput("\x1b[45;5u") // Ctrl+- (undo) → removes " world"
	if input.GetValue() != "hello" {
		t.Fatalf("undo 1: %q", input.GetValue())
	}
	input.HandleInput("\x1b[45;5u") // undo → removes "hello"
	if input.GetValue() != "" {
		t.Errorf("undo 2: %q", input.GetValue())
	}
}

func TestInputUndoBackspace(t *testing.T) {
	input := NewInput()
	for _, ch := range "hello" {
		input.HandleInput(string(ch))
	}
	input.HandleInput("\x7f") // backspace
	if input.GetValue() != "hell" {
		t.Fatalf("after bs: %q", input.GetValue())
	}
	input.HandleInput("\x1b[45;5u") // undo
	if input.GetValue() != "hello" {
		t.Errorf("undo bs: %q", input.GetValue())
	}
}

func TestInputUndoPaste(t *testing.T) {
	input := NewInput()
	input.SetValue("hello world")
	input.HandleInput("\x01") // Ctrl+A
	for i := 0; i < 5; i++ {
		input.HandleInput("\x1b[C") // right 5
	}
	input.HandleInput("\x1b[200~beep boop\x1b[201~") // paste
	if input.GetValue() != "hellobeep boop world" {
		t.Fatalf("after paste: %q", input.GetValue())
	}
	input.HandleInput("\x1b[45;5u") // undo
	if input.GetValue() != "hello world" {
		t.Errorf("undo paste: %q", input.GetValue())
	}
}

func TestInputCJKRenderNoOverflow(t *testing.T) {
	width := 93
	cases := []string{
		"가나다라마바사아자차카타파하 한글 텍스트가 터미널 너비를 초과하면 크래시가 발생합니다",
		"これはテスト文章です。日本語のテキストが正しく表示されるかどうかを確認するためのサンプル",
	}
	for _, text := range cases {
		input := NewInput()
		input.SetValue(text)
		input.focused = true
		line := input.Render(width)[0]
		if tui.VisibleWidth(line) > width {
			t.Errorf("CJK overflow: width=%d (text=%q...)", tui.VisibleWidth(line), text[:20])
		}
	}
}

func TestInputRenderHorizontalScroll(t *testing.T) {
	input := NewInput()
	input.SetValue(strings.Repeat("a", 30))
	input.focused = true
	line := input.Render(20)[0]
	if tui.VisibleWidth(line) > 20 {
		t.Errorf("scroll overflow: width=%d", tui.VisibleWidth(line))
	}
}
