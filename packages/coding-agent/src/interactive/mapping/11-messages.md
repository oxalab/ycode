# 11 — Messages: assistant, user, custom, branch/compaction/skill

> Verified against reference source (cited `file:line`). Files under
> `reference/coding-agent/src/modes/interactive/components/`. All are added to
> `chatContainer` with `Spacer(1)` section separators (see
> [01-screen-layout.md](01-screen-layout.md)).

OSC 133 shell-integration constants (defined in `assistant-message.ts:5-7` and
`user-message.ts:4-6`): `START=\x1b]133;A\x07`, `END=\x1b]133;B\x07`,
`FINAL=\x1b]133;C\x07`.

## assistant-message (`assistant-message.ts` — read in full, 148 lines)

- **Role:** render an assistant message (ordered text + thinking blocks) + abort/error footer; wrap in OSC 133 **only when no tool calls**.
- **Structure** (`:12-39`): `Container > contentContainer: Container`.
- **`updateContent(msg)`** (`:73-145`) clears + rebuilds `contentContainer`:
  - `Spacer(1)` if any visible content (`:83-85`).
  - For each content block in order:
    - **text** (trimmed) → `Markdown(text, paddingX=1, paddingY=0, markdownTheme)` (`:93`).
    - **thinking** (trimmed):
      - hidden → `Text(italic(fg("thinkingText", hiddenLabel)), 1, 0)` (default label `"Thinking…"`, `:101-108`).
      - visible → `Markdown(thinking, 1, 0, markdownTheme, { color: fg("thinkingText"), italic: true })` (`:111-116`).
      - either: `+ Spacer(1)` only if visible content follows (`:97-99, :106-108, :117-119`).
  - If **no tool calls** (`:126-145`): `stopReason=="aborted"` → `Spacer(1)` + `Text(fg("error", "Operation aborted" | errorMessage), 1, 0)`; `stopReason=="error"` → `Spacer(1)` + `Text(fg("error", "Error: "+msg), 1, 0)`.
- **OSC 133** (`:62-71`): `render()` prepends `START` to line[0], appends `END+FINAL` to last line — **skipped if `hasToolCalls` or empty**.
- **Streaming:** `updateContent()` on each `message_update` (clears + rebuilds in place).
- **Theme slots:** `thinkingText` (italic), `error`; md* via `markdownTheme`.
- **pi-tui:** `Container`, `Markdown`, `Spacer`, `Text`.

## user-message (`user-message.ts` — read in full, 36 lines)

- **Role:** user message in a colored box, wrapped in OSC 133.
- **Structure** (`:14-23`): `Container > Box(1, 1, bg=userMessageBg) > Markdown(text, 0, 0, markdownTheme, { color: fg("userMessageText") })`.
- **OSC 133** (`:25-34`): `START` on line[0], `END+FINAL` on last line (only if non-empty).
- **Theme slots:** `bg("userMessageBg")`, `fg("userMessageText")`.
- **pi-tui:** `Container`, `Box`, `Markdown`.

```
┌──────────────────────────────────────────────────┐  bg=userMessageBg
│ <Markdown fg(userMessageText)>                    │
└──────────────────────────────────────────────────┘
```

## custom-message (`custom-message.ts` — read in full, 100 lines)

- **Role:** extension message — either a custom-renderer component or a default labeled box.
- **Structure** (`:20-36`): `Container{ Spacer(1) (:30), <box or customComponent> }`; `box = Box(1, 1, bg=customMessageBg)` (`:33`).
- **`rebuild()`** (`:50-98`): if `customRenderer` returns a component → add it (`:59-71`); else add `box` (`:74`) containing:
  - `Text(fg("customMessageLabel", "\x1b[1m["+customType+"]\x1b[22m"), 0, 0)` (`:78-79`) — bold via hardcoded SGR.
  - `Spacer(1)` (`:80`).
  - `Markdown(text, 0, 0, markdownTheme, { color: fg("customMessageText") })` (`:93-97`); `text` from string content or joined `TextContent` blocks (`:83-91`).
- **Expand:** `setExpanded()` → `rebuild()`; custom renderer receives `{ expanded }` (`:61`).
- **Theme slots:** `customMessageBg`, `customMessageLabel` (bold), `customMessageText`.
- **pi-tui:** `Container`, `Box`, `Markdown`, `Spacer`, `Text`.

## branch / compaction / skill summaries — shared `Box(1,1,bg=customMessageBg)` pattern

All three extend `Box` directly and rebuild on `setExpanded()`:

| Component (file) | Label (bold `customMessageLabel`) | Collapsed body | Expanded body |
|---|---|---|---|
| `branch-summary-message.ts` | `[branch]` (`:35`) | `fg("customMessageText", "Branch summary (") + fg("dim", keyText("app.tools.expand")) + fg("customMessageText", " to expand)")` (`:48-55`) | `Markdown("**Branch Summary**\n\n"+summary, 0,0, {color: fg("customMessageText")})` (`:40-45`) |
| `compaction-summary-message.ts` | `[compaction]` (`:36`); `tokenStr = tokensBefore.toLocaleString()` (`:35`) | `fg("customMessageText", "Compacted from {tokenStr} tokens (") + dim keyText + " to expand)"` (`:48-56`) | `Markdown("**Compacted from {tokenStr} tokens**\n\n"+summary, …)` (`:40-45`) |
| `skill-invocation-message.ts` | `[skill]` (`:38` expanded / `:49` collapsed) | single `Text`: `fg("customMessageLabel","\x1b[1m[skill]\x1b[22m ") + fg("customMessageText", name) + fg("dim", " ("+keyText("app.tools.expand")+" to expand)")` (`:48-52`) | `Text(label)` then `Markdown("**{name}**\n\n"+content, …)` (`:38-45`) — **no Spacer** between label and content |

- **Spacing:** Box pad 1,1; `Spacer(1)` after the label in branch/compaction (`:37`); **no spacer in skill** expanded.
- **Expand key:** `keyText("app.tools.expand")`.
- **Theme slots:** `customMessageBg`, `customMessageLabel` (bold), `customMessageText`, `dim` (collapsed keyhint).
- **pi-tui:** `Box`, `Markdown`, `Text` (+ `Spacer` in branch/compaction).

## Port notes

- Reuse `components.Markdown` (goldmark GFM) + the theme adapter for md* slots.
- OSC 133 on `AssistantMessage` (no tool calls) and `UserMessage`. PLAN R2: port them (cheap, aids shell integration).
- `Box(paddingX, paddingY, bgFn)` → pi-tui `components.Box`; `setBgFn`/`setCustomBgFn` exist for the tool bg state machine.
- Summary expand/collapse = per-component bool + `updateDisplay()`/`rebuild()`; key `app.tools.expand`.
