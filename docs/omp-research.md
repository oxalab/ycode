# oh-my-pi (omp) — Research & Reference

> Source: `reference/oh-my-pi-main/oh-my-pi-main/` (fork of `pi-mono` by Mario Zechner, extended by Can Bölük).
> Generated: 2026-07-11. Purpose: reference for ycode (Go port informed by omp).

## What omp Is

omp is a **fork of `pi-mono`** rewritten as a **coding-first surface**. Tagline: *"The most capable agent surface that ships — continuously tuned by real-world use, complete out of the box, open all the way down."*

Scale: **40+ providers · 32 tools · 14 LSP ops · 28 DAP ops · ~55k lines of Rust core**.
Stack: **Bun/TypeScript + Rust N-API addon** (`@oh-my-pi/pi-natives`). The Rust does the heavy lifting for search/shell/AST/highlighting.

## Stack & Scale

- Languages: TypeScript (agent logic) + Rust (perf-critical native core) on Bun runtime.
- Four Rust crates: `pi-natives` (N-API cdylib aggregate), `pi-shell` (embedded bash via vendored `brush`, PTY, process mgmt), `pi-ast` (tree-sitter, 50+ grammars), `pi-iso` (workspace isolation: APFS clones, btrfs/zfs reflinks, overlayfs, projfs, rcopy).
- Vendored: `brush-core`, `brush-builtins` (full bash in-process).
- Shared `pi-walker` crate (parallel, ignore-aware, globset-based) underpins grep/glob/fs-scan-cache.
- N-API distribution: ESM loader picks per-platform `.node` binary (`${platform}-${arch}`, x64 CPU variant `modern`/`baseline`); separate optional-dependency leaf packages; embedded-archive extraction for compiled CLI; version sentinel; Windows node_modules staging.

---

## Tier 1 — Raises the Quality Floor

### 1.1 Hashline Anchors + Snapshot Store (`@oh-my-pi/hashline`)

**Problem it solves:** model emits a diff/patch with stale line numbers → patch corrupts the file. Every agent that does `str_replace` or line-number patches hits this.

**Mechanism:**
- `read`/`grep`/`glob` emit a **hashline header** before results: `[src/foo.ts#0A1B]` where `0A1B` is a 4-hex content-hash tag derived from file content.
- Each line is prefixed `LINE:TEXT` (or `*LINE:text` for matches in grep, `LINE|text` variants).
- The **snapshot store** records whole-file content hashes (`computeFileHash()` → 4-hex tag) so anchors are verifiable.
- `edit` consumes the hashline patch language: `[PATH#TAG]` sections with ops `SWAP N.=M:`, `DEL N.=M`, `INS.PRE/POST/HEAD/TAIL:`, plus **tree-sitter block ops** `SWAP.BLK N:`, `DEL.BLK N`, `INS.BLK.POST N:` (resolve full syntactic span from parse tree).
- Markdown `#`/`##` headings resolve as whole sections.
- **Stale-anchor recovery:** `Patcher` first attempts snapshot-store-based merge; on failure throws `MismatchError` with current hash + context. No silent corruption.
- **Lenient parsing:** tolerates `SWAP N:`, `DEL N`, missing colons, `N..M`/`N…M`/`N M` variants.
- **No-op guard:** after `NOOP_HARD_LIMIT = 3` consecutive byte-identical no-ops, escalates soft warning to `ToolError`.
- Rejects `apply_patch`/unified-diff `@@` sentinels with steered guidance.

**Impact:** Grok Code Fast 1 6.7%→68.3%. Grok 4 Fast −61% tokens. MiniMax 2.1× pass rate.

**Grammar:** `packages/hashline/src/grammar.lark`. Patches consumed from one `input` string. Post-edit header `[path#TAG]` carries the fresh tag.

**For Go:** Content-hash-verified edits are directly portable. Tree-sitter block ops need `tree-sitter-go` bindings. The hashline patch language itself is a small parser.

---

### 1.2 Session as Append-Only JSONL Tree

