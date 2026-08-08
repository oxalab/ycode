# 02 — Theme color slots (the ~50 semantic colors)

Source: `reference/coding-agent/src/modes/interactive/theme/theme.ts` schema, ported
verbatim to `packages/coding-agent/src/core/tui/theme/schema.go` (`ThemeColors`,
lines 28–93) + `dark.json`/`light.json`.

The new `interactive` package **reuses** `core/tui/theme` as-is — same slots,
same JSON, so colors match the pi agent 1:1. `theme.Theme` exposes:
`Fg(ThemeColor, text)`, `Bg(ThemeBg, text)`, `Bold/Italic/Dim/Inverse(text)`,
`GetFgAnsi(ThemeColor)`, `GetThinkingBorderColor(level)`, `GetBashModeBorderColor()`.

The `theme_adapter.go` wraps these as pi-tui `StyleFunc`:
`sf := func(s string) string { return t.Fg("mdHeading", s) }` and builds the
per-component theme structs (`MarkdownTheme`/`EditorTheme`/`SelectListTheme`).

## Background slots (6) — `bg("…")`

| Slot | Used by |
|---|---|
| `selectedBg` | Selected row in selectors/lists |
| `userMessageBg` | `UserMessage` Box background |
| `customMessageBg` | `CustomMessage`, `BranchSummary`, `CompactionSummary`, `SkillInvocation` Box background |
| `toolPendingBg` | `ToolExecution` while running/partial |
| `toolSuccessBg` | `ToolExecution` on success |
| `toolErrorBg` | `ToolExecution` on error |

## Core UI foreground (11) — `fg("…")`

| Slot | Used by |
|---|---|
| `accent` | logo, keybinding keys, emphasis, spinner (`BorderedLoader`), titles |
| `border` | `DynamicBorder` default color |
| `borderAccent` | emphasized borders *«port-time»* |
| `borderMuted` | de-emphasized borders *«port-time»* |
| `success` | checkmarks / "active" indicators in selectors, success states |
| `error` | error messages, abort, footer context **>90%**, delete-confirm |
| `warning` | warnings, footer context **>70%**, `(cancelled)`, retry spinner |
| `muted` | descriptions, secondary text, loader message text, output lines |
| `dim` | pwd, footer stats, model name, keybinding-hint keys, section metadata |
| `text` | primary text (rarely direct; usually via Markdown default style) |
| `thinkingText` | assistant thinking traces + hidden "Thinking…" label (italic) |

## Backgrounds & content text (5 fg) — paired with the bg boxes

| Slot | Used by |
|---|---|
| `userMessageText` | `UserMessage` Markdown text color |
| `customMessageText` | `CustomMessage`/summaries content text |
| `customMessageLabel` | `[type]` labels (`[branch]`, `[compaction]`, `[skill]`, `[custom]`) — bold |
| `toolTitle` | tool name in fallback renderer |
| `toolOutput` | tool output text in fallback renderer |

## Markdown (10) — feed `components.MarkdownTheme` via the adapter

`mdHeading`, `mdLink`, `mdLinkUrl`, `mdCode`, `mdCodeBlock`,
`mdCodeBlockBorder`, `mdQuote`, `mdQuoteBorder`, `mdHr`, `mdListBullet`.

## Tool diffs (3) — `diff.ts`

| Slot | Used by |
|---|---|
| `toolDiffContext` | context lines (` <lineNum> <content>`) |
| `toolDiffRemoved` | removed lines (`-<lineNum> <content>`) |
| `toolDiffAdded` | added lines (`+<lineNum> <content>`) |

Intra-line word-diff highlights changed words with `theme.inverse(...)` on top of
the removed/added color.

## Syntax highlighting (9) — `HighlightCode` (chroma) in code blocks

`syntaxComment`, `syntaxKeyword`, `syntaxFunction`, `syntaxVariable`,
`syntaxString`, `syntaxNumber`, `syntaxType`, `syntaxOperator`, `syntaxPunctuation`.

## Thinking-level borders (6) — `getThinkingBorderColor(level)` → editor border

`thinkingOff`, `thinkingMinimal`, `thinkingLow`, `thinkingMedium`,
`thinkingHigh`, `thinkingXhigh`.

## Bash mode (1)

| Slot | Used by |
|---|---|
| `bashMode` | editor border when input starts with `!`; bash-execution command header + borders |

## Adapter mapping (target → slots)

```
components.MarkdownTheme:
  Heading=mdHeading  Link=mdLink  LinkUrl=mdLinkUrl  Code=mdCode
  CodeBlock=mdCodeBlock  CodeBlockBorder=mdCodeBlockBorder
  Quote=mdQuote  QuoteBorder=mdQuoteBorder  HR=mdHr  ListBullet=mdListBullet
  Bold=t.Bold  Italic=t.Italic  Strikethrough=...  Underline=...
  HighlightCode = chroma(token) → syntax* slots

components.EditorTheme:
  BorderColor = getThinkingBorderColor(level) | getBashModeBorderColor()
  SelectList  = buildSelectListTheme(t)

components.SelectListTheme:
  SelectedPrefix/SelectedText/Description/ScrollInfo/NoMatch → accent/bold/muted/dim/muted
  (selected row bg via selectedBg)
```

## Color-mode + var-refs

`theme` resolves `vars` references and detects truecolor vs 256-color
(`DetectColorMode`). The new TUI inherits this for free by reusing the package.
On theme switch (`OnThemeChange`), rebuild the component themes and
`eng.RequestRender()`.
