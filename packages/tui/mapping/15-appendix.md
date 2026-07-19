# 15 — Appendix: test helpers, demos, build config, regression history

This doc closes the gaps a completeness audit surfaced. The per-module docs cover
**100% of the public API** (every export in `index.ts`) and every test **suite**.
This file accounts for the non-suite helpers, the build/config files, and the
CHANGELOG as a regression-history reference.

---

## Test helpers & demos (not assertion suites — but needed for the port)

| File | Role | Go equivalent |
|---|---|---|
| [`test/test-themes.ts`](../../reference/tui/test/test-themes.ts) | Shared chalk (`level:3`) theme fixtures: `defaultSelectListTheme`, `defaultMarkdownTheme`, `defaultEditorTheme`. Used by most component tests. | Create `components/testdata/themes.go` (test-only) with the same theme shapes. **Use lipgloss or hand-built ANSI strings** so assertions are deterministic. The exact colors don't matter — the *shape* (which StyleFunc each theme field holds) does. |
| [`test/virtual-terminal.ts`](../../reference/tui/test/virtual-terminal.ts) (218) | `@xterm/headless`-backed fake terminal that records output bytes, for renderer/overlay tests. | Port as a Go `VirtualTerminal` implementing the `Terminal` interface + a byte buffer. Alternative: assert directly on the ANSI stream the engine emits (simpler, sufficient for most cases). See README "Test harness". |
| [`test/chat-simple.ts`](../../reference/tui/test/chat-simple.ts) (129) | End-to-end demo: Markdown messages, Loader, Editor w/ autocomplete, Spacers. `npx tsx` runnable. | Port as `packages/tui/example/chat/` — the smoke test that the whole stack composes. Good first integration target (port phase 8). |
| [`test/key-tester.ts`](../../reference/tui/test/key-tester.ts) (123) | Interactive key-code logger (`KeyLogger implements Component`) — prints raw bytes + `matchesKey`/`parseKey` results for each keypress. **Debugging tool, not a test.** | Port as `packages/tui/cmd/keytester/` — invaluable for validating the key/terminal adapter on a real terminal during development. |
| [`test/image-test.ts`](../../reference/tui/test/image-test.ts), [`test/viewport-overwrite-repro.ts`](../../reference/tui/test/viewport-overwrite-repro.ts) | Visual/manual repro scripts. | Port as needed when debugging image/viewport regressions; not required for correctness coverage. |

> These five were the only test-directory files not cited in a per-doc "Tests to port"
> section, because they're helpers/demos, not assertion suites. All 27 actual
> `*.test.ts` suites are cited.

---

## Build / config files

