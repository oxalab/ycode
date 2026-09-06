# Migration Progress: coding-agent/src/core

> Last updated: 2026-06-04
> Branch: `workspace`
> Source reference: `reference/coding-agent/src/core/`

## Overview

Migrating the TypeScript `coding-agent/src/core/` package to Go. The reference TS source is ~3000+ lines in `agent-session.ts` alone, plus supporting modules.

---

## Phase 11: Agent Session (COMPLETE ✅)

Split from a single 3000+ line TS file into 9 Go files by concern.

### Phase 11.1: agent_session_types.go ✅
- **Lines:** 175
- **Content:** All type definitions — `AgentSessionEvent`, `AgentSessionConfig`, `PromptOptions`, `ModelCycleResult`, `SessionStats`, `TokenStats`, `ToolDefinitionEntry`, `ParsedSkillBlock`, `ParseSkillBlock()`, `ExtensionRunnerRef`, `ExtensionBindings`, thinking level constants
- **TODOs:** 0

### Phase 11.2: agent_session.go ✅
- **Lines:** ~460
- **Content:** `AgentSession` struct, `NewAgentSession()` constructor, event system (`Subscribe`, `emit`, `processEventQueue`), agent lifecycle handlers (`handleAgentEvent`, `processAgentEvent`), disconnect/reconnect, dispose, `GetModel()`, `GetThinkingLevel()`, `IsStreaming()`, `GetCwd()`, `GetActiveToolNames()`, `GetAllTools()`, `GetToolDefinition()`, `setActiveToolsByName()`, `IsCompacting()`, `GetSessionFile/ID`, `GetScopedModels/SetScopedModels`, `GetPromptTemplates`, `removeFromSteering/removeFromFollowUp`, helper funcs
- **TODOs:** 0
- **Notes:** `buildRuntime` and `refreshToolRegistry` live in `agent_session_extensions.go`. `emitExtensionEvent` lives in `agent_session_extensions.go`. `createRetryPromiseForAgentEnd` lives in `agent_session_retry.go`. `IsStreaming()` delegates to `s.Agent.IsStreaming()`. `getUserMessageText`/`getRole` extract from `llm.Message` via type switch. `CreateReplacedSessionContext()` builds `extensions.ReplacedSessionContext` for session replacement callbacks.

### Phase 11.3: agent_session_prompt.go ✅
- **Lines:** ~400
- **Content:** `Prompt()`, `Steer()`, `FollowUp()`, `Abort()`, `handleSkillBlock()`, `expandPromptText()`, skill/template expansion, steering/followUp queue management, compaction check before prompt
- **TODOs:** ~6 (Agent.Prompt/Steer/FollowUp/Abort depend on agent-sdk, executeSkill cases, sendUserMessage, bash image handling)
- **Dependencies:** agent-sdk API for Prompt/Steer/FollowUp/Abort

### Phase 11.4: agent_session_model.go ✅
- **Lines:** ~300
- **Content:** `ClampThinkingLevel()` (standalone, usable before AgentSession), `CycleModel()`, `SetModel()`, `GetAvailableModels()`, `GetSupportedThinkingLevels()`, `SetThinkingLevel()`, `CycleThinkingLevel()`, `clampThinkingLevel()` (delegates to standalone), `setSteeringMode()`, `setFollowUpMode()`
- **TODOs:** 1 (model-specific thinking levels from llm package)
- **Dependencies:** agent-sdk for SetSteeringMode/SetFollowUpMode

### Phase 11.5: agent_session_compaction.go ✅
- **Lines:** ~400
- **Content:** `Compact()` (manual), `checkCompaction()` (auto), `runAutoCompaction()`, `runCompaction()`, `runCompactionCore()`, `AbortCompaction()`, `SetAutoCompactionEnabled()`, `IsAutoCompactionEnabled()`, `getCompactionSettings()`, `getCompactionDeps()`, `completeSimple()`, message factory functions (`createBranchSummaryMessage`, `createCompactionSummaryMessage`, `createCustomMessage`)
- **TODOs:** 2 (completeSimple stub for Phase 13, Agent.Abort/Continue)
- **Key design:** `runCompactionCore` is shared by both manual and auto compaction. TS duplicated this logic between `compact()` and `_runAutoCompaction()`.
- **Wires into:** `agent_session.go:processAgentEvent` (after agent_end), `agent_session_prompt.go:Prompt` (before sending)
- **Uses:** `compaction/` package (PrepareCompaction, Compact, ShouldCompact, EstimateContextTokens, CalculateContextTokens), `llm/src/utils.IsContextOverflow`, `ConvertToLlm` from `messages.go`