**Problem it solves:** flat session logs can't branch, can't rewind without data loss, and compaction becomes destructive surgery.

**Data model:**
- On-disk: `~/.omp/agent/sessions/<dir-encoded>/<timestamp>_<sessionId>.jsonl`.
- Dir-encoding: home-relative `-<rel>`, temp `-tmp-<rel>`, legacy absolute `--<abs>--`; `/`, `\\`, `:` → `-`.
- Line 1 = header (`type:"session"`, `version:3`, `id`, `timestamp`, `cwd`, optional `title`/`titleSource`/`parentSession`).
- Remaining lines = `SessionEntry` union.
- Every entry has `id` (8-char) and `parentId` (`null` = root). **Append creates child of current leaf; branching moves leaf only.**
- Mutable `leafId` pointer is the *only* mutable state — entries themselves are immutable.

**Entry taxonomy:** `message`, `thinking_level_change`, `model_change`, `service_tier_change`, `compaction`, `branch_summary`, `custom` (extension state, ignored by context builder), `custom_message` (extension message, participates in context), `label`, `ttsr_injection`, `session_init`, `mode_change`, `mcp_tool_selection`.

**`SessionEntryIndex` (runtime):** `entriesById: Map`, `children: Map<parentId, entries[]>`, `labels: Map`, `leaf: string|null`, `usage`. Kept in lockstep with journal array.

**Three leaf primitives:**
- `branch(entryId)` — validate + set leaf, no new entry.
- `resetLeaf()` — leaf=null, next append is new root.
- `branchWithSummary(branchFromId, summary, details?, fromExtension?)` — sets leaf + appends `branch_summary`.

**Operations:**
- `/tree` — in-file navigation. Computes abandoned path, optionally summarizes, moves leaf. Summary attached at *new* position, not abandoned tail.
- `/branch` — forks to new file. Requires source = user message. Root user msg → `newSession({parentSession})`. Otherwise `createBranchedSession(parentId)`.
- `/fork` — duplicates session (new id + file, `parentSession` = previous, `providerPromptCacheKey` inherited, all entries copied unchanged).
- `/resume` — switch session (terminal breadcrumb → newest by mtime → `resolveResumableSession`).

**`buildSessionContext(entries, leafId?, byId?, options?)`:** walks parentId chain leaf→root, reverses, derives runtime state (thinkingLevel, model map with role fallback, deduped injectedTtsrRules, latest MCP selection, latest mode), builds message list handling compaction boundary replay (`firstKeptEntryId`).

**Versioning/migration:** v3 current. v1→v2 adds `id`/`parentId`, reconstructs linear chain, migrates `firstKeptEntryIndex`→`firstKeptEntryId`. v2→v3 rewrites `role === "hookMessage"` → `"custom"`. Migrations at load, `#rewriteRequired` flag, synchronous full rewrite on next append.

**Blob externalization:** large strings truncated to `MAX_PERSIST_CHARS = 500,000`; image blocks with base64 ≥1024 chars → `blob:sha256:<hash>` refs. Blobs at `~/.omp/agent/blobs/<sha256>`.

**Two storage interfaces:** `SessionStorage` (filesystem ops) + `SessionStorageWriter` (append/flush/close). `FileSessionStorage` and `MemorySessionStorage` impls. Atomic full rewrites via temp-write+rename (EPERM-safe move-aside fallback).

**History (separate):** SQLite at `~/.omp/agent/history.db`, table `history(id, prompt, created_at, cwd, session_id)` + FTS5 `history_fts` with triggers.

**For Go:** Append-only JSONL + mutable leaf is trivially portable. The tree index is a couple of maps. Blob store is SHA-256 files on disk.

---

### 1.3 Tools That Compose (System, Not List)

**Problem it solves:** isolated tools each reinvent discovery, don't share state, and force the model to glue results together with prose.

