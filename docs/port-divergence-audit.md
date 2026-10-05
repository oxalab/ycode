# Port Divergence Audit — TS Reference vs Go Port

> **Created:** 2026-08-26 · **Status:** 34 of 34 findings fixed
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

## ✅ FIXED — Core session

### 16. `GetAvailableThinkingLevels` returns `[off]` for no-model
- **Was:** no model → `[off]`; TS returns all five `THINKING_LEVELS` when
  `this.model` is undefined (agent-session.ts:1553-1556, :238) — the thinking
  picker/settings offered only "off" before a model was chosen.
- **Fix:** only a *selected* non-reasoning model narrows the list to `[off]`;
  no-model and reasoning-model both return the full list. Set/cycle behavior
  already matched TS. Tests: `agent_session_model_test.go`.
- **Noted (structural, unfixed):** TS additionally filters reasoning models
  per-model via `thinkingLevelMap` (can drop levels, include `xhigh`); Go's
  `llm.Model` has no such field, so reasoning models always get the fixed 5.
- **Reference:** agent-session.ts:1553-1556, :238; models.ts:48-59.

### 15. `turn_end` events stuff the message into `RetryError`
- **Was:** `agentEventToSessionEvent` stringified the whole message struct into
  `RetryError` on every `turn_end` (`fmt.Sprintf("%v", e.Message)`) — wire
  clients (RPC/print JSON) got a garbage blob on every turn and no payload.
  Verified: the three real `auto_retry_*` emit sites already match TS 1:1.
- **Fix:** `AgentSessionEvent` gains structured `Message any` +
  `ToolResults []llm.ToolResultMessage[any]`; `turn_end` carries them and
  leaves `RetryError` empty (mirrors TS turn_end `{ message, toolResults }`,
  pi/packages/agent/src/types.ts:401). Tests: `agent_session_events_test.go`.
