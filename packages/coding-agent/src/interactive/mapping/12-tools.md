# 12 — Tools: tool-execution, bash-execution, diff

> Verified against reference source (cited `file:line`). Files under
> `reference/coding-agent/src/modes/interactive/components/`.

## tool-execution (`tool-execution.ts` — read in full, 353 lines)

- **Role:** render a tool call + result with a **bg-color state machine**, custom call/result renderers, image embedding, expand/collapse.
- **Constructor** (`:43-79`): `ToolExecutionComponent(toolName, toolCallId, args, options={showImages?, imageWidthCells?}, toolDefinition, ui, cwd)`. Defaults: `showImages=true`, `imageWidthCells=60` (`:58-59`). Resolves a built-in def via `createAllToolDefinitions(cwd)[toolName]` (`:57`).
- **Top-level children** (`:63-76`): `Spacer(1)`; then if `hasRendererDefinition()` → `contentBox` (renderShell `"default"`) or `selfRenderContainer` (`"self"`); else `contentText`.
  - `contentBox = Box(1, 1, bg=toolPendingBg)` (`:68`); `contentText = Text("", 1, 1, bg=toolPendingBg)` (`:69`); `selfRenderContainer = Container()` (`:70`).
- **bg-color state machine** (`:229-233`): `isPartial → bg("toolPendingBg")`; `result.isError → bg("toolErrorBg")`; else `bg("toolSuccessBg")`. Applied to the box (`setBgFn`, `:240`) or text (`setCustomBgFn`, `:291`).
- **Renderers** (built-in or custom; `getCallRenderer`/`getResultRenderer` `:81-99`):
  - `renderCall(args, theme, ctx)` → Component (`:250`).
  - `renderResult({content, details}, {expanded, isPartial}, theme, ctx)` → Component (`:271-276`).
  - `renderShell`: `"default" | "self"` (`:105-113`).
  - **Fallbacks** (`:135-145`): call → `Text(fg("toolTitle", bold(toolName)))`; result → `Text(fg("toolOutput", getTextOutput()))`. `formatToolExecution` (`:340-351`) = `bold(toolName) + "\n\n" + JSON.stringify(args, null, 2) + "\n" + output`.
- **Images** (`:305-329`): per `image` result block, if `caps.images && showImages && data && mimeType` → `Spacer(1)` + `Image(data, mimeType, { fallbackColor: fg("toolOutput") }, { maxWidthCells: imageWidthCells })`. Kitty: non-PNG converted async via `convertToPng` (`:178-199`); kitty skips non-PNG until converted (`:314`).
- **Lifecycle:** `updateArgs`, `markExecutionStarted` (`:152`), `setArgsComplete` (`:158`), `updateResult(result, isPartial=false)` (`:164-176`), `setExpanded`, `setShowImages`, `setImageWidthCells`.
- **`hideComponent`** (`:221-226, :331-333`): if a renderer is defined but produces no content and there are no images → `render()` returns `[]`.
- **Theme slots:** `toolPendingBg`/`toolSuccessBg`/`toolErrorBg`, `toolTitle`, `toolOutput`, `bold`.
- **pi-tui:** `Container`, `Box`, `Text`, `Image`, `Spacer`, `getCapabilities`, `TUI`.

## bash-execution (`bash-execution.ts` — read in full, 219 lines)

