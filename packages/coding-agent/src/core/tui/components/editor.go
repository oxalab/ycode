package components

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/autocomplete"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Editor wraps bubbles/textarea.Model and adds:
//   - Enter to submit, Shift+Enter (or Alt+Enter) for newline
//   - Bash mode detection (first line starts with "!")
//   - Placeholder text that adapts to mode
//   - Themed styling via the project theme system
//
// Port of reference/coding-agent/src/modes/interactive/components/custom-editor.ts.
type Editor struct {
	textarea textarea.Model
	theme    *theme.Theme

	// isBashMode is true when the first line of input starts with "!".
	isBashMode bool

	// width is the allocated width from the parent layout.
	width int

	// focused tracks whether the textarea has focus.
	focused bool

	// Autocomplete state (Phase 17.4). acProvider supplies suggestions for the
	// slash token at the cursor; when acShowing is true the popup renders below
	// the textarea and intercepts navigation/selection keys.
	acProvider autocomplete.AutocompleteProvider
	acItems    []autocomplete.AutocompleteItem
	acSelected int
	acShowing  bool
}

// autocompleteMaxVisible is the most popup rows rendered at once.
const autocompleteMaxVisible = 8

// NewEditor creates a new Editor component.
func NewEditor(t *theme.Theme, width int) Editor {
	ta := textarea.New()
	ta.Prompt = " > "
	ta.Placeholder = "Type a message..."
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // unlimited
	ta.SetWidth(width)
	ta.SetHeight(1) // single-line by default, grows with content

	// Style the textarea with theme colors
	styleTextarea(&ta, t)

	// Focus by default
	ta.Focus()

	return Editor{
		textarea: ta,
		theme:    t,
		width:    width,
		focused:  true,
	}
}

// SetValue sets the editor's text content.
func (e *Editor) SetValue(s string) {
	e.textarea.SetValue(s)
	e.detectBashMode()
}

// Value returns the editor's current text content.
func (e Editor) Value() string {
	return e.textarea.Value()
}

// Focus gives the editor focus and returns any commands.
func (e *Editor) Focus() tea.Cmd {
	e.focused = true
	return e.textarea.Focus()
}

// Blur removes focus from the editor.
func (e *Editor) Blur() {
	e.focused = false
	e.textarea.Blur()
}

// Focused returns whether the editor has focus.
func (e Editor) Focused() bool {
	return e.focused
}

// SetWidth updates the editor's width.
func (e *Editor) SetWidth(w int) {
	e.width = w
	// Reserve 2 columns for the rounded border's left/right edges so the
	// bordered editor fits within the allocated width.
	contentWidth := w - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	e.textarea.SetWidth(contentWidth)
}

// SetHeight updates the editor's visible height (lines shown).
func (e *Editor) SetHeight(h int) {
	e.textarea.SetHeight(h)
}

// IsBashMode returns whether the editor is in bash mode (! prefix).
func (e Editor) IsBashMode() bool {
	return e.isBashMode
}

// PopupHeight returns the number of rows the autocomplete popup occupies, or 0
// when it is hidden. The parent layout subtracts this from the chat height so
// the popup fits on screen instead of overflowing the terminal (the popup
// renders below the editor, which is docked at the bottom of the screen).
func (e Editor) PopupHeight() int {
	if !e.acShowing || len(e.acItems) == 0 {
		return 0
	}
	n := len(e.acItems)
	if n > autocompleteMaxVisible {
		n = autocompleteMaxVisible
	}
	return n
}

// Reset clears the editor content and resets bash mode.
func (e *Editor) Reset() {
	e.textarea.Reset()
	e.isBashMode = false
	e.hideAutocomplete()
}

// SetAutocompleteProvider installs the provider used for slash-command
// completions. Pass nil to disable. The popup is hidden until the next refresh.
func (e *Editor) SetAutocompleteProvider(p autocomplete.AutocompleteProvider) {
	e.acProvider = p
	e.hideAutocomplete()
}