- **Noted (adjacent, unfixed):** `auto_retry_end` wire field naming
  (`RetrySuccess/RetryAttempt/RetryError` vs TS `success/attempt/finalError`)
  — part of the documented PascalCase event-wire divergence; Go TUI never
  displays retry-failure `RetryError` (TS shows "Retry failed after N
  attempts: …") — deferred to the TUI-chrome redo tier.
- **Reference:** pi/packages/agent/src/types.ts:401; agent-session.ts:139-140,
  561-568, 2451-2492.

### 14. `waitForRetry()` after `agent.prompt()` has no Go counterpart
- **Was:** NOT moot (audit's "may be moot" disproven). `Prompt()` returned at
  the initial agent_end: print mode's `extractPrintResult` raced the retry
  pipeline (exit 1 even when a retry would succeed), multi-message print sent
  the next prompt during backoff so the retry's `Continue` failed
  "already processing" and was silently dropped. Also `Abort()` never called
  `AbortRetry()` (TS :1388) — a retry could fire after the user aborted.
- **Fix:** `retryChan` now spans the whole retry chain (all backoffs +
  retried turns, like TS `_retryPromise`); per-cycle aborts moved to a new
  `backoffAbort` channel. `resolveRetry` nils the chan (also kills a
  latent stale-token instant-cancel after max-retries). `Prompt()` calls new
  `waitForRetry()` (TS :1108-1110, :2521-2527); `Abort()` calls
  `AbortRetry()`; `Continue` errors settle the chain so the wait can't
  deadlock. `Abort()`'s unguarded `bashAbort` access now uses `bashAbortMu`.
  Tests: `agent_session_retry_test.go`.
- **Reference:** agent-session.ts:1108-1110, 1388, 2432-2527.

### 13. `throwIfExtensionCommand` panics instead of erroring
- **Was:** `panic(fmt.Sprintf(...))` in a library path; an RPC
  `steer`/`follow_up` of an extension command killed the process (dispatch
  runs on bare `go srv.handleLine` goroutines, no recover).
- **Fix:** returns the TS error (identical message) — `Steer`/`FollowUp`
  propagate it and rpc_mode.go's existing `errResp` plumbing turns it into an
  RPC error response. Tests: `agent_session_prompt_test.go`.
- **Reference:** agent-session.ts:1251-1261, :1184, :1204.

---

## 🟠 OPEN — TUI chrome (medium)

### 17. Auto-retry status: no loader, no escape-to-cancel, word order flipped — ✅ FIXED (2026-09-17)
- **Was:** Go retry status was a static Text with flipped word order and no
  cancel affordance: `"Retrying in %ds... (attempt %d/%d)"` (retry.go); no
  escape wiring at all (OnEscape never set — escape only cancelled
  autocomplete); `handleRetryEnd` ignored `RetrySuccess`/`RetryError`, so a
  final failure was invisible.
- **Fix:** animated `components.Loader` (warning spinner/muted message, like
  the TS statusContainer loader) with the reference's word order + key hint:
  `Retrying (X/Y) in Ns... (escape to cancel)` (keyText(appInterrupt); the Go
  key formatter spells "escape"); live countdown via the existing
  CountdownTimer → `SetMessage`; OnEscape saved/swapped to
  `go session.AbortRetry()` during retry and restored on end; final failure
  appends `Retry failed after N attempt(s): <error>` to the chat (the
  user-aborted chain reports "Retry cancelled", which keeps its own notice
  path — no duplicate line). Loader + escape state disposed on Stop().
  Tests: `TestRetryStatusText` (exact wording + clamp), new
  `TestRetryLifecycleLoaderEscapeAndFailureNotice` (loader present/cleared,
  OnEscape swapped/restored, failure/success/cancelled notice behavior)
  using a local `nullTerminal` engine.
- **Reference:** pi/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2904-2957.

### 18. Working spinner vanishes between tool calls — ✅ FIXED (2026-09-17)
- **Was:** the working loader was shown on `message_start` and hidden on
  `message_end` (events.go), so it vanished for the entire tool-execution
  gap between model calls; Go had no `EventAgentStart`/`EventAgentEnd`
  handling at all. TS creates the loader once on `agent_start` and stops it
  on `agent_end`; no mid-run event touches it.
- **Fix:** loader lifecycle moved to `agent_start` (which also tears down any
  still-active retry chrome via a shared `stopRetryChrome()` helper — TS
  stops the retry loader there since `auto_retry_end` may not have arrived)
  and `agent_end`; the retry-path cleanup in Stop() now uses the same
  helper. Tests: `TestWorkingLoaderSpansToolGap` (loader alive across
  message_end + tool execution + second model call, hidden on agent_end) and
  `TestAgentStartTearsDownRetryChrome`.
- **Noted (cosmetic, unfixed):** TS appends " (esc to interrupt)" to the
  working message via a hint update (interactive-mode.ts:1797-1798); Go
  shows static "Working...".
- **Reference:** pi/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2632-2657, 2818-2822.

### 19. Compaction status: one generic label, no escape-to-cancel — ✅ FIXED (2026-09-17)
- **Was:** compaction_start showed one hardcoded `"Compacting context…
  (esc to cancel)"` (also `…` where TS uses `...`), reason ignored; escape
  was a dead hint — never wired, even though `AgentSession.AbortCompaction()`
  exists and cancels the real context; compaction_end silently dropped
  `CompactionAborted`/`ComapctionError` and reused the generic working
  loader (clobberable by Working.../compaction swaps).
- **Fix:** dedicated compaction chrome (compaction_status.go, mirroring
  retry.go): reason-specific labels — `Compacting context... (escape to
  cancel)` / `Context overflow detected, Auto-compacting... (escape to
  cancel)` / `Auto-compacting... (escape to cancel)` (TS
  interactive-mode.ts:2845-2849); escape swapped to
  `go session.AbortCompaction()` and restored on end; end states render
  TS's notices — `Compaction cancelled` (error-styled, manual) vs
  `Auto-compaction cancelled` (dim, auto), the error message as an
  error-styled line, and the summary card on success (unchanged). Teardown
  in Stop() via `stopCompactionChrome()`. Test:
  `TestCompactionLifecycleLoaderEscapeAndNotices` (all three labels, escape
  swap/restore, all three end states).
- **Noted (unfixed, separate rendering divergence):** TS rebuilds the chat
  from post-compaction messages on success (`rebuildChatFromMessages`) and
  invalidates the footer; Go appends the summary card only.
  `CompactionWillRetry` queue flushing and terminal `setProgress` wiring
  also remain out of scope (no Go compaction queue).
- **Reference:** pi/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2835-2902.

### 20. Footer provider count cached at construction — ✅ FIXED (2026-09-17)
- **Was:** `Footer.provCount` was computed once in `NewFooter` with the
  comment "providers don't change mid-session" — wrong: `/login`,
  add-provider, logout, and `SetScopedModels` all change the set at runtime,
  so the `(provider) model` prefix stayed stale until restart. The helper
  also ignored scoped models where TS's `getModelCandidates` prefers them
  (interactive-mode.ts:3942-3945).
- **Fix:** count computed live in `Render` — `GetAvailable()` is a mutex +
  in-memory filter (model_registry.go:581-610), trivial per frame, and no
  trigger site can go stale; logout needs no `registry.Refresh()` because
  `HasConfiguredAuth` reads auth storage live. `distinctAvailableProviders`
  now prefers scoped models (TS parity) via a `countUniqueProviders` helper.
  Tests: `TestCountUniqueProviders`, `TestDistinctAvailableProviders_PrefersScoped`
  (scoped precedence proven against a registry-less session).
- **Reference:** pi/packages/coding-agent/src/modes/interactive/interactive-mode.ts:3942-3960;
  components/footer.ts:167.

### 21. Model selector hides registry errors when list non-empty — ✅ FIXED (2026-10-02)
- **Was:** TS model-selector.ts:266-284 appends error lines even with models
  present + unconditional `Model Name:` footer; Go returned early on empty
  list, and `loadModels` discarded `GetError()` entirely — a broken
  models.json was invisible whenever built-in models were available.
- **Fix:** `ModelSelectorComponent` now captures `registry.GetError()` during
  `loadModels` and renders it as red error lines after the list (error takes
  precedence over the footer and the empty text, matching the TS if-chain).
  The generic `Selector` gained an optional `FooterFn` hook — rendered after
  the scroll indicator and even on the empty list — used here for the muted
  `Model Name: <highlighted model>` footer that follows the cursor through
  filtering. Tests: `TestModelSelectorShowsRegistryErrorWithModelsPresent`,
  `TestModelSelectorShowsModelNameFooter`, `TestSelectorFooterRendersOnEmptyList`.
- **Reference:** pi/packages/coding-agent/src/modes/interactive/components/model-selector.ts:139-163,266-284.

### 22. Pending-queue previews: hard 80-char cut, no spacer — ✅ FIXED (2026-10-02)
- **Was:** TS :3593-3608 — Spacer + width-aware TruncatedText + "↳ … to edit all"
  hint (hint omission is documented); Go `working.go` rune-slices at 80.
- **Fix:** `renderPending` now adds the leading `Spacer(1)` when anything is
  queued and renders each preview via the TUI's width-aware `TruncatedText`
  (padding 1,0 — same as the TS constructor), dropping the hard 80-rune
  `truncateOneLine` cut. Newlines are no longer collapsed: TruncatedText shows
  the first line only, matching the reference. The "↳ … to edit all" hint stays
  omitted (documented: no Go handler). Tests: `TestRenderPendingSpacerAndPreviews`,
  `TestRenderPendingTruncatesToWidth`, `TestRenderPendingMultilineFirstLineOnly`
  (obsolete `TestTruncateOneLine` removed).
- **Reference:** pi/packages/coding-agent/src/modes/interactive/interactive-mode.ts:3593-3610.

### 23. Compaction summary always fully expanded — ✅ FIXED (2026-10-03)
- **Was:** TS compaction-summary-message.ts renders collapsed by default —
  `Compacted from N tokens (ctrl+o to expand)` — and ctrl+o
  (`app.tools.expand` → `toolOutputExpanded`, interactive-mode.ts:3418-3427)
  toggles every expandable chat child; Go rendered the full markdown
  unconditionally and its toggle only reached Tool/Bash components via an
  explicit type switch. Deferral was documented in the file header.
- **Fix:** `CompactionSummary` stores its inputs and rebuilds its box children
  via `updateDisplay()` — collapsed one-liner with the dim `keyText` hint vs
  expanded markdown body — with `SetExpanded(bool)` (reference
  `setExpanded`/`updateDisplay`). `toggleToolOutputExpansion` now uses an
  `expandable interface{ SetExpanded(bool) }` type assertion, the Go equivalent
  of TS `isExpandable` duck-typing, so the summary joins Tool/Bash components
  automatically. `handleCompactionEnd` adds the leading Spacer(1) TS adds and
  creates the card with `SetExpanded(o.headerExpanded)`. Out of scope:
  `rebuildTranscript` still skips compaction entries (part of the #19-documented
  rebuild gap); TS `toLocaleString` comma-grouping of the token count
  (cosmetic). Tests: `TestCompactionSummaryCollapsedByDefaultAndToggles`,
  `TestCompactionSummaryInheritsExpandStateAtCreation`.
- **Reference:** pi/packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts:10-59;
  interactive-mode.ts:3024-3029,3418-3427.

---

## 🟡 OPEN — CLI / modes

### 24. `--mode <invalid>` not consumed → becomes the prompt — ✅ FIXED (2026-09-10)
- **Was:** the `i++` sat inside the validity check (args.go:143-148) —
  `ycode --mode bogus "real prompt"` sent "bogus" as the initial prompt.
- **Fix:** value consumed unconditionally before validating, silent on
  invalid (TS args.ts:74-78 sends no diagnostic for --mode). Locked by
  `TestParseArgs_ModeValidation` (messages empty, no diagnostics).
- **Reference:** pi/packages/coding-agent/src/cli/args.ts:74-78.

### 25. URL-scheme sources treated as local paths — ✅ FIXED (2026-09-10)
- **Was:** `resolveOneCliPath` (cmd/ycode/helpers.go) treated a value as
  non-local only if absolute or containing `://` — `--skill github:owner/repo`
  cwd-joined into `<cwd>/github:owner/repo`, then failed `os.Stat` → error
  diagnostic + exit 1. Core already had the correct six-prefix `isLocalPath`
  (resource_loader.go) — only the CLI helper was wrong.
- **Fix:** exported `core.IsLocalPath`; `resolveOneCliPath` now passes the six
  schemes (npm: git: github: http: https: ssh:) through untouched (TS
  paths.ts:23-37 parity). The `filepath.IsAbs` clause stays — Go's
  `filepath.Join`, unlike TS `resolve()`, doesn't treat absolute operands as
  final. Locked by `TestResolveCliPaths`.
- **Noted (unrelated):** `PackageManager` interface (resource_loader.go:90)
  declares a 2-arg `ResolveExtensionSources` while `DefaultPackageManager`
  implements a 3-arg error-returning version — wiring inconsistency, separate look.
- **Reference:** pi/packages/coding-agent/src/utils/paths.ts:23-37; main.ts:381-383.- TS paths.ts:23-37 — non-local prefixes `npm: git: github: http: https: ssh:`;
  Go `helpers.go:150-155` uses `filepath.IsAbs(p) || Contains(p, "://")` —
  `github:owner/repo` becomes `<cwd>/github:owner/repo`. Affects
  `--extension/-e --skill --prompt-template --theme`.

### 26. Print mode collapses per-block newlines — ✅ FIXED (2026-09-10)
- **Was:** `extractPrintResult` concatenated all text blocks, then a single
  `Fprintln` — blocks `["A","B"]` printed `"AB\n"` instead of TS's `"A\nB\n"`
  (one `writeRawStdout(text+"\n")` per block, print-mode.ts:138-142). An
  all-empty join also suppressed output entirely; TS emits a bare `\n` per
  empty text block.
- **Fix:** `printResult.texts []string` (one entry per text block, empty
  blocks included); the text branch loops `Fprintln` per entry. Thinking
  blocks excluded on both sides (faithful). `TestExtractPrintResult_TextBlocks`
  inverted to assert the non-fused shape.
- **Reference:** pi/packages/coding-agent/src/modes/print-mode.ts:128-145.

### 27. `images.autoResize` setting ignored + stub drops images — ✅ FIXED (2026-09-10)
- **Was:** no `GetImageAutoResize` (main.go hardcoded `true`); `Settings.Images`
  json tag was `imageSettings` where TS writes `images` (settings_types.go) —
  the setting could never load; the session's read tool never received the
  flag (`CreateAllToolDefinitions(cwd, nil)`) and on resize failure silently
  attached oversized bytes instead of TS's omission note (read.go vs
  read.ts:255-262).
- **Fix:** `GetImageAutoResize` (default true) + `SetImageAutoResize` (writes
  the global backing struct — save() rebuilds the merged view); tag corrected
  to `images`; main.go reads the getter; `buildRuntime` threads it into
  `CreateAllToolDefinitions` (TS agent-session.ts:2334-2345); read tool
  degrades to the "[Image omitted: …]" note on resize failure.
  Tests: `settings_manager_test.go` (default, TS-key load, setter).
- **Noted (deferral stands):** the real `ResizeImage` implementation remains
  the documented deferral — with default true, oversized @file images still
  degrade to the omission note (byte-identical to TS's degraded path when its
  resizer fails). Settings-UI toggle wiring (`auto-resize-images` item
  unpopulated in both TUI builders) also deferred.
- **Reference:** pi/packages/coding-agent/src/main.ts:640-644;
  settings-manager.ts:966-976; agent-session.ts:2334-2345; read.ts:255-262.

### 28. Dialog requests lose `title`/`statusKey`; select discards `defaultIdx` — ✅ FIXED (2026-09-17)
- **Was:** `ExtensionUIContext` signatures had no title/key params
  (`Select(options, defaultIdx, timeout)`, `Confirm(message, timeout)`,
  `Input(message, placeholder, timeout)`, `Status(message)`), so the wire
  fields `Title`/`StatusKey` never populated (rpc_mode.go emitted
  `{method, options, timeout}` / `{method, statusText}`); RPC's Select
  discarded defaultIdx (`_`); Input stuffed the prompt into wire `Message`
  where the reference sends title+placeholder only; RPC had no `Editor` at
  all; interactive confirm never displayed title/message (bare Yes/No list).
- **Fix:** signatures now pi-shaped with title first (`Select(title, options,
  defaultIdx, timeoutMs)`, `Confirm(title, message, timeoutMs)`,
  `Input(title, placeholder, timeoutMs)`, `Editor(title, prefill)`,
  `Status(key, message)`); rpc buildUIContext populates Title/StatusKey and
  forwards `defaultIdx` on the select wire (ycode extension — the reference
  has no defaultIndex; unknown fields are ignored by hosts that lack it) and
  wires the RPC editor dialog (no timeout, like pi rpc-mode.ts:247-264); the
  JS glue is pi-shaped (`ui.select(title, options, {timeout, defaultIdx})`,
  `ui.confirm(title, message, opts)`, `ui.input(title, placeholder, opts)`,
  `ui.editor(title, prefill)`, `ui.status(key, text)`); interactive
  extensionSelect renders an accent-bold title header (extension-selector.ts
  layout: border → spacer → title → spacer → list → border) and confirm shows
  `title\nmessage` above Yes/No (interactive-mode.ts:2056). Tests: RPC
  request-shape test (title/statusKey/defaultIdx/input-has-no-Message/editor
  round-trip, via an injectable `out io.Writer` on rpcServer) + rewritten
  pi-shaped JS glue tests.
- **Noted (follow-ups):** JS `ui.select` still resolves an index; the
  reference resolves the option string (`string | undefined`) — real
  pi-extension compat gap, separate story. Keyed footer statuses (reference
  keeps a `Map<key, text>`; Go footer has one slot). Dialog timeout countdown
  + AbortSignal in interactive. RPC `setWidget`/`setTitle`/`set_editor_text`
  remain unwired.
- **Reference:** pi/packages/coding-agent/src/modes/rpc/rpc-mode.ts:83-143,
  247-264; rpc/rpc-types.ts:213-238; core/extensions/types.ts:89-94, 124-138;
  modes/interactive/components/extension-selector.ts:42-46; footer-data-provider.ts:132-138.

### 29. `cycle_model` "no result" omits `data` instead of `null` — ✅ FIXED (2026-09-17)
- **Was:** `okResp(id, CmdCycleModel, nil)` put an untyped nil in `Data any`,
  so `omitempty` dropped the key — wire was `{"success":true}` where TS sends
  `{"success":true,"data":null}` (rpc-mode.ts:466; response type `…| null`).
  `cycle_thinking_level` had the byte-identical divergence (rpc-mode.go:460 vs
  rpc-mode.ts:488, response type rpc-types.ts:148-153).
- **Fix:** typed-nil pass-through — `omitempty` only omits when the interface
  itself is nil, so the nil `*ModelCycleResult` flows through (special case
  deleted) and cycle_thinking_level emits `(*sdk.ThinkingLevel)(nil)`; both
  marshal as `"data":null`, mirroring the TS success() helper's
  undefined-vs-null distinction. `NewSuccessResponse` doc updated. Test:
  `TestRpcResponse_NullDataVsOmitted` (untyped nil omits / typed nils emit
  `"data":null`).
- **Noted (left as-is):** `get_last_assistant_text` — TS's
  `getLastAssistantText` returns `string | undefined`, so with no text TS
  actually serializes `{"text":{}}` (undefined-in-object quirk) while its own
  declared type says `string | null`; Go's `{"text":""}` is benign.
- **Reference:** pi/packages/coding-agent/src/modes/rpc/rpc-mode.ts:57-66,
  463-469, 485-490; rpc-types.ts:130-137, 148-153, 171-176.

---

## 🟡 OPEN — Compaction internals (quality, not correctness)

### 30. Tool-call args serialized with whole ToolCall per key — ✅ FIXED (2026-09-17)
- **Was:** `SerializeConversation` (compaction/utils.go:144) did
  `json.Marshal(b)` inside the per-argument loop — the summarizer prompt
  contained the entire ToolCall struct (`type`/`id`/`name`/nested
  `arguments`/thought signature) duplicated once per argument key, and the
  argument values themselves never appeared.
- **Fix:** marshal the per-key value only — `json.Marshal(b.Arguments[k])` —
  matching TS utils.ts:132-138 (`read(path="/a.go", limit=10)`). The sorted
  arg order (vs TS insertion order) stays the documented intentional
  deviation. New `utils_test.go` pins `read(limit=10, path="/a.go")` and
  rejects struct-JSON leakage (first test in the compaction package).
- **Noted (next compaction story):** Go's `getMessageFromEntry`
  (compaction.go:109-112) drops `ToolResultMessage` entries where TS's
  (compaction.ts:80-82) includes them — separate divergence, unfixed.
- **Reference:** pi/packages/coding-agent/src/core/compaction/utils.ts:132-138.

### 31. `FindCutPoint` keeps walking when it should break — ✅ FIXED (2026-09-17)
- **Was:** when the keep-recent token budget was first exceeded at an entry
  with no valid cut point at/after it (typically a trailing tool result),
  the backward walk continued to older entries and cut at a user/assistant
  deeper in history — Go summarized more and kept fewer recent messages than
  TS, which breaks unconditionally after the inner search (compaction.ts:419),
  keeping the default `cutPoints[0]`. Worst case: TS keeps everything from
  the first message; Go discarded a whole span.
- **Fix:** unconditional `break` after the inner cut-point search inside the
  budget-exceeded block (the found case exits via the existing `goto done`).
  New `TestFindCutPoint` pins the regression (budget exceeded at a tool
  result → `FirstKeptEntryIndex == 0`), the within-budget case, and the
  split-turn case (cut at assistant → `IsSplitTurn`, `TurnStartIndex` at the
  starting user).
- **Noted (adjacent, unfixed):** Go treats `BranchSummary`/`CustomMessage`
  as user messages for split-turn purposes (TS only role `"user"` → TS
  reports split turn there); Go's `findValidCutPoints` omits the
  `bashExecution`/`custom`/`compactionSummary` roles TS includes
  (compaction.ts:307-310).
- **Reference:** pi/packages/coding-agent/src/core/compaction/compaction.ts:386-448.

### 32. `completeSimple` ignores `maxTokens` — ✅ FIXED (2026-09-17)
- **Was:** `completeSimple` (agent_session_compaction.go) accepted
  `maxTokens int` but never assigned it to `opts.StreamOptions.MaxTokens` —
  compiles silently (unused params are legal). Providers then substituted
  their defaults (`min(model.maxTokens, 32000)` OpenAI-family,
  `model.maxTokens/3` Anthropic) instead of the caller's budget (compaction
  summary `80%` of reserve capped by model, turn prefix `50%`, branch
  summary `2048`); summaries could hit the `max_tokens` stop reason and
  truncate.
