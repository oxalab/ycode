# Feature roadmap — the post-audit plan of action

The port-divergence audit (docs/port-divergence-audit.md) is CLOSED: 42 stories, 34/34
findings, the P9–P16 backlog, and every deferred-by-choice item fixed (story 42, 2026-11-24).
What remains between ycode and the TS reference is not divergence — it is a short list of
documented, feature-sized gaps in the extension runtime. This file is the plan of action for
closing them, with the same working agreements as the audit: one story per turn, plan approval
before code, hand-verify the TS reference at source, mutation-verify every fix (`-count=3`;
`-race` is broken machine-wide), and the user runs every git command.

The order below is the recommended queue — dependency-driven, not priority-ranked. Any item can
jump ahead if its capability is needed sooner. **Status: F1 ✅ RESOLVED 2026-11-25; F2 ✅
RESOLVED 2026-11-27; F4, F3 pending.**

---

## F1 — Bridge the action surface to JS — ✅ RESOLVED (2026-11-25)

**Ground truth going in (hand-verified at source)**: the Go machinery was complete and
production-wired — `BindCommandContext` called from `applyExtensionBindings` in all three
modes, `CreateCommandContext` exposing the six command actions — but `CreateCommandContext`
had ZERO callers (commands got the base ctx via the shared `SetToolCtxProvider` provider) and
the six action impls were wired NOWHERE (all modes pass zero-value `CommandContextActions`).
`BuildJSContext` dropped every func-valued entry, so even `ctx.abort()`/`ctx.compact()`/
`ctx.shutdown()` (TS ExtensionContext members, types.ts:298-327) were `undefined` from JS.
TS parity targets pinned at source: only slash-command handlers get ExtensionCommandContext
(types.ts:1066 vs 1079); the switch actions return `Promise<{cancelled}>` (the only source of
`cancelled:true` is a `session_before_switch`/`session_before_fork` veto); `withSession` runs
last on the NEW session's runner.

**Implementation**:
- *Deferred action bridge* (js_runtime.go): a dispatcher goroutine + pure-JS promise
  registry. The shim (`__piMakeAction`, installed in a separate bootstrap eval) returns a
  native Promise and `__piSubmitAction` (Go SetFunc) only enqueues; the dispatcher executes
  OUTSIDE `j.mu` (session switches emit events that re-enter the runtime via ordinary lock
  contention — never inside the submitting handler's Eval, where qjs forbids re-entrancy),
  then settles the promise via `globalThis.__piCompleteAction` from a fresh Eval entry.
  `SetActionExecutor` is the seam (core supplies the runtime-backed switch — the
  SetToolCtxProvider precedent).
- *Probe result*: the qjs eval PUMPS pending jobs across entries — `await ctx.newSession()`
  continuations resume (probe-verified before implementation; pinned by
  TestActionDispatcherResumesAwaitContinuation).
- *ctx rendering* (js_context.go): `abort`/`compact`/`shutdown` as fire-and-forget shims on
  EVERY ctx (they emit events; the story-42 compact precedent); the six command actions as
  dispatcher-backed shims ONLY when the map carries them — the TS interface split falls out
  of which map the caller hands over.
- *Command ctx source* (js_api.go): the command bridge resolves `SetCommandCtxProvider` →
  tool provider → `{"cwd"}`; wired in buildRuntime to `CreateCommandContext()`.
- *Production wiring* (agent_session_runtime.go injectSessionCallbacks — the one place
  holding both runtime and session): runtime-backed default command actions stored on the
  session (the modes' deferred headers said these were wired nowhere — now they are, for all
  three modes at once); explicit `ExtensionBindings.CommandContextActions` (now a pointer)
  overrides. The callbacks honor parentSession/position/withSession; the reload guard carries
  TS's exact wording.
- *ReplacedSessionContext*: gains `Ctx` (the replacement session's `CreateCommandContext`);
  `BuildReplacedSessionContext` renders it + fire-and-forget sendMessage/sendUserMessage;
  `invokeWithSessionCallback` fires the pinned JS fn on the submitting runtime (old
  instance's closure, NEW session's ctx — TS-faithful; nothing closes JSRuntimes mid-switch).

**Bonus bugs (both pre-existing, found by the F1 test matrix)**:
1. **PIValue double-free**: `InvokeFactory` stashes the pi value on `globalThis.__pi`
   (qjs SetPropertyStr consumes the transferred reference) and resets it — the value was
   already dead when every caller's `SafeFree(jsAPI.PIValue())` ran (loadJSExtension + 6 test
   sites). Removed all of them; ownership now matches story-42's rule: InvokeFactory consumes.
2. **get→set→Free ref-accounting bug**: `GetPropertyStr` hands over one reference and
   `SetPropertyStr` consumes it — so `fnVal.Free()` after `obj.SetPropertyStr(jsName, fnVal)`
   left TWO holders on ONE reference (a dangling-property bomb that detonates when the
   scratch global is re-installed). Fixed in `setMethod` (every pi method!) and all six
   buildJSUI shims + the new helpers: never Free after a consuming set.

**Tests**: dispatcher resolve/reject/no-executor/panic-recovery/await-resume pins; event-vs-
command ctx surface (TS split); CreateCommandContext six + silent no-op defaults
(TS bindCommandContext(undefined)); withSession end-to-end through the dispatcher;
BuildReplacedSessionContext rendering + stale-panic degradation; core defaults + override +
payload threading + arg guards + reload guard; commandCtxMap stale recovery.

**Mutations: 8/8 detected** — command closure→base provider, abort shim dropped, completion
id off-by-one, withSession skipped, parentSession dropped, defaults not injected, cancelled
flipped, commandCtxMap recover removed (panic). Build + vet + full suite + `-count=3` green
(one providers WS abort test flakes under ×3 load timing — standalone ×5 green; pre-existing,
untouched by this story).

**Residuals (documented, small)**: `newSession`'s `setup(sessionManager)` callback is not
bridged (SessionManager has no JS rendering); the modes' TUI glue (editor text on
navigateTree, loading animation) does not run for extension-initiated switches; send-shim
arguments coerce via `.String()` (CustomMessage object fidelity is future work); Go's
NavigateTree takes no options (no summarize/instructions variants).

