package components

import (
	"strings"
	"testing"

	"ycode/packages/tui"
)

type editorStubTerminal struct{ cols, rows int }

func (s *editorStubTerminal) Start(func(string), func()) error { return nil }
func (s *editorStubTerminal) Stop() error                      { return nil }
func (s *editorStubTerminal) DrainInput(int, int) error        { return nil }
func (s *editorStubTerminal) Write(string)                     {}
func (s *editorStubTerminal) Columns() int                     { return s.cols }
func (s *editorStubTerminal) Rows() int                        { return s.rows }
func (s *editorStubTerminal) KittyProtocolActive() bool        { return false }
func (s *editorStubTerminal) MoveBy(int)                       {}
func (s *editorStubTerminal) HideCursor()                      {}
func (s *editorStubTerminal) ShowCursor()                      {}
func (s *editorStubTerminal) ClearLine()                       {}
func (s *editorStubTerminal) ClearFromCursor()                 {}
func (s *editorStubTerminal) ClearScreen()                     {}
func (s *editorStubTerminal) SetTitle(string)                  {}
func (s *editorStubTerminal) SetProgress(bool)                 {}

func newTestEditor() *Editor {
	tt := &editorStubTerminal{80, 24}
	t := tui.NewTUI(tt)
	return NewEditor(t, EditorTheme{
		BorderColor: func(s string) string { return s },
		SelectList: SelectListTheme{
			SelectedPrefix: func(s string) string { return s },
			SelectedText:   func(s string) string { return s },
			Description:    func(s string) string { return s },
			ScrollInfo:     func(s string) string { return s },
			NoMatch:        func(s string) string { return s },
		},
	})
}

// --- History ---

func TestEditorHistoryNavigate(t *testing.T) {
	e := newTestEditor()
	e.AddToHistory("first")
	e.AddToHistory("second")
	e.AddToHistory("third")
	e.HandleInput("\x1b[A") // Up → third
	if e.GetText() != "third" {
		t.Errorf("up=%q want third", e.GetText())
	}
	e.HandleInput("\x1b[A") // → second
	if e.GetText() != "second" {
		t.Errorf("up=%q want second", e.GetText())
	}
	e.HandleInput("\x1b[B") // Down → third
	if e.GetText() != "third" {
		t.Errorf("down=%q want third", e.GetText())
	}
}

func TestEditorHistoryDraftRestore(t *testing.T) {
	e := newTestEditor()
	e.AddToHistory("old")
	e.SetText("draft")
	e.HandleInput("\x1b[A") // Up on non-empty draft → line start
	if e.GetText() != "draft" {
		t.Fatalf("first up should keep draft: %q", e.GetText())
	}
	_, col := e.GetCursor()
	if col != 0 {
		t.Fatalf("should be at col 0: got %d", col)
	}
	e.HandleInput("\x1b[A") // Up at start → history
	if e.GetText() != "old" {
		t.Errorf("second up=%q want old", e.GetText())
	}
	e.HandleInput("\x1b[B") // Down → draft
	if e.GetText() != "draft" {
		t.Errorf("down=%q want draft", e.GetText())
	}
}

func TestEditorHistoryEmptyAndDedup(t *testing.T) {
	e := newTestEditor()
	e.AddToHistory("")
	e.AddToHistory("   ")
	e.AddToHistory("valid")
	e.HandleInput("\x1b[A")
	if e.GetText() != "valid" {
		t.Errorf("got %q want valid", e.GetText())
	}
	e2 := newTestEditor()
	e2.AddToHistory("same")
	e2.AddToHistory("same")
	e2.HandleInput("\x1b[A")
	if e2.GetText() != "same" {
		t.Errorf("dedup got %q", e2.GetText())
	}
	e2.HandleInput("\x1b[A")
	if e2.GetText() != "same" {
		t.Errorf("dedup second up got %q", e2.GetText())
	}
}

// --- Backslash+Enter ---

