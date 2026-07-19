# 03 — Terminal (interface + ProcessTerminal)

- **TS source:** [`terminal.ts`](../../reference/tui/src/terminal.ts) (531 LOC)
- **Go target:** `terminal.go`
- **Package:** `tui`

The `Terminal` abstraction decouples the engine from stdio (enables `VirtualTerminal`
for tests and the Go `tea.Program` for real use).

---

## `Terminal` interface

```go
type Terminal interface {
    Start(onInput func(data string), onResize func()) error
    Stop() error
    DrainInput(maxMs, idleMs int) error
    Write(data string)
    Columns() int
    Rows() int
    KittyProtocolActive() bool
    MoveBy(lines int)
    HideCursor()
    ShowCursor()
    ClearLine()
    ClearFromCursor()
    ClearScreen()
    SetTitle(title string)
    SetProgress(active bool)
}
```

| TS `Terminal` | Go |
|---|---|
| `start(onInput, onResize)` | `Start(onInput func(string), onResize func()) error` |
| `stop()` | `Stop() error` |
| `drainInput(maxMs?, idleMs?)` | `DrainInput(maxMs, idleMs int) error` |
| `write(data)` | `Write(data string)` |
| `get columns/rows` | `Columns()/Rows() int` |
| `get kittyProtocolActive` | `KittyProtocolActive() bool` |
| `moveBy(lines)` | `MoveBy(lines int)` |
| `hideCursor/showCursor` | `HideCursor()/ShowCursor()` |
| `clearLine/clearFromCursor/clearScreen` | same |
| `setTitle(title)` | `SetTitle(title)` |
| `setProgress(active)` | `SetProgress(active bool)` |

**Debug logging:** TS honors `PI_TUI_WRITE_LOG` (appends raw ANSI to a file).
Keep as an env-driven optional logger in Go.

## `ProcessTerminal`

Wraps real stdio. In TS it's `process.stdin/stdout` + Node APIs. **In Go we have two
choices** (decide in Step 3):

- **(Recommended) Back a `*tea.Program` (Bubble Tea) for the low-level raw mode,
  resize, and Kitty/modifyOtherKeys negotiation.** Bubble Tea already does: raw mode,
  `ENABLE_VIRTUAL_TERMINAL_INPUT` on Windows, bracketed paste, Kitty keyboard
  protocol detection, resize events, mouse, alt-screen. We implement `Terminal` as an
  adapter that translates `tea.KeyMsg`/`tea.MouseMsg`/window-size into the
  `onInput(data string)` / `onResize()` callbacks the engine expects.
- **(Lower-level) Drive `os.Stdin`/`os.Stdout` directly** via `golang.org/x/term`
  (`MakeRaw`/`GetState`/`Restore`) + `golang.org/x/sys/windows` for VT input. More
  faithful to the TS, more code, re-implements what Bubble Tea gives free.

Either way, the **engine only ever sees the `Terminal` interface**, so this choice is
isolated to `terminal.go`.

### Responsibilities to port regardless of adapter

| TS behavior | Go |
|---|---|
| Save/restore raw mode on start/stop | adapter handles (tea) or `x/term` |
| Enable bracketed paste `\x1b[?2004h` on start, disable on stop | write the escapes in Start/Stop |
| `StdinBuffer` splits batched input → individual sequences | see [05-stdin-buffer.md](05-stdin-buffer.md) |
| Kitty keyboard negotiation (flags 7 → query; enable if response≠0; else modifyOtherKeys fallback) | **tea handles** if using adapter; otherwise port `queryAndEnableKittyProtocol` + the `keyboardProtocolNegotiationBuffer` reassembly |
| `drainInput` — read until idle/timeout to flush late key releases before exit | port; needed to avoid leaking Kitty release seqs to parent shell over SSH |
| `setProgress` OSC 9;4 keepalive interval | `time.Ticker` every 1000ms |
| `setTitle` OSC 0 | trivial |
| Apple-Terminal shift-enter normalization (`normalizeAppleTerminalInput`) via `isNativeModifierPressed("shift")` | darwin-only; see [14-platform-native.md](14-platform-native.md) |

### Kitty negotiation detail (if porting directly)
- Query: `\x1b[>7u\x1b[?u\x1b[c` (push flags 7, query, Device-Attributes sentinel).
- Flags: 1=disambiguate, 2=report event types, 4=report alternate keys.
- `parseKeyboardProtocolNegotiationSequence` recognizes `\x1b[?Nu` (kitty-flags) and
  `\x1b[?..c` (device-attributes); buffers split responses with a 150ms fragment
  timeout (`KEYBOARD_PROTOCOL_RESPONSE_FRAGMENT_TIMEOUT_MS`).
- Non-zero kitty-flags → `_kittyProtocolActive=true`, call `SetKittyProtocolActive(true)`
  (in keys.go). Zero flags or DA-only → `enableModifyOtherKeys` (`\x1b[>4;2m`).
- `Stop()`/`drainInput()` send `\x1b[<u` (pop) + `\x1b[>4;0m` (disable modOtherKeys).

## Helpers / constants

| TS | Go |
|---|---|
| `isAppleTerminalSession()` | `IsAppleTerminalSession() bool` |
| `normalizeAppleTerminalInput(data, isApple, shiftPressed)` | same |
| `parseKeyboardProtocolNegotiationSequence(seq)` | exported for tests |
| constants: `TERMINAL_PROGRESS_*`, `APPLE_TERMINAL_SHIFT_ENTER_SEQUENCE`, `DESIRED_KITTY_KEYBOARD_PROTOCOL_FLAGS=7` | `const` |

## Tests to port
- `terminal.test.ts` (233) — though much of it exercises Node stdio specifics;
  adapt to the chosen Go adapter. The negotiation-sequence parser is pure and ports
  directly.
- `drainInput` race behavior is hard to unit-test; cover with an integration test.
