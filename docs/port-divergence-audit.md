# Port Divergence Audit — TS Reference vs Go Port

> **Created:** 2026-08-26 · **Status:** 13 of 32 findings fixed
>
> Four parallel audits compared `reference/coding-agent/` (TypeScript, source of
> truth) against the Go port, hunting for **behavioral divergences in code that
> exists on both sides** — extra guards, changed thresholds, dropped data — as
> opposed to missing features. Every finding below was verified by reading both
> codebases; several "Mirrors TS" header comments were proven wrong.

## How to use this file

- **Fixed** sections include file:line of the fix — verify with `git log`/blame.
- **Open** sections are ready to pick up: each has TS reference lines, Go sites,
  and the intended behavior. Work top-down within a severity tier.
- Re-run an audit after fixing a tier; the tools-audit was killed partway and
  only partially reported (it flagged `edit_diff.go` "major issues" without
  detail — unverified, needs re-auditing).

---

## ✅ FIXED — Critical (core session)

### 1. `Prompt()` silently dropped images, nextTurn and extension messages
- **Was:** `agent_session_prompt.go` built a full messages slice (user content
  + images + `pendingNextTurnMessages` + `before_agent_start` extension
  messages) then called `Agent.Prompt(ctx, text)` — text only. Images never
  reached the model; `img.(map[string]any)` could panic on typed images.
- **Fix:** builds `[]sdk.AgentMessage` (typed `llm.UserMessage`) and passes the
  whole slice — `normalizePromptInput` in agent-sdk already passes
  `[]AgentMessage` through. Added shared `buildUserContent()` helper.
- **Reference:** agent-session.ts:1052-1111.

### 2. Auto-retry self-cancelled instantly; `AbortRetry` was dead code
- **Was:** `retryChan` (cap 1) buffered at construction; `resolveRetry()` fired
  on every `agent_end`; the retry goroutine consumed the stale token and
  emitted "Retry cancelled" instead of ever waiting. `retryAbort` was never
  assigned so `AbortRetry()` did nothing.
- **Fix:** `retryChan` starts **nil** (TS: `_retryPromise` undefined), created
  only when a retry starts, cleared via `defer` on both branches.
  `resolveRetry()` no-ops when nil. `AbortRetry()` signals the channel.
  Removed dead `retryAbort` field.
- **Reference:** agent-session.ts:453-495, 589-595.

### 3. One failed manual `/compact` permanently disconnected the session
- **Was:** `Compact()` reconnected to the agent only on the success path; any
  error left the session eventless forever. `compactionAbort = func(){}` made
  `AbortCompaction()` a no-op.
- **Fix:** `defer` runs cleanup + `reconnectToAgent()` on **every** path (TS
  `finally` semantics). `compactionAbort` is a real `context.WithCancel`
  threaded through `runCompaction(ctx, …)` → provider calls + extension hook.
- **Reference:** agent-session.ts:1734-1737.

### 4. Retry counter + overflow flag reset on error responses
- **Was:** any assistant `message_end` reset `retryAttempt` (→ endless retry
  loops, never hits max 3) and `overflowRecoveryAttempted`; any `message_start`
  (incl. the retry's own stream) reset the overflow flag (→ compact→retry→
  compact loops).
- **Fix:** both flags reset **only when `StopReason != error`**; overflow flag
  resets only on **user** `message_start`.
- **Reference:** agent-session.ts:503, 554-565.

## ✅ FIXED — Medium (core session)

### 5. `ExpandPromptTemplates: false` was impossible
- **Was:** plain `bool` + `if !expandTemplates { expandTemplates = true }` made
  the opt-out unreachable; extension-sent literal `/foo` executed as a command.
- **Fix:** `PromptOptions.ExpandPromptTemplates` is now `*bool` (nil → default
  true). `SendUserMessage` passes explicit `false`. Only 3 usage sites existed;
  print/rpc modes keep the nil default. **API change** — any new caller setting
  the field must now pass `&value` or nil.