func TestEditorBackslashEnter(t *testing.T) {
	e := newTestEditor()
	e.HandleInput("\\")
	e.HandleInput("\r")
	if e.GetText() != "\n" {
		t.Errorf("got %q want newline", e.GetText())
	}
}

func TestEditorBackslashNormal(t *testing.T) {
	e := newTestEditor()
	e.HandleInput("\\")
	e.HandleInput("x")
	if e.GetText() != "\\x" {
		t.Errorf("got %q", e.GetText())
	}
}

// --- Kill-ring ---

func TestEditorKillRing(t *testing.T) {
	e := newTestEditor()
	e.SetText("foo bar baz")
	e.HandleInput("\x17") // Ctrl+W → delete "baz"
	if e.GetText() != "foo bar " {
		t.Fatalf("after W=%q", e.GetText())
	}
	e.HandleInput("\x01") // Ctrl+A
	e.HandleInput("\x19") // Ctrl+Y
	if e.GetText() != "bazfoo bar " {
		t.Errorf("after yank=%q", e.GetText())
	}
}

func TestEditorKillRingAltY(t *testing.T) {
	e := newTestEditor()
	e.SetText("first")
	e.HandleInput("\x17") // delete "first"
	e.SetText("second")
	e.HandleInput("\x17") // delete "second"
	e.SetText("third")
	e.HandleInput("\x17") // delete "third"
	e.HandleInput("\x19") // yank "third"
	if e.GetText() != "third" {
		t.Fatalf("yank=%q", e.GetText())
	}
	e.HandleInput("\x1by") // Alt+Y → second
	if e.GetText() != "second" {
		t.Errorf("alt-y=%q want second", e.GetText())
	}
	e.HandleInput("\x1by") // → first
	if e.GetText() != "first" {
		t.Errorf("alt-y=%q want first", e.GetText())
	}
}

func TestEditorKillRingAccumulate(t *testing.T) {
	e := newTestEditor()
	e.SetText("one two three")
	e.HandleInput("\x17") // delete "three"
	e.HandleInput("\x17") // delete "two " (accumulate)
	e.HandleInput("\x17") // delete "one " (accumulate)
	if e.GetText() != "" {
		t.Fatalf("after 3x W=%q", e.GetText())
	}
	e.HandleInput("\x19") // yank accumulated "one two three"
	if e.GetText() != "one two three" {
		t.Errorf("yank=%q want 'one two three'", e.GetText())
	}
}

// --- Undo ---

func TestEditorUndoCoalescing(t *testing.T) {
	e := newTestEditor()
	for _, ch := range "hello" {
		e.HandleInput(string(ch))
	}
	e.HandleInput(" ")
	for _, ch := range "world" {
		e.HandleInput(string(ch))
	}
	if e.GetText() != "hello world" {
		t.Fatalf("text=%q", e.GetText())
	}
	e.HandleInput("\x1b[45;5u") // undo → remove "world"
	if e.GetText() != "hello" {
		t.Errorf("undo=%q want hello", e.GetText())
	}
	e.HandleInput("\x1b[45;5u") // undo → remove "hello"
	if e.GetText() != "" {
		t.Errorf("undo=%q want empty", e.GetText())
	}
}

func TestEditorUndoBackspace(t *testing.T) {
	e := newTestEditor()
	e.HandleInput("hello")
	e.HandleInput("\x7f") // backspace
	if e.GetText() != "hell" {
		t.Fatalf("text=%q", e.GetText())
	}
	e.HandleInput("\x1b[45;5u") // undo
	if e.GetText() != "hello" {
		t.Errorf("undo=%q want hello", e.GetText())
	}
}

func TestEditorUndoPaste(t *testing.T) {
	e := newTestEditor()
	e.SetText("hello world")
	e.HandleInput("\x01") // Ctrl+A
	for i := 0; i < 5; i++ {
		e.HandleInput("\x1b[C") // right 5
	}
	e.HandleInput("\x1b[200~beep boop\x1b[201~")
	if e.GetText() != "hellobeep boop world" {
		t.Fatalf("paste=%q", e.GetText())
	}
	e.HandleInput("\x1b[45;5u") // undo → restore pre-paste
	if e.GetText() != "hello world" {
		t.Errorf("undo=%q want 'hello world'", e.GetText())
	}
}