** omp's composable tool system:**
- `read`/`grep`/`glob` emit **hashline anchors** → directly consumable by `edit`.
- Every FS mutation calls `invalidateFsScanAfterWrite/Delete/Rename(path)` → clears the shared scan cache.
- `write`/`edit` trigger **LSP writethrough** (format-on-write + diagnostics-on-write).
- Internal URL schemes (`pr://`, `issue://`, `agent://`, `skill://`, `rule://`, `conflict://`, `memory://`, `artifact://`) resolve transparently inside every FS-shaped tool.
- `gh` mutating subcommands in `bash` auto-invalidate the github-cache so later `issue://`/`pr://` reads stay fresh.

**Shared FS scan cache (`fs_cache.rs`):**
- Cache key (hard contract): canonicalized `root` + `include_hidden` + `use_gitignore` + `skip_node_modules` + `detail` (`Minimal`=path+filetype vs `Full`=+mtime+size). `follow_links` NOT in key.
- Freshness (global, env-overridable): `FS_SCAN_CACHE_TTL_MS` (1000), `FS_SCAN_EMPTY_RECHECK_MS` (200), `FS_SCAN_CACHE_MAX_ENTRIES` (16).
- `get_or_scan`: TTL=0 bypass; hit within TTL returns cloned entries + `cache_age_ms`; expired → evict+rescan+store.
- `force_rescan(store=false)`: remove+scan+don't repopulate. `force_rescan(store=true)`: remove+scan+store.
- Max entries = oldest-first eviction by `created_at` after insert.
- **Empty-result fast recheck (caller-side):** if filtered result empty AND `scan.cache_age_ms >= empty_recheck_ms()`, caller does one `force_rescan(store=true)` and retries.
- Process-local in-memory (`DashMap`); `.git` always excluded at scan time.

**Bounded output management (`OutputSink`):**
- Rolling **tail** window (`spillThreshold`, default 50KB); on overflow trims to tail (UTF-8 boundary safe), marks `truncated`.
- When `headBytes > 0` (default 20KB), retains **head** window, elides middle, splices elision marker.
- Per-line column cap: `maxColumns` (default 768 bytes) → ellipsis-truncated at write time.
- Tracks total bytes/lines; mirrors **raw uncapped** stream to artifact file on overflow/column-cap/file-active.
- `dump()` returns output, truncated, totalLines/totalBytes, outputLines/outputBytes, elidedBytes/Lines, columnDroppedBytes/columnTruncatedLines, artifactId.

**Command interception (bash steering):**
- `checkBashInterception()` blocks `cat`/`head`/`tail`→`read`, `grep`/`rg`→`grep`, `find`/`fd`→`glob`, `sed -i`/`perl -i`→`edit`, `echo > file`→`write`. Only when suggested tool is in `ctx.toolNames`.

**For Go:** scan cache = `sync.Map`/concurrent-map with struct key. OutputSink = bounded buffer + head/tail windows. Interception = regex rules table. All directly portable.

---

### 1.4 Bounded Output Management (OutputSink)

(see 1.3 OutputSink details)

---

## Tier 2 — High-Leverage Features

### 2.1 TTSR — Time Traveling Stream Rules

**Problem it solves:** rules injected at system-prompt-time eat context tax every turn and can't react to mid-stream violations.

**Mechanism:** rules sit dormant until model goes off-script; a regex/AST match **aborts the stream mid-token, injects the rule as a system reminder, and retries from the same point**.

**Rule discovery & bucketing (`rulebook-matching-pipeline`):**
- Canonical `Rule`: `name`, `path`, `content`, `globs?`, `alwaysApply?`, `description?`, `condition?` (regex), `astCondition?` (ast-grep), `scope?`, `interruptMode?`.
- Providers (priority desc): native (100), omp-plugins (90), agents (70), cursor (50), windsurf (50), cline (40), builtin-defaults (1).
- Native: `<cwd>/.omp/rules/*.{md,mdc}` + `~/.omp/agent/rules/*.{md,mdc}` + sticky `~/.omp/agent/RULES.md` + nearest ancestor `.omp/RULES.md`.
- `bucketRules`: (1) drop disabled; (2) drop builtins if off; (3) register `condition`/`astCondition` rules into `TtsrManager` → TTSR-only; (4) remaining `alwaysApply===true` → always-apply; (5) remaining with `description` → rulebook.
- Runtime: `description` rules listed in `<domain-rules>`; `alwaysApply` full content auto-injected in `<generic-rules>` block.

