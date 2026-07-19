package components

import (
	"strings"
	"unicode/utf8"

	"ycode/packages/tui"

	"github.com/rivo/uniseg"
)

// InputState is a snapshot for undo.
type InputState struct {
	Value  string
	Cursor int // rune index
}

type inputAction int

const (
	actionNone inputAction = iota
	actionKill
	actionYank
	actionTypeWord
)

// Input is a single-line text input with horizontal scrolling, Emacs kill/yank,
// and undo.
type Input struct {
	value    string
	cursor   int // rune index
	focused  bool

	OnSubmit func(string)
	OnEscape func()

	pasteBuffer string
	isInPaste   bool

	killRing   *tui.KillRing
	lastAction inputAction
	undoStack  *tui.UndoStack[InputState]
}

// NewInput returns an empty Input.
func NewInput() *Input {
	return &Input{
		killRing:  tui.NewKillRing(),
		undoStack: tui.NewUndoStack[InputState](nil),
	}
}

// GetValue returns the current text.
func (i *Input) GetValue() string { return i.value }

// SetValue sets the text, clamping the cursor.
func (i *Input) SetValue(v string) {
	i.value = v
	runeCount := utf8.RuneCountInString(v)
	if i.cursor > runeCount {
		i.cursor = runeCount
	}
}

// Focused reports the focus state.
func (i *Input) Focused() bool { return i.focused }

// SetFocused sets the focus state (for IME cursor).
func (i *Input) SetFocused(b bool) { i.focused = b }

// Invalidate is a no-op.
func (i *Input) Invalidate() {}

