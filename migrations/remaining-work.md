# Migration: Remaining Work & Gap Analysis

> Last updated: 2026-06-05
> Branch: `workspace`
> All core business logic (Phases 11-13) is **complete and compiling**.
> **Sprint 1 complete** — All agent-sdk API calls wired (9 TODOs resolved). `AgentState` interface expanded with lifecycle + queue methods.
> **Sprint 2 complete** — Stub functions implemented (`SendCustomMessage`, `SendUserMessage`, images), 5 extension actions wired, runtime callbacks injected.
> **Sprint 3 complete** — P1 utilities verified (most already existed). Added path helpers (`CanonicalizePath`, `GetCwdRelativePath`, `FormatPathRelativeToCwdOrAbsolute`), detached process tracking (`TrackDetachedChildPid`, `KillTrackedDetachedChildren`), exported `KillProcessTree` from tools package.
> **Sprint 4 complete** — Missing core files implemented. `diagnostics` already existed in `resource_loader.go`. `output-guard` already existed as `intercept.go`. Added `footer_data_provider.go` (git branch/status) and `keybindings.go` (54 shortcuts + manager).
> **Sprint 5 complete** — Model/LLM gaps resolved. Thinking levels now check `model.Reasoning`. AWS Signature V4 signing implemented for Bedrock provider.

---

## Part A: Unresolved TODOs & Stubs in Existing Go Code

These are already-written Go files with `// TODO` comments, placeholder returns, or unwired functionality.

### A.1 Agent-SDK API Calls — ✅ RESOLVED (Sprint 1)

All 9 agent-sdk TODOs have been wired. The `AgentState` interface in `agent-sdk/src/types.go` was expanded to include lifecycle methods (`Prompt`, `Continue`, `Abort`, `WaitForIdle`), queue operations (`Steer`, `FollowUp`, `SetSteeringMode`, `SetFollowUpMode`, `HasQueuedMessages`, `ClearAllQueues`), subscription (`Subscribe`), and `Reset`.

| # | File | Wired Call |
|---|------|-----------|
| 1 | `agent_session_compaction.go:36` | `s.Agent.Abort()` |
| 2 | `agent_session_compaction.go:252` | `s.Agent.Continue(context.Background())` |
| 3 | `agent_session_retry.go:144` | `s.Agent.Continue(context.Background())` |
| 4 | `agent_session_prompt.go:232` | `s.Agent.Prompt(context.Background(), expandedText)` |
| 5 | `agent_session_prompt.go:264` | `s.Agent.Steer(llm.UserMessage{...})` |
| 6 | `agent_session_prompt.go:270` | `s.Agent.FollowUp(llm.UserMessage{...})` |
| 7 | `agent_session_prompt.go:409` | `s.Agent.Abort()` |
| 8 | `agent_session_model.go:270` | `s.Agent.SetSteeringMode(sdk.QueueMode(mode))` |
| 9 | `agent_session_model.go:275` | `s.Agent.SetFollowUpMode(sdk.QueueMode(mode))` |

### A.2 Unimplemented Function Bodies — ✅ RESOLVED (Sprint 2)

| # | Function | Implementation |
|---|----------|---------------|
| 1 | `SendCustomMessage()` | 4-case switch: `nextTurn` → pending queue, streaming → Steer/FollowUp, triggerTurn → Prompt, default → append + emit events |
| 2 | `SendUserMessage()` | Delegates to `Prompt()` with `expandPromptTemplates: false`, `source: "extension"` |
| 3 | Image handling in `Prompt()` | Appends `currentImages` to `userContent` with type assertion to `map[string]any` |

Additional changes: `PromptOptions.Images` type changed from `llm.ImageContent` to `[]any`. `opts.Images` access updated from `.Data` to `len()`.

### A.3 Extension Wiring Placeholders — ✅ RESOLVED (Sprint 2)

All 5 extension action placeholders wired via runtime callback pattern:

| # | Action | Implementation |
|---|--------|---------------|
| 1 | `NewSession` | Delegates to `s.onNewSession(opts)` → `AgentSessionRuntime.NewSession()` |
| 2 | `Fork` | Delegates to `s.onFork(entryID, opts)` → `AgentSessionRuntime.Fork()` |
| 3 | `NavigateTree` | Calls `s.NavigateTree(targetID)` directly (session method) |
| 4 | `SwitchSession` | Delegates to `s.onSwitchSession(sessionPath, opts)` → `AgentSessionRuntime.SwitchSession()` |
| 5 | `Reload` | Calls `s.Reload()` directly (session method) |

New infrastructure: `AgentSession` struct has 3 callback fields (`onNewSession`, `onFork`, `onSwitchSession`). `AgentSessionRuntime.injectSessionCallbacks()` wires them after creation and after session replacement (`apply()`).

### A.4 Model/LLM Feature Gaps — ✅ RESOLVED (Sprint 5)

