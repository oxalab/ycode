# Test-suite inventory — `reference/tui/test/`

Master index of every file under [`reference/tui/test/`](../../../../reference/tui/test):
what it verifies, its size, and where its porting notes live. There are **27 `*.test.ts`
suites + 6 helpers/demos/repros** (33 files, ~14k LOC).

> Each suite below is either documented in a dedicated file in this `tests/` directory
> (the large/behavior-dense ones) or inline in the corresponding module mapping doc's
> **"Tests to port"** section (the small pure-logic ones). This index points to both.

## Dedicated test-inventory docs (this directory)

| Test file | LOC | Verifies | Inventory doc |
|---|---|---|---|
| `editor.test.ts` | 4051 | editor component | [editor.md](editor.md) |
| `markdown.test.ts` | 1422 | markdown component | [markdown.md](markdown.md) |
| `overlay-non-capturing.test.ts` | 1202 | engine overlay **focus FSM** | [overlay-focus.md](overlay-focus.md) |
| `tui-render.test.ts` | 767 | engine renderer / resize / shrink / image cleanup | [engine-render.md](engine-render.md) |
| `overlay-options.test.ts` | 541 | engine overlay **layout math** | [overlay-layout.md](overlay-layout.md) |
| `terminal-image.test.ts` | 492 | image capability detection + encoders | [terminal-image.md](terminal-image.md) |
| `terminal.test.ts` | 233 | ProcessTerminal Kitty negotiation + Apple shift-enter | [terminal.md](terminal.md) |
| `bug-regression-isimageline-startswith-bug.test.ts` | 237 | `isImageLine` crash regression | [terminal-image.md](terminal-image.md) |
| `tui-overlay-style-leak.test.ts` | 80 | per-line SGR reset / overlay compositing leak | [engine-render.md](engine-render.md) |
| `tui-cell-size-input.test.ts` | 81 | `consumeCellSizeResponse` / `queryCellSize` | [engine-render.md](engine-render.md) |
| `tui-shrink.test.ts` | 44 | `clear()` + shrink clears rows | [engine-render.md](engine-render.md) |
| `overlay-short-content.test.ts` | 61 | overlay when content < terminal height | [engine-render.md](engine-render.md) |
| `regression-overlay-cjk-boundary.test.ts` | 68 | overlay starting inside a wide grapheme | [engine-render.md](engine-render.md) |
| `regression-regional-indicator-width.test.ts` | 52 | regional-indicator width = 2 | [utils-regressions.md](utils-regressions.md) |

## Suites documented inline (in their module mapping doc)

| Test file | LOC | Verifies | Module doc "Tests to port" |
|---|---|---|---|
| `keys.test.ts` | 614 | key matching (kitty/modOtherKeys/legacy) | [04-keys.md](../04-keys.md) |
| `stdin-buffer.test.ts` | 458 | sequence splitting + paste + kitty dedup | [05-stdin-buffer.md](../05-stdin-buffer.md) |
| `input.test.ts` | 647 | input component (kill-ring/undo/render) | [components/20-input.md](../components/20-input.md) |
| `autocomplete.test.ts` | 542 | CombinedAutocompleteProvider (`/cmd`, `@file`, paths) | [11-autocomplete.md](../11-autocomplete.md) |
| `terminal-colors.test.ts` | 249 | OSC 11 / color-scheme parse + query lifecycle | [09-terminal-colors.md](../09-terminal-colors.md) |
| `wrap-ansi.test.ts` | 246 | ANSI/CJK/OSC8 wrap preservation | [06-utils.md](../06-utils.md) |
| `word-navigation.test.ts` | 191 | findWordBackward/Forward + atomic segments | [07-word-navigation.md](../07-word-navigation.md) |
| `truncated-text.test.ts` | 129 | TruncatedText component | [components/25-display.md](../components/25-display.md) |
| `fuzzy.test.ts` | 112 | fuzzyMatch / fuzzyFilter | [12-fuzzy.md](../12-fuzzy.md) |
| `select-list.test.ts` | 116 | SelectList layout + truncation | [components/23-select-list.md](../components/23-select-list.md) |
| `truncate-to-width.test.ts` | 76 | truncateToWidth / visibleWidth / Thai-Lao AM | [06-utils.md](../06-utils.md) |
| `tab-width.test.ts` | 28 | sliceWithWidth / extractSegments tab accounting | [06-utils.md](../06-utils.md) |
| `keybindings.test.ts` | 38 | KeybindingsManager resolution + conflicts | [08-keybindings.md](../08-keybindings.md) |

## Helpers / demos / repros (not suites)

