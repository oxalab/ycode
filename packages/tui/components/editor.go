package components

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"ycode/packages/tui"

	"github.com/rivo/uniseg"
)

// EditorTheme styles the editor border + the autocomplete SelectList.
type EditorTheme struct {
	BorderColor tui.StyleFunc
	SelectList  SelectListTheme
}

// EditorOptions configures the editor.
type EditorOptions struct {
	PaddingX               int
	AutocompleteMaxVisible int
}

// EditorState is a snapshot for undo.
type EditorState struct {
	Lines      []string
	CursorLine int
	CursorCol  int
}

func (s EditorState) Clone() EditorState {
	lines := make([]string, len(s.Lines))
	copy(lines, s.Lines)
	return EditorState{Lines: lines, CursorLine: s.CursorLine, CursorCol: s.CursorCol}
}

type editorAction int

const (
	editorActNone editorAction = iota
	editorActKill
	editorActYank
	editorActTypeWord
)

type jumpDirection string

const (
	jumpForward  jumpDirection = "forward"
	jumpBackward jumpDirection = "backward"
)

var (
	pasteMarkerGlobal = regexp.MustCompile(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`)
	pasteMarkerSingle = regexp.MustCompile(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`)
)

// Editor is a multi-line text editor with word wrap, kill-ring, undo, history,
// char-jump, and async autocomplete.
type Editor struct {
	state    EditorState
	focused  bool
	tui      *tui.TUI
	theme    EditorTheme
	paddingX int

	lastWidth    int
	scrollOffset int
	BorderColor  tui.StyleFunc

	// Paste tracking.
	pastes       map[int]string
	pasteCounter int

	// Bracketed paste buffering.
	pasteBuffer string
	isInPaste   bool

	// History.
	history      []string
	historyIndex int
	historyDraft *EditorState

	// Kill-ring.
	killRing   *tui.KillRing
	lastAction editorAction

	// Char-jump.
	jumpMode *jumpDirection

	// Sticky column.
	preferredVisualCol  *int
	snappedFromCursorCol *int

	// Undo.
	undoStack *tui.UndoStack[EditorState]

	OnSubmit      func(text string)
	OnChange      func(text string)
	DisableSubmit bool

	// Autocomplete.
	autoProvider       tui.AutocompleteProvider
	autoList           *SelectList
	autoState          string // ""=none, "regular", "force"
	autoPrefix         string
	autoMaxVisible     int
	autoTriggerChars   []string
	autoTriggerPattern *regexp.Regexp
}

// NewEditor returns an Editor.
func NewEditor(t *tui.TUI, theme EditorTheme, options ...EditorOptions) *Editor {
	opts := EditorOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	paddingX := opts.PaddingX
	if paddingX < 0 {
		paddingX = 0
	}
	return &Editor{
		state:      EditorState{Lines: []string{""}},
		tui:        t,
		theme:      theme,
		paddingX:   paddingX,
		BorderColor: theme.BorderColor,
		pastes:     map[int]string{},
		killRing:    tui.NewKillRing(),
		undoStack:   tui.NewUndoStack(func(s EditorState) EditorState { return s.Clone() }),
		lastWidth:     80,
		historyIndex:  -1,
		autoMaxVisible: 5,
	}
}

// Focused reports the focus state.
func (e *Editor) Focused() bool { return e.focused }

// SetFocused sets the focus state (for IME cursor).
func (e *Editor) SetFocused(b bool) { e.focused = b }

// Invalidate is a no-op.
func (e *Editor) Invalidate() {}

// --- Public accessors ---

func (e *Editor) GetText() string  { return strings.Join(e.state.Lines, "\n") }
func (e *Editor) GetLines() []string {
	out := make([]string, len(e.state.Lines))
	copy(out, e.state.Lines)
	return out
}
func (e *Editor) GetCursor() (line, col int) { return e.state.CursorLine, e.state.CursorCol }
func (e *Editor) GetPaddingX() int            { return e.paddingX }
func (e *Editor) SetPaddingX(n int) {
	if n < 0 { n = 0 }
	if e.paddingX != n {
		e.paddingX = n
		if e.tui != nil { e.tui.RequestRender() }
	}
}

// SetText replaces the entire text.
func (e *Editor) SetText(text string) {
	e.cancelAutocomplete()
	e.lastAction = editorActNone
	e.exitHistoryBrowsing()
	normalized := e.normalizeText(text)
	if e.GetText() != normalized {
		e.pushUndoSnapshot()
	}
	e.setTextInternal(normalized, true)
}

