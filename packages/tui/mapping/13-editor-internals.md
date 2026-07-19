# 13 — Editor internals (kill-ring, undo-stack, native-modifiers)

Three small modules that back the `Input`/`Editor` editing model.

---

## 13a. KillRing (Emacs kill/yank ring)

- **TS source:** [`kill-ring.ts`](../../reference/tui/src/kill-ring.ts) (46 LOC)
- **Go target:** `killring.go`
- **Package:** `tui`

| TS | Go |
|---|---|
| `class KillRing` | `type KillRing struct{ ring []string }` |
| `push(text, {prepend, accumulate?})` | `Push(text string, prepend, accumulate bool)` |
| `peek()` | `Peek() (string, bool)` |
| `rotate()` | `Rotate()` (move last→front for yank-pop) |
| `get length` | `Len() int` |

**Semantics:** `accumulate=true` merges into the most-recent entry — `prepend=true`
for backward deletion (Ctrl+W/Ctrl+U), `prepend=false` for forward (Ctrl+K/Alt+D).
This is how consecutive deletes coalesce into one yankable unit. Full behavior is
pinned by `input.test.ts` "Kill ring" suite.

## 13b. UndoStack (clone-on push)

- **TS source:** [`undo-stack.ts`](../../reference/tui/src/undo-stack.ts) (28 LOC)
- **Go target:** `undostack.go`
- **Package:** `tui`

| TS | Go |
|---|---|
| `class UndoStack<S>` (generic) | `type UndoStack[S any] struct{ stack []S }` (Go generics) |
| `push(state)` (uses `structuredClone`) | `Push(state S)` — **caller passes a deep copy**, or Push copies |
| `pop()` | `Pop() (S, bool)` |
| `clear()` | `Clear()` |
| `get length` | `Len() int` |

**Deep-copy note:** TS uses `structuredClone`. Go has no built-in generic deep copy.
Two options:
- Editor/Input state is a small value type (lines `[]string` + cursor ints). Define a
  `Clone()` method on the state struct and have `Push` call it. Cleanest.
- Or have callers pass an already-copied snapshot.

**Coalescing:** the undo-coalescing logic ("type-word", "kill", "yank" last-action
tracking) lives in `Input`/`Editor`, not here — `UndoStack` is just the stack.

## 13c. native-modifiers (darwin-only)

- **TS source:** [`native-modifiers.ts`](../../reference/tui/src/native-modifiers.ts) (59 LOC)
- **Go target:** `nativemodifiers_darwin.go` (build-tagged) + `nativemodifiers_other.go`
- **Package:** `tui`

| TS | Go |
|---|---|
| `type ModifierKey` (`shift/command/control/option`) | `type ModifierKey string` |
| `isNativeModifierPressed(key)` | `IsNativeModifierPressed(key ModifierKey) bool` |

**Behavior:** macOS-only (`process.platform === "darwin"`, arch x64/arm64). Loads the
native addon `darwin-modifiers.node` from several candidate paths; calls
`isModifierPressed(name)` which polls `CGEventSourceFlagsState`. Returns false on any
other platform or load failure. See [14-platform-native.md](14-platform-native.md)
for the CGo/CoreGraphics port. The `_other.go` stub returns `false` unconditionally.

Used by `terminal.ts` `forwardInputSequence` to disambiguate Apple-Terminal
shift-enter (`\r` → shift+enter when native shift is held).

## Tests to port
- KillRing + UndoStack behavior is fully covered by `input.test.ts` (kill-ring
  suite: accumulate prepend/append, rotate, yank-pop cycling, coalescing) and
  `editor.test.ts` (undo coalescing). No standalone test files exist for these two.
- native-modifiers: no standalone tests (platform-specific).