// Height returns the natural height the editor wants to occupy,
// clamped to a minimum of 1 and maximum of maxHeight.
func (e Editor) Height() int {
	lineCount := e.textarea.LineCount()
	if lineCount < 1 {
		lineCount = 1
	}
	maxHeight := 10
	if lineCount > maxHeight {
		lineCount = maxHeight
	}
	// Include the rounded border's top + bottom rows so the parent's
	// chat-height math reserves the editor's full rendered footprint.
	return lineCount + 2
}

// Init satisfies tea.Model (no initial commands needed).
func (e Editor) Init() tea.Cmd {
	return nil
}

// Update handles messages. Returns the updated editor and an optional command.
// The parent App should check for submitMsg after calling Update.
func (e Editor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	var cmd tea.Cmd

	// While the autocomplete popup is open it owns navigation/selection keys;
	// those never reach the textarea. All other keys fall through to the
	// textarea (and refresh the popup afterwards).
	if keyMsg, ok := msg.(tea.KeyMsg); ok && e.acShowing {
		switch keyMsg.String() {
		case "up", "ctrl+p":
			e.acMove(-1)
			return e, nil
		case "down", "ctrl+n":
			e.acMove(1)
			return e, nil
		case "tab", "enter":
			// Enter/Tab select the highlighted item (Phase 17.4 spec).
			e.applyCurrentCompletion()
			e.refreshAutocomplete()
			return e, nil
		case "escape":
			e.hideAutocomplete()
			return e, nil
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			// Enter = submit (do NOT pass to textarea — don't insert newline)
			value := strings.TrimSpace(e.textarea.Value())
			if value == "" {
				return e, nil // ignore empty submit
			}

			if e.isBashMode {
				// "!cmd" runs in context; "!!cmd" runs excluded from context.
				excluded := strings.HasPrefix(value, "!!")
				cmd := value
				if excluded {
					cmd = strings.TrimPrefix(cmd, "!!")
				} else {
					cmd = strings.TrimPrefix(cmd, "!")
				}
				cmd = strings.TrimSpace(cmd)
				e.Reset()
				return e, func() tea.Msg { return BashSubmitMsg{Command: cmd, ExcludedFromContext: excluded} }
			}

			e.Reset()
			return e, func() tea.Msg { return SubmitMsg{Text: value} }

		case "shift+enter", "ctrl+enter":
			// Shift+Enter / Ctrl+Enter = insert actual newline
			e.textarea, cmd = e.textarea.Update(msg)
			e.detectBashMode()
			e.refreshAutocomplete()
			return e, cmd

		default:
			// All other keys pass through to textarea normally
			e.textarea, cmd = e.textarea.Update(msg)
			e.detectBashMode()
			e.refreshAutocomplete()
			return e, cmd
		}
	}

	// Non-key messages (resize, etc.) pass through
	e.textarea, cmd = e.textarea.Update(msg)
	e.refreshAutocomplete()
	return e, cmd
}

