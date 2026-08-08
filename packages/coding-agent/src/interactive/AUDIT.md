# Visual & Fidelity Audit — interactive + tui ports vs reference

> Generated 2026-06-27. Compares the `interactive/` and `tui/` Go ports against the
> TS reference (`reference/coding-agent/src/modes/interactive/`, `reference/tui/`,
> `reference/ai/src/providers/`) and the migration docs (`migrations/`,
> `packages/tui/mapping/`). Every claim is `file:line`-cited.
>
> Related: [PLAN.md](PLAN.md), [SIGNOFF.md](SIGNOFF.md), [README.md](README.md).

## 🔴 Urgent — TEMP debug still in the code (from the z.ai provider debugging)

These were added while debugging the openai-completions streaming path and should
**not ship**. Revert them, keeping the real fixes.

- **Raw request/response/params dumps** writing to `~/.ycode/zai-*.txt` —
  `openai-completions.go` (request dump ~:167, response dump ~:115, params dump
  ~:238) + the `os`/`path/filepath` imports they need.
- **Event logger + `[chat dump]`** — `events.go` `debugLogEvent` (+ call sites in
  `handleAgentEvent`/`handleSessionEvent`, the MessageEnd/`[chat dump]` blocks) +
  the `os`/`fmt`/`path/filepath` imports.
- **`EventTurnEnd` error-display block** — `events.go` (between the `=== TEMP ===`
  markers).
- **HTTP `Timeout: 60s`** — `openai-completions.go:~92`. This one is a **real fix
  worth keeping** — just drop the `TEMP` comment.

**Real fixes to keep** (the actual z.ai bugs): SSE line parser
(`processOpenAICompletionsStream`), value/pointer normalization
(`normalizeMessagePtrs`/`normalizeContentPtrs` in `transform-messages.go`),
`msgRole` value handling, the `UserMessage` value/pointer branch, the text/thinking
sync into `output.Content` blocks, the `extractToolCalls` pointer fix.

---

## ⚠️ CAVEAT — value/pointer mismatch (silent-failure hazard)

**This is the single most dangerous class of bug in the Go port.** The TS
reference is structurally typed (`block.type === "toolCall"`), so it never
distinguishes value vs reference. The Go port is split:

- **Producers (llm/providers + agent-sdk) create POINTERS** — `&src.TextContent{}`,
  `&src.ToolCall{}`, `&src.UserMessage{}`, etc.
- **Some consumers assert VALUES** — `content.(llm.ToolCall)`, `m.(*src.UserMessage)`
  vs `m.(src.UserMessage)`, etc.

A value-assertion against a pointer (or vice-versa) **silently fails the type
switch** — no panic, no error — and the content is dropped. The agent then
"silently does nothing" (no tool runs, no message content, empty reply).

### Sites found & fixed during the z.ai bring-up (2026-06)
| Site | Symptom | Fix |
|---|---|---|
| `msgRole` (`providers/github-copilot-headers.go:41`) | user messages dropped → `messages` illegal / no reply | assert both `*UserMessage` + `UserMessage` (etc.) |
| `convertOpenAICompletionsMessages` user branch (`openai-completions.go:~453`) | user message dropped from request | type-switch both forms |
| `TransformMessages` (`providers/transform-messages.go`) | value messages/blocks panicked or dropped | `normalizeMessagePtrs`/`normalizeContentPtrs` shim at entry |
| **`extractToolCalls` (`agent-sdk/agent_loop.go:695`)** | **every tool call dropped → agent never executes tools, stops after reasoning** | assert `*llm.ToolCall` (the form providers produce) |
| `setActiveToolsByName` (`core/agent_session.go:372`) | tools never set on agent → no `tools` field sent → model invents text `<tool_call>` | map toolRegistry → `Agent.SetTools` |

### Rule for anyone touching this code
When iterating `[]llm.ContentBlock` or `[]llm.Message`, **always type-switch BOTH
the pointer and value forms** (`*T` and `T`), or normalize to one form first
(`normalizeMessagePtrs`). Never assume which form a value holds. The providers
produce pointers; the agent-sdk producer layer (`agent.go`, `agent_loop.go`)
produces values. They meet at the provider boundary, and that's where the bugs live.

### How to spot it
Symptom: the agent "silently does nothing" — a message has no content, a tool
never runs, a request is missing a field — with **no error, no panic**. Grep for
`.(llm.<Type>)` / `.(*llm.<Type>)` assertions and check both arms are handled.

**First thing to suspect** for any "silently does nothing" in the agent flow.

