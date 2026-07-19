# pi-tui → Go migration mapping

> **▶ Start here: [MIGRATION.md](MIGRATION.md)** — the single entry point. It holds the
> compact context reference for a fresh session **and** the phased checklist (with
> file references per phase) that gets ticked off as each phase ships. New session?
> Read that first, then come back here for the full file→Go-file matrix.
>
> **Deviations & known issues:** [CAVEATS.md](CAVEATS.md) — every place the Go port
> intentionally differs from the reference, or hits a toolchain limitation. One
> entry per caveat, added in the same commit that ships it.

This directory holds the **exact, file-and-symbol-level mapping** for porting
`reference/tui` (`@earendil-works/pi-tui` v0.79.9, TS) into the new Go package at
[`packages/tui`](..).

These docs are the source of truth for **Step 2** (exact mapping). The migration
plan (Step 3) will be built directly from them.

> Reference source: [`reference/tui/`](../../../reference/tui) — 72 files,
> ~13k LOC of TS source + ~14k LOC of tests. Read in full before writing these.

---

## Proposed Go package layout

Module is `ycode` (see root [go.mod](../../../go.mod)). Existing internal imports
follow `ycode/packages/<name>/src/...` (e.g. `ycode/packages/coding-agent/src/core`).
We mirror that:

```
packages/tui/                     import path: ycode/packages/tui
├── go.mod                        (see "module setup" below)
├── component.go                  ← tui.ts            (Component/Container/Focusable/CURSOR_MARKER)
├── editorcomponent.go            ← editor-component.ts (EditorComponent interface)
├── engine.go                     ← tui.ts            (TUI engine + renderer + overlays + focus)
├── terminal.go                   ← terminal.ts       (Terminal interface + ProcessTerminal)
├── keys.go                       ← keys.ts           (Key, matchesKey, parseKey, kitty/modOtherKeys/legacy)
├── stdinbuffer.go                ← stdin-buffer.ts
├── utils.go                      ← utils.ts          (width/wrap/truncate/slice, AnsiCodeTracker)
├── wordnav.go                    ← word-navigation.ts
├── keybindings.go                ← keybindings.ts
├── termcolors.go                 ← terminal-colors.ts
├── termimage.go                  ← terminal-image.ts
├── autocomplete.go               ← autocomplete.ts
├── fuzzy.go                      ← fuzzy.ts
├── killring.go                   ← kill-ring.ts
├── undostack.go                  ← undo-stack.ts
├── nativemodifiers_darwin.go     ← native-modifiers.ts (//go:build darwin)
├── nativemodifiers_other.go      ← no-op stub for non-darwin
└── components/                   import path: ycode/packages/tui/components
    ├── editor.go                 ← editor.ts            (2307 LOC — the heavyweight)
    ├── input.go                  ← input.ts
    ├── markdown.go               ← markdown.ts
    ├── selectlist.go             ← select-list.ts
    ├── settingslist.go           ← settings-list.ts
    ├── text.go                   ← text.ts
    ├── box.go                    ← box.ts
    ├── truncatedtext.go          ← truncated-text.ts
    ├── spacer.go                 ← spacer.ts
    ├── loader.go                 ← loader.ts
    ├── cancellableloader.go      ← cancellable-loader.ts
    └── image.go                  ← image.ts
```

**Package split rationale:** core (`package tui`) defines the `Component`
interface and engine; `components` (sub-package) depends on `tui` (never the
reverse), mirroring the TS `components/` directory. This keeps the dependency
graph acyclic. There is **no cyclic dependency** in the TS source: components
import only from `..`/`../utils`, never the reverse.

### Module setup (open question for Step 3)

Two options — decide during planning:
- **(A) Single root module** `ycode` — add files under `ycode/packages/tui`, import
  as `ycode/packages/tui`. Matches every other package here. **Recommended.**
- **(B) Separate module** `packages/tui/go.mod` — only if we want `tui` to be a
  reusable library independent of the ycode monorepo.

These docs assume **(A)**.

---

## File → Go-file master matrix