// View renders the editor.
func (e Editor) View() string {
	if e.theme == nil {
		return e.textarea.View()
	}

	// Render a border above the editor area. In bash mode it takes the theme's
	// bash color (mirrors theme.getBashModeBorderColor()); otherwise a neutral
	// gray. The prompt also switches to " ! " in bash mode (set below).
	borderColor := lipgloss.TerminalColor(lipgloss.Color("240"))
	if e.isBashMode {
		borderColor = e.theme.ToLipglossColor("bashMode")
	}
	borderStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderTop(true).
		BorderRight(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderForeground(borderColor)

	// Adjust prompt for bash mode
	prompt := " > "
	if e.isBashMode {
		prompt = " ! "
	}

	// We need to set prompt dynamically before rendering
	originalPrompt := e.textarea.Prompt
	e.textarea.Prompt = prompt
	rendered := e.textarea.View()
	e.textarea.Prompt = originalPrompt

	bordered := borderStyle.Render(rendered)

	// Render the autocomplete popup beneath the textarea when open (Phase 17.4).
	popup := e.renderAutocomplete()
	if popup == "" {
		return bordered
	}
	return lipgloss.JoinVertical(lipgloss.Left, bordered, popup)
}

// --- Internal ---

// detectBashMode checks whether the first non-whitespace character is "!",
// mirroring the TS "text.trimStart().startsWith('!')" check. Leading whitespace
// (or blank leading lines) before the "!" still counts as bash mode.
func (e *Editor) detectBashMode() {
	trimmed := strings.TrimLeft(e.textarea.Value(), " \t\r\n")
	e.isBashMode = strings.HasPrefix(trimmed, "!")
}

// ============================================================================
// Autocomplete popup (Phase 17.4)
// ============================================================================

// hideAutocomplete closes the popup and clears selection state.
func (e *Editor) hideAutocomplete() {
	e.acShowing = false
	e.acItems = nil
	e.acSelected = 0
}

// refreshAutocomplete recomputes suggestions for the slash token at the cursor.
// Called after every buffer change. Shows the popup when the provider returns
// items for a slash token, hides it otherwise. Selection resets to the top each
// refresh (the list only changes on buffer edits, never during navigation).
func (e *Editor) refreshAutocomplete() {
	if e.acProvider == nil {
		e.hideAutocomplete()
		return
	}
	prefix := e.slashTokenBeforeCursor()
	if prefix == "" {
		e.hideAutocomplete()
		return
	}
	items := e.acProvider.GetItems(prefix)
	if len(items) == 0 {
		e.hideAutocomplete()
		return
	}
	e.acItems = items
	e.acSelected = 0
	e.acShowing = true
}

// acMove moves the selection by delta, wrapping around the item list.
func (e *Editor) acMove(delta int) {
	n := len(e.acItems)
	if n == 0 {
		return
	}
	e.acSelected += delta
	e.acSelected %= n
	if e.acSelected < 0 {
		e.acSelected += n
	}
}

// applyCurrentCompletion replaces the token at the cursor with the selected item.
func (e *Editor) applyCurrentCompletion() {
	if !e.acShowing || e.acSelected < 0 || e.acSelected >= len(e.acItems) {
		return
	}
	e.applyCompletion(e.acItems[e.acSelected])
}

// applyCompletion replaces the slash token at the cursor with item.Value. In
// command mode (no whitespace in the token) it appends a trailing space so arg
// completion can trigger (e.g. selecting "/model" opens the model list); in
// argument mode it replaces only the word being typed. The cursor is left
// immediately after the inserted text.
//
// bubbles v1 note: the textarea exposes no public column accessor, so this
// assumes the cursor sits at the end of the current logical line — the normal
// case while typing a slash command. Mid-line cursors are treated as end-of-line.
func (e *Editor) applyCompletion(item autocomplete.AutocompleteItem) {
	line := e.textarea.Line()
	lines := strings.Split(e.textarea.Value(), "\n")
	if line < 0 || line >= len(lines) {
		return
	}
	runes := []rune(lines[line])
	col := len(runes) // cursor assumed at end of line

	// Locate the "/" that begins the current token (at start or after whitespace).
	slashIdx := -1
	for i := col - 1; i >= 0; i-- {
		if runes[i] == '/' {
			slashIdx = i
			break
		}
	}
	if slashIdx < 0 {
		return
	}
	if slashIdx > 0 && !isSpaceRune(runes[slashIdx-1]) {
		return // embedded in a word (e.g. "a/b") — not a slash command
	}

	// Last whitespace between the slash and the cursor decides command vs arg mode.
	wsIdx := -1
	for i := col - 1; i >= slashIdx; i-- {
		if isSpaceRune(runes[i]) {
			wsIdx = i
			break
		}
	}

	valRunes := []rune(item.Value)
	var built []rune
	targetCol := 0
	if wsIdx >= 0 {
		// Argument mode: keep "/<cmd> " and replace the typed arg word.
		built = append(built, runes[:wsIdx+1]...)
		built = append(built, valRunes...)
		targetCol = wsIdx + 1 + len(valRunes)
	} else {
		// Command mode: replace the token and add a trailing space.
		built = append(built, runes[:slashIdx]...)
		built = append(built, valRunes...)
		built = append(built, ' ')
		targetCol = slashIdx + len(valRunes) + 1
	}

	lines[line] = string(built)
	e.textarea.SetValue(strings.Join(lines, "\n"))

	// SetValue leaves the cursor at the buffer's end; the edit only touched one
	// line so the row count is unchanged. Walk up to the target row, then set the
	// column so the cursor lands right after the inserted text.
	totalLines := e.textarea.LineCount()
	for up := (totalLines - 1) - line; up > 0; up-- {
		e.textarea.CursorUp()
	}
	e.textarea.SetCursor(targetCol)
	e.detectBashMode()
}

// slashTokenBeforeCursor returns the slash-command token at the cursor — the
// substring from the most recent word-starting "/" to the end of the current
// line (inclusive of any spaces, so "/model cl" is returned whole for argument
// completion). Returns "" when the cursor is not inside a slash token.
//
// bubbles v1 exposes no cursor column, so the token is taken to the end of the
// current logical line (cursor assumed at line end while typing).
func (e Editor) slashTokenBeforeCursor() string {
	line := e.textarea.Line()
	lines := strings.Split(e.textarea.Value(), "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	runes := []rune(lines[line])
	slashIdx := -1
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == '/' {
			slashIdx = i
			break
		}
	}
	if slashIdx < 0 {
		return ""
	}
	if slashIdx > 0 && !isSpaceRune(runes[slashIdx-1]) {
		return ""
	}
	return string(runes[slashIdx:])
}

// renderAutocomplete renders the popup rows, or "" when hidden.
func (e Editor) renderAutocomplete() string {
	if !e.acShowing || len(e.acItems) == 0 || e.theme == nil {
		return ""
	}
	// Scroll window: show a page of autocompleteMaxVisible rows that follows the
	// selection. Once the selection moves past the first page the window advances
	// one line at a time, so items beyond the first page come into view and the
	// highlighted row is always on-screen.
	visible := autocompleteMaxVisible
	offset := 0
	if len(e.acItems) > visible {
		offset = e.acSelected - visible + 1
		if offset < 0 {
			offset = 0
		}
		if max := len(e.acItems) - visible; offset > max {
			offset = max
		}
	}
	end := offset + visible
	if end > len(e.acItems) {
		end = len(e.acItems)
	}
	items := e.acItems[offset:end]

	width := e.width - 2
	if width < 20 {
		width = 20
	}

	rows := make([]string, 0, len(items))
	for i, it := range items {
		var row string
		if offset+i == e.acSelected {
			row = e.theme.Fg("accent", "▸ "+e.theme.Bold(it.Label))
		} else {
			row = e.theme.Fg("muted", "  "+it.Label)
		}
		if it.Description != "" {
			row += "  " + e.theme.Dim(it.Description)
		}
		rows = append(rows, row)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	return lipgloss.NewStyle().Width(width).Render(body)
}

// isSpaceRune reports whether r is an inline whitespace rune.
func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t'
}

// styleTextarea applies theme-based styling to the textarea.
func styleTextarea(ta *textarea.Model, t *theme.Theme) {
	if t == nil {
		return
	}
	// The bubbles textarea uses lipgloss styles for focused/blurred states.
	// We apply a subtle style via the existing lipgloss style fields.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
}

// ============================================================================
// Editor message types — exported for use by the parent App
// ============================================================================

// SubmitMsg is sent when the user submits the editor input (Enter key).
type SubmitMsg struct {
	Text string
}

// BashSubmitMsg is sent when the user submits a bash command (! prefix).
//
// ExcludedFromContext is true for the "!!" form, which runs the command without
// adding its output to the agent's context (mirrors the TS !! handling).
type BashSubmitMsg struct {
	Command             string
	ExcludedFromContext bool
}