- **Fix:** `opts.StreamOptions.MaxTokens = &maxTokens` guarded on `>0` — the
  guard matters because Go's `getDefaultMaxTokens` would send a literal `0`
  where TS's `options?.maxTokens || default` falls back. New
  `TestCompleteSimple_PassesMaxTokens` pins both paths via a capturing
  provider registered against the llm api registry (4096 → authoritative;
  0 → nil, defaults preserved).
- **Reference:** pi/packages/ai/src/stream.ts:52-58;
  providers/simple-options.ts:3-20; anthropic.ts:893;
  coding-agent/test/compaction-summary-reasoning.test.ts:119-132.

### 33. Interactive TUI ignores the CLI initial message — ✅ FIXED (2026-09-10, found during #24 live check)
- **Was:** `launchInteractivePi` discarded `cli.InitialMessageResult`
  (runtime_pi.go) and `Options.InitialMessage` was declared but never read —
  `ycode "hi"` opened the TUI with an empty session. TS sends `initialMessage`
  (+`initialImages`), then leftover positionals as sequential prompts
  (interactive-mode.ts:738-756).
- **Fix:** `launchInteractivePi` threads `InitialMessage`/`InitialImages`/
  `InitialMessages` into Options; `Run()` calls `submitInitialPrompts()` after
  engine start — one goroutine, sequential Prompts (ordering preserved by #14),
  each rendering the user bubble via `sendInitialPrompt` (handleSubmit's tail
  without editor history or slash/bash routing).
- **Reference:** pi/packages/coding-agent/src/modes/interactive/interactive-mode.ts:738-756.

### 34. OpenAI-completions provider sets a 60s overall client timeout (Go-only) — ✅ FIXED (2026-10-02)
- **Was:** `openai-completions.go` built its client as
  `&http.Client{Timeout: 60 * time.Second}` — the only provider with a
  timeout (every other one uses a bare `&http.Client{}`, as does the TS
  reference). `http.Client.Timeout` spans the entire response body read, so
  any stream longer than 60s died mid-body with
  `context deadline exceeded (Client.Timeout or context cancellation while
  reading body)` — a failure generator, not hang protection. The reference
  sets no timeout; hang protection is the user's escape-to-interrupt.
- **Fix:** timeout removed, bare client (TS parity); guard test
  `TestProvidersHaveNoOverallClientTimeout` forbids `http.Client{Timeout`
  in any provider file, plus `TestStreamOpenAICompletionsSpacedChunksComplete`
  proves a trickle-fed SSE body completes whole against an httptest server.
- **Reference:** pi/packages/llm/src/providers/openai-completions.ts (no
  timeout anywhere in pi/packages/llm/src/providers).

---

## 🔧 Tools re-audit findings (2026-10-03) — OPEN, fix story-by-story

Re-ran the killed tools audit via a three-way parallel comparison of every
file in `core/tools/` against pi's `core/tools/*.ts`. The killed run's
"major issues in edit_diff.go" are confirmed real (verified by a throwaway
test: `GenerateDiffString` panics with `index out of range` on any non-empty
diff; no recover in the tool path; no edit_diff tests exist). Findings
grouped by area; severities: HIGH = wrong behavior/output or crash, MEDIUM =
missing feature/surface, LOW = cosmetic/wording.

### A. edit_diff.go / edit.go / write.go — the "major issues"

- **A1 HIGH edit_diff.go:315 — ✅ FIXED (2026-10-03)** — LCS inner loop condition is `i <= n` instead
  of `j <= n` → `dp[i][j]` index-out-of-range panic on any non-empty diff,
  i.e. **every successful edit panics** (edit.go:207, no recover, file already
  written). Empirically confirmed. Fixed + `TestGenerateDiffString_NoPanicAndOutput`.
- **A2 HIGH edit_diff.go:25 — ✅ FIXED (2026-10-03)** — `NormalizeToLF` replaces `"\r\n"` with the
  literal `"n"` → CRLF files corrupted on match and written back corrupted.
  Fixed + `TestNormalizeToLF`.
- **A3 HIGH edit_diff.go:263 — ✅ FIXED (2026-10-03)** — splice uses `edit.matchIndex+edit.matchIndex`
  instead of `+matchLength` → wrong content written or slice panic.
  Fixed + `TestApplyEditsToNormalizedContent_Splice`.
- **A4 HIGH edit_diff.go:12-22 — ✅ FIXED (2026-10-03)** — `DetectLineEnding` branches inverted →
  LF-only files get rewritten as CRLF. Fixed (TS-verbatim port) +
  `TestDetectLineEnding`, `TestEditRoundTrip_PreservesLineEndings`.
- **A5 MED edit_diff.go:49** — fuzzy normalization trims only tabs, TS
  trims all trailing whitespace (`trimEnd`).
- **A6 MED edit_diff.go:45** — missing NFKC normalization for fuzzy match.
- **A7 MED edit_diff.go:279-305** — files >5000 lines fall back to whole-file
  remove+add diff; npm `diff` computes a real diff at any size.
- **A8 MED edit.go:66-100** — `prepareEditArguments` (repairs `edits` sent as
  JSON string) never wired into the ToolDefinition; TS `prepareArguments` is.
- **A9 MED edit.go:233-249** — result lacks all diff UI chrome: no
  `renderDiff` colors/intra-line highlight, no streaming preview diff, no
  success/error header backgrounds (TS edit.ts:434-483, diff.ts:79-147).
- **A10 MED write.go:105-130** — content preview drops ALL blank lines (TS
  keeps interior ones), no `... (N more lines, M total, to expand)` hint, no
  syntax highlighting.
- **A11 LOW** — error-string drift across edit_diff.go/edit.go ("THe old
  text", "overalp", "whitespaces", "atleast", "inavlid arguments", lowercase
  first letters, missing periods); read.go:108 same.
- **A12 LOW** — `firstChangedLine` `-1` sentinel survives `omitempty` (TS
  omits); `file_path` alias not accepted by edit/write input structs; access
  check `os.Stat` vs R_OK|W_OK; write.go byte count vs TS UTF-16 length.

### B. read.go / truncate.go / render_utils.go / path_utils.go

- **B1 HIGH render_utils.go:152-193** — `GetTextOutput` type-switches on
  `*TextContentBlock`/`*ImageContentBlock`, but tools store
  `*llm.TextContent`/`*llm.ImageContent` → always returns `""`; TUI tool
  results show no output.
- **B2 HIGH truncate.go:106** — `TruncateHead` loop missing the `i < maxLines`
  bound → files with >2000 short lines (<50KB) are NOT truncated while the
  footer claims they were.
- **B3 MED read.go:112-114** — access check is `os.Stat` + generic
  "cannot read file"; TS uses R_OK access and surfaces the real fs error
  (EACCES/ENOENT indistinguishable in Go).
- **B4 MED read.go:136** — image-omitted text missing the
  `Read image file [mime]` first line; **B5 MED** non-vision-model image note
  not ported (no model context in Go Execute).
- **B6 MED path_utils.go:26-30** — `tryNFDVariant` is a no-op TODO (TS does
  `normalize("NFD")`); **B7 MED** Windows `/foo`-style root-relative paths
  resolve against cwd, TS resolves against the drive root.
- **B8 MED render_utils.go:12** — ANSI stripper only handles simple SGR CSI;
  OSC/private-mode escapes leak into the TUI (npm strip-ansi handles all).
- **B9 LOW** — terminal-capability detection diverges (case-sensitive
  `iTerm.app`, misses ghostty/wezterm/kitty paths); image fallback text
  format; `ycode-output` vs `pi-output` temp prefix; rune-vs-UTF-16
  truncation cut points; offset-error casing; `TruncatedBy: ""` vs `null`;
  output_accumulator split-UTF-8 (footnoted).

### C. bash / find / grep / ls / queue / schema

- **C1 HIGH bash.go:253-258** — `wg.Wait()` blocks on inherited stdio of
  backgrounded children (`daemon &` hangs the tool until the descendant
  exits); TS engineered a 100ms post-exit grace + stream destroy
  (`waitForChildProcess`) precisely to avoid this.
- **C2 HIGH output_accumulator.go:23-27** — random ID buffer never filled →
  every spilled temp file is `ycode-base-0000000000000000.log`; runs overwrite
  each other (also C-low: `ycode-base` vs `pi-bash` prefix).
- **C3 HIGH bash.go:396-400** — timeout drops the collected output + formatted
  "Command timed out after N seconds" message, returns raw `timeout:30` (LLM
  loses all output); `%0.f` rounds fractional timeouts.
- **C4 HIGH bash_unix.go:15** — abort kills process group with SIGTERM; TS
  uses SIGKILL (trappable SIGTERM may not stop the command).
- **C5 HIGH find.go:227** — `strings.TrimRight(p, "/r")` strips trailing `/`
  AND `r` characters → mangled result paths (`driver` → `drive`).
- **C6 HIGH find.go:321** — fd pattern rewrite builds `"**/*"+pattern` (TS
  `"**/"+pattern`) → `src/*.ts` misses top-level matches.
- **C7 HIGH grep.go:173-211** — 64KB `bufio.Scanner` cap on `rg --json` lines;
  a long minified line silently ends scanning and can deadlock (rg blocked on
  pipe, `cmd.Wait()` waiting); scanner.Err() unchecked, rg exit status
  ignored (bad regex → "No matches found.").
- **C8 MED** — no fd/rg auto-download (TS `ensureTool` fetches; Go silently
  falls back to non-gitignore-aware WalkDir while the description promises
  .gitignore respect); ripgrep download error wording.
- **C9 MED bash.go:349-408** — no 100ms-throttled streaming partial output
  (Go footnote acknowledges); **C10 MED** nil-pointer risk: `*opts.Timeout`
  deref when caller ctx has deadline; **C11 MED** signal-kill mapped to
  exit -1 → error, TS treats null exit as success.
- **C12 MED ls.go:84-92** — missing `PromptSnippet: "List directory contents"`
  (system prompt loses the ls snippet).
- **C13 MED schema.go:70** — Go injects `additionalProperties: false` into
  every tool schema; TS TypeBox emits none (wire-visible to providers).
- **C14 MED tool_definition_wrapper.go** — drops `prepareArguments` and
  ignores `ctxFactory` (extension tools get no context).
- **C15 LOW** — wording/casing drift (bash cwd/abort/"(no output)", find
  limit notices, grep "limti"/abort/period, ls error casing); find/ls apply
  the 2000-line default where TS is byte-only; `integer` vs `number` schema
  types; file-mutation-queue lock map never cleaned + relative-path key
  normalization latent.
- **OSC 133 note resolved:** no shell-integration markers exist in the bash
  tool on either side — the earlier "OSC 133 partial" note is about message
  rendering in pi (assistant/user-message.ts), nothing to port here.

Suggested fix order (one story per turn): (1) ~~A1-A4 edit_diff criticals~~
✅ done 2026-10-03 (tests in `edit_diff_test.go`), (2) B1/B2, (3) C1-C3,
(4) C5-C7, then mediums/lows by area. B1 deserves a fast check — if the TUI
really shows no tool output, it may be masked by another render path.

---

## ⚠️ Unverified / needs re-audit

- Audit also mentioned without detail: markdown theme cache missing;
  provider parity unverified. (OSC 133 resolved — see tools re-audit above.)
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