| File | Purpose | Go equivalent |
|---|---|---|
| [`package.json`](../../reference/tui/package.json) | npm package metadata, `engines: node>=22.19.0`, deps (`get-east-asian-width`, `marked`), devDeps (`@xterm/headless`, `chalk`). Scripts: `build` (`tsgo`), `test` (`node --test test/*.test.ts`), `clean`, `prepublishOnly`. | `packages/tui/go.mod` (or root `go.mod` — see README module-setup option). Build = `go build ./...`, test = `go test ./...`. |
| [`tsconfig.build.json`](../../reference/tui/tsconfig.build.json) | `outDir ./dist`, `rootDir ./src`, extends monorepo base. | (none — Go has no separate compile config) |
| [`vitest.config.ts`](../../reference/tui/vitest.config.ts) | Runs **only** `test/wrap-ansi.test.ts` under vitest (the rest use Node's built-in `node:test`). | In Go all tests run uniformly via `go test`; no per-file selection needed. |
| [`native/*/prebuilds/**/*.node`](../../reference/tui/native) | Prebuilt Node-API addons (darwin/win32 × x64/arm64). | **Not needed** — replaced by `x/sys/windows` + optional CGo (see [14-platform-native.md](14-platform-native.md)). |

**Node engine note:** pi-tui requires Node ≥22.19 (for `Intl.Segmenter` v2 + Unicode
15). Go has no equivalent runtime constraint; `rivo/uniseg` bundles its own Unicode
tables — pin a recent version for current Unicode behavior.

---

## CHANGELOG as regression-history reference

[`CHANGELOG.md`](../../reference/tui/CHANGELOG.md) (961 lines) is not mappable code,
but it is the **single best record of subtle behavior** that is easy to break in a
naive port. Read it before porting each module. Key fact: **the current source
(v0.79.9) already contains every fix below** — so the mapping docs capture them
implicitly. Listed here so they're explicitly on the radar as "port gotchas":

> Format: `changelog item → doc that covers it → risk if ported carelessly`

**Engine / rendering**
- Clear stale lines when content shrinks to zero → [02-engine.md](02-engine.md) `clearOnShrink` → omit and old lines linger.
- Tab width accounting in column slicing + overlay compositing (no width overflow) → [06-utils.md](06-utils.md) → tabs as width-3 everywhere; off-by-one crashes the renderer.
- Overlay compositing over CJK wide chars keeps borders aligned → [02-engine.md](02-engine.md) `compositeLineAt` strict slicing → overlay starting mid-wide-cell misaligns.
- Overlay focus restoration for non-capturing overlays → [02-engine.md](02-engine.md) focus-restore FSM → overlays go dead after rerender.
- Kitty image placement when viewport shorter than image; WezTerm full-redraw image rows → [02-engine.md](02-engine.md) + [27-image.md](components/27-image.md) reserved-rows → images lose rows / scroll artifacts.

**Input / terminal**
- Kitty keyboard negotiation is **response-driven, not timeout-driven** (intermittent Shift+Enter) → [03-terminal.md](03-terminal.md) → timeouts cause misdetection.
- Ignore mismatched/delayed terminal responses in negotiation → [03-terminal.md](03-terminal.md) → false Kitty detection.
- WezTerm Kitty escape edge cases → [04-keys.md](04-keys.md) + [05-stdin-buffer.md](05-stdin-buffer.md) (ESC+ESC+CSI split).
- Apple-Terminal Shift+Enter via local macOS modifier polling → [03-terminal.md](03-terminal.md) + [14-platform-native.md](14-platform-native.md) → shift+enter never fires on Terminal.app.
- Windows Terminal 0x08 = Ctrl+Backspace locally, plain Backspace over SSH → [04-keys.md](04-keys.md) `matchesRawBackspace` → wrong on SSH.

**Text / markdown**
- ANSI wrap must not stack-overflow on very long lines → [06-utils.md](06-utils.md) iterative wrap → recurse → crash on huge inputs.
- Mixed Latin/CJK wraps at grapheme boundaries (no trailing gaps); CJK editor wrap same → [06-utils.md](06-utils.md) + [21-editor.md](components/21-editor.md) `wordWrapLine` CJK break → ugly gaps.
- Markdown streaming code-fence partial-close trimming (no shrink/flicker) → [22-markdown.md](components/22-markdown.md) `trimPartialClosingFences`.
- Markdown list-marker preservation (unordered `+` not forced to `-`); loose-list blank-line separation; continuation-line indentation; task-list checkbox → [22-markdown.md](components/22-markdown.md).
- OSC8 hyperlinks pass through tmux only when client supports them; BEL terminator preserved for OAuth URLs → [10-terminal-image.md](10-terminal-image.md) + [06-utils.md](06-utils.md) `AnsiCodeTracker`.
- Capability detection: Warp added; Windows Terminal OSC8 on; JetBrains truecolor-but-no-OSC8 → [10-terminal-image.md](10-terminal-image.md).

**Editor / autocomplete**
- Cursor Up on non-empty draft jumps to line start before history browsing → [21-editor.md](components/21-editor.md) history.
- History: restore draft on return; cursor at start (↑) / end (↓) → [21-editor.md](components/21-editor.md).
- Autocomplete re-queries after cursor movement → [21-editor.md](components/21-editor.md) `moveCursor` → stale picker.
- `AutocompleteProvider.triggerCharacters` for natural provider-defined triggers → [11-autocomplete.md](11-autocomplete.md).
- Slash-separated fuzzy queries stay matchable after insertion → [12-fuzzy.md](12-fuzzy.md).
- IME hardware cursor positioning while autocomplete visible → [21-editor.md](components/21-editor.md) `emitCursorMarker`.
- Word navigation uses Unicode boundaries but preserves ASCII punctuation → [07-word-navigation.md](07-word-navigation.md).

**Misc**
- Loader indicator init must not run before frames initialized → [26-loader.md](components/26-loader.md).
- Truecolor detection aligns image rendering with theme detector → [10-terminal-image.md](10-terminal-image.md).

**Recommendation:** during Step 3 (migration plan) and again before declaring each
module "done", grep the CHANGELOG for that module's PR numbers to confirm the port
preserves the fixed behavior, and run the corresponding ported test suite.