**Streaming monitor:**
- `turn_start` → `resetBuffer()`.
- `message_update` → monitor `text_delta`/`thinking_delta`/`toolcall_delta`.
- Tools with `matcherDigest` → replace buffer with snapshot + `checkSnapshot`; else append delta + `checkDelta` (synchronous regex).
- `hasAstRules()` → `await checkAstSnapshot` for edit/write streams.

**AST conditions:** list of ast-grep patterns (OR'd); repeated metavariables must be equal (`if ($X) clearTimeout($X)` matches, `clearTimeout($Y)` doesn't). Only on edit/write tool-argument streams. Language inferred from file path. Matched against reconstructed source snapshot, not raw wire delta. In-memory native `astMatch` (Smart strictness). Throttled per stream key.

**Trigger decision (interrupting):**
- matches + at least one allows interrupt → dedup into `#pendingTtsrInjections` → `#ttsrAbortPending=true` → `agent.abort()` immediately (synchronous) → `ttsr_triggered` fire-and-forget → retry scheduled with **50ms delay**.

**Retry (after 50ms):**
- `#ttsrAbortPending=false` → read `contextMode`.
- `discard` → `agent.replaceMessages(...slice(0, targetAssistantIndex))`.
- `keep` → partial remains, reminder appended after.
- Build injection from `ttsr-interrupt.md`: `<system-interrupt reason="rule_violation" rule="{{name}}" path="{{path}}">...{{content}}</system-interrupt>`.
- Append+persist hidden `custom_message` (`customType:"ttsr-injection"`).
- Mark injected + persist `ttsr_injection` entry.
- `agent.continue()`.

**Non-interrupting matches (by `matchContext.source`):**
- `source==="tool"`: bucket per tool call id; no abort; when tool produces result, `afterToolCall` hook **prepends** `ttsr-tool-reminder.md` (`<system-reminder ...>`) to `ctx.result.content`.
- `source==="text"/"thinking"`: queue in `#pendingTtsrInjections`; inject hidden `ttsr-injection` after successful message + schedule continuation.

**Repeat policy:** per-rule `lastInjectedAt`; `once` = trigger once after injection record; `after-gap` = re-trigger when `messageCount - lastInjectedAt >= repeatGap`.

**Persistence:** `appendTtsrInjection(ruleNames)`/`getInjectedTtsrRules()`/`SessionContext.injectedTtsrRules`/`restoreInjected(ruleNames)`. Restored in `createAgentSession`. **Injections survive compaction.**

**For Go:** self-contained subsystem. Synchronous-abort + deferred-continue (50ms) timing is the critical detail. AST conditions need tree-sitter. Regex is trivial.

---

### 2.2 Capability Discovery System

**Problem it solves:** hard-coded per-format importers; no principled precedence when multiple tools wrote config last quarter.

**Mechanism:** generic `loadCapability(<id>)` with provider-priority-desc + first-wins dedup by capability-specific key.

**Capability types & keys:**
- skills: key = `name`
- rules: key = `rule.name`
- slash-commands: key = `cmd.name`
- hooks: key = `${type}:${tool}:${name}`
- tools: key = `name`
- settings: **no dedup** (deep-merged in order)
- mcp servers: key = `server.name`
- extensions: dedup by absolute path
- agents: key = `name`

**Provider priorities:**
| Provider | Priority |
|---|---|
| native (`.omp`) | 100 |
| omp-plugins | 90 |
| claude (`.claude`) | 80 |
| claude-plugins / agents / codex | 70 |
| gemini | 60 |
| opencode | 55 |
| github | 30 |
| agents-md | 10 |
| builtin-defaults (rules) | 1 |
| omp-managed (skills) | 5 |

