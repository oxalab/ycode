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

## 🐛 Session-resume deserialization bug (2026-10-09) — ✅ FIXED (story 8)

User-reported: `/resume` (alias of `/switch`) lands in an empty transcript — and
the restored agent has **no memory of the session either**. Root cause (verified
empirically): `SessionEntry.Message` is the non-empty interface `llm.Message`
with no custom `UnmarshalJSON` anywhere, so every persisted message entry fails
`json.Unmarshal` ("cannot unmarshal object into ... llm.Message") and is
silently dropped by both `GetEntries` (session_manager.go:977) and `buildIndex`
(~845). Consequences: empty transcript after resume/`--continue`, AND
`agent.SetMessages` never restores history (`BuildSessionContext` sees zero
messages) — the model resumes with no prior conversation. TS never hits this
(`JSON.parse` is type-erased); it is a port-introduced defect class. **Fixed
2026-10-09:** `core/session_decode.go` (shared role-keyed entry decoder for
both readers, content-block decode, legacy tagless-shape sniffing) +
`rebuildTranscript` brought to pi `renderSessionContext` parity (tool
components matched to results by id, bash cards, compaction cards, editor
history) + round-trip/render tests. Full write-up in
**[session-resume-bug.md](session-resume-bug.md)** (§6 = applied fix).

---

## 🐛 Agent-loop context bug (2026-10-09) — ✅ FIXED (story 7); secondaries OPEN

Found while investigating a live TUI loop (model repeatedly misread its own tool
results as "injected into the human turn" and re-ran the same command). Root
cause: `packages/agent-sdk/src/agent_loop.go` passed `AgentContext` **by value**
to `streamAssistantResponse`, so the assistant message carrying `tool_calls`
was never appended back to the loop's context — every tool follow-up request
sent **orphaned `role:"tool"` messages with no preceding assistant tool_calls**,
which lenient backends (GLM/z.ai) fold into the user turn. Fixed 2026-10-09:
one append in `runLoop` (shared by both entry points) +
`TestAgentLoopKeepsAssistantToolCallsInContext` (mutation-verified). Full
write-up — mechanism, TS parity, and three secondary porting bugs found in the
same sweep — lives in
**[agent-loop-context-bug.md](agent-loop-context-bug.md)**: (5a) GLM thinking
replay dropped + signature-as-key bug — ✅ FIXED (story 9: streaming stores the
wire field name into ThinkingSignature, replay emits under it; SSE +
convert-messages tests), (5b) per-model `Compat` overrides never consulted —
✅ FIXED (2026-10-12: `MergeCompat` + registry plumbing for models.json compat
blocks + `resolveOpenAICompat` overlay; `enable_thinking` now an explicit
bool gated on actual effort), (5c) late tool-call ids never backfilled —
✅ FIXED (2026-10-13: guarded write-through to the live block + finish sync,
first-seen id wins; first tests of the tool-call streaming path);
(5d) rendering ruled innocent.

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
- **A5 MED edit_diff.go:49 — ✅ FIXED (2026-10-08)** — fuzzy normalization trims only tabs, TS
  trims all trailing whitespace (`trimEnd`). Fixed with
  `strings.TrimRightFunc(line, unicode.IsSpace)` (trailing-only, like
  `trimEnd` — leading whitespace survives).
- **A6 MED edit_diff.go:45 — ✅ FIXED (2026-10-08)** — missing NFKC normalization for fuzzy match.
  Fixed: `norm.NFKC.String(text)` prepended, giving TS's exact order
  (edit-diff.ts:34-55: NFKC → trimEnd → rune map); `golang.org/x/text`
  promoted from indirect to direct dependency.
- **A7 MED edit_diff.go:279-305 — ✅ FIXED (2026-10-08)** — files >5000 lines fall back to whole-file
  remove+add diff; npm `diff` computes a real diff at any size. Fixed:
  hand-rolled full-table LCS + `maxDiffLines` fallback replaced with
  `sergi/go-diff` line-mode Myers (`DiffLinesToRunes` + `DiffMainRunes`) —
  the repo's documented npm-`diff` substitute; O(ND) in edit size so the cap
  is gone entirely (TS has none). Output shape preserved
  (`mergeDiffParts`/`GenerateDiffString` untouched); tie-breaking may differ
  from the old LCS — the accepted npm-substitute divergence class. Tests:
  `TestNormalizeForFuzzyMatch`, `TestFuzzyFindTextTrailingWhitespace`,
  `TestGenerateDiffString_LargeFile` (6000-line one-line change → local hunk
  in ~10ms, no whole-file fallback), `TestGenerateDiffString_UnchangedStillExact`.
- **A8 MED edit.go:66-100 — ✅ FIXED (2026-10-05)** — `prepareEditArguments` (repairs `edits` sent as
  JSON string) never wired into the ToolDefinition; TS `prepareArguments` is.
  Fixed: `extensions.ToolDefinition` gained a `PrepareArguments func(args any)
  (any, error)` shim field; `WrapToolDefinition`, `CreateToolDefinitionFromAgentTool`,
  and `buildAgentToolFromDefinition` copy it (the SDK `AgentTool.PrepareArguments`
  + agent-loop invocation already existed — only the bridges were missing);
  `CreateEditToolDefinition` wires `prepareEditArguments` into it. The string
  parse is now `[]any` (TS `JSON.parse`+`Array.isArray` parity — extra keys in
  edit objects survive). The stale edit.go footnote claiming it's unwired is
  updated. Tests: `edit_tool_test.go` (7 tests: string repair, extra-keys
  survival, legacy top-level, non-map passthrough, AgentTool shim presence,
  prepare→marshal→Execute end-to-end, single-apply no-double-append).