| # | Issue | Fix |
|---|-------|-----|
| 1 | `GetAvailableThinkingLevels()` returned hardcoded levels | Now checks `model.Reasoning` — returns `{off}` for non-reasoning models, all levels for reasoning models |
| 2 | `SupportsThinking()` always returned true | Now returns `model.Reasoning` |
| 3 | AWS Signature V4 signing for Bedrock | Implemented `aws_sigv4.go` (~130 lines): full Sig V4 signing (canonical request, string-to-sign, HMAC-SHA256 derived key, Authorization header). Wired into `amazon-bedrock.go` |
| 4 | OAuth AWS signing | Confirmed already resolved — no TODO exists in current code |

### A.5 Minor / Low-Priority

| # | File:Line | Issue |
|---|-----------|-------|
| 1 | `agent_session.go:109` | `baseToolsOverride: nil, // TODO: wire from config` — needs config plumbing |
| 2 | `agent_session.go:275` | `installAgentToolHooks()` — empty, needs `BeforeToolCall`/`AfterToolCall` hooks |
| 3 | `tools/read.go:248` | `ResizeImage()` — TODO: actual image resizing with `image/draw` |
| 4 | `tools/path_utils.go:27` | `tryNFDVariant()` — TODO: macOS NFD Unicode normalization with `golang.org/x/text` |
| 5 | `system_prompt.go:46,56` | `getReadmePath()`/`getExamplesPath()` — TODO: consider moving to `configuration.go` |
| 6 | `agent-sdk/agent.go:70-72` | `PendingMessageQueue.Clear()` — empty body |

---

## Part B: Missing Core Files (Non-TUI/CLI) — ✅ ALL RESOLVED (Sprint 4)

### B.1 `diagnostics.ts` — ✅ Already Existed
- `ResourceCollision` → `CollisionInfo` in `resource_loader.go`
- `ResourceDiagnostic` in `resource_loader.go` with full collision support

### B.2 `output-guard.ts` — ✅ Already Existed
- Implemented as `intercept.go` with `TakeOverStdout`, `RestoreStdout`, `IsStdoutTakenOver`, `WriteRawStdout`, `FlushRawStdout`

### B.3 `footer-data-provider.ts` — ✅ Implemented (Sprint 4)
- **File:** `footer_data_provider.go` (~340 lines)
- **Content:** `GitPaths`, `FindGitPaths()`, `ResolveBranchSync/Async()`, `ResolveHeadFromFiles()`, `IsGitDirty()`, `FooterDataProvider` with `Watch`/`Refresh`/`OnChange`, poll-based HEAD file watching
- **Note:** Uses polling for file watching — can be replaced with `fsnotify` later

### B.4 `keybindings.ts` — ✅ Implemented (Sprint 4)
- **File:** `keybindings.go` (~340 lines)
- **Content:** `AppKeybinding`/`AppKeybindings` types, `DefaultKeybindings()` (54 shortcuts), `KeybindingNameMigrations` (30+ name mappings), `MigrateKeybindingsConfig()`, `KeybindingsManager` with `Load`/`Keybindings()`, `applyKeybindingOverrides()`

---

## Part C: Missing Utility Files (from `src/utils/`) — Sprint 3 Status

Sprint 3 verified that most P1 utilities already exist in Go. Only small gaps were filled.

### ✅ Already Implemented (verified Sprint 3)

| TS File | Go Location | Status |
|---------|-------------|--------|
| `utils/paths.ts` — `canonicalizePath` | `skills.go:419` | ✅ |
| `utils/paths.ts` — `isLocalPath` | `resource_loader.go:1116` | ✅ |
| `utils/shell.ts` — `getShellConfig`, `SanitizeBinaryOutput` | `shell_config.go` | ✅ |
| `utils/shell.ts` — `getShellEnv` | `tools/bash.go:65` | ✅ |
| `utils/shell.ts` — `killProcessTree` | `tools/bash_unix.go`, `tools/bash_windows.go` | ✅ (exported as `KillProcessTree`) |
| `utils/git.ts` — `parseGitUrl` | `package_manager_utils.go:839` | ✅ |

### ✅ Added in Sprint 3

| Go File | Functions Added |
|---------|----------------|
| `tools/path_utils.go` | `CanonicalizePath`, `GetCwdRelativePath`, `FormatPathRelativeToCwdOrAbsolute` |
| `shell_config.go` | `TrackDetachedChildPid`, `UntrackDetachedChildPid`, `KillTrackedDetachedChildren` |
| `tools/bash_unix.go`, `tools/bash_windows.go` | Exported `killProcessTree` → `KillProcessTree` |

### Still Missing (P2-P3, not blocking core)