---

## 🟡 Visual mismatches — message components

Reference: `reference/coding-agent/src/modes/interactive/components/*-message.ts`.
Port: `packages/coding-agent/src/interactive/*_message.go` (+ `tool_execution.go`,
`bash_execution.go`, `compaction_summary.go`, `diff.go`). Theme slots:
`reference/.../theme/theme.ts:29-92` ↔ `core/tui/theme/schema.go:30-92` (1:1 parity).

| Type | Status | Gap |
|---|---|---|
| assistant | ✅ faithful | OSC133 markers, `thinkingText`, `error`, italic hidden label, spacers, paddingX all match |
| user | ✅ faithful | `userMessageBg` box, `userMessageText`, `Box(1,1)`, OSC133 match |
| tool-execution | ✅ colors/box faithful; ⚠️ **no image rendering** (`tool-execution.ts:316-324`) |
| **bash** | ⚠️ partial | **no red `(exit N)` exit-code** (`bash-execution.ts:190` → port only `(error)` at `bash_execution.go:139`); **no "Output truncated" warning** (`bash-execution.ts:196`) |
| **compaction** | ⚠️ partial | **no collapsed/expand-hint view** (`compaction-summary-message.ts:50-52`); port renders expanded only |
| **branch-summary** | ❌ **missing** | `branch-summary-message.ts` — `customMessageBg` + `[branch]` label not ported |
| **custom-message** | ❌ **missing** | `custom-message.ts` — generic `[type]` labeled bubble not ported |
| **skill-invocation** | ❌ **missing** | `skill-invocation-message.ts` — `[skill]` block not ported |
| diff (tool edits) | ➕ parity-plus | `diff.go` is *more* sophisticated than the reference (word-level intra-line diff) |

> The 3 missing message types share the `customMessageBg` + `[label]` +
> `customMessageText` box pattern already proven in `compaction_summary.go:22-35`.

---

## 🟡 Visual mismatches — chrome

| Element | Status | Mismatch |
|---|---|---|
| header (logo/hints/onboarding) | ✅ faithful | — |
| footer pwd/stats/context/model lines | ✅ faithful | — |
| **footer line 3** | ❌ missing | per-extension status line (`footer.ts:207-216`); port returns 2 lines (`footer.go:218`) |
| editor border color | ✅ faithful | bash + thinking-level wired at `orchestrator.go:252-258` (stale comment at `theme_adapter.go:29-30`) |
| model selector | ✅ faithful | borders, scope, `→`/`[provider]`/`✓`/`(n/m)`/`Model Name:` all match |
| thinking / theme selectors | ✅ faithful | — |
| login dialog | ✅ faithful | OSC8 links, hints, structure match |
| **working spinner** | ⚠️ major | port = static dim text, **no borders, no animation, no cancel hint** (`working.go:35-46` vs `bordered-loader.ts:7-40`). Tradeoff (render-lock); fixable via a render-driven spinner |
| **session selector** | ⚠️ major | plain `SelectList`; missing accent borders, `›` cursor, `selectedBg` highlight, msg-count/age column, tree connectors, sort/filter hints (`session.go:33` vs `session-selector.ts:725,460-503`) |
| **oauth/login chooser** | ⚠️ major | bare provider names, **no status badges** (`✓ configured`/`✓ env:`/`unconfigured`) (`selectors_residual.go:39-58` vs `oauth-selector.ts:120-175`) |
| **scoped-models** | ⚠️ partial | `☐/☑` glyphs (ref `✓/✗`), missing rich footer + `(unsaved)` warning (`selectors_residual.go:426-447` vs `scoped-models-selector.ts:127-231`) |
| **tree selector** | ⚠️ minimal | core port only — no fold/filter/label-edit/badges (documented intentional, `selectors_residual.go:122-125`) |
| settings selector | ⚠️ subset | shell faithful but **6 of ~25 settings** (only those with Go setters) |

---

## 🟢 tui framework (`packages/tui/`) — byte-faithful, a few open items

**Confirmed faithful:** differential renderer (5 branches, `engine.go:604-648`),
overlay compositing + focus-restore FSM (`engine_overlay.go`),
hardware cursor + `CursorMarker`, width/grapheme rules (`utils.go:142-172`),
editor/input/SelectList rendering.

**Open items:**
- **[HIGH] Autocomplete is synchronous** — blocks input while `fd` runs. Mapping
  doc `21-editor.md:172-186` *claims* async/debounce but it isn't
  (`editor_autocomplete.go:144`, comment "Synchronous (P10b caveat...)"). CAVEATS
  C15 is the truthful doc.