- **A9 MED edit.go:233-249 — ✅ FIXED (2026-10-13, story 12)** — result lacks all diff UI chrome: no
  `renderDiff` colors/intra-line highlight, no streaming preview diff, no
  success/error header backgrounds (TS edit.ts:434-483, diff.ts:79-147).
  **Re-audit correction:** the colorizer (interactive/diff.go renderDiff +
  isDiffLike, 16 tests) and the pending/success/error header backgrounds
  (toolBg) already existed in the active interactive TUI — the real gap was
  that the diff never reached it: `EventToolExecutionEnd` carried only
  `Result.Content`, dropping `EditToolDetails.Diff` at agent_loop.go:695, so
  the box showed "Successfully replaced N block(s)…" and nothing else. Fixed:
  (1) event gained an additive `Details any` field (agent-sdk types + emitter);
  (2) `toolResultText` swaps the success line for Details.Diff (reference
  formatEditResult shows diff only), unwrapping typed / `*any` / decoded-map
  shapes, also used by `rebuildTranscript` so `/resume` restores diffs;
  (3) `ComputeEditPreviewDiff` (core/tools, prepare→parse→read-only diff, same
  repair pipeline as Execute) streams the preview into the pending box from
  `startEditPreview` while the edit runs — preview errors tint the header red
  (toolBg reordered error-first, reference getEditHeaderBg). Tests:
  `edit_preview_test.go` (5), `tool_result_text_test.go` (5 incl. typed-nil
  `*any` guard). Follow-up: TS never shows "Successfully replaced" text even
  after settle — Go matches this on the live path; the BT (non-active) TUI is
  unchanged.
- **A10 MED write.go:105-130 — ✅ FIXED (2026-10-13, story 13)** — content preview drops ALL blank lines (TS
  keeps interior ones), no `... (N more lines, M total, to expand)` hint, no
  syntax highlighting. **Re-audit correction:** the active interactive TUI never
  consumed `RenderCall` (its `formatToolCall` write case was path-only) — only
  the older BT TUI did. Fixed both: `RenderCall` now trims trailing empties
  only (interior blanks kept), collapses to 10 lines with the exact
  `... (N more lines, M total, ctrl+o to expand)` hint, accepts the
  `file_path` alias, and shows `[invalid content arg - expected string]` on
  non-string content; the interactive TUI's write case renders the full styled
  preview (chroma `lexers.Match` highlighting via new `highlightWritePreview`,
  honoring ctrl+o expanded state, hint styled muted + keyHint) and hides the
  "Successfully wrote N bytes" line on success (reference formatWriteResult
  renders nothing on success — errors keep text). Out of scope, noted: TS's
  incremental `WriteHighlightCache` (Go args arrive complete at tool start).
  Tests: `write_preview_test.go` (4, first for write.go), interactive
  `tool_result_text_test.go` (+4: collapsed hint / expanded / highlight smoke /
  success-line suppression).
- **A11 LOW** — error-string drift across edit_diff.go/edit.go ("THe old
  text", "overalp", "whitespaces", "atleast", "inavlid arguments", lowercase
  first letters, missing periods); read.go:108 same.
- **A12 LOW** — `firstChangedLine` `-1` sentinel survives `omitempty` (TS
  omits); `file_path` alias not accepted by edit/write input structs; access
  check `os.Stat` vs R_OK|W_OK; write.go byte count vs TS UTF-16 length.

### B. read.go / truncate.go / render_utils.go / path_utils.go

- **B1 HIGH render_utils.go:152-193 — ✅ FIXED (2026-10-04)** — `GetTextOutput` type-switches on
  `*TextContentBlock`/`*ImageContentBlock`, but tools store
  `*llm.TextContent`/`*llm.ImageContent` → always returns `""`; TUI tool
  results show no output. Fixed: switch now reads the llm types (+ value
  forms), keeps the `map[string]any` case, adds `map[string]string` (the
  app_update.go error path), and the dead local block structs are removed;
  post-loop text-join/image-fallback logic was already a faithful TS port and
  is unchanged. Tests: `render_utils_test.go` (llm blocks, image fallback,
  both map shapes, nil/empty).
- **B2 HIGH truncate.go:106 — ✅ FIXED (2026-10-04)** — `TruncateHead` loop missing the `i < maxLines`
  bound → files with >2000 short lines (<50KB) are NOT truncated while the
  footer claims they were. Fixed (one condition, TS truncate.ts:115 parity);
  `TruncateTail` already had the bound. Tests: `truncate_test.go` (max-lines
  cap, first-line-exceeds path, mid-file byte cap, under-limits pass-through).