- **Reference:** agent-session.ts:968 (`?? true`), :1341.

### 6. `Subscribe`'s unsubscribe never removed anything
- **Was:** compared `&l == &listener` — addresses of two loop copies, always
  false → duplicate listeners / duplicated UI events on re-subscribe.
- **Fix:** monotonic registration ID (`eventListenerSeq`, parallel
  `eventListenerIDs` slice); unsubscribe removes by ID. `Dispose` clears both.
  (Go funcs aren't `==`-comparable, so identity removal needs a handle.)
- **Reference:** agent-session.ts:713-723.

### 7. `currentTimeMillis()` returned hardcoded `0`
- **Was:** "Placeholder" — all user/custom message timestamps were epoch-zero.
- **Fix:** `time.Now().UnixMilli()`.
- **Reference:** `Date.now()` at agent-session.ts:1063,1285.

### 8. Steer/followUp dropped images
- **Was:** `queueSteer`/`queueFollowUp` accepted `images []any`, built content
  from text only — images queued mid-stream were lost.
- **Fix:** both build `llm.UserMessage` via shared `buildUserContent()`
  (handles `llm.ImageContent`, `*llm.ImageContent`, extension `map[string]any`)
  + real timestamps.
- **Reference:** agent-session.ts:1217-1246.

### Also fixed in this effort (context-usage divergence, the audit's seed bug)
- `GetContextUsage()` had an extra `estimate.Tokens <= 0 → unknown` guard the
  reference lacks — fresh sessions showed `?/205k` instead of `0/205k`.
  Guard removed; `?` remains only for no-model and post-compaction-unknown
  (both faithful). See `agent_session_session.go`.

---

## ✅ FIXED — Destructive UX

### 9. Single Ctrl+C exits the app
- **Was:** engine-level `installExitListener` consumed ctrl+c and ran
  `go o.Stop()` on any press; `app.clear` was bound (keybindings.go:45) and
  advertised (header.go) but never handled.
- **Fix:** listener removed; `app.clear` registered on the editor action loop
  (`setupEditorCallbacks`) → `Orchestrator.ctrlC`: first press clears the
  editor, second within 500ms exits (`lastCtrlC` field, mirrors TS
  `lastSigintTime`). Tests: `orchestrator_test.go`.
- **Reference:** interactive-mode.ts:3202-3211, :2381.

### 10. Session delete: no confirmation, no active-session guard
- **Was:** `fireDelete` invoked `onDelete` on first Ctrl+D; deleting the
  session you're in succeeded.
- **Fix:** `fireDelete` refuses `target.current` via new `onError` callback
  ("Cannot delete the currently active session", wired to a chat notice in
  `buildSessionSelector`); otherwise enters a `confirmDelete` mode — Enter
  commits, Escape cancels, other keys ignored (rename-mode pattern; TS
  `confirmingDeletePath`). Tests: `session_selector_test.go`.
- **Reference:** session-selector.ts:157-161, 381-397, 521-536.

---

## ✅ FIXED — RPC protocol

### 11. RPC stdin fully serialized — `abort_bash` cannot preempt
- **Was:** `handleLine` ran inline on the reader goroutine (jsonl.go:61 →
  rpc_mode.go:299); a 10s `bash` blocked reading later lines, so `abort_bash`
  waited until the command finished.
- **Fix:** each stdin line dispatches on its own goroutine (TS `void
  handleInputLine(line)`, rpc-mode.ts:744); responses interleave via the
  existing `outputMu`. `bashAbort` is now mutex-guarded (was only
  single-goroutine-safe by accident). Known follow-up: concurrent commands
  can race the unsynchronized `s.session` pointer across a rebind — same
  logical overlap as TS await points, unaddressed.
- **Reference:** rpc-mode.ts:737-750.

### 12. RPC response `data` emits PascalCase (wire break)
- **Was:** untagged core structs marshaled raw: `SessionReplacementResult`,
  `ModelCycleResult`, `BashResult`, `CompactionResult`, `SessionStats` +
  `TokenStats`, `SourceInfo` — new_session/switch_session, cycle_model, bash,
  compact, get_session_stats, get_commands all leaked PascalCase fields.
- **Fix:** camelCase json tags on all six structs (TS wire names;
  `BashResult.ExitCode`/`FullOutputPath` omitempty — TS drops undefined).
  Pinned by `TestRpcDataShapes_CamelCase` (rpc_types_test.go).
- **Blast radius (accepted):** `CompactionResult` tags also camelCase the
  nested payload of `compaction_end` events on the RPC + print-mode streams
  (mixed-case envelope; TS is fully camelCase). No persistence impact —
  `BashResult`/`CompactionResult` are copied into separately-tagged types
  before session writes.
- **Noted (new divergence, unfixed):** Go `CompactionResult` is structurally
  divergent from TS (`firstKeptEntry` object vs `firstKeptEntryId` string;
  extra tokensAfter/readFiles/modifiedFiles/error).
- **Reference:** rpc-types.ts:76-85,117,135,174-176; agent-session.ts:209-226;
  bash-executor.ts:29-40; compaction.ts:103-109; source-info.ts:6-12.

### 12b. `extension_error` drops the `event` field
- **Fix:** added `"event": e.Event` to the emission map (rpc_mode.go,
  OnError handler) — wire now matches TS `{type, extensionPath, event, error}`.
- **Reference:** rpc-mode.ts:341.

---

## 🟠 OPEN — Core session (remaining)

### 13. `throwIfExtensionCommand` panics instead of erroring
- `agent_session_prompt.go` — `panic(...)` in a library path; TS throws a
  catchable error. Callers (Steer/FollowUp) should return an error.

### 14. `waitForRetry()` after `agent.prompt()` has no Go counterpart
- agent-session.ts:1110 — Go fires `Prompt` and returns; check whether the
  retry-promise timing needs an equivalent wait (may be moot after fix #2 —
  verify retry now actually runs end-to-end with a live provider).

### 15. `turn_end` events stuff the message into `RetryError`
- `agent_session.go` `EventAgentEnd`/`turn_end` path — shape looks wrong;
  verify against TS event payloads.

### 16. `GetAvailableThinkingLevels` returns `[off]` for no-model
- agent-session.ts:1553 returns all five; `agent_session_model.go:230`
  returns `[off]`.

---

## 🟠 OPEN — TUI chrome (medium)

### 17. Auto-retry status: no loader, no escape-to-cancel, word order flipped
- TS interactive-mode.ts:2913-2931 — `Retrying (X/Y) in Ns... (esc to cancel)`
  as animated Loader; Escape re-wired to `abortRetry()` (:2906-2909).
- Go `interactive/retry.go` — plain Text, `"Retrying in %ds... (attempt %d/%d)"`,
  no escape wiring. (Core `AbortRetry` now works after fix #2 — wire it.)

### 18. Working spinner vanishes between tool calls
- TS ties loader to `agent_start`→`agent_end` (:2651-2655, :2818-2822); Go ties
  to message start/end (`interactive/events.go`) — blanks during tool runs.

### 19. Compaction status: one generic label, no escape-to-cancel
- TS :2845-2855 — three variants (manual / overflow / auto) + Escape→abort.
  Go `events.go` — hardcoded string always. (Core abort now works after fix
  #3 — wire it.)

### 20. Footer provider count cached at construction
- TS footer.ts:167 evaluates per render; Go `footer.go` `NewFooter` caches —
  `(provider) model` prefix never appears after `/login` until restart.

### 21. Model selector hides registry errors when list non-empty
- TS model-selector.ts:266-284 appends error lines even with models present +
  unconditional `Model Name:` footer; Go returns early on empty list.

### 22. Pending-queue previews: hard 80-char cut, no spacer
- TS :3593-3608 — Spacer + width-aware TruncatedText + "↳ … to edit all"
  hint (hint omission is documented); Go `working.go` rune-slices at 80.

### 23. Compaction summary always fully expanded
- TS collapsed by default with ctrl+o toggle; Go renders full markdown.
  Header documents the deferral — decide whether to keep deferring.

---

## 🟡 OPEN — CLI / modes

### 24. `--mode <invalid>` not consumed → becomes the prompt
- TS args.ts:74-78 consumes the value then validates; Go `cli/args.go:143-148`
  has `i++` inside the validity check. `ycode --mode bogus "real prompt"` →
  prompts with "bogus". (`--thinking` does it correctly nearby — copy that.)

### 25. URL-scheme sources treated as local paths
- TS paths.ts:23-37 — non-local prefixes `npm: git: github: http: https: ssh:`;
  Go `helpers.go:150-155` uses `filepath.IsAbs(p) || Contains(p, "://")` —
  `github:owner/repo` becomes `<cwd>/github:owner/repo`. Affects
  `--extension/-e --skill --prompt-template --theme`.

### 26. Print mode collapses per-block newlines
- TS print-mode.ts:138-142 writes `text\n` per block; Go concatenates then one
  `Fprintln` → `["A","B"]` prints `AB\n` not `A\nB\n`.

### 27. `images.autoResize` setting ignored + stub drops images
- TS main.ts:640-644 reads the setting (default true); Go `cmd/ycode/main.go`
  hardcodes true, `GetImageAutoResize` doesn't exist. Interacts with the
  documented `tools.ResizeImage` stub: oversized images get silently dropped.

### 28. Dialog requests lose `title`/`statusKey`; select discards `defaultIdx`
- rpc-mode.ts:131-170 sends them; `ExtensionUIContext` signatures lack the
  params so the wire fields never populate.

### 29. `cycle_model` "no result" omits `data` instead of `null`
- TS sends `data: null`; Go `Data any` + `omitempty` drops the key.

---

## 🟡 OPEN — Compaction internals (quality, not correctness)

### 30. Tool-call args serialized with whole ToolCall per key
- TS utils.ts:134-137 `` `${k}=${JSON.stringify(v)}` ``; Go
  `compaction/utils.go:143-148` marshals the entire ToolCall per key — garbage
  input degrades summaries.

### 31. `FindCutPoint` keeps walking when it should break
- TS compaction.ts:411-420 outer break is unconditional; Go `compaction.go:335-344`
  `goto done` only when found → slightly over-aggressive compaction.

### 32. `completeSimple` ignores `maxTokens`
- TS clamps to `min(0.8×reserve, model.maxTokens)`; Go computes the constants
  but never passes them → summaries can blow the budget.

---

## ⚠️ Unverified / needs re-audit

- **Tools audit was killed partway** — it reported "major issues in
  edit_diff.go" with no detail before stopping. Re-run that audit before
  trusting read/write/edit/bash/glob/grep parity.
- Audit also mentioned without detail: OSC 133 shell integration partial;
  markdown theme cache missing; provider parity unverified.
- Known documented deferrals (do NOT re-audit as divergences): qjs threading
  (tracked in `.versions/upcoming/0.1.1.md`), `tools.ResizeImage` stub, image
  dimension notes, branch/skill/custom message renderers.

## Verified clean (no action)

Interactive TUI: countdown_timer, keybinding_hints, user_message,
assistant_message, visual_truncate, custom_editor, thinking/theme selectors,
formatTokens, footer context-color thresholds (>90/>70).
CLI: flag set/shorthands/unknown-flag capture, `--print` next-arg rules, env
precedence chain, `@file` ordering, exit codes, migration run order, session
precedence.