// --- Unicode ---

func TestEditorUnicodeInsert(t *testing.T) {
	e := newTestEditor()
	for _, ch := range []string{"H", "e", "l", "l", "o", " ", "ä", "ö", "ü", " ", "😀"} {
		e.HandleInput(ch)
	}
	if e.GetText() != "Hello äöü 😀" {
		t.Errorf("got %q", e.GetText())
	}
}

func TestEditorUnicodeDeleteEmoji(t *testing.T) {
	e := newTestEditor()
	e.HandleInput("😀")
	e.HandleInput("👍")
	e.HandleInput("\x7f") // backspace → delete 👍
	if e.GetText() != "😀" {
		t.Errorf("got %q want 😀", e.GetText())
	}
}

func TestEditorUnicodeCursorOverEmoji(t *testing.T) {
	e := newTestEditor()
	e.HandleInput("😀")
	e.HandleInput("👍")
	e.HandleInput("🎉")
	e.HandleInput("\x1b[D") // left over 🎉
	e.HandleInput("\x1b[D") // left over 👍
	e.HandleInput("x")
	if e.GetText() != "😀x👍🎉" {
		t.Errorf("got %q", e.GetText())
	}
}

// --- WordWrapLine ---

func TestWordWrapLineBasic(t *testing.T) {
	chunks := WordWrapLine("hello world test", 11, nil)
	if len(chunks) != 2 {
		t.Fatalf("chunks=%d want 2: %+v", len(chunks), chunks)
	}
	if chunks[0].Text != "hello " || chunks[1].Text != "world test" {
		t.Errorf("chunks=%+v", chunks)
	}
}

func TestWordWrapLineBoundary(t *testing.T) {
	chunks := WordWrapLine("hello world test", 12, nil)
	if len(chunks) != 2 {
		t.Fatalf("chunks=%d want 2", len(chunks))
	}
	if chunks[0].Text != "hello world " || chunks[1].Text != "test" {
		t.Errorf("chunks=%+v", chunks)
	}
}

func TestWordWrapLineLongWord(t *testing.T) {
	chunks := WordWrapLine("Check https://example.com/very/long/path/that/exceeds/width here", 30, nil)
	for _, c := range chunks {
		if w := tui.VisibleWidth(c.Text); w > 30 {
			t.Errorf("chunk %q width %d > 30", c.Text, w)
		}
	}
}

// --- Render ---

func TestEditorRenderBasic(t *testing.T) {
	e := newTestEditor()
	e.SetText("hello")
	lines := e.Render(40)
	if len(lines) < 3 {
		t.Fatalf("render lines=%d", len(lines))
	}
	// First and last lines should be borders.
	if !containsStr(strings.Split(lines[0], ""), "─") {
		t.Errorf("top border missing: %q", lines[0])
	}
}

// --- Submit ---

func TestEditorSubmit(t *testing.T) {
	e := newTestEditor()
	var submitted string
	e.OnSubmit = func(text string) { submitted = text }
	e.HandleInput("h")
	e.HandleInput("i")
	e.HandleInput("\r") // Enter → submit
	if submitted != "hi" {
		t.Errorf("submitted=%q want hi", submitted)
	}
	if e.GetText() != "" {
		t.Errorf("after submit text=%q want empty", e.GetText())
	}
}

func TestEditorSubmitBackslashLiteral(t *testing.T) {
	e := newTestEditor()
	var submitted string
	e.OnSubmit = func(text string) { submitted = text }
	e.HandleInput("hello")
	e.HandleInput("\\")
	e.HandleInput("\r") // Enter → \ at cursor → newline instead of submit
	if submitted != "" {
		t.Errorf("should not submit: %q", submitted)
	}
	if e.GetText() != "hello\n" {
		t.Errorf("text=%q want 'hello\\n'", e.GetText())
	}
}