// HandleInput processes a key sequence.
func (i *Input) HandleInput(data string) {
	// Bracketed paste.
	if strings.Contains(data, "\x1b[200~") {
		i.isInPaste = true
		i.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if i.isInPaste {
		i.pasteBuffer += data
		if idx := strings.Index(i.pasteBuffer, "\x1b[201~"); idx >= 0 {
			content := i.pasteBuffer[:idx]
			i.handlePaste(content)
			i.isInPaste = false
			remaining := i.pasteBuffer[idx+6:]
			i.pasteBuffer = ""
			if remaining != "" {
				i.HandleInput(remaining)
			}
		}
		return
	}

	kb := tui.GetKeybindings()

	if kb.Matches(data, "tui.select.cancel") {
		if i.OnEscape != nil {
			i.OnEscape()
		}
		return
	}
	if kb.Matches(data, "tui.editor.undo") {
		i.undo()
		return
	}
	if kb.Matches(data, "tui.input.submit") || data == "\n" {
		if i.OnSubmit != nil {
			i.OnSubmit(i.value)
		}
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharBackward") {
		i.handleBackspace()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharForward") {
		i.handleForwardDelete()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordBackward") {
		i.deleteWordBackward()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordForward") {
		i.deleteWordForward()
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineStart") {
		i.deleteToLineStart()
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineEnd") {
		i.deleteToLineEnd()
		return
	}
	if kb.Matches(data, "tui.editor.yank") {
		i.yank()
		return
	}
	if kb.Matches(data, "tui.editor.yankPop") {
		i.yankPop()
		return
	}
	if kb.Matches(data, "tui.editor.cursorLeft") {
		i.lastAction = actionNone
		i.moveCursorByGrapheme(-1)
		return
	}
	if kb.Matches(data, "tui.editor.cursorRight") {
		i.lastAction = actionNone
		i.moveCursorByGrapheme(1)
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineStart") {
		i.lastAction = actionNone
		i.cursor = 0
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineEnd") {
		i.lastAction = actionNone
		i.cursor = utf8.RuneCountInString(i.value)
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordLeft") {
		i.moveWordBackward()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordRight") {
		i.moveWordForward()
		return
	}
	// Kitty CSI-u printable.
	if ch, ok := tui.DecodeKittyPrintable(data); ok {
		i.insertCharacter(ch)
		return
	}
	// Regular printable (no control chars).
	if !hasControlChars(data) {
		i.insertCharacter(data)
	}
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

// --- Editing methods ---

func (i *Input) insertCharacter(ch string) {
	if isWhitespaceRune(firstRuneOf(ch)) || i.lastAction != actionTypeWord {
		i.pushUndo()
	}
	i.lastAction = actionTypeWord

	runes := []rune(i.value)
	chRunes := []rune(ch)
	result := make([]rune, 0, len(runes)+len(chRunes))
	result = append(result, runes[:i.cursor]...)
	result = append(result, chRunes...)
	result = append(result, runes[i.cursor:]...)
	i.value = string(result)
	i.cursor += len(chRunes)
}

func (i *Input) handleBackspace() {
	i.lastAction = actionNone
	if i.cursor > 0 {
		i.pushUndo()
		runes := []rune(i.value)
		before := string(runes[:i.cursor])
		lastLen := lastGraphemeRuneCount(before)
		i.value = string(runes[:i.cursor-lastLen]) + string(runes[i.cursor:])
		i.cursor -= lastLen
	}
}

func (i *Input) handleForwardDelete() {
	i.lastAction = actionNone
	runes := []rune(i.value)
	if i.cursor < len(runes) {
		i.pushUndo()
		after := string(runes[i.cursor:])
		firstLen := firstGraphemeRuneCount(after)
		i.value = string(runes[:i.cursor]) + string(runes[i.cursor+firstLen:])
	}
}

func (i *Input) deleteToLineStart() {
	if i.cursor == 0 {
		return
	}
	i.pushUndo()
	runes := []rune(i.value)
	deleted := string(runes[:i.cursor])
	i.killRing.Push(deleted, true, i.lastAction == actionKill)
	i.lastAction = actionKill
	i.value = string(runes[i.cursor:])
	i.cursor = 0
}

func (i *Input) deleteToLineEnd() {
	runes := []rune(i.value)
	if i.cursor >= len(runes) {
		return
	}
	i.pushUndo()
	deleted := string(runes[i.cursor:])
	i.killRing.Push(deleted, false, i.lastAction == actionKill)
	i.lastAction = actionKill
	i.value = string(runes[:i.cursor])
}

func (i *Input) deleteWordBackward() {
	runes := []rune(i.value)
	if i.cursor == 0 {
		return
	}
	wasKill := i.lastAction == actionKill
	i.pushUndo()
	oldCursor := i.cursor
	i.moveWordBackward()
	deleteFrom := i.cursor
	i.cursor = oldCursor
	deleted := string(runes[deleteFrom:i.cursor])
	i.killRing.Push(deleted, true, wasKill)
	i.lastAction = actionKill
	i.value = string(runes[:deleteFrom]) + string(runes[i.cursor:])
	i.cursor = deleteFrom
}

func (i *Input) deleteWordForward() {
	runes := []rune(i.value)
	if i.cursor >= len(runes) {
		return
	}
	wasKill := i.lastAction == actionKill
	i.pushUndo()
	oldCursor := i.cursor
	i.moveWordForward()
	deleteTo := i.cursor
	i.cursor = oldCursor
	deleted := string(runes[i.cursor:deleteTo])
	i.killRing.Push(deleted, false, wasKill)
	i.lastAction = actionKill
	i.value = string(runes[:i.cursor]) + string(runes[deleteTo:])
}

func (i *Input) yank() {
	text, ok := i.killRing.Peek()
	if !ok {
		return
	}
	i.pushUndo()
	runes := []rune(i.value)
	textRunes := []rune(text)
	result := make([]rune, 0, len(runes)+len(textRunes))
	result = append(result, runes[:i.cursor]...)
	result = append(result, textRunes...)
	result = append(result, runes[i.cursor:]...)
	i.value = string(result)
	i.cursor += len(textRunes)
	i.lastAction = actionYank
}

func (i *Input) yankPop() {
	if i.lastAction != actionYank || i.killRing.Len() <= 1 {
		return
	}
	i.pushUndo()
	prevText, _ := i.killRing.Peek()
	prevRunes := []rune(prevText)
	runes := []rune(i.value)
	// Delete previously yanked text.
	i.value = string(runes[:i.cursor-len(prevRunes)]) + string(runes[i.cursor:])
	i.cursor -= len(prevRunes)
	// Rotate and insert new.
	i.killRing.Rotate()
	newText, _ := i.killRing.Peek()
	newRunes := []rune(newText)
	runes = []rune(i.value)
	result := make([]rune, 0, len(runes)+len(newRunes))
	result = append(result, runes[:i.cursor]...)
	result = append(result, newRunes...)
	result = append(result, runes[i.cursor:]...)
	i.value = string(result)
	i.cursor += len(newRunes)
	i.lastAction = actionYank
}

func (i *Input) handlePaste(pasted string) {
	i.lastAction = actionNone
	i.pushUndo()
	clean := strings.ReplaceAll(strings.ReplaceAll(pasted, "\r\n", ""), "\r", "")
	clean = strings.ReplaceAll(clean, "\n", "")
	clean = strings.ReplaceAll(clean, "\t", "    ")
	runes := []rune(i.value)
	pasteRunes := []rune(clean)
	result := make([]rune, 0, len(runes)+len(pasteRunes))
	result = append(result, runes[:i.cursor]...)
	result = append(result, pasteRunes...)
	result = append(result, runes[i.cursor:]...)
	i.value = string(result)
	i.cursor += len(pasteRunes)
}

// --- Cursor movement ---

func (i *Input) moveCursorByGrapheme(dir int) {
	runes := []rune(i.value)
	if dir < 0 && i.cursor > 0 {
		before := string(runes[:i.cursor])
		i.cursor -= lastGraphemeRuneCount(before)
	} else if dir > 0 && i.cursor < len(runes) {
		after := string(runes[i.cursor:])
		i.cursor += firstGraphemeRuneCount(after)
	}
}

func (i *Input) moveWordBackward() {
	if i.cursor == 0 {
		return
	}
	i.lastAction = actionNone
	i.cursor = tui.FindWordBackward(i.value, i.cursor, nil)
}

func (i *Input) moveWordForward() {
	if i.cursor >= utf8.RuneCountInString(i.value) {
		return
	}
	i.lastAction = actionNone
	i.cursor = tui.FindWordForward(i.value, i.cursor, nil)
}

// --- Undo ---

func (i *Input) pushUndo() {
	i.undoStack.Push(InputState{Value: i.value, Cursor: i.cursor})
}

func (i *Input) undo() {
	s, ok := i.undoStack.Pop()
	if !ok {
		return
	}
	i.value = s.Value
	i.cursor = s.Cursor
	i.lastAction = actionNone
}

// --- Render ---

// Render produces a single line with the prompt, horizontally-scrolled text, and
// a fake reverse-video cursor.
func (i *Input) Render(width int) []string {
	prompt := "> "
	available := width - len(prompt)
	if available <= 0 {
		return []string{prompt}
	}

	totalWidth := tui.VisibleWidth(i.value)
	var visibleText string
	cursorDisplay := 0 // rune index into visibleText

	runes := []rune(i.value)
	if totalWidth < available {
		visibleText = i.value
		cursorDisplay = i.cursor
	} else {
		scrollWidth := available
		if i.cursor == len(runes) {
			scrollWidth = available - 1
		}
		cursorCol := tui.VisibleWidth(string(runes[:i.cursor]))
		if scrollWidth > 0 {
			half := scrollWidth / 2
			startCol := 0
			if cursorCol > totalWidth-half {
				startCol = totalWidth - scrollWidth
				if startCol < 0 {
					startCol = 0
				}
			} else if cursorCol >= half {
				startCol = cursorCol - half
			}
			visibleText = tui.SliceByColumn(i.value, startCol, scrollWidth, true)
			beforeCursor := tui.SliceByColumn(i.value, startCol, maxInt(0, cursorCol-startCol), true)
			cursorDisplay = utf8.RuneCountInString(beforeCursor)
		} else {
			visibleText = ""
			cursorDisplay = 0
		}
	}

	// Build fake cursor.
	visibleRunes := []rune(visibleText)
	atCursorStr := " "
	if cursorDisplay < len(visibleRunes) {
		remainder := string(visibleRunes[cursorDisplay:])
		gr := uniseg.NewGraphemes(remainder)
		if gr.Next() {
			atCursorStr = gr.Str()
		}
	}
	atCursorLen := utf8.RuneCountInString(atCursorStr)

	beforeCursor := string(visibleRunes[:cursorDisplay])
	var afterCursor string
	if cursorDisplay+atCursorLen <= len(visibleRunes) {
		afterCursor = string(visibleRunes[cursorDisplay+atCursorLen:])
	}

	marker := ""
	if i.focused {
		marker = tui.CursorMarker
	}
	cursorChar := "\x1b[7m" + atCursorStr + "\x1b[0m"
	textWithCursor := beforeCursor + marker + cursorChar + afterCursor

	visualLen := tui.VisibleWidth(textWithCursor)
	pad := ""
	if pad := available - visualLen; pad > 0 {
		_ = pad
	}
	if p := available - visualLen; p > 0 {
		pad = strings.Repeat(" ", p)
	}
	return []string{prompt + textWithCursor + pad}
}

// --- grapheme helpers ---

func lastGraphemeRuneCount(s string) int {
	if s == "" {
		return 1
	}
	count := 1
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		count = utf8.RuneCountInString(gr.Str())
	}
	return count
}

func firstGraphemeRuneCount(s string) int {
	if s == "" {
		return 1
	}
	gr := uniseg.NewGraphemes(s)
	if gr.Next() {
		return utf8.RuneCountInString(gr.Str())
	}
	return 1
}

func firstRuneOf(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func isWhitespaceRune(r rune) bool { return tui.IsWhitespaceChar(r) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