### Phase 11.6: agent_session_extensions.go ✅
- **Lines:** ~560
- **Content:** `emitExtensionEvent()` (translates 10 agent event types to extension events), `replaceMessageInPlace()`, `BindExtensions()`, `applyExtensionBindings()`, `extendResourcesFromExtensions()`, `buildExtensionResourcePaths()`, `getExtensionSourceLabel()`, `bindExtensionCore()` (full 12-action wiring: SendMessage, SendUserMessage, NewSession, Fork, NavigateTree, SwitchSession, Reload, Shutdown, SetModel, SetThinkingLevel, RegisterProvider, UnregisterProvider), `refreshToolRegistry()` (with `buildAgentToolFromDefinition`), `buildRuntime()` (wires `tools.CreateAllToolDefinitions` + `extensions.NewExtensionRunner`), helper funcs (`makeMapSetOfKeys`, `normalizePromptSnippet`, `normalizePromptGuidelines`, `dedupeStrings`, `buildAgentToolFromDefinition`, `flattenGuidelines`)
- **TODOs:** 0
- **Key design:** `buildRuntime` creates tool definitions via `tools.CreateAllToolDefinitions`, creates `ExtensionRunner` from extension result, binds core + UI, refreshes tools. `refreshToolRegistry` merges base + extension + SDK custom tools with actual `AgentTool[any]` instances. `bindExtensionCore` wires all 12 extension actions.

### Phase 11.7: agent_session_retry.go ✅
- **Lines:** ~200
- **Content:** `isRetryableError()` (regex pattern matching), `createRetryPromiseForAgentEnd()`, `handleRetryableError()` (exponential backoff), `AbortRetry()`, `getRetrySettings()`, `findLastAssistantInMessages()`
- **TODOs:** 1 (Agent.Continue for retry)
- **Key design:** Compiled regex for retryable errors. Context overflow excluded (handled by compaction). Retry promise created synchronously in `handleAgentEvent` before `processAgentEvent` runs.
- **Wires into:** `agent_session.go:processAgentEvent` (checks retryable before compaction)

### Phase 11.8: agent_session_bash.go ✅
- **Lines:** ~130
- **Content:** `ExecuteBash()`, `recordBashResult()`, `AbortBash()`, `IsBashRunning()`, `HasPendingBashMessages()`, `flushPendingBashMessages()`, `BashRecordOptions`
- **TODOs:** 0
- **Key design:** `BashExecutionMessage` is defined in `messages.go` (implements `llm.Message` via `IsMessage()`). It's a "coding message" — persisted to session but NOT added to `Agent.Messages()` (which is `[]llm.Message` for LLM context). Converted to `llm.UserMessage` via `ConvertToLlm()` when building context. Deferred recording when streaming.

### Phase 11.9: agent_session_session.go ✅
- **Lines:** ~220
- **Content:** `GetSessionStats()`, `GetContextUsage()`, `SetSessionName()`, `GetSessionName()`, `NavigateTree()`, `Reload()`
- **TODOs:** 0
- **Key design:** Context usage checks for post-compaction usage to avoid stale sizes. `NavigateTree` branches + rebuilds agent state + system prompt. `Reload` emits shutdown/start events + refreshes settings + resources.

---

## Supporting Packages (COMPLETE ✅ — migrated before Phase 11)

### tools/ (17 files, ~3800 lines, 0 TODOs)
Complete tool implementations: bash, edit, edit_diff, find, grep, ls, read, write, index, output_accumulator, tool_definition_wrapper, path_utils, render_utils, truncate, file_mutation_queue, bash_unix, bash_windows

### compaction/ (4 files, ~1574 lines, 0 TODOs)
Core compaction engine: `compaction.go` (PrepareCompaction, Compact, GenerateSummary, ShouldCompact, FindCutPoint, EstimateTokens), `types.go` (SessionEntry, Deps, all compaction types), `utils.go` (FileOperations, serialization), `branch_summarization.go`

### extensions/ (4 files, ~1730 lines, 0 TODOs)
Extension system: `runner.go` (ExtensionRunner, full event dispatch ~50 methods), `types.go` (50+ structs/interfaces), `loader.go` (extension discovery/loading), `wrapper.go`

