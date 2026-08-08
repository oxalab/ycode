# 01 — Screen layout (orchestrator container tree)

Source: `reference/coding-agent/src/modes/interactive/interactive-mode.ts`
(container fields ~lines 234–332, tree construction ~365–381, add-to-UI order
~576 + 639–646, header ~621–636).

## Vertical container tree (top → bottom)

Root = the pi-tui `TUI` (a `Container`). Children added in this exact order:

| # | Container | Always present? | Contents |
|---|---|---|---|
| 1 | `headerContainer` | yes | `Spacer(1)` → `ExpandableText` (built-in header) → `Spacer(1)`. **Or** a `Text("",0,0)` when quiet-startup. Header content (verified, `interactive-mode.ts:621-625`): `logo + "\n" + (compact\|expanded)Instructions + "\n\n" + onboarding`, `paddingX=1`, expandable via `getStartupExpansionState()`. `ExpandableText` is defined at `interactive-mode.ts:149`. |
| 2 | `chatContainer` | yes | Append-only message log. Each section: `DynamicBorder()` → message components → `Spacer(1)` separators. See [11-messages.md](11-messages.md) / [12-tools.md](12-tools.md). |
| 3 | `pendingMessagesContainer` | conditional | Queued steer/follow-up previews (`TruncatedText`), shown while streaming. |
| 4 | `statusContainer` | conditional | Loaders during agent activity (`Loader`, `CancellableLoader`, `BorderedLoader`). Cleared when idle. |
| 5 | `widgetContainerAbove` | conditional | Extension widgets above the editor. Empty → omitted. Max ~10 lines. |
| 6 | `editorContainer` | yes | Holds exactly one child — the `CustomEditor`, **or** a selector/dialog swapped in. See "editor-swap" below. |
| 7 | `widgetContainerBelow` | conditional | Extension widgets below the editor. |
| 8 | `footer` (`FooterComponent`) | yes | 2–3 lines: pwd/branch/session, token+context+model stats, extension statuses. See [10-chrome.md](10-chrome.md). |

> **Focus anchor = `editorContainer`.** `ui.setFocus(editorContainer)`; the focus
> FSM routes keys to whichever child currently occupies it.

## Full-screen mockup (active agent turn)

Derived from the structure above + the component docs. `<…>` = variable content;
`─` = DynamicBorder.

```
<Spacer(1)>
<ExpandableText: logo + instructions + onboarding>            ← headerContainer
<Spacer(1)>
─────────────────────────────────────────────────────────────  ← chatContainer section border
<Spacer(1)>
┌──────────────────────────────────────────────────────────┐  ← UserMessage (Box bg=userMessageBg, pad 1,1)
│ <Markdown fg(userMessageText)>                            │
└──────────────────────────────────────────────────────────┘
<Spacer(1)>
<Markdown assistant text (padX=1,padY=0)>                     ← AssistantMessage (no bg)
  <Markdown thinking, italic fg(thinkingText)>                ←   (or hidden "Thinking…" label)
<Spacer(1)>
┌──────────────────────────────────────────────────────────┐  ← ToolExecution (Box bg=toolPending→Success/Error)
│ <tool call renderer>   <tool result renderer>             │
└──────────────────────────────────────────────────────────┘
<Spacer(1)>
─────────────────────────────────────────────────────────────
<TruncatedText "Steering: <msg>">                             ← pendingMessagesContainer (while streaming)
<TruncatedText "Follow-up: <msg>">
<TruncatedText "↳ <key> to edit all queued messages">
<Loader spinner fg(accent)> "Working…"                        ← statusContainer
<CustomEditor border=fg(thinking<level> or bashMode)>         ← editorContainer (border color = current state)
  <user input text>
<FooterComponent>                                             ← footer
  ~{pwd} ({branch}) • {sessionName}                           ←   line 1 (fg dim)
  ↑{in} ↓{out} R{cr} W{cw} ${cost} {ctx}%/{win} (auto)    {model}  ←   line 2 (fg dim; ctx red>90/amber>70)
  <extension statuses>                                        ←   line 3 (optional, fg dim)
```

## Editor-border color logic

Source: `interactive-mode.ts` `updateEditorBorderColor()` (via `editor.onChange`)
+ `theme/theme.ts` lines 419–441.

- If the editor text (left-trimmed) **starts with `!`** (bash mode) →
  `theme.getBashModeBorderColor()` = `fg("bashMode")`.
- Otherwise → `theme.getThinkingBorderColor(thinkingLevel)`:
  - `off` → `thinkingOff`, `minimal` → `thinkingMinimal`, `low` → `thinkingLow`,
    `medium` → `thinkingMedium`, `high` → `thinkingHigh`, `xhigh` → `thinkingXhigh`
    (default fallback → `thinkingOff`).
- Recomputed on every `editor.onChange` when bash-mode flips, and when the
  thinking level changes. *«port-time»*: in the Go port the border is the
  Editor's `BorderColor` StyleFunc; swap it on `OnChange`/`thinking_level_changed`.

## Selectors/dialogs: editor-swap vs overlay

- **Selectors** (`/model`, `/theme`, `/settings`, …) and **login/extension
  dialogs** are **swapped into `editorContainer`** (not overlays): clear the
  container, add the selector, `SetFocus(selector)`; on done/cancel, restore the
  `CustomEditor` and refocus. (Reference `showSelector` pattern.)
- `ShowOverlay` is used **only** by extension-provided custom components
  (optional `overlay: true`), with `{width}` options. The new TUI should support
  it but the dominant pattern is the editor-swap.

## Static-tree port notes (tree.go)

- Build the 8 containers in `buildStaticTree()` in the order above; store handles
  on the `Orchestrator` (header, chat, pending, status, widgetAbove, editor,
  widgetBelow, footer).
- Add to the engine root via `eng.AddChild(...)` in order.
- `editorContainer` starts with the `CustomEditor` as its only child.
- Conditional containers (pending/status/widgets) are added up-front but kept
  empty; the event bridge populates/clears them.
