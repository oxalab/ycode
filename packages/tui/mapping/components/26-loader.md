# Components 26 — Loader + CancellableLoader

- **TS source:** [`loader.ts`](../../../reference/tui/src/components/loader.ts) (92) +
  [`cancellable-loader.ts`](../../../reference/tui/src/components/cancellable-loader.ts) (40)
- **Go target:** `components/loader.go` + `components/cancellableloader.go`
- **Package:** `components`

## Loader — animated spinner extending `Text`

| TS | Go |
|---|---|
| `class Loader extends Text` | `type Loader struct{ *Text; tui *tui.TUI; ... }` (embed Text) |
| `interface LoaderIndicatorOptions { frames?, intervalMs? }` | `type LoaderIndicatorOptions struct{ Frames []string; IntervalMs int }` |
| `new Loader(tui, spinnerColorFn, messageColorFn, message="Loading...", indicator?)` | `NewLoader(t *tui.TUI, spinner, message StyleFunc, msg string, ind ...LoaderIndicatorOptions)` |
| `start()` / `stop()` | `Start()` / `Stop()` |
| `setMessage(m)` | `SetMessage(m string)` |
| `setIndicator(opts?)` | `SetIndicator(...)` |
| `render(width)` prepends a blank line | `Render(width)` |

**Defaults:** frames = 10 braille dots `["⠋","⠙",...]`, intervalMs = 80.
**Animation:** `setInterval` advances frame, calls `updateDisplay`, then
`tui.requestRender()`. Go: `time.Ticker` in a goroutine, stopped by `Stop()`.
`renderIndicatorVerbatim` flag — if true, emit frame unstyled (for custom indicators).
`Render` returns `["", ...super.Render(width)]` (blank line first).

## CancellableLoader — Escape-to-cancel with AbortSignal

| TS | Go |
|---|---|
| `class CancellableLoader extends Loader` | `type CancellableLoader struct{ *Loader; cancel context.CancelFunc; ctx context.Context; OnAbort func() }` |
| `onAbort?` | `OnAbort func()` |
| `get signal` (`AbortSignal`) | `Context() context.Context` + internal cancel |
| `get aborted` | `Aborted() bool` |
| `handleInput(data)` | `HandleInput(data string)` |
| `dispose()` | `Dispose()` |

**Behavior:** on `tui.select.cancel` (Esc/Ctrl+C via `Keybindings.Matches`), abort
the context + call `OnAbort`. Go replaces `AbortController` with a
`context.WithCancel`; expose `Context()` for the async worker to select on. `Dispose`
stops the animation ticker.

## Async note
The `interval`/`signal` patterns are the only places pi-tui uses timers in a
component. Keep them goroutine-safe: the ticker goroutine must not touch the Text
fields concurrently with `Render` — either serialize via the engine's render loop (send
a "request render" + update fields under a mutex) or drive ticks through the engine's
input channel. Decide in Step 3; document the chosen approach in `loader.go`.

## Tests to port
- No standalone loader tests. Behavior (spinner frame advance, abort on Esc) covered
  by integration in the chat demo. Add focused unit tests for `SetIndicator` frame
  selection + `CancellableLoader` abort → `Context().Done()`.