- **[HIGH] Paste-marker atomic snapping not implemented** (CAVEATS C16 open) —
  cursor can land inside a `[paste #N +X lines]` marker. Markers store/expand
  fine; only cursor atomicity is missing.
- **[MED] CJK word-nav** segments per-grapheme, not per-word
  (`wordnav.go:50-61`); mapping `06-utils.md:19` stale. CJK *wrapping* matches.
- **[LOW]** no width cache (documented-allowed, `06-utils.md:61`); emoji is a
  heuristic not RGI set; `cursorInPadding` overflow trim absent; SGR reset uses
  `\x1b[0m` vs `\x1b[27m` (equivalent).
- **Doc fixes:** `06-utils.md:19` (claims `NewWords` — false); CAVEATS C13
  denominator (says 35, actually 44 reference cases).

---

## 🔵 Migration cross-check (structural)

- **[HIGH] openai-completions hand-rolls HTTP** (no OpenAI SDK, unlike the TS
  reference `openai-completions.ts:1-12`) — `openai-completions.go` uses raw
  `http.Request` + `map[string]interface{}` chunks. **Undocumented divergence**
  (zero mentions in `migrations/`). Plus leftover TEMP debug dumps (see 🔴).
- **[HIGH] ⚠️ Value/pointer mismatch hazard (CAVEAT)** — see the dedicated section
  below. This is a recurring, silent-failure class of bug across the agent-sdk +
  llm/providers + core layers. Four sites were found and fixed during the z.ai
  bring-up; **suspect this first** for any "agent silently does nothing."