| # | TS File | Lines | What It Does | Priority | Notes |
|---|---------|-------|-------------|----------|-------|
| 1 | `utils/fs-watch.ts` | ~100 | Cross-platform file watcher | **P2** | Needed by footer-data-provider, resource-loader |
| 2 | `utils/clipboard.ts` | ~80 | Clipboard read/write | **P2** | Needed by /copy command |
| 3 | `utils/clipboard-native.ts` | ~100 | Native clipboard via platform APIs | **P2** | Platform-specific clipboard |
| 4 | `utils/clipboard-image.ts` | ~60 | Image clipboard support | **P3** | Image paste in prompt |
| 5 | `utils/image-convert.ts` | ~150 | Image format conversion | **P3** | Image preprocessing |
| 6 | `utils/image-resize.ts` | ~100 | Image resizing | **P3** | Image preprocessing |
| 7 | `utils/frontmatter.ts` | ~80 | YAML/JSON frontmatter parser | **P2** | Skill/prompt file parsing |
| 8 | `utils/mime.ts` | ~40 | MIME type detection | **P2** | File type detection |
| 9 | `utils/child-process.ts` | ~60 | Child process helpers | **P2** | Process management |
| 10 | `utils/changelog.ts` | ~100 | Changelog parsing/display | **P3** | /changelog command |
| 11 | `utils/version-check.ts` | ~80 | Version comparison | **P3** | Update notifications |
| 12 | `utils/pi-user-agent.ts` | ~20 | User-Agent header | **P3** | HTTP headers |
| 13 | `utils/tools-manager.ts` | ~150 | Custom tool discovery/loading | **P2** | Custom tool registration |
| 14 | `utils/exif-orientation.ts` | ~80 | EXIF orientation correction | **P3** | Image handling |
| 15 | `utils/photon.ts` | ~30 | Photon WASM bindings | **P3** | Bun-specific, skip |

---

## Part D: TUI + CLI + Entry Points (DEFERRED)

> These will be planned separately. Listed here for completeness only.

### D.1 Root Entry Points (6 files, ~2,440 lines)
| File | Lines | Content |
|------|-------|---------|
| `main.ts` | 727 | App entry point, argument parsing, mode selection |
| `config.ts` | 500 | Configuration loading, merge, validation |
| `index.ts` | 353 | Public API surface / exports |
| `package-manager-cli.ts` | 523 | Package manager CLI commands |
| `migrations.ts` | 315 | Config/settings migration system |
| `cli.ts` | 22 | CLI entry bootstrap |

### D.2 CLI Package (6 files, ~1,500+ lines)
`src/cli/args.ts`, `config-selector.ts`, `file-processor.ts`, `initial-message.ts`, `list-models.ts`, `session-picker.ts`

### D.3 TUI / Modes/Interactive (44 files, ~15,000+ lines)
Entire terminal UI: `modes/interactive/interactive-mode.ts` + 45+ components (assistant-message, bash-execution, footer, model-selector, session-selector, settings-selector, theme-selector, etc.)

### D.4 RPC Mode (4 files, ~2,000+ lines)
`modes/rpc/rpc-client.ts`, `rpc-mode.ts`, `rpc-types.ts`, `jsonl.ts`

### D.5 Other Modes
`modes/print-mode.ts` (~500 lines) — non-interactive output mode

---

## Part E: Execution Status

### Sprint 1 — ✅ COMPLETE — Wire Agent-SDK Calls
9 TODOs resolved. `AgentState` interface expanded to 28 methods.

### Sprint 2 — ✅ COMPLETE — Implement Stub Functions + Extension Wiring
`SendCustomMessage`, `SendUserMessage`, image handling, 5 extension placeholders wired. Runtime callback injection.

### Sprint 3 — ✅ COMPLETE — P1 Utilities
Most already existed. Added path helpers + detached process tracking. Exported `KillProcessTree`.

### Sprint 4 — ✅ COMPLETE — Missing Core Files
Diagnostics + output-guard already existed. Added `footer_data_provider.go` + `keybindings.go`.

### Sprint 5 — ✅ COMPLETE — Model/LLM Gaps
Thinking levels check `model.Reasoning`. AWS Sig V4 signing for Bedrock.

### Sprint 6 — 🔴 DEFERRED — TUI + CLI
Separate planning doc required. Depends on TUI framework choice (Bubble Tea / OpenTUI).

---

## Statistics

| Category | Items | Est. Lines |
|----------|-------|-----------|
| Agent-SDK wiring (A.1) | 9 TODOs | ~30 lines changed |
| Stub functions (A.2) | 3 functions | ~100 lines |
| Extension wiring (A.3) | 5 placeholders | ~80 lines |
| Model/LLM gaps (A.4) | 4 items | ~150 lines |
| Minor (A.5) | 6 items | ~50 lines |
| Missing core files (B) | 4 files | ~680 lines |
| P1 utilities (C, 1-3) | 3 files | ~600 lines |
| P2 utilities (C, 4-16) | 13 files | ~900 lines |
| **Total (excl. TUI/CLI)** | **~47 items** | **~2,590 lines** |
| TUI + CLI + Entry (D) | ~60 files | ~21,000 lines |