| File | LOC | Role | Notes |
|---|---|---|---|
| `virtual-terminal.ts` | 218 | **xterm.js-headless `Terminal` impl** for renderer/overlay/engine tests | see API below |
| `test-themes.ts` | 38 | chalk `level:3` theme fixtures (`defaultSelectListTheme`, `defaultMarkdownTheme`, `defaultEditorTheme`) | [15-appendix.md](../15-appendix.md) |
| `chat-simple.ts` | 129 | end-to-end demo (Markdown + Loader + Editor + autocomplete) | port as `example/chat` |
| `key-tester.ts` | 123 | interactive raw-key logger (`matchesKey`/`parseKey`) | port as `cmd/keytester` debug tool |
| `image-test.ts` | 56 | inline-image visual demo | manual |
| `viewport-overwrite-repro.ts` | 106 | renderer overwrite regression repro | also noted in [engine-render.md](engine-render.md) |

---

## Test harness — `VirtualTerminal` (the key helper to port)

`virtual-terminal.ts` implements the `Terminal` interface over `@xterm/headless` and
exposes test-only methods. **It always reports `kittyProtocolActive = true`** (so key
tests run in Kitty mode). API the engine/component tests rely on:

| Method | Purpose |
|---|---|
| `new VirtualTerminal(cols, rows)` | xterm-backed fake terminal |
| `start(onInput, onResize)` / `stop()` | writes bracketed-paste enable/disable |
| `write(data)` | feeds ANSI to xterm (the engine's output) |
| `sendInput(data)` | invokes the engine's input handler with raw bytes |
| `resize(cols, rows)` | resizes + fires onResize |
| `getViewport()` | visible rows (`translateToString(true)`, trimmed) |
| `getScrollBuffer()` | full buffer incl. scrollback |
| `getCursorPosition()` | `{x, y}` |
| `flush()` / `flushAndGetViewport()` | drain pending writes |
| `waitForRender()` | `nextTick` + 20ms + `flush` — lets the TUI's throttled render settle |
| `clear()` / `reset()` | xterm clear/reset |

Subclass used by image tests: `LoggingVirtualTerminal` records raw `write` bytes
(`getWrites()`/`clearWrites()`) for byte-substring assertions (delete-before-draw
ordering, image pre-clear sequences). xterm buffer cells are read for attribute
checks via `getCellItalic`/`getCellUnderline` helpers (cast to reach the xterm
instance).

## Go test-harness strategy

There is no direct Go equivalent of `@xterm/headless`. Two viable approaches —
**both must let tests (a) feed raw input bytes, (b) capture the engine's ANSI output
stream, (c) read the "viewport" the engine produced, and (d) synchronize one render
frame**:

1. **Byte-stream assertions (simplest, recommended first).** A Go `TestTerminal`
   implementing `tui.Terminal` that records every `Write()` into a buffer and lets
   tests assert on the ANSI substring (e.g. `contains("\x1b[2K\r\n\x1b[2K\x1b[1A")`,
   delete-before-draw ordering). This covers the renderer, overlay, image, and
   terminal-negotiation suites. Port the `TestTerminal` from
   `terminal-colors.test.ts` as the canonical helper.
2. **A real virtual terminal emulator** (port `virtual-terminal.ts`) for tests that
   need true cell-state (italic/underline-leak checks) — either wrap a Go VT library
   (e.g. `github.com/containers/common/pkg/cgroups`? no — a terminal emulator like
   `tcell`/`vterm`) or **replace those specific assertions with byte-substring checks
   on the emitted reset codes** (`\x1b[0m`/`\x1b[24m`/OSC8 close), which is what the
   fix actually guarantees.

**Async/flush:** TS uses `process.nextTick` + `setTimeout(20)` + xterm `write(cb)`.
Go: the engine's render loop must expose a **synchronous flush** for tests (e.g.
`TUI.RenderSync()` that runs `doRender` once, or run the engine with a test clock +
drain the render channel). Without this, every render-dependent test becomes flaky.
Build this seam into the engine port from the start ([02-engine.md](../02-engine.md)).

**Timers/debounce:** autocomplete debounce (20ms), stdin-buffer timeout (10ms), Kitty
fragment timeout (150ms), loader interval (80ms). Inject a clock interface
(`func() time.Time` / a `*fakeClock`) so tests advance time deterministically rather
than sleeping.

## Coverage summary
- **Pure-logic** modules (keys, utils, fuzzy, word-nav, stdin-buffer, terminal-colors,
  keybindings, autocomplete, truncate, wrap): full table-test coverage — port first.
- **Engine/renderer/overlays**: the densest coverage (`tui-render` + `overlay-*` +
  `overlay-options` + `tui-shrink` + `tui-cell-size-input` + style-leak + CJK
  boundary). Requires the byte-stream harness + sync flush seam.
- **Components**: editor (4051, the spec), markdown (1422), input (647),
  select-list, truncated-text. Editor + markdown are the long poles.
- **Demos** (`chat-simple`, `key-tester`): port as the integration smoke test +
  debugging CLI.