- **[MED] Extension input/editor widgets** built + tested but **unwired**
  (`extension_input.go`, `extension_editor.go` self-document "NOT yet
  event-wired"; no `extension_input_request` event source).
- **[MED] `ExtensionCommandContextActions` never constructed** — only `OnError` +
  `Shutdown` wired; `/fork` `/clone` `/switch` `/new` `/reload` can't reach
  agent-session facilities via the extension path.
- **[MED] JSON/RPC stream emits PascalCase** (`AgentSessionEvent` has no `json:`
  tags) — diverges from TS camelCase; breaks TS-compatible RPC consumers
  (interactive/text mode unaffected).
- **[LOW] `BorderedLoader` fully built, zero call sites** (`core/tui/components/bordered_loader.go`).
- **[LOW] `/share` `/changelog`** emit honest "not yet supported" notices
  (handlers in `slash.go:106-109`).
- **[LOW] `/scoped-models`** persists in-memory only (no `SetEnabledModels` setter).

**Doc-claim integrity:** every "done"/"✓" in PLAN.md/SIGNOFF.md holds; all
deferred items are honestly documented. The only *undocumented* gaps are the
openai-completions SDK divergence and the value-vs-pointer hazard.

---

## 🔍 Tool execution rendering (deep dive)

The reference uses a **two-tier** system: (1) a generic fallback renderer, and (2)
**per-tool custom renderers** (`renderCall`/`renderResult` in `core/tools/*.ts`) that
override the fallback with tool-specific visuals. **The port implements only tier 1
(the fallback)** — tier 2 is entirely absent (`tool_execution.go:5-9` self-documents
it as deferred). That single omission is the source of most gaps below.

Reference: `components/tool-execution.ts`, `components/bash-execution.ts`,
`interactive-mode.ts` (event wiring), `core/tools/{bash,read,write,edit,grep,find,ls}.ts`.
Port: `tool_execution.go`, `bash_execution.go`, `events.go`, `diff.go`.

| Aspect | Reference | Port | Gap |
|---|---|---|---|
| Call header (name) | bold name (fallback `tool-execution.ts:341`); per-tool replaces it (bash `$ cmd` `bash-execution.ts:51`) | bold name `tool_execution.go:69`; bash special-cased | fallback faithful |
| **Call ARGS** | fallback pretty JSON (`:342`); per-tool replaces with compact views (path in `accent`, pattern `/.../`, etc.) | raw `json.MarshalIndent` for ALL non-bash tools `tool_execution.go:70-72` | **every non-bash tool dumps raw JSON args** |
| Running state | pending `toolPendingBg` (`:229`); bash **animated Loader** + "(esc to cancel)" + elapsed timer (`bash-execution.ts:54-61`, `core/tools/bash.ts:410-434`) | pending bg ✓; bash = static dim "Running..." `bash_execution.go:124,143` | **no spinner/timer/cancel for bash** |
| Output streaming | `tool_execution_update` → partial `interactive-mode.ts:2798`; bash 100ms throttle | wired ✓ `events.go:123-136` | wire OK (bash throttle absent) |
| **Result truncation** | per-tool ~15-20 line preview + "… N more lines" (`bash-execution.ts:181-184`); context cap 2000 lines/50KB (`:122-125`) | **generic tools: NO cap** — dumps entire result `tool_execution.go:73-79`; bash caps at 20 lines ✓ | **non-bash tools flood the screen** (read/grep of big output) |
| Diff coloring (edit) | structured diff via `core/tools/edit.ts:447-483` | heuristic `isDiffLike` → `diff.go` (word-level LCS — **more** sophisticated) | parity-plus (but heuristic, not structured) |
| **Image content** | inline `Image` + `[image: WxH type]` fallback (`:296-329`) | `extractResultText` keeps text only — **images silently dropped** `events.go:175-183` | **images never render** |
| success/error bg | 3-way `toolSuccessBg`/`toolErrorBg`/`toolPendingBg` (`:229-233`) | same 3-way `tool_execution.go:85-94` | faithful |
| **Expand/collapse** | `setExpanded` on every tool/bash at creation + ctrl+o global toggle (`interactive-mode.ts:2717,2786,3018,3420`) | **`app.tools.expand` (ctrl+o) registered but NEVER wired** to any tool/bash `SetExpanded` — only the header calls it `orchestrator.go:205`; `ToolExecution` has no `expanded` field | **expand/collapse is dead code** |
| **Bash exit code** | red `(exit N)` (`bash-execution.ts:190`) | generic `(error)` `bash_execution.go:139` (no exit-code param) | **no exit-code number** |
| Bash truncation warning | "Output truncated. Full output: \<path\>" warning (`:194-197`) | missing `bash_execution.go:128-146` | silent truncation |
| Empty-result hiding | hides component if no content/images (`:222-224,331-333`) | renders empty colored box | empty results show a blank box |
| ANSI stripping | `render-utils.ts:39` | only bash strips `bash_execution.go:74`; generic tools don't `events.go:175-183` | raw ANSI codes shown for non-bash |

### Most visible misses (what you likely felt) — status after fixes
1. ✅ **Per-tool call renderers** — `read`/`grep`/`edit`/`write`/`find`/`ls` now render a compact call line (path in `accent`, grep `/pattern/`, edit `(N edits)`) instead of raw JSON. Unknown/MCP tools still fall back to JSON. (Result rendering stays generic text + diff.)
2. ✅ **Generic-tool truncation** — output capped to a 20-line preview + `… N more lines` hint (overridden when expanded).
3. ✅ **Expand/collapse wired** — `ctrl+o` (`app.tools.expand`) now toggles a shared flag applied to the header AND every tool/bash component in the chat (`ToolExecution.expanded` + `SetExpanded`).
4. ✅ **Bash: `(exit N)` + "Output truncated" + context cap** — `SetComplete` takes an exit code (rendered red as `(exit N)`); output capped at 2000 lines / 50KB (tail kept) with a warning-colored `Output truncated` notice.
5. ⚠️ **Image content** — no longer silently dropped; rendered as a `[image: mime]` marker (full inline `components.Image` rendering deferred — rare in tool output, needs terminal image-protocol support + mixed-content layout).
6. ✅ **Bash animated loader** — running state now shows an animated spinner + `(esc to cancel)` hint (the `Loader` was made render-lock-safe by splitting `renderText` from `RequestRender`).
7. ✅ **Empty-result hiding** — a completed tool with no args and no result renders nothing instead of a blank box.

**Deferred:** full per-tool *result* renderers (read line-numbers, grep `file:line:` formatting — the Go tools already return formatted strings, so the generic text view is acceptable); inline image rendering.

---

## Recommended next steps (by impact)

1. **Revert TEMP debug** (🔴) — shipping risk.
2. **3 missing message components** (branch/custom/skill) — share a pattern, high coverage gain.
3. **bash exit-code `(exit N)` + truncation warning** — most user-visible message gap.
4. **Working spinner** — restore animated bordered loader via a render-driven spinner (no `RequestRender` under lock).
5. **Session selector visual shell** — accent borders, `›` cursor, `selectedBg`, hints.
6. **OAuth status badges** + **scoped-models footer** — selector polish.
7. **Async autocomplete** + **paste-marker snapping** (tui) — input-fidelity.
8. Add a **CAVEATS entry** for the value-vs-pointer hazard.