**Discovery roots:**
- Project: `<cwd>/.omp/*` (native = cwd-only, NO ancestor walk for most; skills + AGENTS.md/RULES.md walk-up to repo root).
- User: `~/.omp/agent/*` (profile-aware: `--profile <name>` → `~/.omp/profiles/<name>/agent/*`).
- Config files: `~/.omp/agent/config.yml`, `<cwd>/.omp/config.yml`, `~/.omp/agent/models.yml`, `<cwd>/.omp/mcp.json`.

**Cross-tool inheritance:** omp reads `.claude`, `.cursor`, `.windsurf`, `.gemini`, `.codex`, `.cline`, `.github/copilot`, `.vscode`, AGENTS.md — **no migration script**. Frontmatter YAML parser (`parseFrontmatter`) with `key: value` fallback if YAML fails.

**Context files (AGENTS.md / CLAUDE.md / etc.):**
- Dedup by scope: one user file kept (native wins); one project file per directory depth; at same depth higher priority wins; across depths multiple survive.
- Sorted root-first, cwd-last (cwd most prominent).
- `@` imports: expand inline before injection; resolve relative to importing file; `~/` → home; recurse up to 5 hops; cycles skipped; missing target leaves `@token` as-is.

**Settings resolution:**
- Five layers (lowest→highest): built-in defaults → global `~/.omp/agent/config.yml` → project `<cwd>/.omp/config.yml` → CLI overlays (`--config <file>`) → runtime overrides (CLI flags/env).
- **Deep-merge semantics:** objects deep-merge; scalars and arrays replaced wholesale (arrays do NOT append).
- Path-scoped arrays: `enabledModels`/`disabledProviders` accept bare strings or `{path: ~foo, models: [...]}` scoped entries.
- `disabledProviders` = one shared namespace gating model providers AND discovery sources.

**For Go:** the registry is a generic layer with a `key(item)` func per capability type. Deep-merge with array-replace is a ~50-line function. Path-scoping needs post-merge resolution against cwd.

---

### 2.3 Subagents (`task`)

**Problem it solves:** fan-out work with typed results, isolation, lifecycle, and concurrency control.

**Wire schema (shape-swapped by `task.batch`, default on):**
- Batch: `{agent, context, tasks: item[]}`. `context` rendered into every subagent system prompt `CONTEXT` section.
- Flat: `{agent, ...item}`.
- Item: `{id?, description?, role?, assignment, isolated?}`.

**Agent discovery (`task-agent-discovery`):**
- `AgentDefinition`: `name`/`description`/`systemPrompt` + optional `tools`/`spawns`/`model`/`thinkingLevel`/`output`/`blocking`/`autoloadSkills`/`readSummarize`.
- Source order (first-wins dedup by exact `name`): project `.omp/agents` → user `~/.omp/agent/agents` → Claude plugin `agents/` → bundled last.
- Bundled: `scout`, `designer`, `reviewer`, `librarian`, `task`, `sonic`.
- Availability: `task.disabledAgents`; parent spawn policy `getSessionSpawns()` (`*`/`""`/CSV); `PI_BLOCKED_AGENT` self-recursion guard; `task.maxRecursionDepth` (at max, `task` removed from child).

**Execution:**
- Sync runs when `async.enabled=false`/no job manager/`blocking:true`.
- Background: one `type:"task"` job per spawn; ids allocated up front via `AgentOutputManager`.
- `runSubprocess` creates child session with isolated settings (forces `async.enabled=false`, `bash.autoBackground.enabled=false`), child `agentId`, output schema, `CONTEXT`, `role` persona, IRC roster.
- Child tool availability: explicit `agent.tools`; auto-add `task` if spawns + depth allows; strip at max depth; ensure `irc`; expand `exec`→`eval`+`bash`; strip parent `todo`.
- Child must finish via hidden `yield` (up to 3 reminders, last forces `toolChoice=yield`).