### Top-level support files (~20 files, ~5000+ lines, 0 TODOs)
`bash_executor.go`, `event_bus.go`, `session_cwd.go`, `session_manager.go` (1461 lines), `slash_commands.go`, `system_prompt.go`, `telemetry.go`, `resource_loader.go` (1157 lines), `package_manager*.go` (5 files), `prompt_templates.go`, `resolve_config_value.go`, `settings_manager.go`, `settings_types.go`, `model_registry.go`, `model_resolver.go`, `messages.go`, `defaults.go`, and more

---

## Remaining Open TODOs Across All Files

| Priority | What | Where | Depends On |
|----------|------|-------|------------|
| P2 | Theme loading stub | `resource_loader.go` | TUI port |
| P2 | `SessionCwdSource` interface typo (`GetSesionFile`) | `session_cwd.go` | Pre-existing; blocks `AssertSessionCwdExists` calls in runtime |

---

## Key Architecture Decisions

1. **AgentMessage = llm.Message** — type alias in agent-sdk. No conversion needed between agent state and compaction package.

2. **Compaction Deps struct** — breaks circular import between `ycode` ↔ `compaction` packages. Parent provides `CompleteSimple`, `CreateBranchSummaryMessage`, etc. through function fields.

3. **BashExecutionMessage** — implements `llm.Message` (has `IsMessage()`). Stored in session as custom entry. Converted to `llm.UserMessage` via `ConvertToLlm()` for LLM context. NOT added to `Agent.Messages()` directly.

4. **messages.go** — central place for custom message types (BashExecutionMessage, CustomMessage, BranchSummaryMessage, CompactionSummaryMessage) and `ConvertToLlm()` which transforms them to `llm.UserMessage`.

5. **settings_types.go vs compaction/types.go** — both have `CompactionSettings` but in different packages. `ycode.CompactionSettings` is for settings JSON, `compaction.CompactionSettings` is for runtime. `getCompactionSettings()` bridges them.

6. **Event system** — Go uses `context.CancelFunc` instead of TS `AbortController`. `compactionAbort`/`autoCompactionAbort` fields.

7. **AgentSessionRuntime** — owns the current AgentSession + its cwd-bound services. Session replacement methods (`SwitchSession`, `NewSession`, `Fork`, `ImportFromJsonl`) tear down the current runtime via extension `session_shutdown` events, then create and apply the next runtime through a `CreateAgentSessionRuntimeFactory` closure.

8. **CreateAgentSession** — the main session factory in `sdk.go`. Creates the `*sdk.Agent` with full configuration (streaming function wrapping auth/retry/attribution headers, extension hooks for `before_provider_request`/`after_provider_response`/`context` transform, steering/followUp modes), resolves model and thinking level from session history or settings, then constructs `*AgentSession`.

9. **CreateAgentSessionFromServices** — thin delegation layer. Takes pre-created `AgentSessionServices` and forwards to `CreateAgentSession`, keeping service creation separate from session creation so callers can resolve model/tools/settings against the target cwd first.

10. **Stream function** — wraps `modelRegistry.GetApiKeyAndHeaders()` + `settingsManager.GetProviderRetrySettings()` + `getAttributionHeaders()`, then dispatches to `llm.GetApiProvider(model.Api).StreamSimple()`. Error cases return `AssistantMessageEvent` with error message through a buffered channel.

11. **Image blocking** — `filterImagesFromMessage` wraps `ConvertToLlm` to replace `ImageContent` blocks with text placeholder when `settingsManager.GetBlockImages()` is true. Dedupes consecutive placeholders. Checked dynamically so mid-session setting changes take effect.

12. **AgentState interface fixes** — `SetTool` → `SetTools`, `StreamingMessage` → `IsStreamingMessage` corrected in `agent-sdk/types.go` to match `Agent` struct implementation.

---

## Phase 12: Agent-Session-Runtime (COMPLETE ✅)

Full runtime setup — creating the agent, wiring providers, extension loading lifecycle, session replacement.

### Phase 12.1: sdk.go ✅
- **Lines:** ~340
- **Content:** `CreateAgentSessionOptions` type, `CreateAgentSessionResult` type, `CreateAgentSession()` (full session creation: defaults resolution, resource loader, model restoration via `FindInitialModel`, thinking level clamping, image blocking wrapper, `*sdk.Agent` creation with streaming/auth/extension hooks, message restoration, `*AgentSession` construction), `getAttributionHeaders()`, `filterImagesFromMessage()`, `filterImageBlocks()`, `isImageBlock()`, `convertThinkingBudgets()`
- **TODOs:** 0
- **Key design:** The stream function is a closure over `modelRegistry`, `settingsManager`, `extensionRunnerRef`. Extension hooks (`onPayload`, `onResponse`, `transformContext`) delegate to the extension runner. The `extensionRunnerRef` is shared between the Agent (for callbacks) and AgentSession (for binding).

