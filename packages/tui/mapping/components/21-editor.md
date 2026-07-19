# Components 21 — Editor (multi-line)

The heavyweight: **2307 LOC**. Multi-line editor with word-wrap, sticky-column
vertical navigation, Emacs kill/yank, undo, history, char-jump, async autocomplete,
large-paste markers, vertical scrolling. Implements `tui.Component`, `tui.Focusable`,
`tui.InputHandler`, and the `EditorComponent` extension interface.

- **TS source:** [`editor.ts`](../../../reference/tui/src/components/editor.ts)
- **Go target:** `components/editor.go`
- **Package:** `components`
- **Depends on:** `tui` (Component/Focusable/InputHandler, CursorMarker, Keybindings,
  MatchesKey, DecodePrintableKey, VisibleWidth, IsCJKBreak, grapheme+word segmenters,
  IsWhitespaceChar, TruncateToWidth), `tui.UndoStack`, `tui.KillRing`,
  `wordnav.FindWord*`, `components.SelectList`, `autocomplete.AutocompleteProvider`.

> **Strategy:** This is too large to port in one pass. Port in this order:
> (1) state model + `layoutText`/`wordWrapLine`, (2) text mutation (insert/paste/newline),
> (3) cursor movement (grapheme + word + line + sticky-column vertical), (4) deletion +
> kill-ring, (5) undo, (6) history, (7) char-jump, (8) autocomplete wiring,
> (9) `render` (scroll + borders + cursor). Each against the corresponding
> `editor.test.ts` block.

---

## State

```go
type EditorState struct {
    Lines      []string
    CursorLine int
    CursorCol  int
}
type Editor struct {
    state   EditorState
    focused bool
    tui     *tui.TUI
    theme   EditorTheme
    paddingX int

    lastWidth     int
    scrollOffset  int
    borderColor   StyleFunc

    // autocomplete
    provider             AutocompleteProvider
    triggerChars          []string
    triggerPattern, debouncePattern *regexp.Regexp
    list                  *SelectList
    autoState             autoState  // "regular"|"force"|none
    autoPrefix            string
    autoMaxVisible        int
    autoCancel            context.CancelFunc
    autoDebounce          *time.Timer
    autoReqTask           chan struct{}
    autoStartToken        int
    autoReqID             int

    // paste
    pastes        map[int]string
    pasteCounter  int
    pasteBuffer   string
    inPaste       bool

    // history
    history      []string
    historyIndex int
    historyDraft *EditorState

    killRing   *tui.KillRing
    lastAction lastActionKind

    jumpMode  *jumpDir  // "forward"|"backward"|nil

    preferredVisualCol  *int   // sticky column
    snappedFromCursorCol *int   // pre-snap position for atomic segments

    undoStack *tui.UndoStack[EditorState]

    onSubmit func(string)
    onChange func(string)
    disableSubmit bool
}
```

| TS types/fields | Go |
|---|---|
| `EditorTheme { borderColor, selectList }` | `type EditorTheme struct{ BorderColor StyleFunc; SelectList SelectListTheme }` |
| `EditorOptions { paddingX?, autocompleteMaxVisible? }` | `type EditorOptions struct{ PaddingX, AutocompleteMaxVisible int }` |
| `TextChunk { text, startIndex, endIndex }` | `type TextChunk struct{ Text string; Start, End int }` |
| `LayoutLine { text, hasCursor, cursorPos? }` | `type LayoutLine struct{ Text string; HasCursor bool; CursorPos int }` |

## Public API