**Isolation (`task.isolation.mode`):**
- Modes: `none`/`auto`/`apfs`/`btrfs`/`zfs`/`reflink`/`overlayfs`/`projfs`/`block-clone`/`rcopy`.
- PAL (`crates/pi-iso`) resolves with fallback.
- Merge = patch (capture/apply) or branch (commit `omp/task/<id>`, cherry-pick into parent).

**Lifecycle end states:**
- hard abort → `aborted` (terminal).
- isolated → `parked` (no reviver, workspace merged+cleaned).
- else → `idle` with `AgentLifecycleManager.global().adopt(id, {idleTtlMs, revive})`. `idle` parked after `task.agentIdleTtlMs` (default 420,000ms/7min).

**IRC tool (`irc`):**
- Process-global mailbox bus between live agents. Ops: `send`/`wait`/`inbox`/`list`.
- Fire-and-forget send. `MAILBOX_CAP=100` per agent.
- Delivery by recipient status: running→injected as non-interrupting aside; idle→real turn started; parked→`ensureLive(to)` revives then wake.
- **Wake-on-message is the only resume primitive.**

**Limits:** session-scoped `Semaphore` from `task.maxConcurrency`; `MAX_OUTPUT_BYTES=500_000`; `MAX_OUTPUT_LINES=5000`; `PROGRESS_COALESCE_MS=150`; `MAX_YIELD_RETRIES=3`.

**For Go:** session-per-subagent, semaphore for concurrency, channel-based mailbox bus. PAL isolation is OS-specific and the hard part (can start with copy-based).

---

## Tier 3 — Flashy but Expensive (Reference Only)

### 3.1 Snapcompact (Context Compression)

Replaces LLM summarization with **deterministic local archival**: discarded history serialized, whitespace-collapsed, printed onto model-aware PNG frames using bundled pixel fonts. Shape resolved from model id (Claude `11on16-bw`, Gemini `8on22-bw` at 2048px, GPT `8on22-bw` at 1568px, Kimi/GLM `8on16-bw`). Archive persists under `CompactionEntry.preserveData.snapcompact` as bounded source text + frames. Reconstructed as plain-text edge → imaged middle (foveated HQ/LQ/HQ) → plain-text edge. `maxFrames` default 80.

**Other compaction features:**
- 6 triggers (manual `/compact`, overflow recovery, incomplete-output recovery, threshold maintenance, mid-turn, idle).
- Pre-compaction pruning (`pruneToolOutputs`): protect newest 40k tool-output tokens, require ≥20k savings, never blank below 50 tokens.
- Useless-result elision (`USELESS_NOTICE = "[Uneventful result elided]"`).
- Cut-point logic: never cut at toolResult.
- Strategy `handoff` schedules post-prompt auto-handoff instead of writing compaction entry.

**For Go:** hardest to port — needs image rendering, pixel fonts, per-model shape tables. Skip for v1; use LLM summarization.

### 3.2 Advisor / Watchdog

Second model on `advisor` role reviews each turn, injects notes (nit/concern/blocker). EmissionGuard (dedup + rate limit in code). `immuneTurns` (default 3) after interrupt. `WATCHDOG.md`/`WATCHDOG.yml` rosters. Persisted to `<session>/__advisor.jsonl`.

### 3.3 Browser Automation

Puppeteer + 14 stealth patches, CDP-attached apps, or cmux WKWebView. Playwright ARIA snapshot bundle run in-page for deterministic `eN` ref ids.

### 3.4 Collab (Live Session Sharing)

`/collab` puts session on relay (default `wss://my.omp.sh`). E2E encrypted (AES-256-GCM, key in URL fragment, relay content-blind). Guests render natively in own TUI. Dot-joined room secret = 32-byte key + 16-byte write token.

### 3.5 DAP Debugger (28 ops)

Full DAP surface: launch/attach/step/breakpoints (incl. instruction/data)/evaluate/memory read-write/disassemble. Bundled profiling/heap-snapshot/SSE-capture/report-bundle.