- **B3 MED read.go:112-114 — ✅ FIXED (2026-10-16, story 14)** — access check is `os.Stat` + generic
  "cannot read file"; TS uses R_OK access and surfaces the real fs error
  (EACCES/ENOENT indistinguishable in Go). Fixed: `defaultReadOps.Access` now
  probes readability via `os.Open`+`Close` (portable `access(R_OK)` —
  `syscall.Access` isn't Windows-portable), and Execute returns the raw fs
  error (reference re-throws unmodified; the model now sees the real reason —
  not-found vs permission — with the path, matching edit.go's established
  informative style). Tests: `read_test.go` (first tests for read.go: Access
  op readable/missing, Execute missing-file raw error — mutation-verified —,
  small-file read, directory path).
- **B4 MED read.go:136 + B5 MED — ✅ FIXED (2026-10-17, story 15)** — image-omitted text missing the
  `Read image file [mime]` first line; **B5** non-vision-model image note
  not ported (no model context in Go Execute). Fixed together (one code path):
  the degrade note now leads with `Read image file [<mime>]` (read.ts:257) and
  the ported `getNonVisionImageNote` (read.ts:90-95) appends
  `[Current model does not support images. …]` on every image path when the
  model's input lacks "image". The model context rides a revived
  `ExtensionContext` (extensions: new `ExtensionContext{Model}` + optional
  `ToolDefinition.ExecuteContext`; `buildAgentToolFromDefinition` takes a
  ctxFactory, supplied by `buildRuntime` as `s.GetModel()` — live, so
  mid-session model switches are honored; nil factory = old behavior). The
  wrapper's footnote-1 "lost ExtensionContext param" is thus resolved.
  **Bonus root-cause fix found by the new tests:** no package imported
  `image/png|jpeg|gif`, so `image.DecodeConfig` failed for EVERY image and all
  image reads degraded to "[Image omitted]" — formats now registered in
  render_utils.go (webp has no stdlib decoder and still degrades; footnote
  updated). Out of scope: `formatDimensionNote` success-path scaling note
  (B9). Tests: read_test.go +4 (note helper, ExecuteContext vision/text-only
  end-to-end via synthesized PNGs with patched IHDR+CRC, degrade mime line —
  this test locked the decoder bug), agent_session_tools_test.go (+2 wiring).
- **B6 MED path_utils.go:26-30 — ✅ FIXED (2026-10-18)** — `tryNFDVariant` was a
  no-op TODO (the footnote claiming "no external deps yet" was stale —
  golang.org/x/text v0.30.0 was already direct, `norm.NFKC` already used in
  edit_diff.go). Now `norm.NFD.String`, mirroring TS `normalize("NFD")`
  (path-utils.ts:17); the NFD fallback and the previously-dead combined
  NFD+curly-quote variant in `ResolveReadPath` are live. Stale NOTE + footnote
  flag removed (flag 2, swallowed UserHomeDir error, kept — out of scope).
  **B7 MED — ✅ FIXED (2026-10-18)** — on Windows `filepath.IsAbs("/foo")` is
  false (Node's `path.isAbsolute` is true), so `ResolveToCwd` joined `/foo`
  under cwd. Now a `runtime.GOOS`-guarded `isWindowsRootRelative` branch
  anchors `/foo`/`\foo` (no volume, leading separator) to the cwd's drive via
  `filepath.Join(filepath.VolumeName(cwd), …)` — Node `path.resolve(cwd, "/foo")`
  semantics; Unix behavior untouched (guard) and `\foo` stays cwd-relative
  there. New path_utils_test.go: 5 tests (NFD variant, NFD+curly combined,
  curly-only guard, resolve relative/absolute guard, Windows root-relative
  skipped off-Windows). Mutation-verified: the three fix-dependent tests fail
  on the unfixed tree.
- **B8 MED render_utils.go:12 — ✅ FIXED (2026-10-19)** — `tools.StripAnsi` only
  matched plain CSI (`ESC[…letter`); OSC (incl. the app's own OSC 133),
  private-mode CSI, colon SGR params and C1 introducers leaked. All three
  runtime strippers now embed the verbatim npm `ansi-regex@6.2.2` pattern (the
  lockfile-pinned dep of the reference's strip-ansi@7.2.0; fetched from unpkg
  since node_modules dirs are empty): tools.StripAnsi (+ the reference's
  no-ESC/no-0x9B fast path), interactive ansiStripRe, and the core/tui
  components stripANSI state machine (which terminated on any 0x40-0x7E byte —
  an OSC's `]` "ended" it, BEL bytes leaked and the first visible char after
  was eaten). Aligned as one finding, three instances; package-local copies
  per the no-cross-package-helper convention. Out of scope: test-only helpers
  (commands_residual_test.go, packages/tui stripAnsiForTest) and export-html's
  SGR parser (converter, not stripper). Tests: render_utils_test.go TestStripAnsi
  (9-case table: SGR/erase-line regression guards + private/OSC-BEL/OSC-ST/
  colon/C1/mixed) + TestGetTextOutputStripsOSC; interactive/ansi_strip_test.go;
  components bash_execution_ansi_test.go. Mutation-verified: all three fail on
  the unfixed tree.
- **B9 LOW — ✅ FIXED (2026-10-20, story 19)** — the grab-bag, item by item:
  (1) `tools.GetCapabilities` was case-sensitive on `"iTerm.app"`, read a
  nonexistent `TERMKIT` var, and missed ghostty/wezterm/ITERM_SESSION_ID and
  the tmux/screen guard — rewritten to reference `detectCapabilities`
  semantics (lowercased comparisons; image-positive = KITTY_WINDOW_ID /
  TERM_PROGRAM kitty|ghostty|wezterm|iterm.app / TERM contains ghostty /
  GHOSTTY_RESOURCES_DIR / WEZTERM_PANE / ITERM_SESSION_ID; TMUX or
  tmux*/screen* TERM forces false) for the images verdict the render pipeline
  needs — pi-tui's full protocol detector stays in packages/tui/termimage.go;
  stale FOOTNOTE updated. (2) `ImageFallback` mime now bracketed:
  `[Image: [image/png]]` / `[Image: [image/png] WxH]` (reference
  imageFallback). (3) bash tool accumulator prefix `"ycode-base"` →
  `"ycode-bash"` (reference bash.ts passes `pi-bash`; executor already
  ycode-bash-). (4) `OutputAccumulator.Append` decoded each pipe chunk with
  `string(data)` — 4096-byte pipe reads split multi-byte runes into U+FFFD
  (the footnote claiming "complete writes" was false); now a streaming decoder
  (pendingTail + completeUTF8PrefixLen) holds incomplete trailing sequences
  and `Finish()` flushes the dangling tail (TextDecoder{stream:true} parity);
  temp file always got raw bytes (unchanged). Same bug class remains in
  bash_executor.go:60 `string(data)` (session-level path) — noted, out of
  scope. (5) `TruncationResult` gained camelCase json tags (was untagged →
  PascalCase in details JSON) and `TruncatedBy` is now `*string` — nil =
  `"truncatedBy":null` when not truncated (TS union with null); call sites
  (read.go footer, bash.go footer, accumulator Snapshot) nil-safe; old
  session JSON still decodes (encoding/json case-insensitive field match).
  (6) offset-beyond-EOF error now reads `Offset N is beyond end of file…`
  (reference casing). (7) `TruncateLine` rune-vs-UTF-16 cut points: ACCEPTED
  DIVERGENCE — Go cuts at rune boundaries (correct); TS counts UTF-16 code
  units and can split surrogate pairs on emoji; pinned by test + footnote.
  Tests: render_utils_test.go (TestGetCapabilities 13-case table incl.
  tmux-override + lowercase iterm; fallback-format updates), truncate_test.go
  (+2: camelCase/null marshal, emoji rune-boundary pin), read_test.go (+1
  offset casing), bash_exec_test.go (+1 end-to-end spill prefix),
  output_accumulator_test.go (NEW, first accumulator coverage: split-rune
  reassembly, dangling-tail flush, ASCII multi-append, spill raw-bytes
  invariant, prefix defaults). Mutation-verified per item (stash rounds +
  surgical one-line reverts; B9.5's signal is the pointer-type compile
  failure).

### C. bash / find / grep / ls / queue / schema

- **C1 HIGH bash.go:253-258 — ✅ FIXED (2026-10-04)** — `wg.Wait()` blocks on inherited stdio of
  backgrounded children (`daemon &` hangs the tool until the descendant
  exits); TS engineered a 100ms post-exit grace + stream destroy
  (`waitForChildProcess`) precisely to avoid this. Fixed: `cmd.Wait` moved to
  its own goroutine; after exit the stream pumps get the 100ms grace
  (`exitStdioGraceMs`), then both pipe read-ends are force-closed (the Go
  equivalent of TS stream destroy) and `close(done)` moved after
  finalization so the kill watcher can't leak. `sleep 30 & echo hi` now
  returns in ~0.1s (`TestExecBackgroundChildDoesNotHang`).
- **C2 HIGH output_accumulator.go:23-27 — ✅ FIXED (2026-10-04)** — random ID buffer never filled →
  every spilled temp file is `ycode-base-0000000000000000.log`; runs overwrite
  each other. Fixed with `crypto/rand` (`rand.Read(b)`, the bash_executor.go
  pattern). Test: `TestDefaultTempFilePathUnique`.
- **C3 HIGH bash.go:396-400 — ✅ FIXED (2026-10-04)** — timeout drops the collected output + formatted
  "Command timed out after N seconds" message, returns raw `timeout:30` (LLM
  loses all output); `%0.f` rounds fractional timeouts. Fixed: the timeout
  mapping now returns `appendStatus(statusText, ...)` (output preserved;
  error paths use raw snapshot content, no "(no output)" placeholder — TS
  `formatOutput(snapshot, "")` parity); sentinel formatted with `%g`
  (preserves `2.5`) and nil-Timeout guarded (parent-ctx deadline no longer
  derefs); folded in the LOW abort-wording finding ("Command Aborted" +
  placeholder → "Command aborted"). Tests: `TestExecTimeoutPreservesOutput`,
  `TestExecAbortWording` (in `bash_exec_test.go` — the first real-execution
  bash tests).
- **C4 HIGH bash_unix.go:15** — abort kills process group with SIGTERM; TS
  uses SIGKILL (trappable SIGTERM may not stop the command).
- **C5 HIGH find.go:227 — ✅ FIXED (2026-10-05)** — `strings.TrimRight(p, "/r")` strips trailing `/`
  AND `r` characters → mangled result paths (`driver` → `drive`). Fixed:
  post-processing extracted to `relativizeFindResults`, a TS-verbatim port of
  find.ts:303-316 (strip single trailing `\r`, trim, preserve trailing dir
  slash recorded before relativizing and re-appended, prefix-strip/Rel).
  Test: `TestRelativizeFindResults`.
- **C6 HIGH find.go:321 — ✅ FIXED (2026-10-05)** — fd pattern rewrite builds `"**/*"+pattern` (TS
  `"**/"+pattern`) → `src/*.ts` misses top-level matches. Fixed: extracted
  `buildFdPattern` helper, `"**/"` anchor per TS find.ts:242-248 (`--full-path`
  only when the pattern contains `/`). Test: `TestBuildFdPattern`.
- **C7 HIGH grep.go:173-211 — ✅ FIXED (2026-10-05)** — 64KB `bufio.Scanner` cap on `rg --json` lines;
  a long minified line silently ends scanning and can deadlock (rg blocked on
  pipe, `cmd.Wait()` waiting); scanner.Err() unchecked, rg exit status
  ignored (bad regex → "No matches found."). Fixed: 1MB initial/10MB max
  scanner buffer; `scanner.Err()` checked → kill + error instead of
  deadlocking `cmd.Wait()`; stderr captured (`new(strings.Builder)` was
  written and discarded) and mapped TS-style (non-0/1 exit → stderr text or
  `ripgrep exited with code N`; ctx cancel → `Operation aborted`); folded in
  LOW wordings: "No matches found." → "No matches found", "limti" typo,
  "invalid agruments" typo. Tests: `TestGrepLongLineNoDeadlock`,
  `TestGrepBadRegexIsError`, `TestGrepNoMatchesWording`,
  `TestGrepLimitNoticeTypo` (in `find_grep_test.go`; real-rg, skip-gated).
- **C8 MED — ✅ FIXED (2026-10-20, story 20)** — the reference's
  `ensureTool` (utils/tools-manager.ts) is ported as
  `core/tools/tools_manager.go`: `GetToolPath` (managed bin dir
  `~/.ycode/agent/bin` first — flat, `.exe` on Windows — then PATH probes,
  fd also tries `fdfind`), `EnsureTool` (latest GitHub release via the API —
  fd `sharkdp/fd` tag `v*`, rg `BurntSushi/ripgrep` bare tags; exact
  asset matrix incl. rg-linux-x86_64=musl; tar.gz/zip extraction via Go
  stdlib where the reference shells system tar / npm extract-zip; layout
  probe asset-dir → root → recursive DFS; per-call temp extract dirs;
  chmod 0755 non-Windows; `YCODE_OFFLINE` + Android/Termux gates applied
  regardless of `silent`, only notices silenced). **User-locked parity
  decisions:** (a) bug-for-bug integrity — NO checksums, NO version pinning
  (HTTPS is the only guarantee, like the reference); (b) the gitignore-blind
  WalkDir fallback is DELETED — find hard-fails with the reference wording
  `fd is not available and could not be downloaded` (the "Respects
  .gitignore" description is now true via fd; the old fallback also silently
  swallowed fd run failures — now `stderr` / `fd exited with code N` /
  `Failed to run fd` surfaces, reference-style). grep wording is
  reference-exact (`ripgrep (rg) is not available and could not be
  downloaded`). Interactive startup pre-fetches fd+rg concurrently,
  non-silent, before the TUI starts (reference Promise.all in init) and
  passes the resolved fd path into the autocomplete provider (previously
  `""` — a bin-dir fd was invisible to autocomplete's bare LookPath).
  Drive-by: `core.isOfflineModeEnabled` `value == "l"` → `lower == "1"`
  (mangled check, `YCODE_OFFLINE=1` never worked there). Tests:
  tools_manager_test.go (asset matrix 14 cases, v-tag strip, tar.gz/zip
  extraction + layout probe/DFS vs in-test fixtures, GetToolPath order via
  `YCODE_CODING_AGENT_DIR`, offline gate); find_grep_test.go (find
  hard-fail + grep wording — both mutation-verified against the old wiring;
  real-download e2e gated behind `YCODE_TEST_DOWNLOADS=1`, run once:
  PASS, fd downloaded + runs in 1.6s).
- **C9 MED — ✅ FIXED (2026-10-23, story 21)** — streaming partial output is
  ported end to end. New additive `ToolDefinition.ExecuteUpdate`
  (extensions/types.go — the Go-shaped analogue of TS execute's 4th param,
  following the existing ExecuteContext precedent); both ToolDefinition→
  AgentTool bridges forward it (`tools/tool_definition_wrapper.go` — which
  used to receive the SDK's onUpdate and DISCARD it — via a shared
  `toAgentToolResult` conversion; and `agent_session_extensions.go`
  buildAgentToolFromDefinition, which now prefers the streaming form).
  `agent-sdk/agent_loop.go` emits update events LIVE during execution (the
  old code queued closures and fired them only after Execute returned;
  TS's onUpdate fires synchronously mid-execute). bash.go implements the
  reference throttle verbatim (bash.ts:287-343): immediate empty kickoff
  partial, trailing-edge 100ms timer coalescing (`outputUpdateThrottle` —
  mutex-guarded because Go's OnData arrives from two stream-pump goroutines
  where TS is single-threaded), partials carry the raw snapshot WITHOUT the
  truncation footers (those stay final-result-only), final flush in the
  finish path; the previously-dead `bashUpdateThrottleMs` constant is now
  live. The TUI needed no change (interactive/events.go handleToolUpdate
  already streamed EventToolExecutionUpdate into the bash component). Tests:
  `TestBashStreamsThrottledPartials` (5000 chunks → <25 partials, the
  reference's own bound; kickoff empty; final flush complete),
  `TestBashThrottleSpreadsEmissions`, `TestWrapToolDefinitionForwardsPartials`
  — mutation-verified (bash.go stash → build failure; wrapper partial-drop
  mutation → FAIL).
- **C10 MED — ✅ FIXED (2026-10-23, story 21; guard landed with the C1-C3
  rewrite)** — the `*opts.Timeout` deref is guarded (`opts.Timeout != nil &&
  *opts.Timeout > 0`) and `timeoutSentinel` is nil-guarded (plain "timeout"
  for an inherited caller deadline). Pinning it surfaced a latent wording
  bug on that previously-unreachable path: the plain sentinel fell through
  the TrimPrefix branch producing "Command timed out after timeout seconds"
  — the two sentinel forms now map separately (inherited deadline →
  "Command timed out", no seconds). Test:
  `TestExecParentDeadlineWithoutTimeoutOption` (mutation-verified: removing
  the guard panics with exactly the nil deref the finding predicted).
- **C11 MED — ✅ FIXED (2026-10-23, story 21)** — `BashExecResult.ExitCode`
  is now `*int` (TS `number | null`): `exitCodeFromRaw` maps the signal-kill
  raw -1 to nil, the tool treats nil as SUCCESS (bash.ts:392 — no spurious
  "Command exited with code -1" error), and the session path passes nil
  through (`bash_executor.go`; `BashResult` was already `*int` and
  messages.go already null-safe). Windows is unchanged and correct:
  taskkill /F produces a real exit 1, matching TS on win32. Tests:
  `TestBashNilExitCodeIsSuccess`, `TestBashNonZeroExitStillErrors`,
  `TestExecSignalKillYieldsNilExitCode` (unix-gated, `kill -TERM $$`),
  `TestExecuteBashNilExitCodePropagates` — mutation-verified via the bash.go
  stash build failure.
- **C12 MED — ✅ FIXED (2026-10-24, story 22)** — `CreateLsToolDefinition`
  (ls.go) now carries `PromptSnippet: "List directory contents"`
  (reference ls.ts:108 verbatim; it was the lone built-in without one, so
  read-only contexts — where ls IS exposed via CreateReadOnlyToolDefinitions,
  `index.go:98` — lost the line in the system-prompt tool list built by
  `agent_session_prompt.go`). Pinned by `TestBuiltinToolsCarryReferencePromptSnippets`
  (ls_test.go): asserts ALL seven built-ins' PromptSnippets against the exact
  reference strings through the coding+read-only exposure sets, so a future
  dropped snippet or set omission fails by name — mutation-verified (line
  revert → test names ls).
- **C13 MED — ✅ FIXED (2026-10-24, story 23; finding half-corrected)** —
  the reference does NOT emit `additionalProperties` for 6 of 7 tools (bare
  `Type.Object(...)` in bash/read/write/ls/grep/find) but DOES pass
  `{ additionalProperties: false }` explicitly for **edit** (edit.ts:39
  nested edits[] items, edit.ts:50 top level) — Go's blanket injection
  (`schema.go:70` + nested :91) matched edit coincidentally and diverged for
  the other six. Fix: `objectSchema` no longer emits the key (default now
  matches TypeBox default); new `objectSchemaStrict` keeps it and is used
  only by edit; `arrayItems` keeps its nested false (edit-only caller,
  comment annotated). Nothing in Go reads the key — wire-visible only via
  the raw-pass-through providers (openai-completions, openai-responses,
  bedrock, mistral, google); anthropic rebuilds input_schema from
  properties/required on BOTH sides (anthropic.go:695-720), unchanged.
  Pinned by `TestAdditionalPropertiesMatchesReference` (absent for the six,
  present top-level+nested for edit) — mutation-verified (edit reverted to
  plain objectSchema → test names edit.ts:50). The old schema.go comment
  claimed the injection "mirrors TypeBox" — it was accurate only for edit.
- **C14 MED — ✅ RESOLVED (2026-10-24, story 24; finding half-stale)** —
  (a) "drops `prepareArguments`" was STALE since story 5 (A8): both bridges
  copy `PrepareArguments` (tool_definition_wrapper.go, 
  agent_session_extensions.go), agent_loop consumes it, tests pin it.
  (b) "ignores `ctxFactory`" was REAL in the core wrapper only: it never
  called `def.ExecuteContext` and the param was dead (`func() any`). Fixed:
  `WrapToolDefinition`/`WrapToolDefinitions` now take
  `func() extensions.ExtensionContext` and prefer ExecuteUpdate >
  ExecuteContext (factory present) > Execute — the exact order
  `buildAgentToolFromDefinition` already used, so both bridges are identical
  (TS wrapper semantics: `definition.execute(toolCallId, params, signal,
  onUpdate, ctxFactory?.())`). All Create*Tool call sites pass nil →
  behavior unchanged there. Tests: `TestWrapToolDefinitionPrefersExecuteContext`,
  `TestWrapToolDefinitionNilFactoryFallsBackToExecute`,
  `TestWrapToolDefinitionExecuteUpdateBeatsContext` — both mutations
  verified (branch inverted → Execute ran; order swapped → ExecuteContext ran
  despite update). Runtime note: production flows through the session bridge
  (which honored ExecuteContext since story 15), so this fix is
  parity-surface correctness, not a behavior change.
- **C16 MED — LOGGED 2026-10-24 (story 24), DEFERRED by choice** — JS
  extension tools receive only `{cwd}` as ctx (`js_api.go:385` →
  `CallToolExecute(exHandle, params, map{"cwd":...})`); the TS reference
  passes the full runner context (`runner.createContext()`: getModel,
  isIdle, getSignal, abort, hasPendingMessages, shutdown,
  getContextUsage, compact, getSystemPrompt + actions/ui — runner.ts:268-299)
  as the 5th execute arg. Porting it is an extensions-runtime feature family
  (same class as the subagent example extension), not a wrapper fix; the Go
  `ExtensionContext` struct currently carries only `Model` (types.go comment:
  "the Go port starts with the model only"). Related dead code:
  `extensions/wrapper.go WrapRegisteredTool` is a broken stub
  (`def.Execute(nil, nil)`) with zero callers. Revisit if/when JS extension
  tool parity matters in practice.
- **C15 LOW — ✅ FIXED (2026-10-24, story 25; grab-bag verified item-by-item,
  3 sub-items were already STALE)** — STALE (parity, no code): bash abort
  wording ("Command aborted", TestExecAbortWording), "(no output)" placeholder
  (exact match incl. error-path exemption via formatOutput(snapshot, "")),
  grep wording trio ("No matches found" no-period / limti / "Operation
  aborted" — story 4). LIVE, fixed: (1) bash cwd error casing + period →
  reference-exact `Working directory does not exist: ...\nCannot execute bash
  commands.` (bash.ts:71). (2) find custom-ops limit notice → SHORT form
  (`N results limit reached`, find.ts:197) while the fd path keeps the long
  hint form (find.ts:325) — Go previously emitted the long form everywhere;
  folded in: find aborts now surface `Operation aborted` (find.ts:320) at all
  three ctx-check sites instead of raw ctx.Err(). (3) ls error casing →
  `Path not found` / `Not a directory` / `Cannot read directory` (ls.ts:133/
  :140/:149); Go's extra `Cannot stat path` branch kept (no TS counterpart,
  documented Go-defensive). (4) find/ls/grep truncation is now BYTE-ONLY —
  new `byteOnlyTruncation()` helper (MaxLines=MaxInt32, the TS
  `maxLines: Number.MAX_SAFE_INTEGER`) replaces the zero-options call that
  wrongly applied the 2000-line default at find.go/ls.go/grep.go (grep.ts:334:
  "There is no line limit here because the match limit already capped rows").
  (5) `integer`→`number` schema types on 6 props (find.limit, grep.context,
  grep.limit, ls.limit, read.offset, read.limit) — TS has zero Type.Integer
  in tools; wire-visible like C13. (6) file_mutation_queue.go: entries now
  REFCOUNTED and deleted when the last user releases (TS deletes post-release
  :34-38; plain delete would break mutual exclusion mid-handoff — a third
  arrival could build a second mutex while a waiter is still blocked on the
  first), and the key is absolutized via filepath.Abs before EvalSymlinks
  (TS resolve(filePath)); call sites always passed absolute paths, so this
  hardens the helper. Tests: TestExecCwdErrorWording,
  TestFindCustomOpsLimitNoticeShortForm, TestFindCustomOpsByteOnlyTruncation,
  TestFindAbortWording, TestLsErrorCasing, TestLsTruncatesBytesOnly,
  file_mutation_queue_test.go (cleanup / key-absolutize / 8×25-goroutine
  mutual exclusion + no leak, `-count=3` stress) — mutation-verified: cwd
  wording revert, byteOnlyTruncation revert (both truncation tests fail at
  2002 lines), read offset integer revert (TestReadSchema), queue-cleanup
  removal (survived/leaked). Schema-test updates: TestReadSchema +
  TestGrepSchema now pin "number".
- **OSC 133 note resolved:** no shell-integration markers exist in the bash
  tool on either side — the earlier "OSC 133 partial" note is about message
  rendering in pi (assistant/user-message.ts), nothing to port here.

Suggested fix order (one story per turn): (1) ~~A1-A4 edit_diff criticals~~
✅ done 2026-10-03 (tests in `edit_diff_test.go`), (2) ~~B1/B2~~ ✅ done
2026-10-04 (tests in `render_utils_test.go`, `truncate_test.go`), (3) ~~C1-C3~~
✅ done 2026-10-04 (tests in `bash_exec_test.go`), (4) ~~C5-C7~~ ✅ done
2026-10-05 (tests in `find_grep_test.go`), (5) ~~C9-C11~~ ✅ done 2026-10-23
(tests in `bash_exec_test.go` + wrapper/executor/signal tests), (6) ~~C12~~ ✅
done 2026-10-24 (test in `ls_test.go`), (7) ~~C13~~ ✅ done 2026-10-24 (test
in `schema_test.go`), (8) ~~C14~~ ✅ resolved 2026-10-24 (half stale; tests
in `tool_definition_wrapper_test.go`; C16 logged deferred), (9) ~~C15~~ ✅
done 2026-10-24 (tests in bash_exec/find_grep/ls/schema/file_mutation_queue
tests) — **C-group complete**, (10) ~~theme-cache verification~~ ✅ done
2026-10-29 (recolor fix + line-cache parity verdict), (11) ~~provider-parity
verification~~ ✅ done 2026-10-29 (story 27: first fix wave landed; follow-up
queue = P9-P16 provider backlog above; deferred: C16 (JS runner context,
feature family), catalog typos, add-second-model TUI gap). **All audit items
verified — no remaining unverified entries.** (B1's masked-path
question was resolved in story 2 — the interactive path uses
`extractResultText`; `GetTextOutput` serves the core/tui fallback renderer.)

---

### D. interactive/TUI resize responsiveness (2026-10-19 exploration, user-reported)

Full exploration vs pi-tui: the render/wrap architecture is a faithful port
(16ms requestRender throttle; `widthChanged/heightChanged → fullRender(true)`
clear-screen+scrollback+re-wrap; width passed per `Render(width)` call;
width-keyed caches). Two real bugs:

- **R1 HIGH packages/tui/terminal_resize_other.go — ✅ FIXED (2026-10-20
  story 18)** — non-Unix had NO resize notification (empty `setupResize`
  stub + false "engine polls Columns/Rows" comment; no poll existed), so on
  Windows a terminal resize only repainted when an unrelated event (keypress,
  agent output, spinner) fired a frame. Reference gets Node's
  `process.stdout` 'resize' push event on all platforms. Fixed: poll
  goroutine (250ms, `resizePollInterval` var) comparing live
  `Columns()/Rows()` and firing the existing `resizeHandler` seam (→
  `RequestRender` → existing width/height fullRender logic); stop channel
  closed by the existing `Stop()` teardown path. `ReadConsoleInput`
  WINDOW_BUFFER_SIZE_EVENT rejected: would consume input records racing the
  key reader on the same console handle. Node-on-Windows fires only on column
  changes — width+height poll is a superset. Unix SIGWINCH path untouched.
  Test `terminal_resize_other_test.go` (!unix): fires on COLUMNS change via
  the documented env fallback, zero baseline fires, stops after teardown;
  mutation-verified against the empty stub.
- **R2 MED interactive/expandable_text.go — ✅ FIXED (2026-10-20 story 18)** —
  header `ExpandableText` evaluated its width-reading closures once at
  construction (and on ctrl+o); closures read live `Terminal.Columns()` for
  header centering, so after a resize the header kept old-width centering
  while every other component re-wrapped (audited: all other active-path
  components re-layout per frame; caches width-keyed). Fixed: `Render(width)`
  override re-evaluates the active closure each render (SetText → Text's
  (text,width) cache still suppresses redundant re-wraps), `SetExpanded`
  reduced to the flag. Test `expandable_text_test.go` (re-center on width
  change + toggle); mutation-verified against the baked-text version.
- Noted, NOT fixed (2026-10-19): markdown HR caps at `min(width, 80)`
  (markdown.go — adapts narrower, never fills wider; reference parity
  unverified); no `ENABLE_VIRTUAL_TERMINAL_PROCESSING` on the OUTPUT handle
  anywhere (fine under Windows Terminal/ConPTY where VT is default-on; would
  mangle on legacy conhost); stale comment cmd/ycode/runtime_pi.go claiming
  bubbletea is the default TUI (the runtime.go gate says pi-tui is); scrollback
  wipe on every resize is reference-identical behavior, not a bug.

---

## ⚠️ Unverified / needs re-audit

- **markdown theme cache — ✅ VERIFIED + FIXED (2026-10-24, story 26)** —
  the verification found the LINE cache itself is parity (single-slot,
  keyed (text,width), Go markdown.go:65-96 = reference markdown.ts:87-118;
  no growth either side; resize re-wraps). The real divergence was theme
  SWITCHING: TS renders through a live theme Proxy + `ui.invalidate()` walk
  (interactive-mode.ts:3834-3841) so `/theme` recolors the whole transcript
  mid-session; Go closures captured the `*Theme` pointer and refreshTheme
  only touched footer+editor border — the chat kept old colors forever (even
  invalidating couldn't help: SetTheme pointer-swaps the global). Fixed:
  adapter closures now resolve the live global via new nil-safe
  `theme.Current()` (fallback to captured when uninitialized — tests),
  `refreshTheme` invalidates the chat tree (`o.chat.Invalidate()`; all three
  selector paths — select/preview/cancel — route through it), and
  `highlight.go` memoizes coalesced lexers per language (the analogue of the
  reference's `cachedHighlightThemeFor` memo, theme.ts:952-981; Go's
  per-token coloring is a map lookup, so the lexer chain is the only
  repeated build). Tests: `theme_adapter_test.go` —
  TestThemeSwitchRecolorsMarkdown (switch+invalidate → ANSI differs;
  mutation-verified: fgStyle reverted to captured pointer → "output
  identical"), TestThemeSwitchStaleCacheWithoutInvalidate (proves cache
  staleness observable), TestHighlightLexerMemoReused. Residual (P8
  deferral, unchanged): tool/bash box chrome still captures th at
  construction. Environment note: TestExecAbortWording "failures" during
  this story were a corrupted shell PATH hiding taskkill from the test
  binary (KillProcessTree shells out to it) — passes with system32 on PATH;
  not a code issue.
- **provider parity — VERIFIED 2026-10-29 (story 27), first fix wave landed.**
  All 9 API families compared TS↔Go (17 file pairs, ~18k lines: 5 read-only
  comparison agents + inline core-pair read; load-bearing claims hand-verified
  in the source). Your daily path (openai-completions) is sound at its core;
  severe findings concentrate in unregistered providers. Fixed now (tests in
  `openai_completions_parity_test.go` + `json_parse_partial_test.go`):
  - *Cross-cutting*: `parseStreamingJson` could not parse partial JSON at all
    (marshal-concat + strict unmarshal) — tool-call args stayed `{}` live
    during streaming, and anthropic never accumulated `input_json_delta`
    (final args broken there). Ported the reference's partial-json fallback
    (prefix-closed scanner, `utils/json-parse.go`); the provider seam
    delegates and guarantees non-nil. Mutation-verified (stub → both
    progressive tests fail). Stress run (-count=3) caught a real TrimSpace
    bug in the scanner (trailing space mid-string eaten) — fixed.
  - *openai-completions (C17)*: `prompt_cache_hit_tokens` + `choice.usage`
    usage fallbacks; image-only tool-result placeholder; `lastRole` tracking
    (bridge regression under requiresAssistantAfterToolResult); compat matrix
    completed (moonshot/together/cloudflare-ai-gateway + `together` thinking
    format); anthropic-over-openrouter cache-control is provider-keyed, not
    URL-keyed; missing API key fails fast instead of sending `Bearer `;
    `responseModel` captured from chunk.model; `PI_CACHE_RETENTION=long`
    honored.
  **Backlog (P-group), by family — none reachable from custom models.json
  openai-completions setups except where noted:**
  - *P9 openai-responses (registered)*: SSE parsed with raw `json.Decoder`
    over the `alt=sse` body → every stream fails (openai-responses.go:287);
    no HTTP status check → API errors look like empty successes;
    `Usage.TotalTokens` = input only, cost/service-tier pricing dropped;
    reasoning summary events unhandled (no live thinking); codex targets the
    wrong host/auth/shape (chatgpt.com backend-api, no account-id/originator/
    beta headers, no retry/backoff, no WS transport); azure posts the legacy
    chat path (`api-version=2024-02-15-preview`, no `/responses`) and parses
    chat-completions chunks; mid-stream errors mislabeled `aborted`; `msg_`
    fallback ids built with `string(rune(idx))` (control char at idx≥10).
  - *P10 anthropic (registered)*: no status check → non-2xx = empty success;
    `CalculateCost` never called (cost always 0); redacted-thinking loses its
    flag → replay as thinking text 400s next turn; adaptive `effort` nested
    inside `thinking` instead of top-level `output_config`; `sensitive`/
    unknown stop reasons → stop; tool schemas serialize `properties`/
    `required` as null; interleaved-thinking beta off unless explicitly set +
    fine-grained gate dead (`contextTools` type assert); simple wrapper drops
    effort/budget/maxTokens adjust; no signal/timeout/retry.
  - *P11 google family (unregistered)*: request nests all config under
    `"config"` (SDK-only wrapper, not a REST field); SSE fed to
    `json.Decoder`; `/models` path missing when BaseURL set (404); no status
    check; `skip_thought_signature_validator` injected into Gemini-3 replays
    (the vendored TS test forbids it); vertex base URL has a literal space +
    wrong host/path/version; reads `GOOGLE_API_KEY` instead of
    `GEMINI_API_KEY`; `toolConfig` sent without tools; cost never computed.
  - *P12 amazon-bedrock (unregistered)*: parser reads JSON over eventstream
    framing; request shape wrong (system as string, no `{json:}` schema
    wrapper, `toolUse` without toolUseId, toolResult string with hardcoded
    status); modelId dots→slashes corrupts ARNs/inference profiles; no
    thinking/cachePoint/additionalModelRequestFields; usage reads
    invoke-model-era keys; SigV4 omits the `host` header → every signature
    invalid; stop-reason mapping wrong.
  - *P13 mistral (unregistered)*: SSE via `json.Decoder`; camelCase keys
    against the snake_case wire (usage + tool calls silently lost); request
    payload camelCase ignored by the API; URL lacks `/chat/completions`;
    tool args don't accumulate; `toolcall_end` order randomized (Go map
    iteration); no abort support/status error formatting.
  - *P14 registry/wiring*: only 3 of 9 APIs registered — mistral, google,
    vertex, bedrock, azure, codex implemented but unreachable
    (register_builtins.go:91); api mismatch panics (api-registry.go:45,54 —
    TS throws catchable); catalog `Api` ids diverge (`"anthropic"`,
    `"google-shared"`, `"bedrock-converese-stream"`, `"mistral-chat"`);
    registry stream wrappers drop SessionID/TimeoutMs/MaxRetries/Metadata/
    OnPayload/OnResponse; oauth registry never populated + 2 providers don't
    satisfy the interface (custom providers unregistrable); the env-key
    resolver providers actually call (`helpers.go getEnvApiKey`) is a
    divergent rewrite — `GOOGLE_API_KEY` vs reference `GEMINI_API_KEY`, no
    `ANTHROPIC_OAUTH_TOKEN` precedence, `%s_API_KEY` default for unknown
    providers — while the faithful port (`env_api_keys.go`) is only used by
    auth_storage; `IsCloudflareProvider` misses `cloudflare-ai-gateway` and
    matches the singular `"cloudflare-worker-ai"` spelling;
    `ResolveCloudflareBaseUrl` panics instead of erroring.
  - *P15 structural*: `Model` has no `ThinkingLevelMap` (effort mapping and
    the openai-style off-value / openrouter `off !== null` guard can't mirror
    TS); no retries/timeout/signal on any provider (TS OpenAI SDK defaults:
    2 retries, 10-min timeout); overflow detection missing the Together
    pattern + Xiaomi silent-overflow case; `ValidateToolArguments` is a no-op
    (TS runs TypeBox Convert+Check); faux provider never emits `*_start`
    events and ignores options/abort/usage.
  - *P16 gemini-cli*: Go-only extra (no TS counterpart in this reference
    snapshot; Cloud Code Assist provider, likely from an older upstream pi) —
    unregistered; retry always hits `endpoints[0]` ignoring the failover
    index; dead baseURL trim. No parity verdict possible; keep-or-delete is a
    product call, not an audit item.
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
