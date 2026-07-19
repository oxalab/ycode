# 02 — TUI engine (renderer + overlays + focus)

The heart of the framework. **No library implements this** — it must be ported
directly. ~1100 LOC of the `TUI` class (lines 293–1714 of `tui.ts`).

- **TS source:** [`tui.ts`](../../reference/tui/src/tui.ts) (`class TUI extends Container`)
- **Go target:** `engine.go`
- **Package:** `tui`

---

## `TUI` struct → Go

```go
type TUI struct {
    *Container                       // embedded: children
    Terminal Terminal

    previousLines         []string
    previousKittyImageIDs map[int]struct{}
    previousWidth, previousHeight int
    previousViewportTop   int

    focusedComponent  Component
    inputListeners    []InputListener   // func(string) InputListenerResult

    onDebug func()                       // Shift+Ctrl+D

    // throttle
    renderRequested bool
    renderTimer     *time.Timer
    lastRenderAt    time.Time

    cursorRow         int   // logical (end of content)
    hardwareCursorRow int   // actual terminal cursor (IME positioning)
    showHardwareCursor bool
    clearOnShrink       bool
    maxLinesRendered    int

    stopped bool

    // OSC 11 background query queue
    pendingOsc11Replies int
    pendingOsc11Queries []*osc11Query
    colorSchemeListeners []func(TerminalColorScheme)
    colorSchemeNotifications bool

    // overlay stack
    focusOrderCounter int
    overlayStack      []*overlayEntry
    overlayFocusRestore overlayFocusRestoreState
}
```

| TS | Go |
|---|---|
| `new TUI(terminal, showHardwareCursor?)` | `NewTUI(term Terminal, opts ...Option) *TUI` |
| `start()` / `stop()` | `Start()` / `Stop()` |
| `requestRender(force=false)` | `RequestRender(force ...bool)` |
| `setFocus(c)` | `SetFocus(c Component)` |
| `addInputListener(fn)` returns unsubscribe | `AddInputListener(fn) (cancel func())` |
| `onDebug` field | `OnDebug func()` |
| `fullRedraws` getter | `FullRedraws() int` |
| `getShowHardwareCursor/setShowHardwareCursor` | `ShowHardwareCursor()/SetShowHardwareCursor(bool)` |
| `getClearOnShrink/setClearOnShrink` | `ClearOnShrink()/SetClearOnShrink(bool)` |
| `showOverlay(c, opts)` → handle | `ShowOverlay(c Component, opts *OverlayOptions) *OverlayHandle` |
| `hideOverlay()` | `HideOverlay()` |
| `hasOverlay()` | `HasOverlay() bool` |
| `onTerminalColorSchemeChange(fn)` | `OnTerminalColorSchemeChange(fn) (cancel func())` |
| `setTerminalColorSchemeNotifications(bool)` | `SetTerminalColorSchemeNotifications(bool)` |
| `queryTerminalBackgroundColor({timeoutMs})` | `QueryTerminalBackgroundColor(timeoutMs int) (RgbColor, bool)` |
| `queryTerminalColorScheme({timeoutMs})` | `QueryTerminalColorScheme(timeoutMs int) (TerminalColorScheme, bool)` |

**Throttle / async:** TS uses `process.nextTick` + `setTimeout` with
`MIN_RENDER_INTERVAL_MS = 16`. Go: a render-request channel + a goroutine, or a
`time.Timer` reset pattern. Force-render flushes synchronously (re-uses the same
`nextTick` path). `process.env.PI_*` flags → Go flags/env read at init.

## Overlay types → Go

```go
type OverlayAnchor string // center | top-left | ... | right-center (9 values)
type SizeValue interface{} // int (absolute) or PercentValue{N} → parse via parseSizeValue

type OverlayMargin struct{ Top, Right, Bottom, Left int }
type OverlayOptions struct {
    Width, MaxHeight SizeValue
    MinWidth int
    Anchor OverlayAnchor
    OffsetX, OffsetY int
    Row, Col SizeValue
    Margin any // int or OverlayMargin
    Visible func(termW, termH int) bool
    NonCapturing bool
}
type OverlayUnfocusOptions struct{ Target Component } // nil = leave focus empty
type OverlayHandle struct{ ... }  // opaque; methods below
```

| TS `OverlayHandle` | Go |
|---|---|
| `hide()` | `Hide()` |
| `setHidden(bool)` / `isHidden()` | `SetHidden(bool)` / `IsHidden() bool` |
| `focus()` / `unfocus(opts?)` / `isFocused()` | `Focus()` / `Unfocus(opts ...*OverlayUnfocusOptions)` / `IsFocused() bool` |

`SizeValue` (TS `number | "${number}%"`) — Go: define `type SizeValue any` with a
parser; literals become `int`, percentages a small `Percent float64`-wrapping type.
Cleaner: `type SizeValue struct{ Abs int; Percent float64; IsPercent bool }` with a
constructor per kind.

## `doRender()` — the 3-strategy differential renderer

**This is the single hardest unit to port.** Port the control flow verbatim:

1. Render all children → `newLines`; composite overlays (`compositeOverlays`).
2. Extract cursor position (`extractCursorPosition` — scans for `CursorMarker`).
3. Apply per-line SGR/OSC8 reset (`applyLineResets` → `normalizeTerminalOutput + SEGMENT_RESET`).
4. **Strategy select:**
   - first render (`previousLines` empty, no resize) → `fullRender(false)`
   - width changed → `fullRender(true)` (clear screen + scrollback)
   - height changed (non-Termux) → `fullRender(true)`
   - `clearOnShrink` && content shrunk && no overlays → `fullRender(true)`
   - else: find `firstChanged`/`lastChanged`, expand for Kitty images
     (`expandChangedRangeForKittyImages`), move cursor to `firstChanged`,
     clear-to-end, rewrite changed lines; handle appended/deleted-line bookkeeping.
5. Position hardware cursor for IME (`positionHardwareCursor`).
6. Update `previousLines`/width/height/`maxLinesRendered`/`previousViewportTop`.

All writes wrapped in `\x1b[?2026h` … `\x1b[?2026l` (synchronized output).

**Helpers to port (all private):**

| TS method | Go func | Purpose |
|---|---|---|
| `resolveOverlayLayout` | `resolveOverlayLayout` | compute width/row/col/maxHeight |
| `resolveAnchorRow/Col` | `resolveAnchorRow/Col` | anchor → coordinate |
| `compositeOverlays` | `compositeOverlays` | splice overlays into content lines |
| `compositeLineAt` | `compositeLineAt` | splice one overlay line at column |
| `applyLineResets` | `applyLineResets` | append SGR+OSC8 reset per line |
| `collectKittyImageIds` | `collectKittyImageIDs` | scan lines for Kitty IDs |
| `deleteKittyImages` | `deleteKittyImages` | emit `deleteKittyImage` per id |
| `getKittyImageReservedRows` | `getKittyImageReservedRows` | rows occupied by multi-row image |
| `expandChangedRangeForKittyImages` | `expandChangedRangeForKittyImages` | widen diff range to image blocks |
| `deleteChangedKittyImages` | `deleteChangedKittyImages` | free images on overwritten lines |
| `extractCursorPosition` | `extractCursorPosition` | find+strip `CursorMarker` |
| `positionHardwareCursor` | `positionHardwareCursor` | move/flip hardware cursor for IME |
| `doRender` | `doRender` | orchestrator |
| `parseKittyImageHeader`, `extractKittyImageIds/Rows` | same | parse `\x1b_G…;…\x1b\\` headers |
| `isTermuxSession`, `parseSizeValue` | same | env helpers |

These rely on `utils.ts` `extractSegments`, `sliceByColumn`, `sliceWithWidth`,
`visibleWidth`, `normalizeTerminalOutput`, and `terminal-image.ts`
`deleteKittyImage`/`isImageLine`/`getCapabilities`/`setCellDimensions` — port those
first (see [06-utils.md](06-utils.md), [10-terminal-image.md](10-terminal-image.md)).

## Focus-restore state machine

`setFocusInternal` + the `OverlayFocusRestoreState` sum type (inactive / eligible /
blocked{blockedBy, resume}). ~150 LOC of overlay focus plumbing. Port as a small
state struct + methods; behavior is deterministic and testable via the overlay test
suite.

| TS private method | Go func |
|---|---|
| `setFocusInternal`, `clearOverlayFocusRestore`, `clearOverlayFocusRestoreFor` | same |
| `resolveBlockedOverlayFocusResume`, `getVisibleOverlayFocusRestore` | same |
| `isOverlayFocusAncestor`, `retargetOverlayPreFocus` | same |
| `isComponentMounted`, `containsComponent` | same |
| `isOverlayVisible`, `getTopmostVisibleOverlay` | same |

## OSC 11 / 996 queries

- `queryTerminalBackgroundColor`: writes `\x1b]11;?\x07`, queues a promise resolved
  by `consumeOsc11BackgroundResponse` (called from `handleInput` **before** input
  listeners/components — important ordering). Timeout → resolve `undefined`.
- `queryTerminalColorScheme`: writes `\x1b[?996n`, resolves via
  `consumeTerminalColorSchemeReport` listener.

Go: `context.Context`-bounded; resolve via a channel fed by the input loop.
`consumeCellSizeResponse` (CSI `6;H;Wt`) feeds `setCellDimensions` from termimage.

## `handleInput` dispatch order (must preserve)

1. `consumeOsc11BackgroundResponse` (if pending)
2. `consumeTerminalColorSchemeReport`
3. run `inputListeners` (each may `{consume}` or rewrite `data`)
4. `consumeCellSizeResponse`
5. Shift+Ctrl+D debug
6. focused-overlay visibility re-check / focus restore
7. deliver to focused component's `HandleInput` (skip if key-release and not
   `WantsKeyRelease`)

## Tests to port
- `tui-render.test.ts` (767) — renderer diff strategies
- `overlay-non-capturing.test.ts` (1202), `overlay-options.test.ts` (541),
  `overlay-short-content.test.ts`, `tui-overlay-style-leak.test.ts`,
  `regression-overlay-cjk-boundary.test.ts`, `tui-shrink.test.ts`,
  `tui-cell-size-input.test.ts` — overlay/focus/IME
- `terminal-colors.test.ts` — OSC 11/996 query lifecycle
- `viewport-overwrite-repro.ts` — regression repro