### 3.6 web_search (25 providers)

`auto` walks 25-provider chain. `public` fans out to credential-free engines in parallel, dedupes, ranks by consensus. Site-aware extraction: github/arxiv/SO/MDN/docs.rs → structured markdown.

### 3.7 Marketplace

Claude-registry-compatible (`.omp-plugin/` preferred, `.claude-plugin/` fallback). 5 source types (relative/url/github/git-subdir/npm). User/project scope shadowing. **Marketplace plugins surface skills/commands/hooks/tools/MCP only — NOT extension modules.**

---

## Cross-Cutting Reference

### Provider & Model System

- **Registry assembly:** bundled catalog → `models.yml` → runtime discovery → extension-registered.
- **Availability:** provider NOT in `disabledProviders` AND (keyless OR resolvable credentials).
- **Credential precedence:** runtime override (`--api-key`) → `models.yml` `apiKey` → stored API key → stored OAuth (multi-account rotation) → env var (incl. `.env`) → `models.yml` fallback resolver.
- **`compat` block:** declarative per-endpoint quirk encoding (supportsStore, supportsDeveloperRole, thinkingFormat ∈ {openai,openrouter,zai,qwen,qwen-chat-template}, requiresMistralToolIds, openRouterRouting, etc.).
- **Canonical model coalescing:** multiple concrete ids → one canonical id (user override → bundled official-id → heuristic → fallback).
- **Roles:** `default, smol, slow, vision, plan, designer, commit, tiny, task, advisor`.
- **Context promotion (overflow recovery):** try `contextPromotionTarget` or smallest larger-context same-provider/API model; recorded as temp `model_change`.
- **Round-robin credentials:** stack API keys per provider, rotate with session affinity + per-credential backoff.
- **Fallback chains:** per-role chains under `retry.fallbackChains`; on 429/quota, next entry takes rest of turn, restored on cooldown.

### Streaming Internals

- Unified event shape: `start`; content block triplets `text_*`/`thinking_*`/`toolcall_*`; terminal `done`/`error`.
- `AssistantMessageEventStream` no longer throttles — every event in push order.
- Throttled partial-JSON re-parse: re-parse only after ≥256 new bytes (`STREAMING_JSON_PARSE_MIN_GROWTH`); final `toolcall_end` authoritative. Try `JSON.parse` → `RelaxedJson` → `{}`.
- Per-provider normalization (Anthropic content_block mapping, OpenAI Responses output_item, Google parts iteration).
- Stop-reason maps per provider.
- No hard backpressure (unbounded in-memory queues).

### pi-native Tool-Call Format

- XML-flavored, schema-driven, no special tokens. `<call:NAME …>…</call:NAME>`.
- Three forms: element (`<call:read><path>…</path></call:read>`), attribute (`<call:read path="…"/>`), inline-body (`<call:edit>*** Begin Patch…</call:edit>`).
- Schema-driven typing: `string` → verbatim; non-string scalar → `JSON.parse`; unknown → best-effort.
- Verbatim inline body terminated by named closer (`</call:NAME>`).
- No per-call wire id; results correlated positionally by emission order.

### Config & Settings

- **Files:** `~/.omp/agent/config.yml` (global), `<cwd>/.omp/config.yml` (project), `~/.omp/agent/models.yml`, `<cwd>/.omp/mcp.json`, `~/.omp/agent/agent.db` (auth, SQLite), `~/.omp/agent/keybindings.yml`, `~/.omp/agent/secrets.yml`.
- **Deep-merge:** maps merge; scalars/slices replace wholesale.
- **`.env` cascade:** process env → `$PWD/.env` → `~/.omp/agent/.env` → `~/.omp/.env` → `~/.env`. `OMP_*` mirrored to `PI_*` per-file.

### MCP

