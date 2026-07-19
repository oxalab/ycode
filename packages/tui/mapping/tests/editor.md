# Test inventory — `editor.test.ts`

- **Source:** [`test/editor.test.ts`](../../../../reference/tui/test/editor.test.ts) (4051 LOC, ~85 cases — the **largest** test file)
- **Verifies:** [`src/components/editor.ts`](../../../../reference/tui/src/components/editor.ts) → [components/21-editor.md](../components/21-editor.md)
- **Harness:** `Editor` over `createTestTUI()` (VirtualTerminal); `defaultEditorTheme`; drives via `handleInput(rawBytes)`; asserts `getText()`, `getCursor()`, `isShowingAutocomplete()`, and `render(width)` line widths. Mock `AutocompleteProvider` + `flushAutocomplete()` (`Promise.resolve()` + `setImmediate`). Exports-tested helper `wordWrapLine`.
- **Go target:** `components/editor_test.go` — port block by block (the doc groups below map to Go sub-tests). Raw control bytes (`\x17`=Ctrl+W, `\x15`=Ctrl+U, `\x0b`=Ctrl+K, `\x19`=Ctrl+Y, `\x1by`=Alt+Y, `\x01`=Ctrl+A, `\x05`=Ctrl+E, `\x1b[A/B/C/D`=arrows, `\x1b[1;5C/D`=Ctrl+arrows, `\x1b[3~`=Delete, `\x7f`=Backspace, `\x1b[45;5u`=Ctrl+- undo, `\x1d`=Ctrl+], `\x1b\x1d`=Ctrl+Alt+]).

## Test groups (what each pins)

### Prompt history navigation (16)
- empty history no-op; most-recent on Up; cycles; **jumps to line start before entering history from non-empty draft** (cursor→{0,0}); forward nav with Down restoring draft; exits on typing / `setText`; **empty + whitespace-only not added**; **consecutive dups dropped** (non-consecutive allowed); uses cursor movement (not history) when editor has multiline content; **100-entry cap**; **cursor at start after browsing up, at end after browsing down**; opposite-direction movement within multiline entry.