| TS source (`reference/tui/src/`) | LOC | Go file | Mapping doc |
|---|---|---|---|
| `tui.ts` | 1714 | `component.go` + `engine.go` | [02-engine.md](02-engine.md), [01-component-core.md](01-component-core.md) |
| `terminal.ts` | 531 | `terminal.go` | [03-terminal.md](03-terminal.md) |
| `keys.ts` | 1400 | `keys.go` | [04-keys.md](04-keys.md) |
| `stdin-buffer.ts` | 434 | `stdinbuffer.go` | [05-stdin-buffer.md](05-stdin-buffer.md) |
| `utils.ts` | 1188 | `utils.go` | [06-utils.md](06-utils.md) |
| `word-navigation.ts` | 117 | `wordnav.go` | [07-word-navigation.md](07-word-navigation.md) |
| `keybindings.ts` | 244 | `keybindings.go` | [08-keybindings.md](08-keybindings.md) |
| `terminal-colors.ts` | 73 | `termcolors.go` | [09-terminal-colors.md](09-terminal-colors.md) |
| `terminal-image.ts` | 488 | `termimage.go` | [10-terminal-image.md](10-terminal-image.md) |
| `autocomplete.ts` | 786 | `autocomplete.go` | [11-autocomplete.md](11-autocomplete.md) |
| `fuzzy.ts` | 137 | `fuzzy.go` | [12-fuzzy.md](12-fuzzy.md) |
| `kill-ring.ts` | 46 | `killring.go` | [13-editor-internals.md](13-editor-internals.md) |
| `undo-stack.ts` | 28 | `undostack.go` | [13-editor-internals.md](13-editor-internals.md) |
| `native-modifiers.ts` | 59 | `nativemodifiers_*.go` | [14-platform-native.md](14-platform-native.md) |
| `editor-component.ts` | 74 | `editorcomponent.go` | [01-component-core.md](01-component-core.md) |
| **Components** (`components/`) | | | |
| `editor.ts` | 2307 | `components/editor.go` | [components/21-editor.md](components/21-editor.md) |
| `markdown.ts` | 852 | `components/markdown.go` | [components/22-markdown.md](components/22-markdown.md) |
| `input.ts` | 447 | `components/input.go` | [components/20-input.md](components/20-input.md) |
| `settings-list.ts` | 250 | `components/settingslist.go` | [components/24-settings-list.md](components/24-settings-list.md) |
| `select-list.ts` | 229 | `components/selectlist.go` | [components/23-select-list.md](components/23-select-list.md) |
| `image.ts` | 126 | `components/image.go` | [components/27-image.md](components/27-image.md) |
| `box.ts` | 137 | `components/box.go` | [components/25-display.md](components/25-display.md) |
| `text.ts` | 106 | `components/text.go` | [components/25-display.md](components/25-display.md) |
| `loader.ts` | 92 | `components/loader.go` | [components/26-loader.md](components/26-loader.md) |
| `cancellable-loader.ts` | 40 | `components/cancellableloader.go` | [components/26-loader.md](components/26-loader.md) |
| `truncated-text.ts` | 65 | `components/truncatedtext.go` | [components/25-display.md](components/25-display.md) |
| `spacer.ts` | 28 | `components/spacer.go` | [components/25-display.md](components/25-display.md) |
| **Native** (`native/*/src/*.c`) | | (CGo/syscall, build-tagged) | [14-platform-native.md](14-platform-native.md) |
| *(helpers/demos/config)* | | | [15-appendix.md](15-appendix.md) |

> **Completeness audit:** the per-module docs cover 100% of `index.ts` exports and
> all 27 `*.test.ts` suites. Non-suite helpers (`test-themes.ts`, `key-tester.ts`,
> `virtual-terminal.ts`, `chat-simple.ts`), build/config files, and the CHANGELOG as
> regression-history are in [15-appendix.md](15-appendix.md).

---

## External dependency map

