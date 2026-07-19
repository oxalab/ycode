# Components 20 — Input (single-line)

- **TS source:** [`input.ts`](../../../reference/tui/src/components/input.ts) (447 LOC)
- **Go target:** `components/input.go`
- **Package:** `components`
- **Implements:** `tui.Component`, `tui.Focusable`, `tui.InputHandler`.
- **Depends on:** `tui` (`Keybindings`, `CursorMarker`, `IsFocusable`, grapheme
  segmenter, `VisibleWidth`, `SliceByColumn`, `IsWhitespaceChar`), `tui.KillRing`,
  `tui.UndoStack`, `tui.DecodeKittyPrintable`, `wordnav.FindWord*`.

Single-line text input with horizontal scrolling, Emacs kill/yank, undo, IME cursor.

---

## Struct

```go
type Input struct {
    value      string
    cursor     int
    focused    bool

    onSubmit  func(string)
    onEscape  func()

    // bracketed paste buffering
    pasteBuffer string
    inPaste     bool

    killRing   *tui.KillRing
    lastAction lastActionKind  // "kill"|"yank"|"type-word"|none

    undoStack  *tui.UndoStack[InputState]
}
type InputState struct{ Value string; Cursor int }
```

| TS | Go |
|---|---|
| `class Input implements Component, Focusable` | `type Input struct{...}` |
| `getValue/setValue` | `GetValue() string` / `SetValue(string)` (clamp cursor) |
| `onSubmit?/onEscape?` | `OnSubmit/OnEscape func(string)` (OnEscape is `func()`) |
| `focused` | `Focused() bool` / `SetFocused(bool)` |
| `handleInput(data)` | `HandleInput(data)` |
| `invalidate()` (no-op) | `Invalidate()` |
| `render(width)` | `Render(width)` |

## `handleInput` dispatch order (port exactly)

1. **Bracketed paste** (`\x1b[200~`…`\x1b[201~`): buffer chunks, on end →
   `handlePaste` (strips `\r\n`/`\t`→4 spaces), recurse for trailing input.
2. `tui.select.cancel` → `OnEscape`.
3. `tui.editor.undo` → `undo`.
4. `tui.input.submit` or `\n` → `OnSubmit(value)`.
5. Deletion: `deleteCharBackward/Forward`, `deleteWordBackward/Forward`,
   `deleteToLineStart/End`.
6. Kill-ring: `yank`, `yankPop`.
7. Cursor: `cursorLeft/Right/LineStart/LineEnd/WordLeft/WordRight`.
8. `DecodeKittyPrintable(data)` → `insertCharacter` (before the control-char
   check, since CSI-u contains ESC).
9. Printable char (no C0/DEL/C1 control bytes) → `insertCharacter`.

## Editing methods (port 1:1, all grapheme-aware)

| TS | Go | Notes |
|---|---|---|
| `insertCharacter(char)` | `insertCharacter(char)` | undo coalescing: word-chars coalesce, whitespace/first-char pushes undo; sets `lastAction="type-word"` |
| `handleBackspace/handleForwardDelete` | same | delete one grapheme |
| `deleteToLineStart/deleteToLineEnd` | same | push to kill ring (prepend/append, accumulate if lastAction=="kill") |
| `deleteWordBackwards/deleteWordForward` | same | save lastAction before `moveWord*` (which resets it) |
| `yank/yankPop` | same | yank = peek+insert; yankPop only after yank with >1 entry: delete prev yank, rotate, insert |
| `moveWordBackwards/moveWordForwards` | same | `wordnav.FindWord*` |
| `handlePaste(text)` | same | strip newlines, tabs→4 spaces, insert |
| `pushUndo/undo` | same | snapshot `{value,cursor}`; pop restores |

## `render` — horizontal scroll + fake cursor

- Prompt `"> "`, `availableWidth = width - 2`.
- If value fits → show all. Else compute scroll window centered on cursor (half-width
  logic, reserve 1 col when cursor at end); `SliceByColumn` for the visible slice;
  `cursorDisplay` = length of before-cursor slice.
- Build line: `beforeCursor + (CursorMarker if focused) + reverseVideo(atCursor) +
  afterCursor`; pad to `availableWidth`; prepend prompt.
- Returns `[line]`.

## Tests to port
- **`input.test.ts` (647)** — backslash-on-Enter (single-line, no workaround),
  CJK/fullwidth no-overflow render, horizontal-scroll cursor visibility, **full
  kill-ring suite** (Ctrl+W/U/K, Alt+D, accumulate prepend/append, rotate, yank-pop
  cycling, coalescing break-on-type), **undo suite** (coalescing, per-space undo,
  backspace/delete/word/yank/paste undo, movement breaks coalescing). Port as
  sub-tests feeding raw control bytes (`\x17`=Ctrl+W, `\x1b[45;5u`=Ctrl+-).