### Public state accessors (2)
- `getCursor()` after typing/move; `getLines()` returns a **defensive copy** (mutation doesn't affect editor).

### Backslash+Enter newline workaround (5)
- `\` inserted immediately (no buffering); `\` + Enter → newline; `\` + char → literal; Enter submits when `\` not at cursor; only the last `\` removed when multiple.

### Kitty CSI-u handling (3)
- unsupported-modifier CSI-u ignored; shifted CSI-u letter inserted as text; shifted modifyOtherKeys letter inserted as text. (Pins `decodePrintableKey`.)

### Unicode editing (8)
- mixed ASCII/umlauts/emoji insert; backspace deletes one grapheme (umlaut, emoji); insertion position correct after moving over umlauts; arrow moves one grapheme over emoji; umlauts across line breaks; `setText` unicode round-trip; Ctrl+A + insert at start; **Ctrl+W word deletion** (basic, trailing-ws, punctuation run, ASCII-punct-in-word boundary `foo.bar`/`foo:bar`, multi-line, empty-line merge, emoji-as-word, Alt+Backspace); **Ctrl+Left/Right word movement** (exact cursor cols, punctuation, leading-whitespace, `foo.bar` boundaries); **fullwidth CJK punctuation stops** (#4972); **mixed CJK+ASCII word movement** (exact cols).

### Grapheme-aware wrapping (7)
- wide-emoji width; long emoji wrap; **Thai/Lao AM clusters no width drift**; CJK (2-col) wrap with exact content split; mixed ASCII+wide; cursor on wide char (reverse video); **no overflow at emoji wrap boundary**; cursor-at-end then wrap-on-next-char (for paddingX 0 and 1).

### Word wrapping — `wordWrapLine` direct (16)
- word-boundary (not mid-word); no leading whitespace after wrap; **long word (URL) char-break**; multiple spaces preserved; empty string; exact-fit single word; **trailing-space-at-boundary cases** (whitespace kept/split rules — many exact-output cases); **force-break when wide char after word-boundary still overflows**; **oversized atomic segment** (paste marker) split across chunks / at start / at end / consecutive / normal-wrap-resumes-after. These pin `wordWrapLine`'s backtrack + force-break + atomic-re-wrap logic precisely.

### Kill ring (17)
- Ctrl+W→Ctrl+Y; Ctrl+U/Ctrl+K save; empty-ring no-op; **Alt+Y cycling**; no-op if not after yank / ≤1 entry; **consecutive Ctrl+W accumulates** (prepended); **multiline Ctrl+U accumulates incl newlines**; backward-prepend/forward-append accumulation; non-delete breaks accumulation; non-yank breaks Alt+Y chain; **rotation persists**; cross-line coalescing; Ctrl+K at line-end deletes newline + coalesces; yank/yank-pop **in the middle** of text; **multiline yank/yank-pop**; Alt+D forward word delete; Alt+D at line-end deletes newline.

### Undo (18)
- empty-stack no-op; **word-char coalescing**; spaces one-at-a-time; newline + next-word state capture; backspace/forward-delete/Ctrl+W/Ctrl+K/Ctrl+U/yank undo; **paste atomic** (single + multiline); `insertTextAtCursor` atomic + **multiline + CRLF/CR normalization**; `setText("")` undoable; **submit clears stack**; **exits history browsing on undo** (restores pre-browse, then prior); multiple history navigations; **cursor movement starts new unit**; **no-op deletes don't push**; **undoes autocomplete**; **paste doesn't trigger autocomplete**; **decodes CSI-u Ctrl+letter inside bracketed paste** (tmux #3599).

### Autocomplete (14)
- **single force-file suggestion auto-applied without menu**; multiple → menu; **force-mode (Tab) keeps suggestions open while typing**; **`@` debounced** while typing (50ms); **re-queries picker when cursor moves back into command name** (#5496 regression — stale arg menu must not survive); **`#` debounced**; **custom `triggerCharacters`** (`$`) debounced; **resets trigger chars on provider change**; **aborts active `@` request when typing continues** (`signal.abort`); hides on backspace-to-empty; **Enter applies exact typed slash-arg value** even when first item highlighted; **first prefix-match** when typed arg not exact; **unique prefix highlight**; **first of multiple prefix matches**; `/model` exact-match retained; **awaits async arg completions**; **ignores invalid (non-array) arg results**; no-arg-completer commands → no arg suggestions.

### Character jump — Ctrl+] (12)
- forward first occurrence / next-after-cursor / **across multiple lines**; backward first-before / across lines; no-op when not found (fwd/bwd); **case-sensitive**; **cancel on repeat trigger / on Escape**; special chars; empty text; backward cancel; **resets lastAction** (so post-jump typing is a new undo unit).

### Sticky column (17)
- preserved moving up/down through **shorter/empty lines** (clamp to 0, restore on exit); **reset on** left/right arrow / typing / backspace / Ctrl+A / Ctrl+E / Ctrl+Left / Ctrl+Right / undo; multiple consecutive up/down; **traverse wrapped visual lines without getting stuck**; `setText` resets; **`preferredVisualCol` set when pressing Right at end of last line**; **resize handling when preferred col on same line / different line**; **rewrapped lines** decision-table cases (target fits current visual col / target shorter). These pin `computeVerticalMoveColumn` + `moveToVisualLine` exactly.

### Paste marker atomic behavior (16)
- marker created for large pastes; **atomic for right/left arrow, backspace, forward delete, word movement**; undo restores marker; multiple markers in one line; **manually-typed marker-like text NOT atomic** (no valid paste ID); **no crash when marker wider than terminal** (split); **no crash when text+marker exceeds width with cursor on marker**; **`wordWrapLine` re-checks overflow after backtracking** (force-break twice); **`getExpandedText` expands literally** (incl. regex-special `$` tokens); **snap to marker start when navigating down into it**; sticky preserved through marker line; **not stuck moving down from multi-visual-line marker**; **skip marker-continuation VLs** when preferred col falls in marker tail; **submit expands literal content**.

## Port notes
- This file is the **authoritative spec** for the editor. Port each group as a Go sub-test; the cursor-coordinate assertions (`getCursor()` exact `{line,col}`) are unforgiving — `computeVerticalMoveColumn`'s P/S/T/U decision table and `moveToVisualLine`'s atomic-snap logic must match to the column.
- Async autocomplete cases use `flushAutocomplete()` (microtask + `setImmediate`); Go equivalent: after triggering, run the editor's pending request to completion (call the private flush, or expose a test hook) — the debounce (20ms) cases need a fake clock or a controllable timer.
- The resize cases call `editor.render(narrower)` then move — Go `Render` must update `lastWidth` the same way (it does in TS).
- Mock providers implement `AutocompleteProvider` with a `getSuggestions` closure over `lines/cursorCol` — port as a tiny Go interface mock returning `(suggestions, ok)`.