| TS dependency | Used for | Go replacement | Already in root go.mod? |
|---|---|---|---|
| `get-east-asian-width` | CJK / fullwidth width | `github.com/mattn/go-runewidth` | ❌ **add** |
| `Intl.Segmenter` (grapheme + word) | Unicode segmentation | `github.com/rivo/uniseg` | ✅ v0.4.7 |
| `marked` | markdown tokenization | `github.com/yuin/goldmark` | ✅ v1.7.13 |
| `marked` emoji ext | `:emoji:` in markdown | `github.com/yuin/goldmark-emoji` | ✅ v1.0.6 |
| `alecthomas/chroma` | code highlighting (theme hook) | `github.com/alecthomas/chroma/v2` | ✅ v2.20.0 |
| `chalk` | ANSI styling (themes/tests) | `github.com/charmbracelet/lipgloss` | ✅ v1.1.1 |
| `@xterm/headless` | virtual terminal for tests | custom Go test harness (see below) | — |
| native `.node` (win32) | `ENABLE_VIRTUAL_TERMINAL_INPUT` | `golang.org/x/sys/windows` | ✅ (x/sys) |
| native `.node` (darwin) | CGEventFlags modifier polling | CGo to CoreGraphics, or skip | — |

**Note:** the Charm stack (`bubbletea`/`bubbles`/`glamour`/`x/ansi`) is already in
go.mod but is **not** used by pi-tui — it was pulled in by the *existing*
`coding-agent` TUI. `packages/tui` should depend only on `uniseg`, `go-runewidth`,
`goldmark`, `goldmark-emoji`, `chroma`, and `x/sys`. (Lipgloss is optional for
themes; ANSI strings can be hand-built to match chalk's output exactly — see
[06-utils.md](06-utils.md).)

### Test harness — no `@xterm/headless` equivalent
The TS suite drives a real/virtual terminal. For Go we need:
- A `TestTerminal` that implements the `Terminal` interface and records `Write()`
  output + replays `sendInput` (port the one in `terminal-colors.test.ts`).
- An optional virtual terminal (port `test/virtual-terminal.ts`) for renderer
  diff tests, OR assert directly on the ANSI byte stream the renderer emits.

---

## Suggested port order (dependency-bottom-up)

Each phase must compile + pass its ported tests before the next begins.

1. **Foundation (pure, no I/O)** — `fuzzy`, `wordnav`, `killring`, `undostack`,
   `termcolors`, `utils` (width/truncate/wrap/slice), `keybindings`-types.
   Highest test coverage, zero terminal coupling. Do first.
2. **Input parsing (pure)** — `keys` (matchesKey/parseKey/decode*),
   `stdinbuffer`. Pure logic, full test tables.
3. **Interfaces** — `component.go` (Component/Container/Focusable/CURSOR_MARKER),
   `editorcomponent.go` (EditorComponent interface), `termimage` (encoders/parsers).
4. **Terminal + platform** — `terminal.go` (Terminal iface + ProcessTerminal
   over a real TTY), `nativemodifiers_*`, platform-native (Windows VT input).
5. **Engine** — `engine.go` (renderer, overlays, focus, IME cursor, OSC queries).
   The hardest single unit; needs the test terminal harness.
6. **Components** — bottom-up: `spacer`, `truncatedtext`, `text`, `box`,
   `loader`, `cancellableloader`, `image`, `selectlist`, `settingslist`,
   `input`, `markdown`, then `editor` (depends on selectlist + autocomplete).
7. **Autocomplete** — `autocomplete` (depends on `fuzzy` + `fd` subprocess);
   wire into `editor`.
8. **Integration** — port `chat-simple.ts` demo as an example; run end-to-end.

---

## Conventions used in these docs

- **TS → Go** tables: left column is the exact TS export/symbol; right is the
  proposed Go identifier (exported = capitalized). Methods become value
  receivers unless noted.
- **`string → []string` render contract** is preserved verbatim: a Go `Component`
  returns `[]string`, one per line, each ≤ `width` visible columns.
- **Themes** are `func(string) string` in TS → Go `type StyleFunc func(string) string`
  (theme structs hold these). This keeps 1:1 behavior with chalk.
- **ANSI sequences** are kept byte-for-byte where the TS hardcodes them (e.g.
  `CURSOR_MARKER = "\x1b_pi:c\x07"`, synchronized-output `CSI ?2026`, OSC 8
  hyperlinks). Define as Go `const`.
- **Async / timers** (`setTimeout`, `AbortController`, `setInterval`) → Go
  channels, `context.Context`, `time.Ticker`. See per-doc notes.
- **`Intl.Segmenter`** → `rivo/uniseg` `NewGraphemes` / `NewWords`.
- Every doc ends with a **Tests to port** section listing the TS test file(s)
  that verify that unit.
