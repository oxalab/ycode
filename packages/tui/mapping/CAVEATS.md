# CAVEATS.md — port deviations & known issues

A running log of every place the Go port of pi-tui **intentionally differs** from
the TypeScript reference, or hits an environment limitation. Each entry explains
what the reference does, what Go does, why, the user-visible impact, and where it's
tested/documented.

> **Convention:** whenever a phase introduces a deviation or environment caveat,
> add an entry here (next C# id) in the same commit that ships the code, and link
> it from that phase's status line in [MIGRATION.md](MIGRATION.md). A *caveat* is
> any behavior that diverges from `reference/tui`, any naming/API change from the
> mapping docs, or any toolchain limitation affecting verification.

---

## C1 — CJK word segmentation is per-grapheme, not dictionary-based
- **Phase:** P1 · **Module:** [wordnav.go](../wordnav.go)
- **Reference:** Node's `Intl.Segmenter` (word granularity) performs *dictionary*
  segmentation for CJK — e.g. `"你好世界"` → `"你好"` + `"世界"` (real words).
- **Go port:** CJK ideographs segment **per-grapheme** (`SegmentWords`).
- **Why:** reproducing ICU dictionary segmentation requires a bundled
  Chinese/Japanese/Korean dictionary, out of scope for a self-contained port.
- **Impact:** `Ctrl+W` / `Ctrl+Left` / `Ctrl+Right` on CJK text move/delete one
  grapheme at a time instead of one dictionary word. Latin/digit runs, whitespace,
  and ASCII/fullwidth punctuation behave identically to the reference. This carries
  into the editor (P10) and any `FindWord*` consumer.
- **Tests:** ASCII/whitespace/punctuation/atomic cases in
  [wordnav_test.go](../wordnav_test.go) pass verbatim; CJK behavior pinned by
  `TestWordNavCJKDeviation` (with the reference's expected values documented in
  comments).

## C2 — `FuzzyMatch` result type renamed to `FuzzyMatchResult`
- **Phase:** P1 · **Module:** [fuzzy.go](../fuzzy.go)
- **Reference / mapping:** the [mapping doc](12-fuzzy.md) proposed both a type
  `FuzzyMatch` and a function `FuzzyMatch` — impossible in Go (one identifier).
- **Go port:** function stays `FuzzyMatch`; the result type is `FuzzyMatchResult`.
  `FuzzyFilter` is unchanged.
- **Impact:** none (naming only). Update [12-fuzzy.md](12-fuzzy.md) if it's ever
  re-canonicalized.

## C3 — `go test -race` is unavailable in this environment
- **Phase:** P2 · **Affects:** whole repo (toolchain limitation)
- **Symptom:** `go test -race` exits `0xc0000139` (STATUS_ENTRYPOINT_NOT_FOUND) with
  no data-race report — the race runtime fails to initialize on this Windows
  toolchain. Verified against the **existing** `packages/coding-agent` package with
  a no-op test (same crash), so it is not caused by this port.
- **Mitigation:** design all concurrency race-safe by construction (mutex
  discipline, callbacks outside locks, deterministic ordering) and rely on the
  non-race `go test`. When a proper gcc/mingw toolchain is available, re-run
  `-race` to confirm.
- **Impact on future phases:** anything with goroutines/timers — the engine
  throttle/render loop (P5), the loader interval (P6), the editor's async
  autocomplete (P10) — must be written race-safe without relying on the detector.

## C4 — StdinBuffer adds an explicit mutex (reference is single-threaded)
- **Phase:** P2 · **Module:** [stdinbuffer.go](../stdinbuffer.go)
- **Reference:** driven by Node's single-threaded event loop; `setTimeout` can't
  fire mid-`Process`.
- **Go port:** adds a `sync.Mutex` to serialize the timeout-flush **goroutine**
  against `Process`. `Process` holds the lock for all state mutation + pendingKitty
  updates, deferring `OnData`/`OnPaste` callbacks to **after** the unlock (no
  re-entrancy deadlock). `Process` cancels any pending timer at entry.
- **Impact:** externally identical behavior; only threading guarantees are stronger.

## C5 — Keybindings conflict ordering is sorted, not insertion-ordered
- **Phase:** P2 · **Module:** [keybindings.go](../keybindings.go)
- **Reference:** conflict keybinding lists follow JS object insertion order.
- **Go port:** Go maps are unordered, so user bindings are iterated in **sorted
  action order** and conflict keys are sorted, giving deterministic output.
- **Impact:** for the ported test case the result is identical; future cases with
  several same-priority conflicts may list in a stable-but-sorted order rather than
  insertion order.

## C6 — `keybindings` moved from Phase 1 to Phase 2 (process note)
- **Phase:** P1→P2 · **Module:** keybindings.go
- **Reason:** `KeybindingsManager.Matches` depends on `keys.MatchesKey`, which lives
  in `keys.go` (P2). Moved so each phase compiles independently. No behavior change.
- **Status:** [MIGRATION.md](MIGRATION.md) P1/P2 lines updated.

## C7 — Two `terminal-image.test.ts` cases deferred to P6 (Image component)
- **Phase:** P3 · **Module:** termimage.go (test-coverage gap)
- **Reference:** `terminal-image.test.ts` "Kitty image cursor movement" includes two
  cases that exercise the **`Image` component** ("caps Image height to a square pixel
  box by default", "places image sequence on first line with empty padding rows").
- **Go port:** those two cases are deferred to P6 (`components/image.go`), which is
  when the component exists. All pure `termimage.go` behavior (encoders, capability
  matrix, `IsImageLine`, dimension parsers, `RenderImage`, `Hyperlink`) is covered now;
  the `bug-regression-isimageline` suite is covered by `IsImageLine`/`TestIsImageLineVeryLong`.
- **Impact:** none on behavior — just deferred verification of two component-level
  image-rendering rules. Tracked in [MIGRATION.md](MIGRATION.md) P3 status line.

## C8 — ProcessTerminal uses direct stdio, not a Bubble Tea adapter
- **Phase:** P4 · **Module:** terminal.go
- **Decision:** MIGRATION Phase 4 flagged "Bubble-Tea adapter vs. direct stdio" as open.
  Chose **direct stdio** (`golang.org/x/term` raw mode + os.Stdin reader goroutine).
- **Why:** pi-tui feeds **raw stdin bytes** to the engine (the engine runs
  `MatchesKey`/`ParseKey` on raw escape sequences). A Bubble Tea adapter would deliver
  `tea.KeyMsg` (losing raw-byte fidelity for edge-case sequences) and wants to own the
  event loop. Direct stdio matches the reference exactly.
- **Impact:** ProcessTerminal is the only Terminal impl; the engine is tested via a
  byte-recording `TestTerminal` (P5), not ProcessTerminal.

## C9 — Windows has no SIGWINCH; resize is not auto-detected
- **Phase:** P4 · **Module:** terminal_resize_other.go
- **Reference:** `ProcessTerminal` fires onResize on terminal resize.
- **Go port:** Unix uses `SIGWINCH` (`terminal_resize_unix.go`). Windows has no
  equivalent exposed by `x/term`, so `setupResize` is a no-op there.
- **Impact:** on Windows, the engine won't auto re-render on resize. Mitigation: the
  engine reads `Columns()`/`Rows()` on each render, so a resize is picked up on the
  next render tick (e.g. keypress/spinner). A proper fix would handle
  `WINDOW_BUFFER_SIZE_EVENT` from the Windows console input — deferred.

## C10 — Apple-Terminal shift+enter requires a darwin (CGo) build
- **Phase:** P4 · **Module:** nativemodifiers_darwin.go / _other.go
- **Reference:** `isNativeModifierPressed` polls macOS `CGEventFlags` to detect shift
  on Terminal.app (which sends a plain `\r` for shift+enter).
- **Go port:** CGo to CoreGraphics on `darwin` only; `nativemodifiers_other.go`
  returns `false` on all other platforms.
- **Impact:** on non-darwin builds, shift+enter on Apple Terminal.app is not detected
  (it registers as plain Enter). Acceptable outside macOS; the darwin CGo build is
  required for full fidelity there.

## C11 — ProcessTerminal stdio / DrainInput / reader-stop are integration-tested
- **Phase:** P4 · **Module:** terminal.go
- **Reference:** `terminal.test.ts` mocks `process.stdout.write`/`process.stdin.on`.
- **Go port:** the negotiation state machine is unit-tested via the injectable
  `kittyNegotiator`; the stdio glue (raw mode, the stdin reader goroutine, `DrainInput`,
  clean reader interruption on `Stop`) is exercised in integration (P11 demo), not
  unit-tested, because driving real `os.Stdin`/`os.Stdout` needs a pty.
- **Known gaps:** (a) a blocking `os.Stdin.Read` can't be cleanly interrupted on
  `Stop()` — the reader goroutine exits when the fd errors / the process exits (a CLI
  that calls `Stop` then terminates is fine); (b) `DrainInput` is best-effort
  (interrupting blocking reads is platform-dependent). Both refine-able later.

## C12 — TestTerminal has no SGR-attribute tracking (style-leak tests deferred)
- **Phase:** P5a · **Module:** testterminal_test.go
- **Reference:** `virtual-terminal.ts` is `@xterm/headless`, which exposes per-cell
  attributes (`isItalic()`/`isUnderline()`) — used by `tui-overlay-style-leak.test.ts`
  and the markdown "no style leak into padding" cases.
- **Go port:** `TestTerminal` is a minimal ANSI interpreter that tracks only text
  content (it ignores SGR codes for the viewport) plus cursor movement, clears, and
  bottom-row scrolling. It does **not** record per-cell style attributes.
- **Impact:** the style-leak assertions ("INPUT row not italic", "no underline in
  padding") can't be checked via `Viewport()`. Two mitigations: (a) assert on the raw
  byte stream that the engine emitted a full reset (`\x1b[0m`) at line end — which is
  the actual fix being tested; (b) if needed later, extend `TestTerminal` to track SGR
  state per cell. Deferred until P5c (overlay style-leak) or markdown (P8).

## C14 — Goldmark divergences from marked.js (strikethrough, tables, streaming fences)
- **Phase:** P8 · **Module:** components/markdown.go
- **Reference:** uses `marked` with a `StrictStrikethroughTokenizer` (`~~` only) and
  `trimPartialClosingFences` for streaming. Tables use marked's flat token API.
- **Go port:** uses `goldmark` + `extension.GFM` (different parser, AST-based).
  Divergences:
  1. **Single-tilde strikethrough:** goldmark's GFM Strikethrough accepts `~text~`
     (single tilde), unlike the reference's strict `~~`-only. Minor — rare in practice.
  2. **Table rendering:** deferred (goldmark AST → table column math is a large sub-task;
     not yet ported). Markdown tables currently render as raw text.
  3. **Streaming code-fence stabilization (`trimPartialClosingFences`):** deferred (needs
     source-text-level fence detection before parsing, or AST-level trimming). Partial
     fences may briefly flicker during streaming.
- **Impact:** strikethrough works for `~~` (the common case); single-tilde is an edge.
  Tables and streaming-fence stabilization are gaps to close later. All other markdown
  features (headings, paragraphs, code blocks, lists, blockquotes, links/OSC8, hr, bold/
  italic/code inline with style-prefix restoration) are ported and tested.
- **Tests:** 16 cases covering the core features. Table + streaming-fence cases deferred.

*Future caveats: append below with the next C# id.*

## C15 — Editor autocomplete is synchronous (no async debounce)
- **Phase:** P10b · **Module:** editor_autocomplete.go
- **Reference:** the editor's autocomplete uses async Promises + 20ms debounce +
  AbortController-based cancellation + start-token race safety, so typing doesn't
  block on the `fd` subprocess.
- **Go port:** `requestAutocomplete` calls `GetSuggestions` **synchronously** in
  `HandleInput` (blocks until results, no debounce, no abort). This works but can
  freeze input briefly when `fd` runs.
- **Impact:** noticeable latency on `@`/Tab completion when `fd` runs a large tree.
  Mitigation: wrap `requestAutocomplete` in a goroutine + `context.Context` abort +
  start-token guard (the plumbing exists in `AutocompleteProvider.GetSuggestions`
  which already takes a `context.Context`). Deferred to avoid concurrency complexity
  in the initial port.

## C16 — Paste-marker atomic snapping deferred
- **Phase:** P10a · **Module:** editor.go / editor_render.go
- **Reference:** `segmentWithMarkers` merges paste-marker graphemes into atomic
  segments so the cursor never lands inside `[paste #N +X lines]`. Word-wrap splits
  oversized markers at grapheme level. `moveToVisualLine` snaps to marker boundaries.
- **Go port:** paste markers are stored/expanded correctly (`expandPasteMarkers`,
  `GetExpandedText`, `submitValue`), but cursor movement and word-wrap treat them
  as **regular text** (not atomic). The cursor can land inside a marker; backspace
  deletes one grapheme at a time within it.
- **Impact:** cosmetic — markers display correctly but aren't cursor-atomic. The
  atomic `segmentWithMarkers` + `moveToVisualLine` snapping can be ported later
  using the `graphemeCluster` infrastructure already in place.

## C17 — Engine callbacks (HandleInput / OnSubmit) run under the render lock
- **Phase:** P11 · **Module:** engine.go (handleInput), example/chat
- **Reference:** `TUI.handleInput` dispatches to the focused component's
  `handleInput`, and a submitted editor calls `onSubmit`, which **directly mutates
  `tui.children`** (splice) and calls `tui.requestRender()`. This is safe because
  the reference is single-threaded — `requestRender` just schedules a throttled
  render and returns immediately.
- **Go port:** `handleInput` holds `renderMu` across `Component.HandleInput`, so
  `Editor.OnSubmit` runs **with the lock held**. Consequences, both deliberate:
  1. A submit callback **cannot** call `RequestRender` (it takes `renderMu` →
     self-deadlock). It doesn't need to: `handleInput` calls `requestRenderLocked`
     unconditionally after `HandleInput` returns, so the synchronous child
     mutations (`/delete`, `/clear`, the user-message insert) render on their own.
     The `chat` demo mutates the promoted `app.Children` slice directly in these
     paths.
  2. Creating a `Loader` from within `OnSubmit` would deadlock, because the Loader
     constructor calls `RequestRender` (via `SetIndicator` → `Start` →
     `updateDisplay`). The `chat` demo therefore runs the Loader creation, the
     bot-reply insertion, and the loader removal on a **goroutine** deferred by
     `time.AfterFunc`/`go`, using the new goroutine-safe `TUI.InsertChildBeforeLast`
     / `RemoveChildSafe` (added in P11) which lock+render outside the callback.
- **Impact:** externally identical behavior. The constraint is an API contract for
  anyone wiring submit/HandleInput callbacks: **don't call `RequestRender` (or
  construct a Loader) from a path that's already under the render lock — defer it.**
  `Responding` is an `atomic.Bool`; `editor.DisableSubmit` is toggled in
  temporally-ordered, serially-fed input, which is race-safe by construction at the
  demo's 1s cadence (the race detector is unavailable anyway — see C3).

## C18 — chalk themes are hand-built ANSI; chat Ctrl+C needs an explicit listener
- **Phase:** P11 · **Module:** example/chat/themes.go, example/chat/main.go
- **Reference:** `test-themes.ts` builds the SelectList/Markdown/Editor themes with
  chalk at level 3 (truecolor); `chat-simple.ts` advertises "Press Ctrl+C to exit".
- **Go port:**
  1. The themes are built from **hand-composed SGR sequences** (fg color open /
     `\x1b[39m` close; attribute open / matching off-code) that mirror what chalk
     emits — per mapping §4 ("prefer hand-built ANSI so output matches chalk").
     They are a faithful visual approximation, not a byte-for-byte chalk port, and
     there are no byte assertions on demo output, so this is cosmetically equivalent.
  2. The chat editor **swallows Ctrl+C** (`tui.input.copy` → no-op, like the
     reference editor), so raw Ctrl+C would not exit. The demo registers a
     `TUI.InputListener` that catches `ctrl+c` and defers `Stop`+`os.Exit` to a
     goroutine (Stop takes the render lock, which `handleInput` already holds —
     same constraint as C17). This honors the banner; it's a demo-only addition,
     not an engine change.
- **Impact:** none on the ported library. Both `chat` and `keytester` render
  correctly; Ctrl+C exits both (keytester checks it directly in its focused
  component; chat uses the listener). Non-tty pipe runs can't deliver `\x03` through
  raw-mode stdio (see C11), so exit is verified by the keys test suite
  (`{"\x03","ctrl+c",true}`) rather than by the pipe smoke run.

## C13 — 10/35 `overlay-non-capturing` cases pinned (FSM fully implemented)
- **Phase:** P5c · **Module:** engine_overlay_focus.go
- **Reference:** `overlay-non-capturing.test.ts` has 35 cases exercising the focus
  FSM (microtask-deferred sub-overlay, blocked-replacement chains, focus-cycle,
  explicit-unfocus-target, etc.).
- **Go port:** the FSM (`setFocusInternalLocked`, `OverlayFocusRestoreState`
  inactive/eligible/blocked, `focusRedirectLocked`, all helpers) is **fully ported**;
  10 cases pin the core transitions (creation-preserve, focus/unfocus, hide-restore,
  capturing-past-non-capturing, input-redirect-on-invisible, SetFocus(nil),
  temporarily-invisible restore, hideOverlay-no-reassign, multiple-capturing restore).
- **Impact:** none on behavior — the remaining ~25 are FSM-equivalent variations
  (same code paths). They're good regression coverage; pin them opportunistically
  (or after a `TestTerminal` SGR upgrade unblocks the style-leak cases). Tracked in
  [MIGRATION.md](MIGRATION.md) P5c status line.

---

*Future caveats: append below with the next C# id.*
