# 08 — Keybindings (registry + manager)

- **TS source:** [`keybindings.ts`](../../reference/tui/src/keybindings.ts) (244 LOC)
- **Go target:** `keybindings.go`
- **Package:** `tui`
- **Pure. Maps named actions → one-or-more `KeyID`s; resolves user overrides +
  detects conflicts.**

Components match input via `kb.matches(data, "tui.editor.cursorLeft")` instead of
hardcoded keys, enabling remapping.

---

## API

| TS | Go |
|---|---|
| `type Keybinding` (keyof `Keybindings`) | `type Keybinding string` (the `"tui.editor.*"` / `"tui.input.*"` / `"tui.select.*"` names) |
| `type KeybindingDefinition { defaultKeys, description? }` | `type KeybindingDefinition struct{ DefaultKeys []KeyID; Description string }` |
| `type KeybindingDefinitions = Record<string, KeybindingDefinition>` | `type KeybindingDefinitions map[Keybinding]KeybindingDefinition` |
| `type KeybindingsConfig = Record<string, KeyID \| KeyID[] \| undefined>` | `type KeybindingsConfig map[Keybinding]any` (KeyID or []KeyID) |
| `type KeybindingConflict { key, keybindings }` | `type KeybindingConflict struct{ Key KeyID; Keybindings []Keybinding }` |
| `const TUI_KEYBINDINGS` | `var TUIKeybindings KeybindingDefinitions` (see defaults below) |
| `class KeybindingsManager` | `type KeybindingsManager struct{...}` |
| `new KeybindingsManager(defs, userBindings?)` | `NewKeybindingsManager(defs, userBindings) *KeybindingsManager` |
| `matches(data, keybinding)` | `Matches(data string, kb Keybinding) bool` (delegates to `MatchesKey`) |
| `getKeys(kb)` | `GetKeys(kb) []KeyID` |
| `getDefinition(kb)` | `GetDefinition(kb) KeybindingDefinition` |
| `getConflicts()` | `GetConflicts() []KeybindingConflict` |
| `setUserBindings(cfg)` | `SetUserBindings(cfg)` (rebuild) |
| `getUserBindings()` / `getResolvedBindings()` | `GetUserBindings()` / `GetResolvedBindings()` |
| `setKeybindings(mgr)` / `getKeybindings()` | `SetKeybindings(*KeybindingsManager)` / `GetKeybindings()` (process-global default) |

## Default bindings — `TUI_KEYBINDINGS` (port verbatim)

```
tui.editor.cursorUp        up
tui.editor.cursorDown      down
tui.editor.cursorLeft      left, ctrl+b
tui.editor.cursorRight     right, ctrl+f
tui.editor.cursorWordLeft  alt+left, ctrl+left, alt+b
tui.editor.cursorWordRight alt+right, ctrl+right, alt+f
tui.editor.cursorLineStart home, ctrl+a
tui.editor.cursorLineEnd   end, ctrl+e
tui.editor.jumpForward     ctrl+]
tui.editor.jumpBackward    ctrl+alt+]
tui.editor.pageUp          pageUp
tui.editor.pageDown        pageDown
tui.editor.deleteCharBackward  backspace
tui.editor.deleteCharForward   delete, ctrl+d
tui.editor.deleteWordBackward  ctrl+w, alt+backspace
tui.editor.deleteWordForward   alt+d, alt+delete
tui.editor.deleteToLineStart   ctrl+u
tui.editor.deleteToLineEnd     ctrl+k
tui.editor.yank                ctrl+y
tui.editor.yankPop             alt+y
tui.editor.undo                ctrl+-
tui.input.newLine             shift+enter
tui.input.submit              enter
tui.input.tab                 tab
tui.input.copy                ctrl+c
tui.select.up                 up
tui.select.down               down
tui.select.pageUp             pageUp
tui.select.pageDown           pageDown
tui.select.confirm            enter
tui.select.cancel             escape, ctrl+c
```

## Behavior

- `rebuild()` builds `keysById` map (default keys unless user override), and scans
  user claims for the same `KeyID` bound to >1 action → `conflicts`.
- `normalizeKeys` dedupes + flattens `KeyID | KeyID[] | undefined`.
- Process-global default: `getKeybindings()` lazily builds a manager from
  `TUI_KEYBINDINGS` if none set. Components call `getKeybindings()` — so in Go this
  is a package-level `sync.Once`-initialized `*KeybindingsManager` (or set via
  `SetKeybindings`).

## Tests to port
- `keybindings.test.ts` (38) — resolution + conflict detection.
- Effective binding behavior is also exercised by `input.test.ts`/`editor.test.ts`
  (they send raw control bytes that resolve through these defaults).