## F2 — pi.exec — ✅ RESOLVED (2026-11-27)

**Ground truth (hand-verified at source — four sketch corrections)**: TS exec is a tiny
self-contained module (core/exec.ts, 107 lines, imported ONLY by extensions/loader.ts:300-303):
`ExecOptions = {signal?, timeout?, cwd?}` — NO `env` option (the child inherits the process
environment), no default timeout; `ExecResult = {stdout, stderr, code, killed}` — `code`, NOT
`exitCode`; it **never rejects** (the promise executor only resolves — even ENOENT resolves
`{stdout:"", stderr:"", code:1, killed:false}`, the catch swallows the error); timeout/abort
both funnel into `killProcess` (SIGTERM; the 5s SIGKILL escalation is dead code in TS — Node
sets `proc.killed` on the first kill), `killed:true` is the sole indicator, signal-death exit
code is null → `code ?? 0`; spawn is direct with `shell:false` (no Windows .cmd special-casing —
that's `shouldUseWindowsShell`, used by the bash tool, never here). Two roadmap premises died:
the executor needs NO seam (it's stdlib-only — the "bash-executor-backed implementation" belief
was wrong; `core/exec.go` was a dead zero-caller v1 duplicate and got DELETED), and the
binding idiom is NOT SetAsyncFunc (see below).

**Implementation**:
- *exec.go (NEW)* — the execCommand port, stdlib-only: no shell, `cmd.Dir` (empty = inherit),
  StdoutPipe/StderrPipe pumps, timer armed post-Start only when `timeoutMs > 0`, kill =
  Signal(SIGTERM) → Kill fallback (the TS escalation skipped as dead code), the
  `waitForChildProcess` port (100ms post-exit pump grace then force-close — a detached
  descendant holding the pipe write-end can never block completion, mirroring bash.go's
  `exitStdioGraceMs`), `exitCodeFromRaw` (-1 = signal death = Node's null → 0), Start-error →
  `{code:1, empty}` and NEVER an error return.
- *pi.exec binding* — a leak-free sync `SetFunc` installed in BuildJSAPI's stub slot +
  `setMethod` copy, capturing the load cwd per extension (TS loader.ts:300-303's exact
  per-instance closure); blocking-synchronous exec (the ui.input idiom — the roadmap's own
  original sketch); result returned as a plain await-compatible value. Pre-aborted signal
  short-circuits to `{code:0, killed:true}` with no run; arg validation throws synchronously
  (Node's spawn-arg observable).
- *BONUS FIX 1 — the qjs SetPropertyStr name-handle LEAK (repo-wide)*: the binding's
  `SetPropertyStr` mallocs the property-name handle (NewStringHandle) and NEVER frees it —
  its GetPropertyStr sibling frees the identical handle. Every set leaked WASM heap; the
  leak is the mechanism behind the F1 incident's allocation sensitivity ("flag-default reads
  silently failed"; "extra SetFunc (dummy also broke it)"). NEW js_property.go:
  `setPropertyNoLeak`/`setFuncNoLeak`/`setAsyncFuncNoLeak` (identical host call + the missing
  free; the F1 ref-consumption contract unchanged) — swapped across ALL extensions call
  sites (production + tests).
- *BONUS FINDING 2 — QJS_Eval AWAITS promise completion values* (eval.c:303-305:
  `JS_IsPromise(result)` → `js_std_await`): an eval ending in a `.then(...)` promise pumps
  jobs until it settles while the caller holds `j.mu` — a guaranteed deadlock for any
  resolution that needs the runtime lock. Documented gotcha; test evals end with a string
  completion value.
- *Install-shape law (bisected to ground)*: the BuildJSAPI→factory window tolerates ONLY a
  leak-free sync install + setMethod copy. Async proxies (their C side compiles a JS template
  per creation), evals, or extra installs — even at bootstrap — corrupt the NEXT factory
  compile (the F1 symptom) unless the per-set leak is fixed first; a 5×5 install-shape matrix
  pinned it. Goroutine+delayed `this.Promise().Resolve` after an async proxy was
  probe-verified WORKING (the F1 "Resolve won't work on native promises" note applies to
  `new Promise` only — proxy promises resolve fine) — but the async proxy's creation cost
  prices it out of the poisoned window.

**Tests**: exec.go pins (stdout/exit-code/stderr collection, ENOENT → code 1 empty, timeout
kill fast + killed:true, cwd honored, `exitCodeFromRaw` matrix, stream-drain deadline) via a
helper-process child (this test binary re-spawned — cross-platform, no shell); JS-level pins
(result key-set exactly stdout/stderr/code/killed, args pass-through, spawn-failure resolves,
timeout kills, pre-abort short-circuit, default-cwd plumbed) — all synchronous, no polling.

**Mutations: 8/8 detected** — timeout not armed, kill call removed, signal-death mapping,
ENOENT swallow, args dropped, pre-abort check, load-cwd default, killed flag (distinct tests
each; M2's first attempt was a no-op mutant — duplicated the block instead of disabling it).
Build + vet + full suite + `-count=3` green (all 12 packages ×3).

**Residuals (documented)**: no live AbortSignal — `signal` is the story-42 `{aborted}`
snapshot, checked once at call time (F4 graduates it); pi.exec parks the JS thread for the
command's duration (TS interleaves) — bound by the `timeout` option, the ui.input precedent;
`.then/.catch` chains on the result don't exist (plain value — `await` is unchanged; TS's own
examples all `await`); Windows kill is TerminateProcess direct-child (TS-faithful — tree-kill
is bash-tool-only); `runtime.assertActive` staleness guard not ported (Go has no extension
unload); the 5s SIGKILL escalation skipped (dead code in TS).

## F4 — Live AbortSignal (polish; after F1)

**Goal**: `ctx.signal` graduates from the story-42 `{aborted}` snapshot to the TS AbortSignal
shape: live `aborted` reads (accessor, not a copied bool), `addEventListener("abort", fn)`,
mid-execute polling.

**Scope sketch**: `signal.aborted` as a getter is easy — a `SetFunc` calls into Go
synchronously on each access (the `ui.notify` pattern). The listener is the hard part: firing
a JS callback from a goroutine crashes the non-reentrant runtime — F1's dispatcher machinery
exists for exactly this, and F2 added a second proven path (goroutine + delayed
`this.Promise().Resolve` on an async-proxy promise, probe-verified 2026-11-27) — note the
async proxy can only be created in the bootstrap/handler-time windows, never in the
BuildJSAPI→factory window (see F2's install-shape law).

**Tests**: aborted accessor reflects cancellation mid-execute; listener fires (or the
documented poll-based equivalent); regression: the `{aborted}` snapshot path stays for JSON
bridges (`ScrubRunnerCtx`).

**Risk**: medium, mostly shared with F1's design work.

## F3 — OAuth login-callback bridge (medium-large; hardest)

**Goal**: custom providers registered from JS extensions can carry OAuth login flows — the
last story-38 residual, and the natural sequel to story 41's add-second-model TUI work.

**Ground truth**: TS model-registry.ts:857-864 registers function-carrying provider objects;
story 42 made the non-oauth path work end-to-end (real providerActions flush at bindCore).
What remains: a provider config whose auth method is a JS login function must be callable from
Go (the login dialog invokes it; the flow opens a browser and waits for a redirect server for
minutes) and may call back into `pi`/ui while running.

**Scope sketch**: this is the hardest qjs problem in the queue — a minutes-long flow cannot
park the JS thread like `ui.input`, and invoking a pinned JS function from a goroutine crashes
the non-reentrant runtime. Real design work required (likely a dedicated runtime-invocation
contract or a queued scheme built on F1's machinery), plus the auth-storage plumbing to accept
a bridged login result. Do this one with fresh context, not late in a long turn.

**Tests**: provider with a JS login fn registered → login invoked → credentials stored and
refresh works; failure paths (user cancels, callback server times out); the login dialog reads
`UsesCallbackServer` correctly for bridged providers.

**Risk**: high — new cross-runtime invocation pattern.

---

## Working agreements (unchanged from the audit)

One story per turn, triggered by the user; EnterPlanMode approval before code; read the TS
reference at source, never trust summaries (this file included); mutation-verify every fix
(re-run mutations in isolation when one panics; `-count=3` because `-race` is broken
machine-wide); repair the harness PATH + absolute go/git paths when they arrive corrupted;
the user runs every git command; never read or print `C:\Users\ASUS\.ycode\agent\models.json`
or `auth.json` (real credentials live there — the repo itself is verified key-free).
