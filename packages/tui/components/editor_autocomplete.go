package components

import (
	"context"
	"regexp"
	"strings"

	"ycode/packages/tui"
)

var slashCmdLayout = SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// IsShowingAutocomplete reports whether the autocomplete dropdown is currently
// open. Exposed for app-level editors (e.g. a CustomEditor) that need to gate
// app keybindings on autocomplete state — mirrors reference custom-editor.ts
// isShowingAutocomplete().
func (e *Editor) IsShowingAutocomplete() bool { return e.autoState != "" }

// SetAutocompleteProvider sets the provider and trigger characters.
func (e *Editor) SetAutocompleteProvider(provider tui.AutocompleteProvider) {
	e.cancelAutocomplete()
	e.autoProvider = provider
	e.autoTriggerChars = []string{"@", "#"}
	if provider != nil {
		for _, ch := range provider.TriggerCharacters() {
			if len(ch) == 1 && ch != "/" && !tui.IsWhitespaceChar(rune(ch[0])) {
				found := false
				for _, existing := range e.autoTriggerChars {
					if existing == ch {
						found = true
						break
					}
				}
				if !found {
					e.autoTriggerChars = append(e.autoTriggerChars, ch)
				}
			}
		}
	}
	e.autoTriggerPattern = buildEditorTriggerPattern(e.autoTriggerChars)
}

func buildEditorTriggerPattern(chars []string) *regexp.Regexp {
	var escaped []string
	for _, ch := range chars {
		if ch == "@" {
			continue
		}
		r := []rune(ch)
		for _, c := range r {
			switch c {
			case '\\', '^', '$', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '|':
				escaped = append(escaped, "\\"+string(c))
			default:
				escaped = append(escaped, string(c))
			}
		}
	}
	pattern := `(?:^|[\s])@(?:"[^"]*|[^\s]*)|[` + strings.Join(escaped, "") + `][^\s]*$`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return regexp.MustCompile(`$^`) // never matches
	}
	return re
}

// --- Trigger context helpers ---

func (e *Editor) isSlashMenuAllowed() bool {
	return e.state.CursorLine == 0
}

func (e *Editor) isAtStartOfMessage() bool {
	if !e.isSlashMenuAllowed() {
		return false
	}
	currentLine := e.state.Lines[e.state.CursorLine]
	runes := []rune(currentLine)
	beforeCursor := string(runes[:e.state.CursorCol])
	trimmed := strings.TrimSpace(beforeCursor)
	return trimmed == "" || trimmed == "/"
}

func (e *Editor) isInSlashCommandContext(text string) bool {
	return e.isSlashMenuAllowed() && strings.HasPrefix(strings.TrimLeft(text, " "), "/")
}

func (e *Editor) isTriggerChar(ch string) bool {
	for _, t := range e.autoTriggerChars {
		if t == ch {
			return true
		}
	}
	return false
}

func (e *Editor) textBeforeCursor() string {
	currentLine := e.state.Lines[e.state.CursorLine]
	runes := []rune(currentLine)
	if e.state.CursorCol > len(runes) {
		return currentLine
	}
	return string(runes[:e.state.CursorCol])
}

// --- Request / trigger ---

func (e *Editor) tryTriggerAutocomplete() {
	e.requestAutocomplete(false, false)
}

func (e *Editor) updateAutocomplete() {
	if e.autoState == "" || e.autoProvider == nil {
		return
	}
	force := e.autoState == "force"
	e.requestAutocomplete(force, false)
}

func (e *Editor) handleTabCompletion() {
	if e.autoProvider == nil {
		return
	}
	beforeCursor := e.textBeforeCursor()
	if e.isInSlashCommandContext(beforeCursor) && !strings.Contains(strings.TrimLeft(beforeCursor, " "), " ") {
		e.requestAutocomplete(false, true)
	} else {
		e.requestAutocomplete(true, true)
	}
}

func (e *Editor) requestAutocomplete(force, explicitTab bool) {
	if e.autoProvider == nil {
		return
	}
	if force {
		if !e.autoProvider.ShouldTriggerFileCompletion(e.state.Lines, e.state.CursorLine, e.state.CursorCol) {
			return
		}
	}
	// Synchronous (P10b caveat: async debounce deferred — see CAVEATS).
	suggestions, ok := e.autoProvider.GetSuggestions(
		e.state.Lines, e.state.CursorLine, e.state.CursorCol,
		context.Background(), force,
	)
	if !ok || suggestions == nil || len(suggestions.Items) == 0 {
		e.cancelAutocomplete()
		// Do NOT call e.tui.RequestRender() here — requestAutocomplete runs
		// inside HandleInput, which is under the engine render lock. The engine
		// re-renders automatically after HandleInput returns (see tui.ts
		// handleInput → requestRender at the end). Calling RequestRender from
		// inside the input handler deadlocks the render timer.
		return
	}
	if force && explicitTab && len(suggestions.Items) == 1 {
		e.pushUndoSnapshot()
		e.lastAction = editorActNone
		e.applyAutoCompletionInternal(suggestions.Items[0], suggestions.Prefix)
		return
	}
	e.applyAutoSuggestions(suggestions, force)
}