- 3 transports: stdio, HTTP (Streamable), SSE (legacy).
- 250ms fast-startup gate + `DeferredMCPTool` from cache. Slow servers don't block startup.
- Crash-storm circuit breaker: >5 reconnect attempts in 30s suspends auto-reconnect.
- Two-phase secret resolution: discovery `${VAR}`/`${VAR:-default}` expansion + pre-connect `!cmd` (10s timeout) → env var name → literal.
- Tool naming: `mcp__<sanitized_server>_<sanitized_tool>`.

### Retry Policy (Non-Compaction)

- Retryable: `stopReason==="error"` + errorMessage + not overflow + classifier refusal OR stale replay failure OR transient regex match.
- Partial output blocks retry EXCEPT for classifier refusals.
- Backoff: `min(baseDelayMs * 2^(attempt-1), 8000ms) * (75-100% jitter)`. Attempt 5+ caps at 8000ms pre-jitter.
- Credential switching + model fallback force delay 0.

### Memory Backends

- **`off`** (default), **`local`** (two-phase pipeline: per-session extraction → consolidation into MEMORY.md + skills), **`mnemopi`** (local SQLite + embeddings/FTS, polyphonic recall = 4 signals fused via reciprocal rank), **`hindsight`** (server-side).
- Tools: `retain`, `recall`, `reflect`, `memory_edit` (mnemopi only), `learn` (memory + skill in one call).
- Project-scoped by default (`per-project` = cwd basename + stable hash of absolute path).

### Extensibility Surface

- **Extension** = TS module, superset of hooks. Factory `function(pi: ExtensionAPI)`.
- **Hook** = TS factory `function(pi: HookAPI)`, routed through extension runner.
- **Custom tool** = TS module, model-callable, narrowest surface.
- **Skill** = `SKILL.md` per skill, file-backed.
- **MCP** = JSON-RPC server, 3 transports.
- **Event surface:** session lifecycle (`session_*` with cancelable pre-events), prompt/turn, tool (`tool_call` fail-closed block, `tool_result` middleware chain), reliability (`auto_*`, `ttsr_triggered`).

### Auth Broker/Gateway

- **auth-broker:** holds canonical SQLite vault, background refresher, REST API.
- **auth-gateway:** forward-proxy resolving broker credential, dispatching via pi-ai providers.
- Redacted snapshot model (refresh tokens never leave broker).
- Two-tier usage cache: 5min jittered server + 15s single-flight client.
- Encrypted snapshot cache keyed by token+URL (AES-256-GCM).

---

## Go Porting Notes (Preliminary)

| Subsystem | Difficulty | Go Approach |
|---|---|---|
| Hashline anchors + snapshot store | Medium | Content-hash + tree-sitter-go for block ops |
| Session JSONL tree | Easy | Append-only file + maps + leaf pointer |
| FS scan cache | Easy-Medium | sync.Map + invalidation hooks |
| OutputSink | Easy | Bounded buffer + head/tail windows |
| Command interception | Easy | Regex rules table |
| TTSR | Medium | Sync-abort + deferred-continue + tree-sitter |
| Capability discovery | Medium | Generic registry + priority + key fn |
| Subagents | Medium-Hard | Session-per-child + semaphore + channels |
| In-process shell | Medium | `mvdan/sh` + `creack/pty` |
| Provider/compat system | Hard | Struct config + per-endpoint quirk table |
| TUI append-only renderer | **Hardest** | Custom on Bubble Tea or from scratch |
| Snapcompact | Hardest | Skip (needs image rendering, pixel fonts) |

**Go advantages over omp's Node/Rust split:**
- Static binary (no N-API loader/binary-selection layer).
- `mvdan/sh` = in-process shell for free.
- `creack/pty` = PTY.
- `doublestar` + `go-ignore` = walker.
- Goroutines + channels for async jobs / mailbox.
- `os/exec` with process groups for cancellation.

**Go disadvantages:**
- Bubble Tea's Elm Architecture doesn't naturally express the append-only scrollback ledger.
- No native tree-sitter bindings as mature as Rust's (but usable).
- Reference equality memoization → need dirty flags / version counters.