| TS | Go |
|---|---|
| `new Editor(tui, theme, options?)` | `NewEditor(t *tui.TUI, theme EditorTheme, opts ...EditorOptions)` |
| `focused` | `Focused()/SetFocused(bool)` |
| `borderColor` (field, dynamic) | `BorderColor()/SetBorderColor(StyleFunc)` |
| `getPaddingX/setPaddingX` | `GetPaddingX()/SetPaddingX(int)` (+ requestRender on change) |
| `getAutocompleteMaxVisible/setAutocompleteMaxVisible` | same |
| `setAutocompleteProvider(p)` | `SetAutocompleteProvider(AutocompleteProvider)` |
| `addToHistory(text)` | `AddToHistory(string)` (dedup consecutive, cap 100) |
| `onSubmit?/onChange?/disableSubmit` | fields/`DisableSubmit bool` |
| `getText/getLines/getCursor` | `GetText() string` / `GetLines() []string` / `GetCursor() (line, col int)` |
| `getExpandedText` | `GetExpandedText()` (expand paste markers) |
| `setText` / `insertTextAtCursor` | `SetText(string)` / `InsertTextAtCursor(string)` (atomic undo) |
| `invalidate()` (no-op) | `Invalidate()` |
| `render(width)` | `Render(width)` |
| `handleInput(data)` | `HandleInput(data)` |
| `isShowingAutocomplete()` | `IsShowingAutocomplete() bool` |
| `wordWrapLine(line, maxWidth, preSegmented?)` (exported) | `WordWrapLine(line string, maxWidth int, pre []Grapheme) []TextChunk` |

## `handleInput` dispatch order (port exactly — long)

1. **Jump mode** (`jumpMode != nil`): if the trigger re-pressed → cancel; printable
   → `jumpToChar`; control char → cancel + fall through.
2. **Bracketed paste** buffering → `handlePaste`.
3. `tui.input.copy` (Ctrl+C) → return (let parent handle).
4. `undo`.
5. **Autocomplete active**: cancel (Esc), up/down → list, Tab/Enter → applyCompletion
   (Enter on `/`-prefix submits after applying). Tab when inactive → `handleTabCompletion`.
6. Deletion: deleteToLineEnd/Start, deleteWordBackward/Forward, backspace
   (+shift+backspace), forwardDelete (+shift+delete).