func (e *Editor) applyAutoSuggestions(suggestions *tui.AutocompleteSuggestions, force bool) {
	e.autoPrefix = suggestions.Prefix
	items := make([]SelectItem, len(suggestions.Items))
	for i, item := range suggestions.Items {
		items[i] = SelectItem{Value: item.Value, Label: item.Label, Description: item.Description}
	}
	var layout SelectListLayoutOptions
	if strings.HasPrefix(suggestions.Prefix, "/") {
		layout = slashCmdLayout
	}
	e.autoList = NewSelectList(items, e.autoMaxVisible, e.theme.SelectList, layout)
	bestIdx := bestAutoMatchIndex(suggestions.Items, suggestions.Prefix)
	if bestIdx >= 0 {
		e.autoList.SetSelectedIndex(bestIdx)
	}
	if force {
		e.autoState = "force"
	} else {
		e.autoState = "regular"
	}
}

func bestAutoMatchIndex(items []tui.AutocompleteItem, prefix string) int {
	if prefix == "" {
		return -1
	}
	firstPrefix := -1
	for i, item := range items {
		if item.Value == prefix {
			return i
		}
		if firstPrefix == -1 && strings.HasPrefix(item.Value, prefix) {
			firstPrefix = i
		}
	}
	return firstPrefix
}

func (e *Editor) applyAutoCompletion(item tui.AutocompleteItem) {
	e.pushUndoSnapshot()
	e.lastAction = editorActNone
	e.applyAutoCompletionInternal(item, e.autoPrefix)
}

func (e *Editor) applyAutoCompletionInternal(item tui.AutocompleteItem, prefix string) {
	lines, cursorLine, cursorCol := e.autoProvider.ApplyCompletion(
		e.state.Lines, e.state.CursorLine, e.state.CursorCol, item, prefix,
	)
	e.state.Lines = lines
	e.state.CursorLine = cursorLine
	e.setCursorCol(cursorCol)
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// --- Trigger after insert/delete (called from editor.go) ---

func (e *Editor) checkTriggerAfterInsert(ch string) {
	if e.autoProvider == nil {
		return
	}
	if e.autoState != "" {
		e.updateAutocomplete()
		return
	}
	if ch == "/" && e.isAtStartOfMessage() {
		e.tryTriggerAutocomplete()
		return
	}
	if e.isTriggerChar(ch) {
		tbc := e.textBeforeCursor()
		runes := []rune(tbc)
		if len(runes) == 1 {
			e.tryTriggerAutocomplete()
			return
		}
		if len(runes) >= 2 {
			prev := runes[len(runes)-2]
			if prev == ' ' || prev == '\t' {
				e.tryTriggerAutocomplete()
				return
			}
		}
		return
	}
	if isAlnumOrDotDash(ch) {
		tbc := e.textBeforeCursor()
		if e.isInSlashCommandContext(tbc) {
			e.tryTriggerAutocomplete()
		} else if e.autoTriggerPattern != nil && e.autoTriggerPattern.MatchString(tbc) {
			e.tryTriggerAutocomplete()
		}
	}
}

func (e *Editor) checkTriggerAfterDelete() {
	if e.autoProvider == nil {
		return
	}
	if e.autoState != "" {
		e.updateAutocomplete()
		return
	}
	tbc := e.textBeforeCursor()
	if e.isInSlashCommandContext(tbc) {
		e.tryTriggerAutocomplete()
	} else if e.autoTriggerPattern != nil && e.autoTriggerPattern.MatchString(tbc) {
		e.tryTriggerAutocomplete()
	}
}

func isAlnumOrDotDash(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_'
}

// --- HandleInput autocomplete mode (called from editor.go) ---
// Returns true if the input was consumed by autocomplete handling.

func (e *Editor) handleAutocompleteInput(data string, kb *tui.KeybindingsManager) bool {
	if e.autoState == "" || e.autoList == nil {
		return false
	}
	if kb.Matches(data, "tui.select.cancel") {
		e.cancelAutocomplete()
		return true
	}
	if kb.Matches(data, "tui.select.up") || kb.Matches(data, "tui.select.down") {
		e.autoList.HandleInput(data)
		return true
	}
	if kb.Matches(data, "tui.input.tab") {
		if item, ok := e.autoList.GetSelectedItem(); ok {
			e.applyAutoCompletion(tui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description})
		}
		return true
	}
	if kb.Matches(data, "tui.select.confirm") {
		if item, ok := e.autoList.GetSelectedItem(); ok {
			e.applyAutoCompletion(tui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description})
			isSlash := strings.HasPrefix(e.autoPrefix, "/")
			if isSlash {
				e.cancelAutocomplete()
				// Fall through to submit (return false so HandleInput continues).
				return false
			}
			e.cancelAutocomplete()
			if e.OnChange != nil {
				e.OnChange(e.GetText())
			}
			return true
		}
	}
	return false
}
