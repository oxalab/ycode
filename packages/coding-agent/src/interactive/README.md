# interactive — pi-tui-based interactive TUI (resume context)

> **Read this first if resuming after a context reset.** It orients a fresh agent
> in <2 minutes. Then read [PLAN.md](PLAN.md) (the phased plan) and
> [mapping/](mapping/) (source-verified visual anatomy).

## What this is

The pi-tui-based interactive TUI for the ycode coding-agent — a **re-base** of the
agent UI onto the faithful pi-tui Go port, replacing the Bubble-Tea TUI at
[`packages/coding-agent/src/core/tui/`](../core/tui). Lives in this package
(`ycode/packages/coding-agent/src/interactive`), gated behind `YCODE_TUI=pi`.

## Why (the one-paragraph history)

The original TS agent was built **on** `@earendil-works/pi-tui`. The Go port
**skipped the framework** and re-implemented the app directly on Bubble Tea
(Elm/MVU) — before pi-tui existed in Go. That caused the exact pain driving this
work: a hand-rolled width/grapheme lib, an editor missing undo/kill-ring/history/
word-nav/paste, lost raw-key/Kitty fidelity, and MVU friction (`globalProgram`
hack, `program.Send` bridges). We have since ported pi-tui faithfully to
[`packages/tui`](../../tui) (197 tests) and a framework-agnostic theme system
already exists at [`core/tui/theme`](../core/tui/theme). So we rebuild the
interactive TUI on pi-tui, mirroring the reference architecture. Decisions:
**driver = missing editor/input fidelity**; **full-parity rebuild**;
**coexist behind `YCODE_TUI=pi` until parity, then delete the BT TUI**.

## Source of truth (non-negotiable)

- **`reference/coding-agent/src/modes/interactive/`** — the ONLY look/behavior
  reference. **No hallucinations** — every detail traces to a `file:line`
  (captured in `mapping/`).
- **`packages/tui` + `packages/tui/components`** — the framework (consume it).
- **`core/tui/theme`** — reuse as-is (~50 semantic color slots; the port of
  `theme/theme.ts`).
- **`packages/coding-agent/src/core/tui/` (Bubble-Tea) — the BROKEN LEGACY.**
  Feature-coverage checklist only; never a visual/behavior reference.

## Current status

| Phase | Status |
|---|---|
| **P0** Foundation + seam + theme adapter | ✅ done (builds + vets clean) |
| **P1** Production editor + raw keys + autocomplete | ✅ done (the driver) |
| **P2** Header (logo + keybinding hints + onboarding, expandable) | ✅ done |
| **P3a** Chat: event bridge + assistant/user messages | ✅ done |
| **P3b** Tools + bash in the transcript | ✅ done |
| **P3c** Diff coloring + compaction summary | ✅ done (P3 complete) |
| **P4a** Footer (pwd/branch/session · tokens/context/model stats) | ✅ done |
| **P5a** Selector framework + thinking/theme selectors (editor-swap FSM) | ✅ done |
| **P5b** Model selector (custom search selector: fuzzy filter, scope toggle, provider badges) | ✅ done |
| **P5c** Settings selector (SettingsList: auto-compact/steering/follow-up/transport + thinking/theme submenus) | ✅ done |
| **P5d** Login dialog (OAuth device-flow via AuthStorage.Login, provider chooser, OSC8 URL) | ✅ done |
| **P5e** Session selector (/switch: list + switch + re-subscribe + transcript rebuild) | ✅ done |
| **P6** Slash routing + bash mode + keybinding cycling (/compact /new /name /help /quit, !bash, ctrl+p/shift+tab) | ✅ done |
| **P7** Widgets: CountdownTimer + auto-retry countdown + extension input/editor widgets | ✅ done |
| **P8a** Hardening (no-model submit guard + empty-state robustness) | ✅ done |
| **P8b** Flip default to pi (`YCODE_TUI=bt` opts into legacy Bubble-Tea) | ✅ done |
| **P8c** Sign-off parity checklist (real-terminal verification) | ✅ done |
| **P9** Residuals sweep: P4b loader+queue · backed builtins (/copy /session /export /clone /reload /hotkeys) · backed selectors (/logout /tree /fork /scoped-models) | ✅ done |
| Genuinely-unbacked: /share, /changelog, extension-input bridge (no Go backend) · BT-TUI deletion (separate soak change) | ⬠ honest notice |

**🎉 P0–P9 complete. The pi-tui interactive TUI is the default; every residual with a Go backend is wired.**

**P0 delivers:** `orchestrator.go`, `tree.go`, `theme_adapter.go`,
`keybindings.go`, `doc.go`; `cmd/ycode/runtime_pi.go` (`launchInteractivePi`);
the `YCODE_TUI=pi` gate in `cmd/ycode/runtime.go`.

**P1 delivers:** `custom_editor.go` (CustomEditor with `app.*` key interception:
paste-image / interrupt / exit), merged `tui.* + app.*` keybindings,
`CombinedAutocompleteProvider` (builtin slash commands + `@file`/path from cwd),
`OnSubmit`→history+`session.Prompt`, `OnChange`→bash-mode border, and the
`IsShowingAutocomplete()` accessor on the pi-tui Editor. The base Editor gives
undo / kill-ring / history / word-nav / char-jump / paste / Kitty keys for free.
Slash/bash routing + interrupt/abort are P6.

## How to run / verify

```bash
go build ./... && go vet ./...
ycode                     # pi-tui TUI is now the DEFAULT
YCODE_TUI=bt ycode        # legacy Bubble-Tea TUI (opt-in, until deleted)
```

Flagless bootstrap works: run `ycode` in a real terminal → `/login` → `/model`
→ type. (Piping stdin downgrades to print mode, which needs a model — that's
why smoke runs with `cmd | ycode` hit "No models available".)

## Critical rules for porting

1. **Reference = sole truth.** Port each component from its `mapping/*.md` entry +
   the cited `reference/.../*.ts` file. No invented structure/colors/strings.
2. **Callbacks run under the engine render lock.** `Editor.OnSubmit`,
   `SelectList.OnSelect`, `OnChange` are called inside `handleInput` while
   `renderMu` is held → **never** call `RequestRender()` synchronously
   (self-deadlock) and **never** block. Schedule session work via
   `go func(){ session.Prompt(...) }()`.
3. **The subscribe goroutine mutates the tree only via the goroutine-safe
   helpers** `TUI.InsertChildBeforeLast` / `RemoveChildSafe` (or a dedicated
   `orch.mu`) — not raw `Children` slicing.
4. **Theme:** call `theme.InitTheme("")` before `theme.GetTheme()` (it panics
   otherwise). Bridge semantic slots → pi-tui component themes in
   `theme_adapter.go`.
5. **No `charmbracelet/*` imports** in this package — keeps a clean deletion
   boundary for the BT TUI later.
6. **`go test -race` is unavailable** on this Windows toolchain — rely on mutex
   discipline + a concurrent-event stress test.

## Key files

- [PLAN.md](PLAN.md) — phased plan, parity table, architecture, risks.
- [mapping/](mapping/) — `README.md` index + `01-screen-layout.md`,
  `02-theme-colors.md`, `10-chrome.md`, `11-messages.md`, `12-tools.md`,
  `13-selectors.md` (all `file:line`-verified).
- `orchestrator.go` — lifecycle (`NewOrchestrator`/`Run`/`Stop`), exit listener.
- `tree.go` — the 8-region static container tree.
- `theme_adapter.go` — semantic theme → pi-tui component themes.
- `cmd/ycode/runtime.go` — the `YCODE_TUI=pi` gate (`launchInteractive`).