7. Kill-ring: yank, yankPop.
8. Cursor: lineStart/End, wordLeft/Right.
9. **New line** (shift+enter / alt+enter / ctrl+enter variants / `\x1b\r` / `\n` /
   ctrl+J-in-sequence) — with the `\`+Enter-submits-backslash-then-newline workaround
   (`shouldSubmitOnBackslashEnter`).
10. **Submit** (enter) — `\`+Enter-newline workaround, else `submitValue`.
11. Arrow up/down **with history** (first/last visual line + empty/browsing/col0),
    else vertical move or line start/end.
12. Arrow left/right (grapheme move).
13. Page up/down (`pageScroll`).
14. Jump triggers (ctrl+], ctrl+alt+]).
15. Shift+space → space.
16. `DecodePrintableKey` → `insertCharacter`; else raw char ≥32 → insert.

## Core algorithms (port 1:1 — all intricate, all tested)

| TS | Go | What |
|---|---|---|
| `wordWrapLine` | `WordWrapLine` | word-boundary wrap with backtrack + force-break; atomic segment > width re-wrapped at grapheme level; CJK break between any adjacent CJK |
| `segmentWithMarkers` + `PASTE_MARKER_REGEX` | `segmentWithMarkers` | merge paste-marker graphemes into atomic segments (only valid IDs) |
| `layoutText` | `layoutText` | logical→layout lines; assigns cursor to the correct wrapped chunk (handles trimmed-whitespace cursor clamping) |
| `buildVisualLineMap` | `buildVisualLineMap` | logical line → visual segments {logicalLine, startCol, length} |
| `findVisualLineAt` / `findCurrentVisualLine` | same | locate cursor's visual line |
| `moveCursor(dLine, dCol)` | same | horizontal grapheme move; vertical via `moveToVisualLine` |
| `moveToVisualLine` | same | **sticky-column + atomic-segment snapping**; uses `snappedFromCursorCol` for resize-stable snaps |
| `computeVerticalMoveColumn` | same | the sticky-column **decision table** (P/S/T/U) — port the truth table comment + branches exactly |
| `pageScroll` | same | move by `max(5, floor(rows*0.3))` |
| `jumpToChar` | same | multi-line forward/backward `indexOf`/`lastIndexOf`, skip current cursor |

## Text mutation

| TS | Go |
|---|---|
| `insertCharacter(char, skipUndo?)` | same (undo coalescing; auto-trigger autocomplete for `/`, trigger chars, or letters in slash/symbol context) |
| `insertTextAtCursorInternal` | same (single + multi-line; normalize `\r\n`/`\r`→`\n`, tabs→4 spaces) |
| `handlePaste` | same (decode `ESC[<cp>;5u` ctrl-seqs back to literals; filter non-printable; **large-paste marker** >10 lines or >1000 chars → `[paste #N +X lines]`/`[N chars]` stored in `pastes`) |
| `addNewLine` | same |
| `submitValue` | same (expand markers, trim, reset state, clear pastes/undo, fire onSubmit) |
| `expandPasteMarkers` | same |

## Deletion + kill-ring

`handleBackspace`/`handleForwardDelete` (grapheme; merge lines at boundary);
`deleteToStartOfLine`/`deleteToEndOfLine`/`deleteWordBackwards`/`deleteWordForward`
(all push to kill ring with prepend/append + accumulate, treating newline merges as
`\n`). After backspace/forwardDelete, re-trigger or update autocomplete. Same
semantics as Input (see [20-input.md](20-input.md)) but multi-line.

## Autocomplete engine (async, race-safe)

| TS | Go |
|---|---|
| `requestAutocomplete({force, explicitTab})` | same — debounce (20ms for attachment patterns, 0 for explicit/force) |
| `startAutocompleteRequest` / `runAutocompleteRequest` | goroutine chain over `autoReqTask` channel; `context.Context` for abort |
| `autocompleteStartToken` / `autocompleteRequestId` | counters to drop stale results (`isAutocompleteRequestCurrent` checks token + id + snapshot text/cursor unchanged) |
| `applyAutocompleteSuggestions` | build SelectList, set best-match index (exact > prefix) |
| `getBestAutocompleteMatchIndex` | same |
| `handleTabCompletion` / `handleSlashCommandCompletion` / `forceFileAutocomplete` | same |
| `setAutocompleteTriggerCharacters` | same (default `["@","#"]`, merge provider triggers, reject `/`/whitespace) |
| `cancelAutocomplete` / `cancelAutocompleteRequest` / `clearAutocompleteUi` / `updateAutocomplete` | same |

**AbortController → `context.WithCancel`**; `setTimeout` → `time.Timer`; the promise
chain → a serialized goroutine (only one in-flight request; queue the next).

## `render`

- `paddingX` clamped to `floor((width-1)/2)`; `contentWidth`, `layoutWidth`
  (reserve 1 col for cursor when no padding).
- `layoutText(layoutWidth)`; `maxVisibleLines = max(5, floor(rows*0.3))`;
  adjust `scrollOffset` to keep cursor visible; clamp.
- Top border `─`×width (or `─── ↑ N more ──…` when scrolled).
- Each visible layout line: render, insert reverse-video cursor grapheme (or
  highlighted space at EOL) preceded by `CursorMarker` when focused; pad.
- Bottom border (or `─── ↓ N more ──…`).
- Autocomplete list rendered below if active (padded).

## `isInSlashCommandContext` / `isAtStartOfMessage` / `isSlashMenuAllowed`
Slash menu only on line 0; `@`/`#` trigger at token boundaries
(`autocompleteTriggerPattern`/`autocompleteDebouncePattern` regexes).

## Tests to port
- **`editor.test.ts` (4051)** — the largest test file. Covers: text mutation
  (single/multi-line insert/paste/newline), cursor movement (grapheme, word, line,
  **vertical sticky-column across wraps**, atomic-segment snapping), deletion +
  kill-ring (multi-line yank/yank-pop), undo (coalescing, atomic paste), history
  (up/down, draft restore), char-jump, autocomplete (slash/@/Tab, debounce, stale
  request drop, single-result auto-apply), large-paste markers + expansion, render
  (scroll borders, cursor positioning), backslash-Enter workaround. Port block by
  block; it is the authoritative spec.
- `viewport-overwrite-repro.ts` — renderer regression repro (also relevant to engine).