### Phase 12.2: agent_session_runtime.go ✅
- **Lines:** ~600
- **Content:** `CreateAgentSessionRuntimeResult`, `CreateAgentSessionRuntimeFactory` type, `CreateAgentSessionRuntimeFactoryOptions`, `SessionImportFileNotFoundError`, `AgentSessionRuntime` struct with all getters/lifecycle setters, `emitBeforeSwitch()`, `emitBeforeFork()`, `teardownCurrent()`, `apply()`, `finishSessionReplacement()`, `SwitchSession()`, `NewSession()`, `Fork()`, `ImportFromJsonl()`, `Dispose()`, `CreateAgentSessionRuntime()`, `ExtractUserMessageText()`, option types (`SwitchSessionOptions`, `NewSessionRuntimeOptions`, `ForkOptions`, `SessionReplacementResult`, `ForkResult`), helpers (`getContext`, `copyFile`, `getRoleFromMessage`, `getTextFromMessage`)
- **TODOs:** 0
- **Key design:** Factory pattern — `createRuntime` closure is stored on the runtime and reused for all session replacement flows. `teardownCurrent` emits `session_shutdown` + disposes. `apply` swaps session/services/diagnostics atomically. `Fork` handles both persisted and non-persisted sessions with branching logic.

### Phase 12.3: Pre-requisite fixes ✅
- Fixed `Agent.Subscribe` signature adapter (`AgentSessionConfig.SubscribeFunc` + `handleAgentEvent` now match `func(event AgentEvent, ctx context.Context) error`)
- Added `ClampThinkingLevel()` standalone function in `agent_session_model.go`
- Added `GetProviderRetrySettings()` to `settings_manager.go`
- Added `ReplacedSessionContext` type to `extensions/types.go`
- Added `CreateReplacedSessionContext()` to `agent_session.go`
- Added `NewSessionEntry()` public wrapper to `session_manager.go`
- Moved `CreateAgentSessionResult` type from `agent_session_services.go` stub to proper definition in `sdk.go`

### Phase 12.4: Extension wiring completion ✅
- `buildRuntime()` — now wires `tools.CreateAllToolDefinitions()` + `extensions.NewExtensionRunner()` + runner ref + binding + registry refresh
- `bindExtensionCore()` — full 12-action wiring (SendMessage, SendUserMessage, NewSession, Fork, NavigateTree, SwitchSession, Reload, Shutdown, SetModel, SetThinkingLevel, RegisterProvider, UnregisterProvider)
- `refreshToolRegistry()` — builds `sdk.AgentTool[any]` instances via `buildAgentToolFromDefinition()` instead of nil placeholders
- `buildAgentToolFromDefinition()` — creates AgentTool from ToolDefinitionEntry, delegates to definition's Execute function when available

---

## Phase 13: completeSimple + export-html (COMPLETE ✅)

All SDK methods (Abort, Continue, Prompt, Steer, FollowUp) are already implemented in agent-sdk `Agent` struct. Phase 13 fills the two remaining gaps.

### Phase 13.1: completeSimple ✅
- **File:** `agent_session_compaction.go:420-481`
- **Lines:** ~62
- **Content:** `completeSimple()` — non-streaming LLM call that wraps `llm.GetApiProvider(model.Api).StreamSimple()`. Collects events from the stream channel, extracts the final `AssistantMessage`, returns `*compaction.CompletionResult`. Includes auth/retry/header pipeline via `SettingsManager.GetProviderRetrySettings()`.
- **Helper:** `derefInt()` for nil-safe int dereferencing.
- **TODOs:** 0

### Phase 13.2: export-html/ ✅
- **Package:** `exporthtml` (`packages/coding-agent/src/core/export-html/`)
- **Files:** 4 Go files + 3 template assets + 2 vendor JS assets

