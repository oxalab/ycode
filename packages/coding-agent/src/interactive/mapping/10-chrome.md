# 10 — Chrome: header, footer, keybinding-hints, borders, loaders

> Every claim below is verified against the reference source (cited as `file:line`).
> Files are under `reference/coding-agent/src/modes/interactive/`.

## header (inline in `interactive-mode.ts`)

- **Role:** top banner. Built into the orchestrator (there is no `header.ts`).
- **Structure** (`interactive-mode.ts:630-636`): `headerContainer = Container{ Spacer(1), builtInHeader, Spacer(1) }`.
  Quiet startup → `builtInHeader = new Text("", 0, 0)` (`:635`). Otherwise:
- **`builtInHeader`** = `new ExpandableText(collapsed, expanded, getStartupExpansionState(), 1, …)` (`:621-625`):
  - collapsed fn: `` `${logo}\n${compactInstructions}\n${compactOnboarding}\n\n${onboarding}` ``
  - expanded fn: `` `${logo}\n${expandedInstructions}\n\n${onboarding}` ``
  - `paddingX = 1`. `ExpandableText` is defined at `interactive-mode.ts:149` (extends `Text`, implements `Expandable`).
- **Theme slots:** depends on `logo`/`instructions`/`onboarding` builders (*«read those builders at port time»*).
- **pi-tui:** `Container`, `Spacer`, `Text`/`ExpandableText`.

## footer (`components/footer.ts` — read in full)

- **Role:** 2–3 line status bar. `render(width) → string[]` (`footer.ts:65-219`).
- **Line 1 — pwd** (`:93-104, :204`): `~{pwd-home-stripped} ({branch}) • {sessionName}`, `fg("dim")`, truncated with `fg("dim","...")`. Branch appended only if present; `• {sessionName}` only if a session name is set.
- **Line 2 — stats** (`:111-205`): `{statsLeft}{padding}{rightSide}`, all `fg("dim")` (dim applied around any colored context segment so resets don't leak, `:200-202`).
  - `statsParts` (`:112-139`): `↑{in}`, `↓{out}`, `R{cacheRead}`, `W{cacheWrite}` (each only if nonzero); `${cost}` with ` (sub)` if OAuth subscription (`:119-123`); then the context token.
  - **context token** (`:126-139`): `{percent}%/{contextWindow} (auto)` (the ` (auto)` only if `autoCompactEnabled`), or `?/{contextWindow} (auto)` if percent unknown. **Color:** `fg("error")` if percent **>90**, `fg("warning")` if **>70**, else plain.
  - `rightSide` (`:158-173`): if `model.reasoning` → `{modelId} • thinking off` (or `• {level}`); prepended with `(provider) ` only if `getAvailableProviderCount() > 1` and it fits.
  - min 2-space padding between stats and rightSide; rightSide truncated (`truncateToWidth`) if needed (`:176-195`).
- **Line 3 (optional)** (`:208-216`): extension statuses (`footerData.getExtensionStatuses()`), sanitized (`:10-16`), joined by `" "`, `fg("dim")`, truncated with `fg("dim","...")`, sorted by key.
- **`formatTokens`** (`:21-27`): `<1k → "N"`; `<10k → "X.Xk"`; `<1M → round k; `<10M → "X.XM"`; else round M.
- **Theme slots:** `dim`, `error`, `warning`.
- **pi-tui:** `truncateToWidth`, `visibleWidth`. (Plain `Component`, returns `[]string`.)

## keybinding-hints (`components/keybinding-hints.ts` — read in full, 49 lines)

- **Role:** pure formatting helpers (no render). Used by header/footer/components.
- **API:**
  - `keyText(kb)` (`:34`) → `formatKeys(getKeybindings().getKeys(kb))`.
  - `keyDisplayText(kb)` (`:38`) → same, capitalized.
  - `keyHint(kb, desc)` (`:42`) = `fg("dim", keyText(kb)) + fg("muted", " " + desc)`.
  - `rawKeyHint(key, desc)` (`:46`) = `fg("dim", formatKeyText(key)) + fg("muted", " " + desc)`.
  - `formatKeyText` (`:17-27`): splits on `/` then `+`; on darwin maps `alt → option` (`:13`).
- **Theme slots:** `dim` (keys), `muted` (descriptions).
- **pi-tui:** `getKeybindings()` (→ `tui.KeybindingsManager`).

## dynamic-border (`components/dynamic-border.ts` — read in full, 26 lines)

- **Role:** width-filling horizontal rule.
- **Anatomy** (`:22-24`): `render(width) → [ color("─".repeat(max(1, width))) ]`.
- **Default color** (`:14`): `(str) => theme.fg("border", str)`. A custom color fn may be passed (bash → `bashMode`/`dim`, selectors → default `border`, etc.).
- **pi-tui:** `Component`.

```
────────────────────────────────────────────────────────────────
```

## bordered-loader (`components/bordered-loader.ts` — read in full, 69 lines)

- **Role:** loader framed by top/bottom `DynamicBorder`. `BorderedLoader(tui, theme, message, { cancellable?: true })`.
- **Children, in order** (`:16-39`):
  1. `DynamicBorder(borderColor)` — `borderColor = (s) => theme.fg("border", s)` (`:15`).
  2. `CancellableLoader` (if cancellable, `:18-23`) **or** `Loader` (if not, `:26-31`) — both with spinner `(s) => fg("accent", s)`, message `(s) => fg("muted", s)`.
  3. *(if cancellable)* `Spacer(1)` (`:35`) + `Text(keyHint("tui.select.cancel", "cancel"), 1, 0)` (`:36`).
  4. `Spacer(1)` (`:38`) — **unconditional**.
  5. `DynamicBorder(borderColor)` (`:39`).
- **Extras:** `signal` (AbortSignal), `onAbort`, `handleInput` (forwards to CancellableLoader), `dispose`.
- **Theme slots:** `accent` (spinner), `muted` (message), `border` (borders).
- **pi-tui:** `Container`, `CancellableLoader`/`Loader`, `Spacer`, `Text`, `DynamicBorder`.

## countdown-timer (`components/countdown-timer.ts` — read in full, 39 lines)

- **Role:** pure timer — **no render output.** `CountdownTimer(timeoutMs, tui, onTick, onExpire)`.
- **Behavior** (`:17-29`): `remainingSeconds = ceil(timeoutMs/1000)`; calls `onTick(remaining)` immediately; `setInterval(1000)` decrements, calls `onTick` + `tui.requestRender()`; at ≤0 → `dispose()` + `onExpire()`.
- Used by `extension-input` (title `(Ns)` countdown) and auto-retry.
- **pi-tui:** `tui.RequestRender()` indirectly.

## editor border color (chrome reference)

`interactive-mode.ts:3372-3376` `updateEditorBorderColor()`:
- `isBashMode` → `editor.borderColor = theme.getBashModeBorderColor()` (= `fg("bashMode")`).
- else → `theme.getThinkingBorderColor(session.thinkingLevel || "off")` → `thinkingOff/Minimal/Low/Medium/High/Xhigh` (default `thinkingOff`).
- `isBashMode` flips on `editor.onChange` when `text.trimStart().startsWith("!")` (`:2403-2405`); also recomputed on thinking-level/model/session changes (`:1569, :2672, :3105, :3388, :3401`).