func (e *Editor) setTextInternal(text string, end bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	e.state.Lines = lines
	if end {
		e.state.CursorLine = len(lines) - 1
		e.setCursorCol(utf8.RuneCountInString(e.state.Lines[e.state.CursorLine]))
	} else {
		e.state.CursorLine = 0
		e.setCursorCol(0)
	}
	e.scrollOffset = 0
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// InsertTextAtCursor inserts text at the cursor (atomic for undo).
func (e *Editor) InsertTextAtCursor(text string) {
	if text == "" {
		return
	}
	e.cancelAutocomplete()
	e.pushUndoSnapshot()
	e.lastAction = editorActNone
	e.exitHistoryBrowsing()
	e.insertTextInternal(text)
}

func (e *Editor) insertTextInternal(text string) {
	if text == "" {
		return
	}
	normalized := e.normalizeText(text)
	inserted := strings.Split(normalized, "\n")
	currentLine := e.state.Lines[e.state.CursorLine]
	runes := []rune(currentLine)
	before := string(runes[:e.state.CursorCol])
	after := string(runes[e.state.CursorCol:])
	if len(inserted) == 1 {
		e.state.Lines[e.state.CursorLine] = before + normalized + after
		e.setCursorCol(e.state.CursorCol + utf8.RuneCountInString(normalized))
	} else {
		newLines := make([]string, 0, len(e.state.Lines)+len(inserted))
		newLines = append(newLines, e.state.Lines[:e.state.CursorLine]...)
		newLines = append(newLines, before+inserted[0])
		newLines = append(newLines, inserted[1:len(inserted)-1]...)
		newLines = append(newLines, inserted[len(inserted)-1]+after)
		newLines = append(newLines, e.state.Lines[e.state.CursorLine+1:]...)
		e.state.Lines = newLines
		e.state.CursorLine += len(inserted) - 1
		e.setCursorCol(utf8.RuneCountInString(inserted[len(inserted)-1]))
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// GetExpandedText returns text with paste markers expanded.
func (e *Editor) GetExpandedText() string {
	result := e.GetText()
	for pasteID, content := range e.pastes {
		re := regexp.MustCompile(`\[paste #` + itoaE(pasteID) + `( (\+\d+ lines|\d+ chars))?\]`)
		result = re.ReplaceAllString(result, regexp.QuoteMeta(content))
	}
	return result
}

// History returns a copy of the prompt history (newest first). Read-only
// accessor — used by tests and transcript rebuilds to verify history feeds.
func (e *Editor) History() []string {
	return append([]string(nil), e.history...)
}

// AddToHistory adds a prompt to history (dedup consecutive, cap 100).
func (e *Editor) AddToHistory(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if len(e.history) > 0 && e.history[0] == trimmed {
		return
	}
	e.history = append([]string{trimmed}, e.history...)
	if len(e.history) > 100 {
		e.history = e.history[:100]
	}
}

// --- HandleInput ---

func (e *Editor) HandleInput(data string) {
	kb := tui.GetKeybindings()

	// Jump mode.
	if e.jumpMode != nil {
		if kb.Matches(data, "tui.editor.jumpForward") || kb.Matches(data, "tui.editor.jumpBackward") {
			e.jumpMode = nil
			return
		}
		printable := ""
		if ch, ok := tui.DecodePrintableKey(data); ok {
			printable = ch
		} else if r, _ := utf8.DecodeRuneInString(data); r >= 32 {
			printable = data
		}
		if printable != "" {
			dir := *e.jumpMode
			e.jumpMode = nil
			e.jumpToChar(printable, dir)
			return
		}
		e.jumpMode = nil
	}

	// Bracketed paste.
	if strings.Contains(data, "\x1b[200~") {
		e.isInPaste = true
		e.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if e.isInPaste {
		e.pasteBuffer += data
		if idx := strings.Index(e.pasteBuffer, "\x1b[201~"); idx >= 0 {
			content := e.pasteBuffer[:idx]
			if content != "" {
				e.handlePaste(content)
			}
			e.isInPaste = false
			remaining := e.pasteBuffer[idx+6:]
			e.pasteBuffer = ""
			if remaining != "" {
				e.HandleInput(remaining)
			}
		}
		return
	}

	// Ctrl+C → let parent handle.
	if kb.Matches(data, "tui.input.copy") {
		return
	}

	// Undo.
	if kb.Matches(data, "tui.editor.undo") {
		e.undo()
		return
	}

	// Autocomplete active.
	if e.handleAutocompleteInput(data, kb) {
		return
	}

	// Tab → trigger completion.
	if kb.Matches(data, "tui.input.tab") && e.autoState == "" {
		e.handleTabCompletion()
		return
	}

	// Deletion.
	if kb.Matches(data, "tui.editor.deleteToLineEnd") {
		e.deleteToEndOfLine()
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineStart") {
		e.deleteToStartOfLine()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordBackward") {
		e.deleteWordBackward()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordForward") {
		e.deleteWordForward()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharBackward") || tui.MatchesKey(data, "shift+backspace") {
		e.handleBackspace()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharForward") || tui.MatchesKey(data, "shift+delete") {
		e.handleForwardDelete()
		return
	}

	// Kill-ring.
	if kb.Matches(data, "tui.editor.yank") {
		e.yank()
		return
	}
	if kb.Matches(data, "tui.editor.yankPop") {
		e.yankPop()
		return
	}

	// Cursor movement.
	if kb.Matches(data, "tui.editor.cursorLineStart") {
		e.lastAction = editorActNone
		e.setCursorCol(0)
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineEnd") {
		e.lastAction = editorActNone
		e.setCursorCol(utf8.RuneCountInString(e.state.Lines[e.state.CursorLine]))
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordLeft") {
		e.moveWordBackward()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordRight") {
		e.moveWordForward()
		return
	}

	// New line.
	if kb.Matches(data, "tui.input.newLine") || data == "\x1b\r" || data == "\n" ||
		(data == "\x1b[13;2~") {
		e.addNewLine()
		return
	}

	// Submit.
	if kb.Matches(data, "tui.input.submit") {
		if e.DisableSubmit {
			return
		}
		currentLine := e.state.Lines[e.state.CursorLine]
		runes := []rune(currentLine)
		if e.state.CursorCol > 0 && e.state.CursorCol <= len(runes) && runes[e.state.CursorCol-1] == '\\' {
			e.handleBackspace()
			e.addNewLine()
			return
		}
		e.submitValue()
		return
	}

	// Arrow navigation (with history support).
	if kb.Matches(data, "tui.editor.cursorUp") {
		if e.isOnFirstVisualLine() && (e.isEditorEmpty() || e.historyIndex > -1 || e.state.CursorCol == 0) {
			e.navigateHistory(-1)
		} else if e.isOnFirstVisualLine() {
			e.setCursorCol(0)
		} else {
			e.moveCursor(-1, 0)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorDown") {
		if e.historyIndex > -1 && e.isOnLastVisualLine() {
			e.navigateHistory(1)
		} else if e.isOnLastVisualLine() {
			cl := utf8.RuneCountInString(e.state.Lines[e.state.CursorLine])
			e.setCursorCol(cl)
		} else {
			e.moveCursor(1, 0)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorRight") {
		e.moveCursor(0, 1)
		return
	}
	if kb.Matches(data, "tui.editor.cursorLeft") {
		e.moveCursor(0, -1)
		return
	}

	// Page up/down.
	if kb.Matches(data, "tui.editor.pageUp") {
		e.pageScroll(-1)
		return
	}
	if kb.Matches(data, "tui.editor.pageDown") {
		e.pageScroll(1)
		return
	}

	// Jump triggers.
	if kb.Matches(data, "tui.editor.jumpForward") {
		dir := jumpForward
		e.jumpMode = &dir
		return
	}
	if kb.Matches(data, "tui.editor.jumpBackward") {
		dir := jumpBackward
		e.jumpMode = &dir
		return
	}

	// Shift+space → insert space.
	if tui.MatchesKey(data, "shift+space") {
		e.insertCharacter(" ")
		return
	}

	// Kitty printable.
	if ch, ok := tui.DecodeKittyPrintable(data); ok {
		e.insertCharacter(ch)
		return
	}
	// Regular printable.
	if !editorHasControlChars(data) {
		e.insertCharacter(data)
	}
}

func editorHasControlChars(s string) bool {
	for _, r := range s {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

// --- Editing methods ---

func (e *Editor) insertCharacter(ch string) {
	e.exitHistoryBrowsing()
	firstRune, _ := utf8.DecodeRuneInString(ch)
	if tui.IsWhitespaceChar(firstRune) || e.lastAction != editorActTypeWord {
		e.pushUndoSnapshot()
	}
	e.lastAction = editorActTypeWord

	runes := []rune(e.state.Lines[e.state.CursorLine])
	chRunes := []rune(ch)
	before := string(runes[:e.state.CursorCol])
	after := string(runes[e.state.CursorCol:])
	e.state.Lines[e.state.CursorLine] = before + ch + after
	e.setCursorCol(e.state.CursorCol + len(chRunes))
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
	// Auto-trigger autocomplete after inserting a character (slash commands on
	// "/" at start of message, symbol triggers, and letters in a slash/symbol
	// context). Mirrors reference editor.ts:1109-1139. The logic lives in
	// checkTriggerAfterInsert (editor_autocomplete.go).
	e.checkTriggerAfterInsert(ch)
}

func (e *Editor) handlePaste(pastedText string) {
	e.cancelAutocomplete()
	e.exitHistoryBrowsing()
	e.lastAction = editorActNone
	e.pushUndoSnapshot()

	// Decode CSI-u Ctrl+letter sequences inside bracketed paste (tmux #3599).
	decoded := decodePasteCtrlSeqs(pastedText)
	clean := e.normalizeText(decoded)

	// Filter non-printable.
	var filtered strings.Builder
	for _, r := range clean {
		if r == '\n' || r >= 32 {
			filtered.WriteRune(r)
		}
	}
	filteredText := filtered.String()

	// Prepend space for file paths.
	if strings.HasPrefix(filteredText, "/") || strings.HasPrefix(filteredText, "~") || strings.HasPrefix(filteredText, ".") {
		runes := []rune(e.state.Lines[e.state.CursorLine])
		charBefore := byte(0)
		if e.state.CursorCol > 0 && e.state.CursorCol <= len(runes) {
			r := runes[e.state.CursorCol-1]
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				filteredText = " " + filteredText
			}
		}
		_ = charBefore
	}

	pastedLines := strings.Split(filteredText, "\n")
	if len(pastedLines) > 10 || utf8.RuneCountInString(filteredText) > 1000 {
		e.pasteCounter++
		pasteID := e.pasteCounter
		e.pastes[pasteID] = filteredText
		var marker string
		if len(pastedLines) > 10 {
			marker = "[paste #" + itoaE(pasteID) + " +" + itoaE(len(pastedLines)) + " lines]"
		} else {
			marker = "[paste #" + itoaE(pasteID) + " " + itoaE(utf8.RuneCountInString(filteredText)) + " chars]"
		}
		e.insertTextInternal(marker)
		return
	}
	e.insertTextInternal(filteredText)
}

var pasteCtrlRe = regexp.MustCompile(`\x1b\[(\d+);5u`)

func decodePasteCtrlSeqs(text string) string {
	return pasteCtrlRe.ReplaceAllStringFunc(text, func(match string) string {
		m := pasteCtrlRe.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		var cp int
		for _, c := range m[1] {
			cp = cp*10 + int(c-'0')
		}
		if cp >= 97 && cp <= 122 {
			return string(rune(cp - 96))
		}
		if cp >= 65 && cp <= 90 {
			return string(rune(cp - 64))
		}
		return match
	})
}

func (e *Editor) addNewLine() {
	e.cancelAutocomplete()
	e.exitHistoryBrowsing()
	e.lastAction = editorActNone
	e.pushUndoSnapshot()

	runes := []rune(e.state.Lines[e.state.CursorLine])
	before := string(runes[:e.state.CursorCol])
	after := string(runes[e.state.CursorCol:])
	e.state.Lines[e.state.CursorLine] = before
	newLines := make([]string, 0, len(e.state.Lines)+1)
	newLines = append(newLines, e.state.Lines[:e.state.CursorLine+1]...)
	newLines = append(newLines, after)
	newLines = append(newLines, e.state.Lines[e.state.CursorLine+1:]...)
	e.state.Lines = newLines
	e.state.CursorLine++
	e.setCursorCol(0)
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) submitValue() {
	e.cancelAutocomplete()
	result := strings.TrimSpace(e.expandPasteMarkers(e.GetText()))
	e.state = EditorState{Lines: []string{""}}
	e.pastes = map[int]string{}
	e.pasteCounter = 0
	e.exitHistoryBrowsing()
	e.scrollOffset = 0
	e.undoStack.Clear()
	e.lastAction = editorActNone
	if e.OnChange != nil {
		e.OnChange("")
	}
	if e.OnSubmit != nil {
		e.OnSubmit(result)
	}
}

func (e *Editor) expandPasteMarkers(text string) string {
	result := text
	for pasteID, content := range e.pastes {
		re := regexp.MustCompile(`\[paste #` + itoaE(pasteID) + `( (\+\d+ lines|\d+ chars))?\]`)
		result = re.ReplaceAllString(result, regexp.QuoteMeta(content))
	}
	return result
}

func (e *Editor) normalizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	return text
}

func (e *Editor) handleBackspace() {
	e.exitHistoryBrowsing()
	e.lastAction = editorActNone
	if e.state.CursorCol > 0 {
		e.pushUndoSnapshot()
		runes := []rune(e.state.Lines[e.state.CursorLine])
		before := string(runes[:e.state.CursorCol])
		lastLen := editorLastGraphemeLen(before)
		e.state.Lines[e.state.CursorLine] = string(runes[:e.state.CursorCol-lastLen]) + string(runes[e.state.CursorCol:])
		e.setCursorCol(e.state.CursorCol - lastLen)
	} else if e.state.CursorLine > 0 {
		e.pushUndoSnapshot()
		prevLine := e.state.Lines[e.state.CursorLine-1]
		currLine := e.state.Lines[e.state.CursorLine]
		e.state.Lines[e.state.CursorLine-1] = prevLine + currLine
		newLines := append(e.state.Lines[:e.state.CursorLine], e.state.Lines[e.state.CursorLine+1:]...)
		e.state.Lines = newLines
		e.state.CursorLine--
		e.setCursorCol(utf8.RuneCountInString(prevLine))
	e.checkTriggerAfterDelete()
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) handleForwardDelete() {
	e.exitHistoryBrowsing()
	e.lastAction = editorActNone
	runes := []rune(e.state.Lines[e.state.CursorLine])
	if e.state.CursorCol < len(runes) {
		e.pushUndoSnapshot()
		after := string(runes[e.state.CursorCol:])
		firstLen := editorFirstGraphemeLen(after)
		e.state.Lines[e.state.CursorLine] = string(runes[:e.state.CursorCol]) + string(runes[e.state.CursorCol+firstLen:])
	} else if e.state.CursorLine < len(e.state.Lines)-1 {
		e.pushUndoSnapshot()
		nextLine := e.state.Lines[e.state.CursorLine+1]
		e.state.Lines[e.state.CursorLine] = e.state.Lines[e.state.CursorLine] + nextLine
		newLines := append(e.state.Lines[:e.state.CursorLine+1], e.state.Lines[e.state.CursorLine+2:]...)
		e.state.Lines = newLines
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
	e.checkTriggerAfterDelete()
}

func (e *Editor) deleteToStartOfLine() {
	e.exitHistoryBrowsing()
	currentLine := e.state.Lines[e.state.CursorLine]
	if e.state.CursorCol > 0 {
		e.pushUndoSnapshot()
		runes := []rune(currentLine)
		deleted := string(runes[:e.state.CursorCol])
		e.killRing.Push(deleted, true, e.lastAction == editorActKill)
		e.lastAction = editorActKill
		e.state.Lines[e.state.CursorLine] = string(runes[e.state.CursorCol:])
		e.setCursorCol(0)
	} else if e.state.CursorLine > 0 {
		e.pushUndoSnapshot()
		e.killRing.Push("\n", true, e.lastAction == editorActKill)
		e.lastAction = editorActKill
		prevLine := e.state.Lines[e.state.CursorLine-1]
		e.state.Lines[e.state.CursorLine-1] = prevLine + currentLine
		e.state.Lines = append(e.state.Lines[:e.state.CursorLine], e.state.Lines[e.state.CursorLine+1:]...)
		e.state.CursorLine--
		e.setCursorCol(utf8.RuneCountInString(prevLine))
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteToEndOfLine() {
	e.exitHistoryBrowsing()
	currentLine := e.state.Lines[e.state.CursorLine]
	runes := []rune(currentLine)
	if e.state.CursorCol < len(runes) {
		e.pushUndoSnapshot()
		deleted := string(runes[e.state.CursorCol:])
		e.killRing.Push(deleted, false, e.lastAction == editorActKill)
		e.lastAction = editorActKill
		e.state.Lines[e.state.CursorLine] = string(runes[:e.state.CursorCol])
	} else if e.state.CursorLine < len(e.state.Lines)-1 {
		e.pushUndoSnapshot()
		e.killRing.Push("\n", false, e.lastAction == editorActKill)
		e.lastAction = editorActKill
		nextLine := e.state.Lines[e.state.CursorLine+1]
		e.state.Lines[e.state.CursorLine] = currentLine + nextLine
		e.state.Lines = append(e.state.Lines[:e.state.CursorLine+1], e.state.Lines[e.state.CursorLine+2:]...)
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteWordBackward() {
	e.exitHistoryBrowsing()
	currentLine := e.state.Lines[e.state.CursorLine]
	if e.state.CursorCol == 0 {
		if e.state.CursorLine > 0 {
			e.pushUndoSnapshot()
			e.killRing.Push("\n", true, e.lastAction == editorActKill)
			e.lastAction = editorActKill
			prevLine := e.state.Lines[e.state.CursorLine-1]
			e.state.Lines[e.state.CursorLine-1] = prevLine + currentLine
			e.state.Lines = append(e.state.Lines[:e.state.CursorLine], e.state.Lines[e.state.CursorLine+1:]...)
			e.state.CursorLine--
			e.setCursorCol(utf8.RuneCountInString(prevLine))
		}
	} else {
		e.pushUndoSnapshot()
		wasKill := e.lastAction == editorActKill
		oldCursor := e.state.CursorCol
		e.moveWordBackward()
		deleteFrom := e.state.CursorCol
		e.setCursorCol(oldCursor)
		runes := []rune(currentLine)
		deleted := string(runes[deleteFrom:e.state.CursorCol])
		e.killRing.Push(deleted, true, wasKill)
		e.lastAction = editorActKill
		e.state.Lines[e.state.CursorLine] = string(runes[:deleteFrom]) + string(runes[e.state.CursorCol:])
		e.setCursorCol(deleteFrom)
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteWordForward() {
	e.exitHistoryBrowsing()
	currentLine := e.state.Lines[e.state.CursorLine]
	runes := []rune(currentLine)
	if e.state.CursorCol >= len(runes) {
		if e.state.CursorLine < len(e.state.Lines)-1 {
			e.pushUndoSnapshot()
			e.killRing.Push("\n", false, e.lastAction == editorActKill)
			e.lastAction = editorActKill
			nextLine := e.state.Lines[e.state.CursorLine+1]
			e.state.Lines[e.state.CursorLine] = currentLine + nextLine
			e.state.Lines = append(e.state.Lines[:e.state.CursorLine+1], e.state.Lines[e.state.CursorLine+2:]...)
		}
	} else {
		e.pushUndoSnapshot()
		wasKill := e.lastAction == editorActKill
		oldCursor := e.state.CursorCol
		e.moveWordForward()
		deleteTo := e.state.CursorCol
		e.setCursorCol(oldCursor)
		deleted := string(runes[e.state.CursorCol:deleteTo])
		e.killRing.Push(deleted, false, wasKill)
		e.lastAction = editorActKill
		e.state.Lines[e.state.CursorLine] = string(runes[:e.state.CursorCol]) + string(runes[deleteTo:])
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// --- Kill-ring ---

func (e *Editor) yank() {
	text, ok := e.killRing.Peek()
	if !ok {
		return
	}
	e.pushUndoSnapshot()
	e.insertYankedText(text)
	e.lastAction = editorActYank
}

func (e *Editor) yankPop() {
	if e.lastAction != editorActYank || e.killRing.Len() <= 1 {
		return
	}
	e.pushUndoSnapshot()
	e.deleteYankedText()
	e.killRing.Rotate()
	newText, _ := e.killRing.Peek()
	e.insertYankedText(newText)
	e.lastAction = editorActYank
}

func (e *Editor) insertYankedText(text string) {
	e.exitHistoryBrowsing()
	e.insertTextInternal(text)
}

func (e *Editor) deleteYankedText() {
	yanked, ok := e.killRing.Peek()
	if !ok {
		return
	}
	yankLines := strings.Split(yanked, "\n")
	if len(yankLines) == 1 {
		runes := []rune(e.state.Lines[e.state.CursorLine])
		deleteLen := utf8.RuneCountInString(yanked)
		e.state.Lines[e.state.CursorLine] = string(runes[:e.state.CursorCol-deleteLen]) + string(runes[e.state.CursorCol:])
		e.setCursorCol(e.state.CursorCol - deleteLen)
	} else {
		startLine := e.state.CursorLine - (len(yankLines) - 1)
		startRunes := []rune(e.state.Lines[startLine])
		startCol := len(startRunes) - utf8.RuneCountInString(yankLines[0])
		afterCursor := string([]rune(e.state.Lines[e.state.CursorLine])[e.state.CursorCol:])
		beforeYank := string(startRunes[:startCol])
		newLines := append(e.state.Lines[:startLine], beforeYank+afterCursor)
		newLines = append(newLines, e.state.Lines[e.state.CursorLine+1:]...)
		e.state.Lines = newLines
		e.state.CursorLine = startLine
		e.setCursorCol(startCol)
	}
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// --- Undo ---

func (e *Editor) pushUndoSnapshot() {
	e.undoStack.Push(EditorState{Lines: e.state.Lines, CursorLine: e.state.CursorLine, CursorCol: e.state.CursorCol})
}

func (e *Editor) undo() {
	e.exitHistoryBrowsing()
	s, ok := e.undoStack.Pop()
	if !ok {
		return
	}
	e.state = s.Clone()
	e.lastAction = editorActNone
	e.preferredVisualCol = nil
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) isEditorEmpty() bool {
	return len(e.state.Lines) == 1 && e.state.Lines[0] == ""
}

func (e *Editor) isOnFirstVisualLine() bool {
	vls := e.buildVisualLineMap(e.lastWidth)
	return e.findCurrentVisualLine(vls) == 0
}

func (e *Editor) isOnLastVisualLine() bool {
	vls := e.buildVisualLineMap(e.lastWidth)
	return e.findCurrentVisualLine(vls) == len(vls)-1
}

// --- Cursor movement ---

func (e *Editor) setCursorCol(col int) {
	e.state.CursorCol = col
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
}

func (e *Editor) moveCursor(deltaLine, deltaCol int) {
	e.lastAction = editorActNone
	visualLines := e.buildVisualLineMap(e.lastWidth)
	currentVL := e.findCurrentVisualLine(visualLines)

	if deltaLine != 0 {
		targetVL := currentVL + deltaLine
		if targetVL >= 0 && targetVL < len(visualLines) {
			e.moveToVisualLine(visualLines, currentVL, targetVL)
		}
	}

	if deltaCol != 0 {
		currentLine := e.state.Lines[e.state.CursorLine]
		runes := []rune(currentLine)
		if deltaCol > 0 {
			if e.state.CursorCol < len(runes) {
				afterCursor := string(runes[e.state.CursorCol:])
				e.setCursorCol(e.state.CursorCol + editorFirstGraphemeLen(afterCursor))
			} else if e.state.CursorLine < len(e.state.Lines)-1 {
				e.state.CursorLine++
				e.setCursorCol(0)
			} else {
				if currentVL < len(visualLines) {
					e.preferredVisualCol = intPtr(e.state.CursorCol - visualLines[currentVL].startCol)
				}
			}
		} else {
			if e.state.CursorCol > 0 {
				beforeCursor := string(runes[:e.state.CursorCol])
				e.setCursorCol(e.state.CursorCol - editorLastGraphemeLen(beforeCursor))
			} else if e.state.CursorLine > 0 {
				e.state.CursorLine--
				prevRunes := []rune(e.state.Lines[e.state.CursorLine])
				e.setCursorCol(len(prevRunes))
			}
		}
	}
	if e.autoState != "" {
		e.updateAutocomplete()
	}
}

func (e *Editor) pageScroll(direction int) {
	e.lastAction = editorActNone
	rows := 24
	if e.tui != nil {
		rows = e.tui.Terminal.Rows()
	}
	pageSize := rows * 30 / 100
	if pageSize < 5 {
		pageSize = 5
	}
	visualLines := e.buildVisualLineMap(e.lastWidth)
	currentVL := e.findCurrentVisualLine(visualLines)
	targetVL := currentVL + direction*pageSize
	if targetVL < 0 {
		targetVL = 0
	}
	if targetVL >= len(visualLines) {
		targetVL = len(visualLines) - 1
	}
	e.moveToVisualLine(visualLines, currentVL, targetVL)
}

func (e *Editor) moveWordBackward() {
	e.lastAction = editorActNone
	currentLine := e.state.Lines[e.state.CursorLine]
	if e.state.CursorCol == 0 {
		if e.state.CursorLine > 0 {
			e.state.CursorLine--
			prevRunes := []rune(e.state.Lines[e.state.CursorLine])
			e.setCursorCol(len(prevRunes))
		}
		return
	}
	e.setCursorCol(tui.FindWordBackward(currentLine, e.state.CursorCol, nil))
}

func (e *Editor) moveWordForward() {
	e.lastAction = editorActNone
	currentLine := e.state.Lines[e.state.CursorLine]
	runes := []rune(currentLine)
	if e.state.CursorCol >= len(runes) {
		if e.state.CursorLine < len(e.state.Lines)-1 {
			e.state.CursorLine++
			e.setCursorCol(0)
		}
		return
	}
	e.setCursorCol(tui.FindWordForward(currentLine, e.state.CursorCol, nil))
}

func (e *Editor) jumpToChar(ch string, dir jumpDirection) {
	e.lastAction = editorActNone
	isForward := dir == jumpForward
	lines := e.state.Lines
	step := 1
	if !isForward {
		step = -1
	}
	endIdx := len(lines)
	if !isForward {
		endIdx = -1
	}
	for lineIdx := e.state.CursorLine; lineIdx != endIdx; lineIdx += step {
		line := lines[lineIdx]
		runes := []rune(line)
		searchFrom := 0
		if lineIdx == e.state.CursorLine {
			if isForward {
				searchFrom = e.state.CursorCol + 1
			} else {
				searchFrom = e.state.CursorCol - 1
			}
		}
		var idx int
		found := false
		if isForward {
			if searchFrom >= 0 && searchFrom <= len(runes) {
				slice := string(runes[searchFrom:])
				if i := strings.Index(slice, ch); i >= 0 {
					idx = searchFrom + utf8.RuneCountInString(slice[:i])
					found = true
				}
			}
		} else {
			if searchFrom >= 0 && searchFrom <= len(runes) {
				slice := string(runes[:searchFrom])
				if i := strings.LastIndex(slice, ch); i >= 0 {
					idx = utf8.RuneCountInString(slice[:i])
					found = true
				}
			}
		}
		if found {
			e.state.CursorLine = lineIdx
			e.setCursorCol(idx)
			return
		}
	}
}

// --- History ---

func (e *Editor) navigateHistory(direction int) {
	e.lastAction = editorActNone
	if len(e.history) == 0 {
		return
	}
	newIndex := e.historyIndex - direction
	if newIndex < -1 || newIndex >= len(e.history) {
		return
	}
	if e.historyIndex == -1 && newIndex >= 0 {
		e.pushUndoSnapshot()
		e.historyDraft = &EditorState{Lines: e.state.Lines, CursorLine: e.state.CursorLine, CursorCol: e.state.CursorCol}
		draftClone := e.historyDraft.Clone()
		e.historyDraft = &draftClone
	}
	e.historyIndex = newIndex
	if e.historyIndex == -1 {
		draft := e.historyDraft
		e.historyDraft = nil
		if draft != nil {
			e.state = draft.Clone()
			e.preferredVisualCol = nil
			e.snappedFromCursorCol = nil
			e.scrollOffset = 0
			if e.OnChange != nil {
				e.OnChange(e.GetText())
			}
		} else {
			e.setTextInternal("", true)
		}
	} else {
		placement := true
		if direction == -1 {
			placement = false
		}
		e.setTextInternal(e.history[e.historyIndex], placement)
	}
}

func (e *Editor) exitHistoryBrowsing() {
	e.historyIndex = -1
	e.historyDraft = nil
}

// --- Autocomplete stubs (P10b) ---

func (e *Editor) cancelAutocomplete() {
	e.autoState = ""
	e.autoList = nil
	e.autoPrefix = ""
}

// --- Helpers ---

func editorLastGraphemeLen(s string) int {
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

func editorFirstGraphemeLen(s string) int {
	if s == "" {
		return 1
	}
	gr := uniseg.NewGraphemes(s)
	if gr.Next() {
		return utf8.RuneCountInString(gr.Str())
	}
	return 1
}

func intPtr(n int) *int { return &n }

func itoaE(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