| File | Lines | Content |
|------|-------|---------|
| `export-html.go` | ~405 | Types (`ExportOptions`, `SessionData`, `ToolExportDef`, `RenderedToolHtml`, `SessionManagerInterface`), color helpers (parse/derive/adjust), `generateHtml()`, `ExportSessionToHtml()`, `ExportFromFile()`, `preRenderCustomTools()`, `readSessionFile()` |
| `ansi_to_html.go` | ~268 | `AnsiToHtml()`, `AnsiLinesToHtml()`, full ANSI SGR support (standard/bright/256-color/RGB fg+bg, bold/dim/italic/underline, reset) |
| `tool_renderer.go` | ~106 | `ToolRenderer` interface, `CreateToolRenderer()`, `defaultToolRenderer` (call/result HTML), `NoOpRenderer`, `containsAnsi()` |
| `template.go` | ~47 | `//go:embed` directives for template.html/css/js + vendor marked/highlight JS, `SessionExportData` type, `RenderTemplate()` |

- **Embedded assets:** `template.html`, `template.css`, `template.js`, `vendor/marked.min.js`, `vendor/highlight.min.js`
- **Key design:** Uses `//go:embed` for all static assets. Vendor JS libs (marked, highlight) are embedded and injected into the HTML at export time — the rendered HTML is self-contained and works offline. Theme colors are derived from base color via luminance calculation.

### Phase 13.2 Wiring ✅
- **File:** `agent_session_session.go:224-247`
- **Content:** `AgentSession.ExportToHtml(outputPath string) (string, error)` — collects tool definitions, builds `sessionManagerAdapter`, delegates to `exporthtml.ExportSessionToHtml()`
- **Adapter:** `sessionManagerAdapter` wraps `*SessionManager` to satisfy `exporthtml.SessionManagerInterface` (GetSessionFile, GetHeader, GetEntries, GetLeafID)
- **Slash command:** `/export` already registered in `BuiltinSlashCommands` slice (`slash_commands.go`)

---

## Reference → Go Migration Table

Maps every reference TS file to its migrated Go equivalent(s).

### agent-session.ts → 11 Go files

| TS Source Section | Go File | Status |
|---|---|---|
| Types, constants, config | `agent_session_types.go` | ✅ Phase 11.1 |
| Constructor, events, state accessors | `agent_session.go` | ✅ Phase 11.1 |
| Prompt, Steer, FollowUp, Abort | `agent_session_prompt.go` | ✅ Phase 11.3 |
| Model cycling, thinking level | `agent_session_model.go` | ✅ Phase 11.4 |
| Compaction (manual + auto) | `agent_session_compaction.go` | ✅ Phase 11.5 |
| Extensions, tools, runtime init | `agent_session_extensions.go` | ✅ Phase 11.6, 12.4 |
| Retry logic | `agent_session_retry.go` | ✅ Phase 11.7 |
| Bash execution | `agent_session_bash.go` | ✅ Phase 11.8 |
| Session stats, NavigateTree, Reload | `agent_session_session.go` | ✅ Phase 11.9 |

### sdk.ts → 1 Go file

| TS File | Go File | Status |
|---|---|---|
| `sdk.ts` (CreateAgentSession, types, re-exports) | `sdk.go` | ✅ Phase 12.1 |

### agent-session-runtime.ts → 1 Go file

| TS File | Go File | Status |
|---|---|---|
| `agent-session-runtime.ts` (AgentSessionRuntime, factory, session replacement) | `agent_session_runtime.go` | ✅ Phase 12.2 |

### agent-session-services.ts → 1 Go file

| TS File | Go File | Status |
|---|---|---|
| `agent-session-services.ts` (services types, CreateAgentSessionServices, CreateAgentSessionFromServices) | `agent_session_services.go` | ✅ Phase 11 pre-req, updated Phase 12.3 |

### Supporting TS → Go packages

| TS Directory | Go Package | Files | Status |
|---|---|---|---|
| `tools/` (17 files) | `tools/` | 17 files, ~3800 lines | ✅ Pre-Phase 11 |
| `compaction/` (4 files) | `compaction/` | 4 files, ~1574 lines | ✅ Pre-Phase 11 |
| `extensions/` (4 files) | `extensions/` | 4 files + types updates | ✅ Pre-Phase 11 + Phase 12.3 |
| `export-html/` (5+ files + vendor) | `export-html/` | 4 Go files + 5 assets, ~826 lines | ✅ Phase 13.2 |
| Top-level support (~20 files) | Same package `ycode` | ~20 files, ~5000+ lines | ✅ Pre-Phase 11 |

### Not yet migrated (Phase 13+)

All Phase 13 items are complete. No remaining TS→Go migration items for `coding-agent/src/core/`.