- **Role:** streaming bash output between two borders; running/complete/cancelled/error status; collapsed preview / expanded.
- **Constructor** (`:32-65`): `BashExecutionComponent(command, ui, excludeFromContext=false)`. `colorKey = excludeFromContext ? "dim" : "bashMode"` (`:37`); `borderColor = (s) => fg(colorKey, s)`.
- **Top-level children** (`:41-64`): `Spacer(1)` → `DynamicBorder(borderColor)` → `contentContainer` → `DynamicBorder(borderColor)`.
- **`contentContainer` initial** (`:51-61`): `Text(fg(colorKey, bold("$ {command}")), 1, 0)` + `Loader(ui, spinner=fg(colorKey), msg=fg("muted"), "Running... (keyText('tui.select.cancel') to cancel)")`.
- **`updateDisplay()`** (`:119-203`) clears + rebuilds `contentContainer`:
  - **header** (`:138`): `Text(fg("bashMode", bold("$ {command}")), 1, 0)` — **note:** the rebuild hardcodes `"bashMode"` regardless of `excludeFromContext` (reference quirk; the constructor header at `:51` uses `colorKey`).
  - **context truncation** (`:121-128`): `truncateTail(fullOutput, { maxLines: DEFAULT_MAX_LINES, maxBytes: DEFAULT_MAX_BYTES })` (from `core/tools/truncate.ts`); then `previewLogicalLines = availableLines.slice(-PREVIEW_LINES)`, `PREVIEW_LINES = 20` (`:19`).
  - **output** (`:142-168`): expanded → `Text("\n"+each-line fg("muted") joined, 1, 0)`; collapsed → inline component `render(width)` = `truncateToVisualLines(styledInput, PREVIEW_LINES, width, 1).visualLines` (width-cached).
  - **status** (`:171-202`): if running → re-add `loader`; else `Text("\n"+statusParts.join("\n"), 1, 0)`:
    - `hiddenLineCount > 0`: expanded → `(keyHint("app.tools.expand","to collapse)")`; else `fg("muted","... N more lines") + (keyHint("app.tools.expand","to expand)")`.
    - cancelled → `fg("warning","(cancelled)")`; error → `fg("error","(exit {exitCode})")`.
    - truncated + `fullOutputPath` → `fg("warning","Output truncated. Full output: {path}")`.
- **Streaming** (`:80-96`): `appendOutput` strips ANSI, normalizes `\r\n`/`\r`→`\n`, joins first new line onto the last existing line.
- **`setComplete`** (`:98-117`): `status = cancelled ? "cancelled" : (exitCode!=0 && defined) ? "error" : "complete"`; `loader.stop()`.
- **Theme slots:** `bashMode`/`dim` (border+header), `muted` (output+loader msg), `warning` (cancelled+truncated), `error` (exit), `bold` (header).
- **pi-tui:** `Container`, `Loader`, `Spacer`, `Text`, `DynamicBorder`; **`truncateToVisualLines` (port gap)**.

## diff (`diff.ts` — read in full, 148 lines)

- **Role:** pure function `renderDiff(diffText, _opts?) → string` (not a component).
- **`parseDiffLine`** (`:8-12`): regex `/^([+-\s])(\s*\d*)\s(.*)$/` → `{prefix, lineNum, content}`.
- **`replaceTabs`** (`:17-19`): `\t` → 3 spaces.
- **Line coloring** (`:89, :124-125, :129, :132, :137, :141`):
  - context → `fg("toolDiffContext", " {lineNum} {content}")`.
  - removed → `fg("toolDiffRemoved", "-{lineNum} {content}")`.
  - added → `fg("toolDiffAdded", "+{lineNum} {content}")`.
  - unparsed lines → `fg("toolDiffContext", line)` (`:88-91`).
- **Removed→added blocks** (`:94-134`): collect consecutive `-` then consecutive `+`. If **exactly 1 removed + 1 added** → intra-line word-diff (`:115-125`); else all removed then all added, no intra-line (`:126-134`). Standalone `+` → added (`:135-138`).
- **`renderIntraLineDiff`** (`:26-66`): `Diff.diffWords(old, new)`; for removed/added parts, strip leading whitespace on the first part, wrap the rest in `theme.inverse(value)`.
- **Dependency:** npm `diff` (`Diff.diffWords`). *«port-time»*: Go `github.com/sergi/go-diff/diffmatchpatch` (word mode) or a custom word-splitter.
- **Theme slots:** `toolDiffContext`, `toolDiffRemoved`, `toolDiffAdded`, `inverse()`.

## Port notes (gaps)
- **`truncateToVisualLines`** (`bash-execution.ts:16, :156`): pi-tui port has single-line `truncateToWidth` + `wrapTextWithAnsi` but not multi-line "last N visual lines." Port in P3 (small util over `wrapTextWithAnsi`).
- **`truncateTail` + `DEFAULT_MAX_LINES/BYTES`** (`core/tools/truncate.ts`): reuse from `core` (framework-agnostic).
- **diff word-diff**: add `sergi/go-diff` in P3.
- **`convertToPng`** (`utils/image-convert.ts`): needed for kitty non-PNG images (PLAN R3).
