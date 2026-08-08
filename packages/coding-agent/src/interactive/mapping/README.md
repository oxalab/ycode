# interactive/mapping/ — visual-anatomy reference (reference TS → Go port)

This directory is the **source-of-truth for the look** of the new `interactive`
TUI (the one built on the pi-tui port at `packages/tui`). Every component the pi
agent's interactive TUI renders is documented here with its exact structure,
theme color slots, spacing, prefixes, and an ASCII sketch — all derived from
`reference/coding-agent/src/modes/interactive/` (read-only TS).

**Goal:** when we port each component (PLAN.md phases P0–P8), it reproduces the
pi agent's look 1:1. These docs are the "does it match?" target.

## How to use

- Before porting a component, read its entry here, then read the linked reference
  `.ts` file (paths below are under `reference/coding-agent/src/modes/interactive/`).
- **Visual source of truth = the reference.** The current Bubble-Tea TUI
  (`packages/coding-agent/src/core/tui/`) is a *feature-coverage* reference only,
  never a visual reference (it renders through glamour/lipgloss, which differs).
- Colors come from the shared theme (`packages/coding-agent/src/core/tui/theme/`,
  the port of reference `theme/theme.ts`) — same `dark.json`/`light.json`, same
  ~50 slots, so colors match by construction.

## Doc index

| Doc | Covers | PLAN phase |
|---|---|---|
| [01-screen-layout.md](01-screen-layout.md) | Orchestrator container tree, screen regions, full-screen mockup, editor-border color | P0 |
| [02-theme-colors.md](02-theme-colors.md) | All ~50 theme color slots, grouped, with where-each-is-used | P0 |
| [10-chrome.md](10-chrome.md) | header (in interactive-mode.ts), footer, keybinding-hints, dynamic-border, bordered-loader, countdown-timer | P2/P4 |
| [11-messages.md](11-messages.md) | assistant-message, user-message, custom-message, branch/compaction/skill summaries | P3 |
| [12-tools.md](12-tools.md) | tool-execution, bash-execution, diff | P3 |
| [13-selectors.md](13-selectors.md) | common selector pattern + per-selector variations + login-dialog/extension-editor/extension-input/custom-editor | P5 |

## Reference → Go port file matrix

| Reference `.ts` (under `modes/interactive/`) | Go target (under `src/interactive/`) | Anatomy doc |
|---|---|---|
| `interactive-mode.ts` (orchestrator) | `orchestrator.go`, `tree.go`, `events.go` | 01 |
| `theme/theme.ts` | reuse `core/tui/theme` (+ `theme_adapter.go`) | 02 |
| `components/header` (inline in orchestrator) | `components/header.go` | 10 |
| `components/footer.ts` | `components/footer.go` | 10 |
| `components/keybinding-hints.ts` | `components/keybinding_hints.go` | 10 |
| `components/dynamic-border.ts` | `components/dynamic_border.go` | 10 |
| `components/bordered-loader.ts` | `components/bordered_loader.go` | 10 |
| `components/countdown-timer.ts` | `components/countdown_timer.go` | 10 |
| `components/assistant-message.ts` | `components/assistant_message.go` | 11 |
| `components/user-message.ts` | `components/user_message.go` | 11 |
| `components/custom-message.ts` | `components/custom_message.go` | 11 |
| `components/branch-summary-message.ts` | `components/branch_summary_message.go` | 11 |
| `components/compaction-summary-message.ts` | `components/compaction_summary_message.go` | 11 |
| `components/skill-invocation-message.ts` | `components/skill_invocation_message.go` | 11 |
| `components/tool-execution.ts` | `components/tool_execution.go` | 12 |
| `components/bash-execution.ts` | `components/bash_execution.go` | 12 |
| `components/diff.ts` | `components/diff.go` | 12 |
| `components/*-selector.ts` (×13) | `components/selectors.go` | 13 |
| `components/login-dialog.ts` | `components/login_dialog.go` | 13 |
| `components/extension-editor.ts`, `extension-input.ts` | `components/extension_editor.go`, `extension_input.go` | 13 |
| `components/custom-editor.ts` | `components/custom_editor.go` | 13 |

## Conventions used in these docs

- **Theme slots** are written as the exact string key, e.g. `fg("toolSuccessBg")`,
  `bg("userMessageBg")`. See [02-theme-colors.md](02-theme-colors.md).
- **Spacing** is `Spacer(N)` = N blank lines. **Box** is `Box(paddingX, paddingY, bgFn)`.
- **Prefixes** are verbatim user-facing strings (`$ `, `↑`, `... N more lines`).
- ASCII sketches use `<…>` for variable content and `─` for DynamicBorder lines.
- Where a detail must be confirmed against the reference at port time (e.g. the
  exact header hint set, which depends on `AppKeybindings`), it is marked
  *«port-time»*.
