# Execution Plan: TUI + CLI + Modes Migration

> **How to use:** Each step is one file-level deliverable. Check off `[x]` when done.
> If context is lost, read this file, find the first unchecked step, and continue.
> Every step lists its dependencies — never skip ahead.
>
> **Reference doc:** `migrations/phase-14-20-tui-cli-modes.md` (full architecture, file mapping, design decisions)

---

## Legend

- `[ ]` = not started
- `[~]` = in progress / partial
- `[x]` = complete & compiling
- `Depends on:` = steps that must be `[x]` before starting this one

---

## Phase 14: Foundation (TUI Infrastructure)

### 14.1 — Install Go Dependencies

- [x] `go.mod` — Add all Bubble Tea ecosystem dependencies
  ```
  go get github.com/charmbracelet/bubbletea
  go get github.com/charmbracelet/bubbles
  go get github.com/charmbracelet/lipgloss
  go get github.com/charmbracelet/huh
  go get github.com/charmbracelet/glamour
  go get github.com/lrstanley/bubblezone
  go get github.com/alecthomas/chroma/v2
  go get github.com/muesli/termenv
  go get github.com/spf13/cobra
  ```
- **Verify:** `go mod tidy` succeeds, `go build ./...` still passes ✅

---

### 14.2 — Theme Color Utilities

- [x] `packages/coding-agent/src/core/tui/theme/colors.go` (~200 lines)
  - `hexToRgb()`, `rgbTo256()`, `findClosestCubeIndex()`, `findClosestGrayIndex()`
  - `colorDistance()`, `fgAnsi()`, `bgAnsi()`, `detectColorMode()`
  - `ColorValue` type (hex string or 256-color index)
  - `ColorMode` type (`truecolor` | `256color`)
  - Port from `reference/coding-agent/src/modes/interactive/theme/theme.ts:160-300`
- **Depends on:** 14.1
- **Verify:** `go build ./packages/coding-agent/src/core/tui/theme/` compiles

---

### 14.3 — Theme Type + JSON Schema

- [x] `packages/coding-agent/src/core/tui/theme/schema.go` (~200 lines)
  - `ThemeJson` struct with all 50+ color fields (from theme-schema.json)
  - `LoadThemeJSON(path string) (*ThemeJson, error)` — load + validate
  - `ValidateTheme(theme *ThemeJson) error`
  - `//go:embed dark.json light.json` for default themes
  - `GetDefaultDarkTheme()`, `GetDefaultLightTheme()`
  - Port from `reference/coding-agent/src/modes/interactive/theme/theme.ts:23-94`
- **Depends on:** 14.1
- **Verify:** `go build ./packages/coding-agent/src/core/tui/theme/` compiles

---

### 14.4 — Theme Core + Color Functions

- [x] `packages/coding-agent/src/core/tui/theme/theme.go` (~400 lines)
  - `Theme` struct (resolved colors as `lipgloss.Style` helpers)
  - `ThemeColor` type alias, `ThemeBg` type alias
  - `NewThemeFromJSON(json *ThemeJson) *Theme`
  - `theme.Fg(colorKey ThemeColor, text string) string`
  - `theme.Bg(bgKey ThemeBg, text string) string`
  - `theme.Bold(text string) string`
  - `theme.Italic(text string) string`
  - `theme.Dim(text string) string`
  - `GetMarkdownTheme() MarkdownTheme` (for glamour)
  - `GetEditorTheme() EditorTheme` (for textarea styling)
  - Global `theme` singleton + `SetTheme()`, `InitTheme()`, `OnThemeChange()`
  - `SetRegisteredThemes()`, `GetAvailableThemes()`
  - Port from `reference/coding-agent/src/modes/interactive/theme/theme.ts:300-1143`
- **Depends on:** 14.2, 14.3
- **Verify:** `go build ./packages/coding-agent/src/core/tui/theme/` compiles

---

### 14.5 — Theme JSON Assets

- [x] `packages/coding-agent/src/core/tui/theme/dark.json`
  - Copy from `reference/coding-agent/src/modes/interactive/theme/dark.json`
- [x] `packages/coding-agent/src/core/tui/theme/light.json`
  - Copy from `reference/coding-agent/src/modes/interactive/theme/light.json`
- **Depends on:** none (just copy files)
- **Verify:** Files exist, valid JSON

---

### 14.6 — Theme Tests

- [x] `packages/coding-agent/src/core/tui/theme/theme_test.go` (~100 lines)
  - Test `hexToRgb()`, `rgbTo256()`, color conversion roundtrips
  - Test `LoadThemeJSON()` with dark.json and light.json
  - Test `fgAnsi()` / `bgAnsi()` output for truecolor and 256-color modes
  - **20/20 tests passing ✅**
- **Depends on:** 14.4, 14.5
- **Verify:** `go test ./packages/coding-agent/src/core/tui/theme/` passes

---

### 14.7 — App Message Types

- [x] `packages/coding-agent/src/core/tui/app_types.go` (~250 lines)
  - `OverlayState` enum (None, ModelSelector, SessionSelector, ThemeSelector, etc.)
  - All `tea.Msg` types: `agentEventMsg`, `agentEventError`, `streamStartMsg`,
    `streamDeltaMsg`, `streamEndMsg`, `toolCallStartMsg`, `toolCallDeltaMsg`,
    `toolResultMsg`, `bashOutputMsg`, `bashCompleteMsg`, `compactionStartMsg`,
    `compactionEndMsg`, `retryCountdownMsg`, `retryAbortedMsg`, `submitMsg`,
    `bashSubmitMsg`, `selectorDoneMsg`, `selectorCancelledMsg`,
    `initCompleteMsg`, `shutdownMsg`
  - `AppMode` enum (Interactive, Print, RPC)
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:165-170, 196`
- **Depends on:** 14.1
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.8 — App Struct + Constructor

- [x] `packages/coding-agent/src/core/tui/app.go` (~300 lines)
  - `App` struct with all fields (runtimeHost, session, sessionManager,
    settingsManager, modelRegistry, ui components, state tracking)
  - `NewApp(runtimeHost, options) App`
  - Convenience accessors: `getSession()`, `getAgent()`, `getSessionManager()`,
    `getSettingsManager()`, `getModelRegistry()`
  - `AppOptions` struct (migratedProviders, modelFallbackMessage, initialMessage,
    initialImages, initialMessages, verbose)
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:231-350`
- **Depends on:** 14.7, 14.4
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.9 — Minimal Update()

- [x] `packages/coding-agent/src/core/tui/app_update.go` (~100 lines initial)
  - `Update(msg tea.Msg) (tea.Model, tea.Cmd)` — stub
  - Handle `tea.KeyMsg`: Ctrl+C → quit
  - Handle `tea.WindowSizeMsg`: store width/height
  - Route to sub-components when they exist
  - Will be expanded heavily in Phase 15
  - Includes temporary `Init()` and `View()` stubs to satisfy tea.Model interface
- **Depends on:** 14.8
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.10 — Minimal View()

- [x] `packages/coding-agent/src/core/tui/app_view.go` (~100 lines initial)
  - `View() string` — placeholder layout
  - Layout: header + chat placeholder + editor placeholder + footer
  - Use `lipgloss.JoinVertical` for stacking
  - Will be expanded heavily in Phase 15
- **Depends on:** 14.8
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.11 — Agent Event Bridge

- [x] `packages/coding-agent/src/core/tui/app_events.go` (~400 lines)
  - `subscribeToAgent(session, program) tea.Cmd` — bridge function
  - `handleAgentEvent(event AgentSessionEvent) tea.Cmd` — event dispatcher
  - Map each `AgentSessionEvent` type to the appropriate `tea.Msg`
  - Handle streaming events, tool events, bash events, compaction events
  - Re-subscribe after each event (self-sustaining loop)
  - Phase 14: basic bridge wired. Phase 15 will expand UI state updates.
  - Uses `program.Send()` for async injection into Bubble Tea loop.
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts` (subscribe pattern)
- **Depends on:** 14.8, 14.7
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.12 — Init() + Startup

- [x] `packages/coding-agent/src/core/tui/app_init.go` (~200 lines)
  - `Init() tea.Cmd` — start subscribe loop + spinner
  - Startup sequence: show notices, load changelog, bind extensions
  - Phase 14: minimal init returning initCompleteMsg. Full wiring in Phase 15.
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts` (init/startup sections)
- **Depends on:** 14.9, 14.10, 14.11
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.13 — Header Component

- [x] `packages/coding-agent/src/core/tui/components/header.go` (~100 lines)
  - `Header` struct (sub-model)
  - Shows: app logo/name, version, keybinding hints
  - `NewHeader()`, `Init()`, `Update()`, `View()`
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts` (header area)
- **Depends on:** 14.4
- **Verify:** `go build ./packages/coding-agent/src/core/tui/components/` compiles

---

### 14.14 — Keybinding Hints Helper

- [x] `packages/coding-agent/src/core/tui/components/keybinding_hints.go` (~50 lines)
  - `formatKeyText(binding string) string`
  - `keyDisplayText(binding string) string`
  - `keyHint(binding string, description string) string`
  - `rawKeyHint(binding string, description string) string`
  - Port from `reference/coding-agent/src/modes/interactive/components/keybinding-hints.ts`
- **Depends on:** 14.4
- **Verify:** `go build ./packages/coding-agent/src/core/tui/components/` compiles

---

### 14.15 — Footer Component

- [x] `packages/coding-agent/src/core/tui/components/footer.go` (~220 lines)
  - `Footer` struct (sub-model)
  - `NewFooter(session, footerData, theme) Footer`
  - 2-line footer: pwd line + stats line
  - pwd: cwd (with ~ substitution), git branch, session name
  - stats: ↑input ↓output Rcache Wcache $cost context%/window (auto)
  - model name + thinking level on right side
  - Color-coded context % (green <70%, yellow 70-90%, red >90%)
  - Extension statuses line
  - Port from `reference/coding-agent/src/modes/interactive/components/footer.ts`
- **Depends on:** 14.4, 14.14
- **Verify:** `go build ./packages/coding-agent/src/core/tui/components/` compiles

---

### 14.16 — Dynamic Border Component

- [x] `packages/coding-agent/src/core/tui/components/dynamic_border.go` (~30 lines)
  - `DynamicBorder` — renders a horizontal line using lipgloss border
  - `NewDynamicBorder(colorFunc func(string) string) DynamicBorder`
  - Port from `reference/coding-agent/src/modes/interactive/components/dynamic-border.ts`
- **Depends on:** 14.4
- **Verify:** `go build ./packages/coding-agent/src/core/tui/components/` compiles

---

### 14.17 — Stub Files for Phase 15+ Placeholders

- [x] `packages/coding-agent/src/core/tui/app_selectors.go` (~20 lines stub)
  - Empty file with `// Phase 16: Selector overlay management` comment
- [x] `packages/coding-agent/src/core/tui/app_autocomplete.go` (~20 lines stub)
  - Empty file with `// Phase 17: Autocomplete wiring` comment
- [x] `packages/coding-agent/src/core/tui/app_extensions.go` (~20 lines stub)
  - Empty file with `// Phase 18: Extension UI` comment
- **Depends on:** 14.8
- **Verify:** All compile

---

### 14.18 — Wire Everything in App

- [x] Update `app.go` — wire Header + Footer + placeholder editor into App
- [x] Update `app_view.go` — render Header + chat placeholder + editor placeholder + Footer
- [x] Update `app_update.go` — forward resize events to components
- **Depends on:** 14.13, 14.15, 14.16, 14.17
- **Verify:** `go build ./packages/coding-agent/src/core/tui/` compiles

---

### 14.19 — Entry Point (Minimal)

- [x] `packages/coding-agent/cmd/ycode/main.go` (~100 lines initial)
  - `main()` function
  - Create `AgentSessionRuntime` (reuse existing `CreateAgentSessionRuntime`)
  - Create `App` with runtime
  - `tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())`
  - `program.Run()`
  - Will be expanded in Phase 19 with cobra
- **Depends on:** 14.18
- **Verify:** `go build ./packages/coding-agent/cmd/ycode/` compiles

---

### ✅ Phase 14 Checkpoint

**Run:** `go build ./packages/coding-agent/cmd/ycode/ && ./ycode`
**Expect:** Blank TUI shell with header, empty chat area, input placeholder, footer. Ctrl+C exits.
**If fail:** Debug until this works before moving to Phase 15.

---

## Phase 15: Core Components

### 15.1 — Chat Viewport

- [x] `packages/coding-agent/src/core/tui/components/chat.go` (~200 lines)
  - `Chat` struct wrapping `viewport.Model`
  - `NewChat(width, height) Chat`
  - `AddMessage(rendered string)` — append + auto-scroll to bottom
  - `SetMessages(rendered []string)` — bulk set
  - `ScrollUp()`, `ScrollDown()`, `GotoBottom()`
  - `Update()`, `View()`
  - Tracks whether user has scrolled up (disable auto-scroll if so)
- **Depends on:** 14.1
- **Verify:** Compiles

---

### 15.2 — Editor Component

- [x] `packages/coding-agent/src/core/tui/components/editor.go` (~400 lines)
  - `Editor` struct wrapping `textarea.Model`
  - `NewEditor(ui, theme, keybindings, options) Editor`
  - Multi-line input with Enter to submit, Shift+Enter for newline
  - `SetValue()`, `Value()`, `Focus()`, `Blur()`
  - Placeholder text support
  - Bash mode detection (line starts with `!`)
  - Autocomplete overlay (placeholder for Phase 17)
  - `Update()`, `View()`
  - Port from `reference/coding-agent/src/modes/interactive/components/custom-editor.ts`
- **Depends on:** 14.4, 14.14
- **Verify:** Compiles

---

### 15.3 — Markdown Renderer

- [x] `packages/coding-agent/src/core/tui/components/markdown.go` (~80 lines)
  - `RenderMarkdown(text string, theme *theme.Theme) string`
  - Wrapper around `glamour` with custom theme colors
  - Syntax highlighting via chroma
  - Port from pi-tui's `Markdown` component behavior
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.4 — Assistant Message Component

- [x] `packages/coding-agent/src/core/tui/components/assistant_message.go` (~150 lines)
  - `AssistantMessage` struct (sub-model)
  - Renders: text blocks as markdown, thinking blocks (shown/hidden),
    abort/error messages
  - OSC 133 zone markers for terminal integration
  - `NewAssistantMessage(msg, hideThinking, markdownTheme, hiddenThinkingLabel)`
  - `UpdateContent(msg)`, `SetHideThinkingBlock(bool)`, `SetHiddenThinkingLabel(string)`
  - Port from `reference/coding-agent/src/modes/interactive/components/assistant-message.ts`
- **Depends on:** 15.3
- **Verify:** Compiles

---

### 15.5 — User Message Component

- [x] `packages/coding-agent/src/core/tui/components/user_message.go` (~50 lines)
  - `UserMessage` struct — renders user text in styled bubble
  - `NewUserMessage(text string, theme *theme.Theme) UserMessage`
  - Port from `reference/coding-agent/src/modes/interactive/components/user-message.ts`
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.6 — Custom Message Component

- [x] `packages/coding-agent/src/core/tui/components/custom_message.go` (~100 lines)
  - `CustomMessage` struct — renders extension/custom messages
  - Label + content with custom background
  - Port from `reference/coding-agent/src/modes/interactive/components/custom-message.ts`
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.7 — Diff Renderer

- [x] `packages/coding-agent/src/core/tui/components/diff.go` (~150 lines)
  - `RenderDiff(diffText string, opts RenderDiffOptions) string`
  - Added lines (green), removed lines (red), context lines (dim)
  - Line numbers, file headers
  - Port from `reference/coding-agent/src/modes/interactive/components/diff.ts`
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.8 — Tool Execution Component

- [x] `packages/coding-agent/src/core/tui/components/tool_execution.go` (~350 lines)
  - `ToolExecution` struct (sub-model)
  - Shows: tool name badge, args preview, result with expand/collapse
  - Pending state: spinner + tool name
  - Success state: green badge + output
  - Error state: red badge + error message
  - Custom tool renderers (from ToolDefinition)
  - Image rendering support
  - Port from `reference/coding-agent/src/modes/interactive/components/tool-execution.ts`
- **Depends on:** 15.3, 15.7, 14.16
- **Verify:** Compiles

---

### 15.9 — Bash Execution Component

- [x] `packages/coding-agent/src/core/tui/components/bash_execution.go` (~220 lines)
  - `BashExecution` struct (sub-model)
  - Top border + command header + streaming output + bottom border
  - `AppendOutput(chunk string)`, `SetComplete(exitCode, cancelled, truncationResult, fullOutputPath)`
  - Preview truncation (20 lines), expand/collapse
  - Dim border for `!!` excluded commands
  - Port from `reference/coding-agent/src/modes/interactive/components/bash-execution.ts`
- **Depends on:** 14.16, 14.4
- **Verify:** Compiles

---

### 15.10 — Visual Truncation Utility

- [x] `packages/coding-agent/src/core/tui/components/visual_truncate.go` (~50 lines)
  - `TruncateToVisualLines(text string, maxLines int, width int, paddingX int) VisualTruncateResult`
  - Width-aware line counting, ANSI escape code handling
  - Port from `reference/coding-agent/src/modes/interactive/components/visual-truncate.ts`
- **Depends on:** 14.1
- **Verify:** Compiles

---

### 15.11 — Branch Summary Message

- [x] `packages/coding-agent/src/core/tui/components/branch_summary.go` (~60 lines)
  - `BranchSummary` struct — renders branch navigation summary
  - Port from `reference/coding-agent/src/modes/interactive/components/branch-summary-message.ts`
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.12 — Compaction Summary Message

- [x] `packages/coding-agent/src/core/tui/components/compaction_summary.go` (~60 lines)
  - `CompactionSummary` struct — renders compaction summary
  - Port from `reference/coding-agent/src/modes/interactive/components/compaction-summary-message.ts`
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.13 — Skill Invocation Message

- [x] `packages/coding-agent/src/core/tui/components/skill_invocation.go` (~60 lines)
  - `SkillInvocation` struct — renders skill invocation display
  - Port from `reference/coding-agent/src/modes/interactive/components/skill-invocation-message.ts`
- **Depends on:** 14.4
- **Verify:** Compiles

---

### 15.14 — Expand Update() for All Agent Events

- [x] Update `packages/coding-agent/src/core/tui/app_update.go` (expand to ~600 lines)
  - Handle all `tea.Msg` types from `app_types.go`
  - Streaming: create/update `AssistantMessage` component, append deltas
  - Tool calls: create/update `ToolExecution` components in chat
  - Bash: create/update `BashExecution` components
  - Compaction: show loader, handle completion
  - Retry: show countdown, handle abort
  - Key routing: Ctrl+C (abort/double-quit), Escape (cancel),
    Enter (submit), scroll keys for chat
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts` update sections
- **Depends on:** 14.11, 15.1 through 15.13
- **Verify:** Compiles

---

### 15.15 — Expand View() for Full Chat Layout

- [x] Update `packages/coding-agent/src/core/tui/app_view.go` (expand to ~300 lines)
  - Layout: header + chat viewport + pending messages + editor + footer
  - Render overlay selector on top when active
  - Render loading animation during streaming
  - Render working message indicator
  - Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts` render sections
- **Depends on:** 15.14
- **Verify:** Compiles

---

### ✅ Phase 15 Checkpoint

**Run:** `go build ./packages/coding-agent/cmd/ycode/ && ./ycode`
**Expect:** Send prompt → see streaming assistant response, tool executions, bash output. Full chat working.
**If fail:** Debug until this works before moving to Phase 16.

---

## Phase 16: Selectors & Overlays

### 16.1 — Base Selector Component

- [x] `packages/coding-agent/src/core/tui/components/selector.go` (~250 lines)
  - `Selector[T any]` generic struct (sub-model)
  - Fuzzy search input + filtered list + cursor navigation
  - `NewSelector(items, filterFn, renderFn, onSelect, onCancel) Selector[T]`
  - Key handling: up/down, enter (select), escape (cancel), typing (filter)
  - Port from common pattern across all selector components
- **Depends on:** 14.1, 14.4
- **Verify:** Compiles

---

### 16.2 — Model Selector

- [x] `packages/coding-agent/src/core/tui/components/model_selector.go` (~150 lines)
  - `ModelSelector` struct wrapping `Selector[ModelItem]`
  - Shows: provider/id, scoped models, search
  - Port from `reference/coding-agent/src/modes/interactive/components/model-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles

---

### 16.3 — Tree Selector Component

- [x] `packages/coding-agent/src/core/tui/components/tree_selector.go` (~300 lines)
  - `TreeSelector` struct — tree navigation for session history
  - Expand/collapse nodes, keyboard navigation
  - Port from `reference/coding-agent/src/modes/interactive/components/tree-selector.ts`
- **Depends on:** 14.4, 14.1
- **Verify:** Compiles

---

### 16.4 — Session Selector

- [x] `packages/coding-agent/src/core/tui/components/session_selector.go` (~250 lines)
  - `SessionSelector` struct — session picker with tree view
  - Uses `TreeSelector` internally
  - Port from `reference/coding-agent/src/modes/interactive/components/session-selector.ts`
- **Depends on:** 16.3
- **Verify:** Compiles

---

### 16.5 — Session Selector Search

- [x] `packages/coding-agent/src/core/tui/components/session_selector_search.go` (~200 lines)
  - `SessionSelectorSearch` — search variant of session selector
  - Port from `reference/coding-agent/src/modes/interactive/components/session-selector-search.ts`
- **Depends on:** 16.1
- **Verify:** Compiles

---

### 16.6 — Theme Selector

- [x] `packages/coding-agent/src/core/tui/components/theme_selector.go` (~150 lines)
  - `ThemeSelectorComponent` — wraps `Selector[string]` with available theme names
  - Preselects current theme; live preview via OnPreview on cursor change (mirrors TS onSelectionChange)
  - Current theme checkmarked + accent-colored
  - Port from `reference/coding-agent/src/modes/interactive/components/theme-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.7 — Thinking Level Selector

- [x] `packages/coding-agent/src/core/tui/components/thinking_selector.go` (~160 lines)
  - `ThinkingSelectorComponent` — wraps `Selector[thinkingItem]` over thinking levels + descriptions
  - Preselects current level; current level checkmarked + accent-colored
  - Each row: "level — description" (token-budget text from LEVEL_DESCRIPTIONS)
  - Port from `reference/coding-agent/src/modes/interactive/components/thinking-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.8 — Settings Selector

- [x] `packages/coding-agent/src/core/tui/components/settings_selector.go` (~490 lines)
  - `SettingsSelectorComponent` — searchable list of ~20 cycling settings + 3 nested submenus
  - `SettingsConfig` snapshot struct decoupled from SettingsManager (App wires callbacks)
  - Value cycling (left/right/enter), fuzzy search filter, scroll window
  - Submenus: thinking level (16.7), theme (16.6), warnings (built on Selector)
  - `SettingsCallbacks` reports every change (OnChange) + warnings/theme-preview
  - Port from `reference/coding-agent/src/modes/interactive/components/settings-selector.ts`
- **Depends on:** 16.1, 16.6, 16.7
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.9 — Scoped Models Selector

- [x] `packages/coding-agent/src/core/tui/components/scoped_models_selector.go` (~470 lines)
  - `ScopedModelsSelectorComponent` — multi-select for enabling/reordering Ctrl+P models
  - `enabledIds` state: nil = all enabled; explicit ordered list = cycling order
  - Actions: toggle, enable-all, clear-all, toggle-provider, reorder up/down, save
  - Session-only until Ctrl+S (OnChange session-only, OnPersist writes settings)
  - Pure helpers (scopedToggle/EnableAll/ClearAll/Move/getSortedIDs) mirror TS functions
  - Fuzzy search filter, "(unsaved)" footer tag, model-name preview line
  - Port from `reference/coding-agent/src/modes/interactive/components/scoped-models-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.10 — Config Selector

- [x] `packages/coding-agent/src/core/tui/components/config_selector.go` (~480 lines)
  - `ConfigSelectorComponent` — grouped toggleable list of package resources
  - Groups by origin/scope/source/baseDir; subgroups by resource type (extensions/skills/prompts/themes)
  - buildConfigGroups + configGroupLabel + configDisplayName + formatBaseDir mirror TS helpers
  - Item-only cursor navigation, space/enter toggle, fuzzy search (keeps matching group/subgroup headers)
  - Toggle display in-component; SettingsManager mutation delegated to OnToggle (App owns persistence in 16.18)
  - Port from `reference/coding-agent/src/modes/interactive/components/config-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.11 — Show Images Selector

- [x] `packages/coding-agent/src/core/tui/components/show_images_selector.go` (~115 lines)
  - `ShowImagesSelectorComponent` — Yes/No picker wrapping `Selector[showImagesItem]`
  - Preselects current value (Yes=enabled), checkmarks it
  - Each row: "label — description"
  - Port from `reference/coding-agent/src/modes/interactive/components/show-images-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.12 — User Message Selector

- [x] `packages/coding-agent/src/core/tui/components/user_message_selector.go` (~190 lines)
  - `UserMessageSelectorComponent` — "fork from message" picker (own component; multi-line render)
  - UserMessageItem {id, text, timestamp}; App builds from session history
  - Two-line rows (text + position metadata) + blank separator; wrapping up/down nav
  - Preselects initialSelectedID, else most recent; truncateVisual for previews
  - Port from `reference/coding-agent/src/modes/interactive/components/user-message-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.13 — Extension Selector

- [x] `packages/coding-agent/src/core/tui/components/extension_selector.go` (~120 lines)
  - `ExtensionSelectorComponent` — wraps `Selector[string]` for extension option dialogs
  - Optional countdown timer (`ExtensionSelectorOptions.TimeoutSeconds`) that suffixes the title with "(Ns)" and auto-cancels at zero (self-contained `tea.Tick` loop; no dependency on 16.17)
  - Added `Selector.SetTitle` (additive) so the wrapper can update the title live during the countdown
  - Port from `reference/coding-agent/src/modes/interactive/components/extension-selector.ts`
- **Depends on:** 16.1
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.14 — Login Dialog

- [x] `packages/coding-agent/src/core/tui/components/login_dialog.go` (~300 lines)
  - `LoginDialogComponent` — stateful OAuth login dialog (title + dynamic content lines + input)
  - Imperative API driven by the async OAuth flow: `ShowAuth`, `ShowManualInput`, `ShowPrompt`, `ShowInfo`, `ShowWaiting`, `ShowProgress`, `Cancel`
  - Input requests (`ShowManualInput`/`ShowPrompt`) resolve via buffered `<-chan LoginInputResult`
  - **Key Go porting decision:** the OAuth flow runs in a goroutine, so every imperative method routes through `LoginDialogCallbacks.Send` (App supplies `program.Send`) and is applied safely on the main loop in `Update` via `loginDialogMsg`; direct model mutation would race with the tea loop
  - Browser open via `exec` (best-effort, fire-and-forget), platform-aware click hint
  - Uses `bubbles/textinput` (already a dependency) — **not** `huh.Form`: the plan note didn't match the actual reference (which is an imperative OAuth dialog, not a static form). Avoided adding `huh` to go.mod
  - Port from `reference/coding-agent/src/modes/interactive/components/login-dialog.ts`
- **Depends on:** 16.1 (textinput already available via bubbles)
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.15 — OAuth Selector

- [x] `packages/coding-agent/src/core/tui/components/oauth_selector.go` (~230 lines)
  - `OAuthSelectorComponent` — searchable auth-provider picker for `/login` and `/logout`
  - `AuthSelectorProvider{ID, Name, AuthType}`, `OAuthSelectorMode` (login/logout), `OAuthAuthLookup` interface
  - Reuses the existing Go core `*core.AuthStorage` directly (it satisfies `OAuthAuthLookup` — has `Get` + `GetAuthStatus`); reuses `core.AuthCredential`/`core.AuthStatus` types — no new auth types invented
  - `formatStatusIndicator` ported faithfully (configured / type-mismatch warning / env / runtime / fallback / models_json / unconfigured)
  - **Navigation binds only ↑/↓ (not j/k)** so those letters stay free for the search box — matches the TS reference (the generic Selector's j/k binding would break search typing)
  - Uses the shared `searchInput` helper; empty-state messages vary by mode
  - Port from `reference/coding-agent/src/modes/interactive/components/oauth-selector.ts`
- **Depends on:** 16.1, core auth_storage
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.16 — Bordered Loader

- [x] `packages/coding-agent/src/core/tui/components/bordered_loader.go` (~100 lines)
  - `BorderedLoaderComponent` — spinner (`bubbles/spinner`, Dot style) framed by top/bottom `border`-colored lines, with message + optional cancel hint
  - Cancellable mode: escape/ctrl+c fires `OnAbort` (App wires to context cancel); `Abort()` for programmatic cancel
  - No `dispose`/AbortController analogue needed — the bubbles spinner tick is program-driven (stops being routed once the overlay is removed), so no goroutine to clean up
  - Border color uses `theme.Fg("border", ...)` matching `DynamicBorder`; spinner frame in `accent`, message in `muted`
  - Port from `reference/coding-agent/src/modes/interactive/components/bordered-loader.ts`
- **Depends on:** bubbles/spinner (already available)
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.17 — Countdown Timer

- [x] `packages/coding-agent/src/core/tui/components/countdown_timer.go` (~90 lines)
  - `CountdownTimer` — callback-driven reusable countdown wrapping `bubbles/timer`
  - `CountdownTimerCallbacks{OnTick(remainingSeconds), OnExpire}`; fires OnTick immediately on construction, then each second, then OnExpire at zero (mirrors the TS setInterval semantics)
  - Integrated as a tea.Model (not a self-running goroutine): the owning component forwards messages + issues `Init()`; Update filters to its own `TickMsg.ID` so multiple timers don't cross-fire
  - Non-visual (`View() == ""`) — it's a helper; the owner renders the remaining time; `RemainingSeconds()` + `Stop()` provided
  - Port from `reference/coding-agent/src/modes/interactive/components/countdown-timer.ts`
- **Depends on:** bubbles/timer (already available)
- **Verify:** Compiles ✅ (build + vet clean)

---

### 16.18 — Selector Overlay Management in App

- [x] Updated `packages/coding-agent/src/core/tui/app_selectors.go` (~430 lines) + wiring in `app_update.go`
  - `overlayComponent` interface (tea.Model + SetWidth); added `overlayModel` field to App
  - `openOverlay` / `closeOverlay` / `forwardOverlayMsg`; overlay owns keyboard + tick messages when active (routed at top of `Update`); overlay resized on WindowSizeMsg
  - **Result protocol**: selectors emit typed results via returned `tea.Cmd` → `selectorDoneMsg{result}`; `handleSelectorDone` switches on concrete type (avoids pointer-captured mutation — state changes happen in the value-copy Update loop). `themePreviewMsg` applies live preview without closing.
  - Result handlers fully wire: theme (SetTheme + rebind), model (session.SetModel), session switch (runtimeHost.SwitchSession), thinking (SetDefaultThinkingLevel), fork (runtimeHost.Fork). Noted-pending handlers (show-images, scoped-models, config, oauth) close cleanly + emit a notice — they await settings setters / PackageManager / login flow.
  - `renderSelectorOverlay` dispatches on `a.overlay` + boxes with a themed border + title; 10 openers (`openModelSelector`, `openSessionSelector`, `openThemeSelector`, `openThinkingSelector`, `openScopedModelsSelector`, `openSettingsSelector`, `openShowImagesSelector`, `openConfigSelector`, `openUserMessageSelector`, `openOAuthSelector`)
  - **Core additions:** `AgentSession.GetModelRegistry()`, `ModelRegistry.GetAuthStorage()` getters; changed `ScopedModelsCallbacks` to `[]string` (was unexported `enabledIds`) so the tui package can name it
- **Depends on:** 16.1 through 16.17
- **Verify:** Compiles ✅ (build + vet clean across `packages/coding-agent/...`)

---

### 16.19 — Wire Selectors into App Update + View

- [x] Updated `app_update.go` + `app_view.go`
  - **Key bindings** (active only when no overlay is open + not streaming) in `handleKeyMsg`: `ctrl+l`/`ctrl+m`→Model, `ctrl+s`→Session, `ctrl+e`→Settings, `ctrl+k`→Scoped Models, `ctrl+i`→Show Images, `ctrl+u`→Fork-from-message, `ctrl+b`→Config/Resources, `ctrl+y`→OAuth login. `ctrl+l` is the faithful TS `app.model.select`; the rest are provisional Phase-16 mnemonics (superseded by slash commands in Phase 17 / keybindings system in Phase 19). Escape now uses `closeOverlay()` (tears down the model too, not just the state).
  - **Modal render**: `composeOverlay` renders the active overlay as a centered modal via `lipgloss.Place` (selectors take over the screen — standard TUI pattern; Escape returns to base).
  - Overlay-owned key routing (from 16.18) means in-overlay `ctrl+s`/`ctrl+t`/etc. act on the selector, while editor-focus `ctrl+s` opens it — no runtime collision.
- **Depends on:** 16.18
- **Verify:** Compiles ✅ (build + vet clean; `go build ./cmd/ycode` → 23MB binary builds)

---

### ✅ Phase 16 Checkpoint

**Run:** `go build ./cmd/ycode && ./ycode`
**Expect:** Ctrl+M / Ctrl+L → model selector with search. Ctrl+S → session selector. Ctrl+E → settings (nests theme + thinking). All selectors open + navigate + close; Escape returns to base.
**Status:** ✅ Binary builds (23MB); build + vet clean across `packages/coding-agent/...`. Interactive TUI run deferred (needs a terminal). Noted-pending: show-images/scoped-models/config/oauth persistence await settings setters / PackageManager port / login flow (Phase 19+).

---

## Phase 17: Autocomplete + Editor Features

### 17.1 — Autocomplete Provider Interface ✅

- [x] `packages/coding-agent/src/core/tui/autocomplete/provider.go` (~70 lines)
  - `AutocompleteItem` struct (Value, Label, Description)
  - `AutocompleteProvider` interface: `GetItems(prefix string) []AutocompleteItem`
  - `NewItem(value, label, description)` constructor
  - Package + interface doc explains the simplification vs TS `getSuggestions`
- **Depends on:** 14.1
- **Verify:** ✅ `go build` + `go vet` clean

---

### 17.2 — Combined Provider ✅

- [x] `packages/coding-agent/src/core/tui/autocomplete/combined.go` (~180 lines)
  - `CombinedProvider` — merges slash commands + templates + extensions + skills
  - `NewCombinedProvider(commands, templates, extensions, skills, cwd) CombinedProvider`
  - `GetItems(prefix)` — slash-name completion + `/<cmd> <arg>` routing; file/`#` deferred
  - `WithArgumentCompleter(name, fn)` — composes 17.3's `/model` logic without a hard dep
  - `autocompleteSourceTag`/`prefixDescription` — faithful port of getAutocompleteSourceTag
  - `fuzzy.go` — case-folded subsequence matcher (package-local, no components dep)
  - Supportive core change: exported `core.ParseGitUrl` (was `parseGitUrl`) for git source tagging
- Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:442-517,394-425`
- **Depends on:** 17.1
- **Verify:** ✅ `go build ./...` + `go vet` clean (core + autocomplete)

---

### 17.3 — Slash Command Items ✅

- [x] `packages/coding-agent/src/core/tui/autocomplete/slash.go` (~95 lines)
  - `SlashCommandProvider` — converts `BuiltinSlashCommands` to autocomplete items
  - `ModelArgumentCompleter(modelsFn)` — `/model <prefix>` arg completion (scoped-or-registry)
  - `WithArgumentCompleter(name, fn)` — attach per-command arg completers
  - Shared `completeSlash` helper extracted from combined.go (no routing duplication)
- Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:451-477`
- **Faithful-deviation note:** TS sets `value: item.label` on a `{id,provider}` item (no `label` field → undefined, a latent reference bug); Go sets value to canonical `provider/id`
- **Depends on:** 17.1
- **Verify:** ✅ `go build ./...` + `go vet` clean

---

### 17.4 — Autocomplete Overlay in Editor ✅

- [x] Update `packages/coding-agent/src/core/tui/components/editor.go`
  - Autocomplete state fields + `SetAutocompleteProvider(p)`
  - Popup renders below textarea (`renderAutocomplete`), up to 8 rows, selected row highlighted
  - `refreshAutocomplete` recomputes on every keystroke from the slash token at the cursor
  - Popup owns keys when open: up/down + ctrl+p/n navigate (wrap), Enter/Tab select, Escape closes
  - `applyCompletion`: command-mode inserts value + trailing space; arg-mode replaces the typed word; cursor repositioned
- Port autocomplete overlay behavior from CustomEditor (interactive-mode/components/custom-editor.ts)
- **bubbles v1 constraint:** no public cursor-column accessor / no `MoveToBegin`; token + completion assume cursor at end of current line, reposition via `CursorUp` × rows + `SetCursor`. (Module uses `bubbles v1.0.0`, not v2.)
- **Depends on:** 17.2, 15.2
- **Verify:** ✅ `go build ./...` + `go vet` clean (full module)

---

### 17.5 — Bash Mode in Editor ✅

- [x] Update `packages/coding-agent/src/core/tui/components/editor.go`
  - Detect when first non-whitespace char is `!` (faithful `trimStart` port; was first-line-only)
  - Bash-mode border color via `theme.ToLipglossColor("bashMode")` (green); prompt → ` ! `
  - Submit differentiation: `!cmd` runs in context, `!!cmd` excluded from context
  - `BashSubmitMsg` gains `ExcludedFromContext bool`; `handleSubmitBash` threads it through
- **Honest deferral:** actual bash *execution* (streaming output) remains deferred (Phase 15) — the handler renders the command as a static entry and notes the excluded flag for when execution is wired
- Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:2401-2407,2566-2582,3372-3380`
- **Depends on:** 15.2
- **Verify:** ✅ `go build ./...` + `go vet` clean (full module)

---

### 17.6 — Autocomplete Wiring in App ✅

- [x] Update `packages/coding-agent/src/core/tui/app_autocomplete.go` (~110 lines)
  - `createBaseAutocompleteProvider()` — CombinedProvider from commands + templates + extensions + skills
  - Skills gated by `GetEnableSkillCommands()` (faithful to TS)
  - `/model` arg completer attached via `WithArgumentCompleter` + `ModelArgumentCompleter`
  - `modelsForCompletion()` — scoped-or-registry (TS `scopedModels.length > 0 ? scoped : available`)
  - `setupAutocomplete()` installs on editor; called from `NewApp` + `RebindSession`
- Supportive core additions: `AgentSession.GetExtensionCommands()` + `AgentSession.GetSkills()` getters
- Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:442-530`
- **Honest deferral:** "refresh on extension changes" is wired at construction + session-rebind; reload-triggered refresh waits on the `/reload` slash dispatcher
- **Depends on:** 17.4, 17.5
- **Verify:** ✅ `go build ./...` clean; `go vet` clean on `core` + `tui` (pre-existing `tools/bash.go` vet warnings unrelated)

---

### ✅ Phase 17 Checkpoint

**Run:** `go build ./cmd/ycode/ && ./ycode`
**Expect:** Type `/` → autocomplete popup appears. Type `!ls` → bash mode styling. Select commands.
**Build verified:** ✅ binary builds (23MB). Runtime behavior pending manual TTY test.
**If fail:** Debug until this works before moving to Phase 18.

---

## Phase 18: Extension UI

### 18.1 — Extension Editor Component ✅

- [x] `packages/coding-agent/src/core/tui/components/extension_editor.go` (~210 lines)
  - `ExtensionEditor` — multi-line editor for extension input dialogs
  - Enter submits, Shift/Ctrl+Enter newline, Escape cancels, Ctrl+G external editor
  - `ExtensionEditorCallbacks{OnSubmit func(string) tea.Cmd, OnCancel func() tea.Cmd}` (result-protocol)
  - External editor via `tea.ExecProcess`: temp-file round-trip + $VISUAL/$EDITOR (Windows `cmd /c` wrapper)
  - Implements `tea.Model` + `SetWidth` (overlayComponent-compatible)
- Port from `reference/coding-agent/src/modes/interactive/components/extension-editor.ts`
- **Adaptation note:** TS wraps pi-tui `Editor`; Go builds on bubbles `textarea` (project uses bubbles v1.0.0 / bubbletea v1.3.10). Direct keys (Ctrl+G) used since keybindings.go defines no named actions yet — Phase 19 reconciles.
- **Depends on:** 14.4, 14.1
- **Verify:** ✅ `go build` + `go vet` clean (tui/components)

---

### 18.2 — Extension Input Component ✅

- [x] `packages/coding-agent/src/core/tui/components/extension_input.go` (~165 lines)
  - `ExtensionInput` — single-line text input for extension input dialogs
  - Enter submits, Escape cancels, empty submit ignored
  - Optional countdown (`TimeoutMs`) → title shows remaining seconds, auto-cancels on expire
  - `ExtensionInputCallbacks{OnSubmit func(string) tea.Cmd, OnCancel func() tea.Cmd}` (result-protocol)
  - Implements `tea.Model` + `SetWidth` (overlayComponent-compatible)
- Port from `reference/coding-agent/src/modes/interactive/components/extension-input.ts`
- **Adaptation note:** Uses an embedded `bubbles/timer` (not the reusable `CountdownTimer`) so expiry can return a `tea.Cmd` through the result protocol — `CountdownTimer.OnExpire` is a void callback and can't. Single-line uses `bubbles/textinput`.
- **Depends on:** 14.1
- **Verify:** ✅ `go build` + `go vet` clean (tui/components)

---

### 18.3 — Extension Selector Component ✅

- [x] `packages/coding-agent/src/core/tui/components/extension_selector.go` (created in 16.13; verified + tightened)
  - Already faithful port: accent/text rows with `→ ` marker, title countdown (auto-cancel), ↑↓/j/k nav, fuzzy search (enhancement over ref)
  - **18.3 gap fixed:** title was bold-only; reference uses `accent + bold`. Fixed in the generic `Selector.View()` so ALL selectors match the TS `theme.fg("accent", theme.bold(title))` style (also consistent with boxOverlay's frame title). One-line change, recolors existing internal titles — no new double-titles introduced.
- Port from `reference/coding-agent/src/modes/interactive/components/extension-selector.ts`
- **Depends on:** 16.13
- **Verify:** ✅ `go build` + `go vet` clean (tui/components)

---

### 18.4 — Extension Widget Containers ✅

- [x] Update `packages/coding-agent/src/core/tui/app_extensions.go` (~150 lines)
  - Widget container state above/below editor (`widgetsAbove`/`widgetsBelow`)
  - `setExtensionWidget(key, lines, placement)` / `removeExtensionWidget` / `clearExtensionWidgets`
  - `renderExtensionWidgets` + `hasExtensionWidgets` helpers (consumed by view layer in 18.5)
  - Line truncation at `maxExtensionWidgetLines` (10) + "... (widget truncated)" notice
- Port from `reference/coding-agent/src/modes/interactive/interactive-mode.ts:1718-1837`
- **Honest deferrals (verified against SDK surface):**
  - Component-factory widgets + custom header/footer factories → **NOT PORTABLE**: TS factories return pi-tui `Component`, which has no Go equivalent an extension could construct across the plugin boundary. Only string-line widgets are supported.
  - Terminal-input subscribers → **DEFERRED**: needs a new `ExtensionUIContext` field (no `onTerminalInput` exists yet).
  - **SDK GAP:** `ExtensionUIContext` exposes only Select/Confirm/Input/Notify/Status — no `setWidget`, so containers have no data source yet. Adding a `SetWidget` field is the prerequisite that makes this live (one-line callback, infra built now).
- **Depends on:** 18.1, 18.2, 18.3
- **Verify:** ✅ `go build ./...` clean (no import cycle: tui→extensions safe); `go vet` clean

---

### 18.5 — Wire Extension UI into App ✅

- [x] Update `app_view.go` — widget containers render in `renderBaseLayout` (above-editor between status+editor; below-editor between editor+footer) via `renderWidgetsAbove`/`renderWidgetsBelow`
- [x] Update `app_update.go` — forward-looking note on chat-height calc (widget line counts to subtract once widgets are fed)
- **Honest scope:** rendered the widget containers into the layout. The plan's other two items were not portable/available:
  - Custom header/footer rendering → **NOT PORTABLE** (factory returns pi-tui `Component`; see 18.4).
  - Extension UI event handling in app_update.go → **DEFERRED**: widgets are static string content (no events); dialog results flow through the existing overlay result-protocol once the synchronous→async bridge is built (separate runtime task, needs SDK `ExtensionUIContext` work).
- **Depends on:** 18.4
- **Verify:** ✅ `go build ./...` clean; `go vet` clean (tui); binary builds (23MB)

---

### ✅ Phase 18 Checkpoint

**Run:** `go build ./cmd/ycode/ && ./ycode`
**Expect:** Extensions can provide custom editors, inputs, selectors, widgets. Custom header/footer renders.
**Build verified:** ✅ binary builds (23MB). Runtime extension-UI behavior is gated on the SDK gaps documented in 18.4/18.5 (no `setWidget`/header/footer/terminal-input surface in `ExtensionUIContext` yet).
**If fail:** Debug until this works before moving to Phase 19.

---

## Phase 19: CLI + Entry Points + Config

### 19.1 — Config Package

- [x] `packages/coding-agent/src/config/config.go` (~390 lines)
  - `GetAgentDir()`, `GetAuthPath()`, `GetSessionDir()`, `GetThemesDir()`,
    `GetCustomThemesDir()`
  - `GetDebugLogPath()`, `GetDocsPath()`, `GetShareViewerUrl()`
  - `GetPackageDir()` + asset paths (themes/export-html/docs/readme/examples/CHANGELOG)
  - Environment var support (`YCODE_CODING_AGENT_DIR`, `…_SESSION_DIR`, `PI_PACKAGE_DIR`,
    `PI_SHARE_VIEWER_URL`)
  - Path expansion (`~`/`~\`), home directory resolution
  - Port from `reference/coding-agent/src/config.ts`
- **Depends on:** none (standalone package)
- **Verify:** ✅ `go build ./packages/coding-agent/src/config/` compiles; full
  `go build ./...` passes; vet clean.
- **Notes:**
  - **Canonical home:** `core/configuration.go` previously held a partial port
    already wired into ~15 call sites. It is now thin wrappers delegating to the
    new `config` package (one source of truth). All exported names preserved
    (`AppName`/`ConfigDirName`/`Version`/`PackageName` + getters).
  - **Env-var casing fix:** core previously read lowercase `ycode_CODING_AGENT_DIR`;
    the canonical package uses the TS-correct `YCODE_CODING_AGENT_DIR`
    (`strings.ToUpper(AppName) + "_CODING_AGENT_DIR"`), matching the reference and
    the plan's `PI_CODING_AGENT_DIR` example.
  - **Deferred (documented stubs):** the ~280 lines of npm/pnpm/yarn/bun
    install-method detection + self-update machinery (`DetectInstallMethod`,
    `GetSelfUpdateCommand`, etc.) are N/A for a compiled Go binary — stubbed to
    return `InstallMethodUnknown` / nil, with honest doc. Port the full machinery
    only if ycode is later distributed via an npm-family registry.
  - **Themes:** built-ins are embedded (`core/tui/theme/schema.go` //go:embed), so
    `GetThemesDir()` is asset-location parity only; `GetCustomThemesDir()` is the
    meaningful user-themes path.
  - `Version` is the natural `-ldflags` injection target (now on the config pkg).

---

### 19.2 — CLI Argument Parsing

- [x] `packages/coding-agent/src/cli/args.go` (~430 lines) + `args_test.go` (~190 lines)
  - `ParseArgs(args []string) Args` — hand-rolled parser (NOT a framework)
  - All flags: `--provider`, `--model`, `--api-key`, `--system-prompt`,
    `--append-system-prompt`, `--mode`, `--print`/`-p`, `--continue`/`-c`,
    `--resume`/`-r`, `--session`, `--fork`, `--session-dir`, `--no-session`,
    `--models`, `--no-tools`/`-nt`, `--no-builtin-tools`/`-nbt`, `--tools`/`-t`,
    `--thinking`, `--extension`/`-e`, `--no-extensions`/`-ne`, `--skill`,
    `--no-skills`/`-ns`, `--prompt-template`, `--no-prompt-templates`/`-np`,
    `--theme`, `--no-themes`, `--no-context-files`/`-nc`, `--export`,
    `--list-models [search]`, `--verbose`, `--offline`, `--help`/`-h`,
    `--version`/`-v`
  - `@file` args → `FileArgs`; positional → `Messages`
  - `PrintHelp(extFlags)` + `IsValidThinkingLevel`
  - Port from `reference/coding-agent/src/cli/args.ts`
- **Depends:** none new (config, sdk, core/extensions — no cobra)
- **Verify:** ✅ `go build ./packages/coding-agent/src/cli/` compiles; vet clean;
  full `go build ./...` green; `go test ./packages/coding-agent/src/cli/` — 24
  subtests PASS.
- **Notes:**
  - **Hand-rolled, not cobra:** the plan said "Cobra root command (depends 14.1)"
    but (a) cobra was never actually added to go.mod (14.1's `go get` didn't
    stick) and (b) the reference `args.ts` is a manual loop whose semantics
    don't fit pflag: unknown long flags are *captured* into `UnknownFlags` (so
    extensions register `--plan` etc., not error), `@file` collected separately,
    and `--print` consumes the next arg under special `@`/`-`/`---` rules.
    Ported the loop 1:1 to preserve every quirk. (Decision confirmed with user.)
  - `Args.ListModels` splits the TS `string | true` union into `ListModels bool`
    + `ListModelsSearch string`.
  - `Diagnostics` collects warnings (invalid thinking) + errors (unknown short
    option) instead of returning an error, matching the reference.
  - `PrintHelp` sources dynamic values from the `config` package (app name,
    config dir, env-var names) and bolds section headers via raw ANSI (help is
    printed before the TUI/theme starts). Static product refs (pi.dev URL, "pi
    and installed extensions") kept verbatim from the reference — may want
    ycode-specific adjustment later.
  - **Lifecycle note:** `cmd/ycode/main.go` (19.8) does not yet call
    `cli.ParseArgs` — it still does no arg parsing. Wiring is the 19.8 step.

---

### 19.3 — File Processor

- [x] `packages/coding-agent/src/cli/file_processor.go` (~135 lines) + `file_processor_test.go` (~130 lines)
  - `ProcessFileArguments(fileArgs, cwd, opts) (text string, images []*llm.ImageContent, err error)`
  - @file → absolute path (via `tools.ResolveReadPath`), existence check (fatal on
    missing), empty files skipped, text inlined as `<file name="…">…</file>`,
    images attached as `llm.ImageContent` + a `<file name="…"></file>` reference
  - Port from `reference/coding-agent/src/cli/file-processor.ts`
- **Depends:** `core/tools` (ResolveReadPath, DetectImageMimeType, ResizeImage)
- **Verify:** ✅ `go build ./packages/coding-agent/src/cli/` + `core/tools` +
  full `go build ./...` green; vet clean (only pre-existing bash.go warnings);
  `go test ./packages/coding-agent/src/cli/` — 6 file-processor tests PASS
  (text, empty-skipped, not-found, multiple/order, image-no-resize, default-resize).
- **Notes:**
  - **Reuse, not duplicate:** added an exported package-level `tools.DetectImageMimeType`
    in `read.go` (the read tool's `defaultReadOps.DetectImageMimeType` now
    delegates to it) so @file processing classifies images identically to the
    read tool — single source of truth. Reuses `tools.ResizeImage` for resize.
  - **Signature deviations from the plan sketch** (documented in the file header):
    (a) takes `cwd` explicitly instead of reading `os.Getwd()` internally — Go
    idiom + testability + codebase convention; (b) returns `[]*llm.ImageContent`
    (the faithful `ImageContent[]`) not `[]any` — directly consumable by
    BuildInitialMessage (19.4). Missing file is returned as an error (caller
    exits) rather than `process.exit(1)` inside the library.
  - **Honest deferrals** (documented in the file header, not faked): (1) real
    resizing — `tools.ResizeImage` is a stub; when it can't resize an oversized
    image we emit the reference's "Image omitted" note and skip the attachment
    (matching TS `resizeImage`→null). (2) dimension notes —
    `tools.ResizedImage` carries no dimensions/wasResized, so
    `formatDimensionNote` is not portable; we emit the no-note reference form.
  - **stdin detection** mentioned in the plan sketch belongs to 19.4
    (BuildInitialMessage consumes `stdinContent`); file-processor handles files only.

---

### 19.4 — Initial Message Builder

- [x] `packages/coding-agent/src/cli/initial_message.go` (~60 lines) + `initial_message_test.go` (~75 lines)
  - `BuildInitialMessage(InitialMessageInput) InitialMessageResult`
  - Concatenates stdin + @file text + first positional message (in that order),
    pops the first message off `Parsed.Messages` (the TS `parsed.messages.shift()`)
  - `InitialMessageInput{Parsed *Args, FileText, FileImages, StdinContent}`;
    `InitialMessageResult{InitialMessage, Images}`
  - Port from `reference/coding-agent/src/cli/initial-message.ts`
- **Depends:** 19.2 (Args), 19.3 (FileText/FileImages)
- **Verify:** ✅ `go build ./packages/coding-agent/src/cli/` + full suite green;
  vet clean; `go test` — 5 initial-message tests PASS (concat-all, first-shifted,
  empty, images-through, nil-parsed).
- **Notes:**
  - **`Parsed` is `*Args`** (not a value) so the message shift mutates the
    caller's Args — the remaining messages stay queued for the caller, matching
    the TS pass-by-reference `parsed.messages.shift()`.
  - TS "optional" fields → Go zero values: empty `InitialMessage` / nil `Images`
    both mean "absent" (consumers treat them falsy, as the TS `undefined`).
  - **Deviation from the plan sketch** (`BuildInitialMessage(args, stdinContent)`):
    the faithful port takes an `InitialMessageInput` struct bundling parsed args,
    @file text/images, and stdin — matching the reference's destructure signature
    and the outputs of ParseArgs (19.2) + ProcessFileArguments (19.3). Stdin
    *reading* (pipe detection) happens in the entry point (19.8); this builder
    only consumes already-read `StdinContent`.

---

### 19.5 — Model Listing

- [x] `packages/coding-agent/src/cli/list_models.go` (~205 lines) + `list_models_test.go` (~130 lines)
  - `ListModels(registry *core.ModelRegistry, searchPattern string, out io.Writer) error`
    — load-error warning to stderr, available models, optional fuzzy filter,
    sorted fixed-width table (provider/model/context/max-out/thinking/images)
  - Extracted `renderModelTable(models, out)` for unit-testable table formatting
  - Helpers: `formatTokenCount` (200K/1.5M), `fuzzyMatch`, `yesNo`,
    `hasImageInput`, `formatNoModelsAvailableMessage`/`getProviderLoginHelp`
  - Port from `reference/coding-agent/src/cli/list-models.ts`
- **Depends on:** `core.ModelRegistry`, `config.GetDocsPath`
- **Verify:** ✅ `go build ./packages/coding-agent/src/cli/` + full `go build ./...`
  green; vet clean; `go test` — 5 tests PASS (token formatting, fuzzy match,
  yesNo/hasImage, table render+sort+alignment, no-models message).
- **Notes:**
  - **Reuse:** reuses `padRight`/`bold` from args.go (same package). `fuzzyMatch`
    is a LOCAL verbatim copy of `components.FuzzyMatch` — kept local so a
    stdout-only `--list-models` path does NOT pull the TUI component graph
    (bubbletea/lipgloss/theme) into cli. This mirrors the precedent in
    `core/tui/autocomplete/fuzzy.go`; the codebase now has three near-identical
    fuzzy copies (consolidating into a shared package is a later cleanup).
  - **auth-guidance not ported as a module** — `getProviderLoginHelp`/
    `formatNoModelsAvailableMessage` reproduced here (only consumers this phase)
    via `config.GetDocsPath`. Rest of auth-guidance (no-model-selected/
    no-api-key messages, OAuth login flow) deferred to Phase 19+.
  - **Signature deviations from the plan sketch** (`ListModels(registry) error`):
    adds `searchPattern` (faithful TS 2nd param) + `out io.Writer` (testability;
    TS console.log→stdout, load warning→stderr via `os.Stderr`). Returns the
    `GetAvailable` error defensively (currently always nil).
  - **Lifecycle note:** `ListModels` is not yet called by the entry point —
    `cmd/ycode/main.go` (19.8) wires `--list-models` to it.

---

### 19.6 — Session Picker

- [x] `packages/coding-agent/src/cli/session_picker.go` (~155 lines, larger than ~52 estimate)
  - `SelectSession(sm *core.SessionManager) (sessionPath string, err error)` — standalone `tea.Program` wrapping `SessionSelectorComponent`.
  - Port from `reference/coding-agent/src/cli/session-picker.ts`
  - **Return is a PATH, not an id:** the reference `selectSession` resolves the selected file PATH (or null), and main.ts consumes it as a path (`SessionManager.open(selectedPath)`). The plan sketch's `sessionID` name is a mislabel; faithful port returns `sessionPath`. Cancel → `""` + nil err (caller treats `""` as "no selection", matching TS null → "No session selected").
  - **Loader shape:** reference takes two async loaders; Go `SessionManager` derives both pools itself (`core.ListSessions` + `core.ListAllSessions` + `GetSessionFile()`), mirroring the App's `openSessionSelector` (app_selectors.go:382). Go list funcs are synchronous → no progress callback.
  - **Theme:** reference inherits the user's configured theme (`initTheme`). Settings→theme wiring isn't this phase, so the picker uses the built-in dark theme via `theme.NewThemeFromJSON(theme.GetDefaultDarkTheme(), nil)` — no theme-package changes (reuses already-exported constructors). A later overload can accept the configured theme (19.8).
  - **First cli file to import bubbletea** (interactive picker cannot avoid it, unlike the stdout-only `--list-models` path which deliberately kept bubbletea out via a local `fuzzyMatch` copy).
  - **Testability seams (extracted, not invented):** `SelectSession` drives a real `tea.Program` (needs a TTY) so it's not unit-tested; the two pure seams are — `loadSessionPools(sm)` (data gathering) and `sessionPickerCallbacks(selected)` (select→set path+quit / cancel→clear+quit). `newSessionPickerModel` wraps the component in a minimal `tea.Model` (forwards msgs, resizes on `WindowSizeMsg`).
- [x] `packages/coding-agent/src/cli/session_picker_test.go` (~115 lines, 5 tests, all PASS) — covers `loadSessionPools` nil-safety + a real session file on a temp dir (asserts hermetic `current` pool + `currentPath` round-trip; does NOT assert `all` since `ListAllSessions` reads a global/env dir), `sessionPickerCallbacks` select/cancel/nil-pointer wiring (asserts non-nil quit cmd + side effect — Go forbids comparing funcs, so `cmd() == nil` instead of `== tea.Quit`), and `newSessionPickerModel` render + `WindowSizeMsg` resize.
- **Depends on:** 14.1 (keybindings), 16.4 (SessionSelectorComponent)
- **Verify:** `go build ./...` ✅, `go vet ./packages/coding-agent/src/cli/` ✅, `go test ./packages/coding-agent/src/cli/` ✅ (5/5)

---

### 19.7 — Config Selector

- [x] `packages/coding-agent/src/cli/config_selector.go` (~150 lines, larger than ~52 estimate)
  - `SelectConfig(options ConfigSelectorOptions) error` — standalone `tea.Program` wrapping `ConfigSelectorComponent`.
  - Port from `reference/coding-agent/src/cli/config-selector.ts`
  - **Signature needs options, like 19.6:** the reference takes `ConfigSelectorOptions { resolvedPaths, settingsManager, cwd, agentDir }`. The Go `ConfigSelectorComponent` constructor consumes only `*core.ResolvedPaths` + callbacks (settingsManager/cwd/agentDir were TS-internal), so the faithful port takes `ConfigSelectorOptions{ResolvedPaths *core.ResolvedPaths}`. The no-arg sketch (like 19.6's "sessionID") was incomplete.
  - **Theme:** reuses `defaultTheme()` from session_picker.go (single source of truth — built-in dark theme via `theme.NewThemeFromJSON`). A later overload can accept the configured theme (19.8).
  - **Persistence deferred:** the App's `openConfigSelector` uses a no-op `OnToggle` and an empty `&core.ResolvedPaths{}` (per-resource SettingsManager setters + PackageManager port pending from 16.18). This port matches that state — `OnToggle` is a no-op, `ResolvedPaths` may be empty (selector renders "No resources found" but doesn't persist). Wiring real persistence is deferred.
  - **OnExit → sentinel, not os.Exit:** the TS exit callback (ctrl+c) calls `process.exit(0)`. A library must not call os.Exit, so `SelectConfig` returns `ErrConfigExit` (a sentinel error) on hard-exit; the entry point (19.8) maps it to `os.Exit(0)`. OnClose (escape) → normal completion (nil error), matching TS `resolve()`.
  - **Testability seams (extracted):** `SelectConfig` drives a real `tea.Program` (needs a TTY), so it's not unit-tested; the pure seams are — `configSelectorCallbacks(exitKind)` (close→`configExitClose`+quit / exit→`configExitHard`+quit / OnToggle no-op) and `exitKindToError(exitKind)` (the close↔nil / hard↔ErrConfigExit mapping). `newConfigSelectorModel` wraps the component (forwards msgs, resizes on `WindowSizeMsg`, tolerates nil ResolvedPaths).
- [x] `packages/coding-agent/src/cli/config_selector_test.go` (~95 lines, 4 tests, all PASS) — `configSelectorCallbacks` close/exit/no-op-toggle/nil-pointer wiring (asserts non-nil quit cmd via `cmd()` — Go forbids func comparison), `exitKindToError` mapping (`errors.Is(ErrConfigExit)`), and `newConfigSelectorModel` render (title substring) + `WindowSizeMsg` resize with nil ResolvedPaths.
- **Depends on:** 14.1 (keybindings), 16.x (ConfigSelectorComponent)
- **Verify:** `go build ./...` ✅, `go vet ./packages/coding-agent/src/cli/` ✅, `go test ./packages/coding-agent/src/cli/` ✅ (all pass)

---

### 19.8 — Full Entry Point

- [x] Port `reference/coding-agent/src/main.ts` → `cmd/ycode/` (Full structural port)
  - Split into 5 files in `package main` (one ~727-line file is untestable):
    - `main.go` — `main()` + `run()` orchestrator (offline env, ParseArgs +
      parse diagnostics, app-mode detect, version/export early exits, rpc+@file
      guard, fork-flag validation, migrations no-op, cwd/agentDir, settings +
      diagnostics, session-dir chain, session resolution, missing-cwd guard,
      resolved CLI paths, runtime factory + CreateAgentSessionRuntime,
      help/list-models, stdin pipe detection, initial-message assembly, scoped-
      models banner, diagnostics report + error gate, no-model gate, dispatch).
    - `helpers.go` — pure/testable: `resolveAppMode`, `toPrintOutputMode`,
      `isTruthyEnvFlag`, `forkFlagConflicts`/`validateForkFlags`,
      `resolveCliPaths`/`resolveOneCliPath`, `collectSettingsDiagnostics`,
      `reportDiagnostics`/`diagnosticStyle`, `reportParseDiagnostics`/
      `parseDiagnosticStyle`, `stdinIsTerminal`/`readPipedStdin`,
      `collectExtensionFlags`, ANSI color consts, `appMode` type.
    - `session.go` — `resolveSessionPath`/`resolvedSession`, `promptConfirm`,
      `forkSessionOrExit`, `sessionCwdAdapter` (bridges `*SessionManager` to the
      typo'd `SessionCwdSource.GetSesionFile()`), `createSessionManager`.
    - `runtime.go` — `buildSessionOptions`/`sessionOptionsResult`,
      `findScopedModel`, `createRuntimeFactory` (the cwd-bound runtime closure),
      `prepareInitialMessage`, `launchInteractive`, `printScopedModelsBanner`.
    - `modes.go` — `runDeferredMode` (print/json/rpc stub → stderr + exit 2).
  - Mode selection, session resumption (`--session/--resume/--continue/--fork/
    --no-session`), CLI model + thinking + scoped-models resolution, diagnostics
    reporting, stdin pipe detection, `@file`/stdin initial-message assembly all
    wired. Interactive TUI launch (`tui.NewApp`) behaves as before.
  - Companion tests: `helpers_test.go`, `session_test.go`, `runtime_test.go`
    cover the extracted pure seams (resolveAppMode, toPrintOutputMode,
    isTruthyEnvFlag, forkFlagConflicts, resolveCliPaths, collectSettingsDiagnostics
    nil-safety, diagnosticStyle/reportDiagnostics incl. the `DiagnosticInfo`
    "string"-bug → dim path, parseDiagnosticStyle; resolveSessionPath path/local/
    not-found, sessionCwdAdapter; findScopedModel, buildSessionOptions empty/
    tools/thinking-precedence/scoped-fallback/existing-session-suppression).
  - **Note:** the plan text's path `packages/coding-agent/cmd/ycode/main.go` is
    incorrect; the entry point lives at `cmd/ycode/main.go` (module root).
- **Deferrals (explicit stubs/notes):**
  - print/json/rpc modes → `runDeferredMode` (no modes package ported yet; see
    Phase 20 / 20.5). The no-model gate fires first for unconfigured setups, so
    `-p` currently exits at the model-availability gate, not the mode stub —
    faithful to TS ordering.
  - migrations → no-op (`migratedProviders`/deprecationWarnings nil). Phase 19.9.
  - package/config CLI dispatcher (`ycode install/remove/update/list/config`) →
    not intercepted (no dispatcher exists).
  - stdout takeover (`takeOverStdout`/`restoreStdout`) → omitted (output-guard
    has no Go port).
  - missing-cwd interactive TUI prompt (`promptForMissingSessionCwd`) → falls
    back to error + exit 1.
  - `initTheme`/theme watcher, `PI_STARTUP_BENCHMARK` fast-path, `resetTimings`
    (unexported) → skipped; TUI runs on the default theme.
  - Settings getters `GetImageAutoResize`/`GetShowHardwareCursor`/`GetClearOnShrink`
    → absent; autoResize defaults to true.
  - `sessionCwdAdapter` works around the `SessionCwdSource.GetSesionFile()` typo
    (missing 's') — recorded as a follow-up core fix.
- **Depends on:** 19.1 through 19.7, all previous phases
- **Verify:** `go build ./...` ✓; `go vet ./cmd/ycode/` clean ✓; `go test
  ./cmd/ycode/` PASS ✓; smoke `go run ./cmd/ycode --version` → `0.0.0` ✓,
  `--help` → bold help ✓.

---

### 19.9 — Migration System

- [x] Port `reference/coding-agent/src/migrations.ts` →
  `packages/coding-agent/src/migrations/migrations.go` (package `migrations`)
  - `RunMigrations(cwd) MigrationResult` (auth, sessions, tools→bin, keybindings,
    extension-system + deprecation warnings) + `ShowDeprecationWarnings`.
  - Auth: `MigrateAuthToAuthJson` (oauth.json + settings.json `apiKeys` →
    auth.json; oauth.json retired to `.migrated`; `apiKeys` stripped from
    settings.json); pure `buildMigratedAuth` (oauth precedence, apiKey gap-fill,
    string-only keys).
  - Sessions: `MigrateSessionsFromAgentRoot` moves stray agent-root `.jsonl`
    files into the per-cwd session dir, reusing `core.GetDefaultSessionDir` for
    the canonical `--<cwd>--` encoding (single source of truth).
  - Extension system: `migrateCommandsToPrompts` (commands/ → prompts/),
    `checkDeprecatedExtensionDirs` (hooks//tools/ warnings), pure
    `filterCustomTools`.
  - Keybindings: `migrateKeybindingsConfigFileIn` via `core.MigrateKeybindingsConfig`,
    rewritten only when changed (pure `keybindingsChanged` via JSON compare).
  - Tools: `migrateToolsToBinIn` (fd/rg → bin/).
  - `ShowDeprecationWarnings` (pure `formatDeprecationWarnings` + best-effort
    Enter-to-continue; the TS raw-mode "any key" approximates to Enter, fine for
    the interactive-only call site).
  - **Placement deviation:** the sketch placed this at
    `packages/coding-agent/src/config/migrations.go`, but that is an import
    cycle: the package needs `core.GetDefaultSessionDir`/`core.MigrateKeybindingsConfig`
    and `core` already imports `config` (`core/configuration.go`). The TS file is
    a top-level module importing both `./config` and `./core/…`; the faithful Go
    analog is this dedicated top-level `migrations` package (imports core +
    config, imported only by the entry point — no cycle).
  - **Wiring (19.8 main.go updated):** the 19.8 migrations no-op is replaced by
    `migrations.RunMigrations(cwd)`; `migratedProviders` now flows from
    `MigrationResult.MigratedAuthProviders` into `launchInteractive`, and
    `ShowDeprecationWarnings` runs in interactive mode when warnings exist.
  - Tests (`migrations_test.go`): `buildMigratedAuth` (precedence/gap-fill/
    shapes/empty), `filterCustomTools`, `keybindingsChanged`,
    `formatDeprecationWarnings`, `migrateCommandsToPrompts` (temp-dir FS),
    `migrateAuthToAuthJsonIn` (temp-dir FS: oauth retired, apiKeys stripped,
    auth.json created, idempotent on 2nd run), `migrateOneSessionFile` (temp-dir
    FS: moved to encoded dir, skip-if-exists). FS-touching functions are exposed
    as `...In(dir)` seams so tests use temp dirs, never the real agent dir.
- **Depends on:** 19.1
- **Verify:** `go build ./...` ✓; `go vet ./packages/coding-agent/src/migrations/
  ./cmd/ycode/` clean ✓; `go test ./packages/coding-agent/src/migrations/
  ./cmd/ycode/` PASS ✓.

---

### ✅ Phase 19 Checkpoint

**Run:** `go build ./packages/coding-agent/cmd/ycode/ && ./ycode`
**Expect:** `ycode` → interactive mode. `ycode -p "hello"` → print mode. `ycode --help` → help text.
**If fail:** Debug until this works before moving to Phase 20.

---

## Phase 20: Non-Interactive Modes

### 20.1 — JSONL Serialization

- [x] `packages/coding-agent/src/modes/jsonl.go` (package `modes`)
  - `SerializeJSONLine(value any) (string, error)` — single strict JSONL record +
    one LF; encoding/json's default escaping of `<,>,&` + U+2028/2029 strengthens
    the single-line guarantee.
  - `AttachJSONLLineReader(r io.Reader, onLine func(line string)) error` — strict
    LF-only framing via `bufio.Reader.ReadString` (unbounded record length; strips
    one trailing `\r` per line); emits each framed line + surfaces non-EOF read
    errors.
  - Port from `reference/coding-agent/src/modes/rpc/jsonl.ts`
  - **Go adaptations (documented in-file):**
    - TS `JSON.stringify` throws on failure → Go returns `(string, error)`
      (in practice RPC payloads never fail to marshal).
    - TS attaches push-model listeners + returns a detach fn; Go is pull-based, so
      the reader blocks to EOF and returns the read error in place of a detach
      handle — what the RPC stdin loop (20.4) needs.
  - Tests (`jsonl_test.go`): serialize round-trip + single-LF guarantee for
    embedded LF/CR/U+2028/U+2029 + marshal-error path; reader LF framing
    (incl. no-trailing-newline), CRLF `\r` strip, empty-line + no-spurious-trailing
    semantics, U+2028/2029-kept-in-record, and non-EOF read error surfacing.
- **Depends on:** none
- **Verify:** `go build ./...` ✓; `go vet ./packages/coding-agent/src/modes/`
  clean ✓; `go test ./packages/coding-agent/src/modes/` PASS ✓.

---

### 20.2 — RPC Types

- [x] `packages/coding-agent/src/modes/rpc_types.go` (package `modes`)
  - TS discriminated unions → Go discriminator-tagged structs: `RpcCommand`
    (Type), `RpcResponse` (Command + Success/Error/Data), `RpcExtensionUIRequest`
    (Method), `RpcExtensionUIResponse` (Confirmed is `*bool` so `confirm:false`
    survives omitempty). Plain structs `RpcSessionState`, `RpcSlashCommand`.
  - Named-constant enhancement over the TS inline string literals: `Cmd*`
    (command types), `Method*`/`Notify*`/`WidgetPlacement*` (ext-UI methods),
    `Source*` (slash-command sources), `Mode*` (queue modes), `Streaming*`,
    envelope-type consts — makes 20.4's switches typo-proof.
  - `NewSuccessResponse`/`NewErrorResponse` envelope helpers.
  - Port from `reference/coding-agent/src/modes/rpc/rpc-types.ts`
  - **Type choices (documented in-file):** enums → real `llm.ThinkingLevel` /
    `string` (wire string either way); `Model` → `*llm.Model[llm.Api]` (llm.Model
    has json tags matching the TS `Model<any>` shape); `Images` →
    `[]*llm.ImageContent`; response `Data` + `RpcSlashCommand.SourceInfo` → `any`
    to avoid coupling this protocol file to core's still-evolving types
    (SessionStats/BashResult/CompactionResult/AgentMessage/SourceInfo) — the
    handler (20.4) assigns the concrete core values, which marshal correctly.
    Only `llm` is imported (safe leaf).
  - Tests (`rpc_types_test.go`): JSON round-trip + camelCase wire-name guard
    (guards against json-tag typos like `modelId` vs `model_id`) across commands,
    session state (+Model), response envelopes (success-with/without-data,
    error), slash command, ext-UI request (notify/setWidget), and the
    `*bool` Confirmed false-vs-absent distinction.
- **Depends on:** none
- **Verify:** `go build ./...` ✓; `go vet ./packages/coding-agent/src/modes/`
  clean ✓; `go test ./packages/coding-agent/src/modes/` PASS ✓.

---

### 20.3 — Print Mode

- [x] `packages/coding-agent/src/modes/print_mode.go` (package `modes`)
  - `RunPrintMode(runtimeHost *core.AgentSessionRuntime, options PrintModeOptions) (int, error)`
    — single-shot: signal handling (SIGTERM/SIGHUP), rebind (BindExtensions +
    Subscribe), JSON header + event stream, send initialMessage + messages via
    `session.Prompt`, text-mode last-assistant extraction, dispose.
  - `PrintModeOptions{Mode, Messages, InitialMessage, InitialImages}`; `PrintOutputText`/`PrintOutputJSON` consts.
  - Pure seam `extractPrintResult(messages []llm.Message) printResult` — the
    text-mode block (only the LAST message, only if assistant; error/aborted →
    exit 1 + message; else joined text blocks).
  - Port from `reference/coding-agent/src/modes/print-mode.ts`
  - **Honest deferrals (documented in-file):**
    - `CommandContextActions` NOT wired — grep finds zero construction sites
      anywhere in the Go port (incl. the interactive App), so deferring matches
      current state; only `OnError` is bound. Port when extensions gain
      command-context support (shared builder for interactive + print).
    - output-guard (`writeRawStdout`/`flushRawStdout`) not ported → direct
      `os.Stdout` writes (unbuffered); flush is a no-op.
    - `killTrackedDetachedChildren` not ported → signal path disposes + exits
      with TS codes (143 SIGTERM / 129 SIGHUP).
    - **JSON event stream divergence:** Go `AgentSession.Subscribe` delivers
      `AgentSessionEvent` (queue/compaction/session-info level, no json tags →
      PascalCase), not the TS raw streaming events. Text mode (the primary `-p`
      path) is fully faithful; full JSON event-stream parity awaits a
      streaming-event bridge + json tags.
    - **Error contract:** Prompt/rebind errors → `(1, err)` for the caller to
      log (TS catches+logs+returns 1); stopReason error/aborted → logged here +
      `(1, nil)` (a result, not a thrown exception).
  - Tests (`print_mode_test.go`): `extractPrintResult` — empty, last-not-assistant
    (must NOT scan back), text-block concatenation, non-text-content filtering,
    error stop reason (exit 1 + message), aborted-no-message ("Request aborted"
    fallback). (RunPrintMode drives a live runtime → not unit-tested, like
    interactive mode.)
  - **Verified seams:** `Prompt` is synchronous (returns error); messages hold
    `llm.AssistantMessage` by value (mirror `findLastAssistantInMessages`);
    `SetRebindSession` takes `func(*AgentSession) error` (rebind keeps the outer
    handle in sync).
- **Depends on:** 20.1
- **Verify:** `go build ./...` ✓; `go vet ./packages/coding-agent/src/modes/`
  clean ✓; `go test ./packages/coding-agent/src/modes/` PASS ✓.

---

### 20.4 — RPC Mode

- [x] `packages/coding-agent/src/modes/rpc_mode.go` + `rpc_mode_test.go`
  - `RunRPCMode(runtimeHost) error` — binds RPC UI context, subscribes to events,
    registers SIGTERM(143)/SIGHUP(129) handlers, reads JSONL from stdin until
    EOF/shutdown (blocks for process lifetime; returns err only on rebind failure)
  - Full JSONL command protocol: stdin → `handleLine` (parse → route
    `extension_ui_response` to waiting dialog, else dispatch command); all 29
    commands handled (prompt/steer/follow_up/abort/new_session/get_state/set_model/
    cycle_model/get_available_models/set_thinking_level/cycle_thinking_level/
    set_steering_mode/set_follow_up_mode/compact/set_auto_compaction/abort_retry/
    bash/abort_bash/get_session_stats/export_html/switch_session/fork/clone/
    get_last_assistant_text/set_session_name/get_messages/get_commands + default)
  - Extension UI support via the Go 5-field `ExtensionUIContext` struct
    (Select/Confirm/Input round-trip through correlated `extension_ui_request`/
    `extension_ui_response`; Notify/Status fire-and-forget). Dialogs use a
    pending-channel map keyed by request id (atomic counter, not crypto UUID)
    with optional timeout
  - Pure testable seams: `buildCommandsList`, `lastAssistantText`,
    `concatTextBlocks`, `dialogSelectIndex`/`dialogConfirmValue`/`dialogInputValue`,
    `imagesToAny`; `rpcServer` drives the live runtime (not unit-tested, like
    RunPrintMode)
  - Port from `reference/coding-agent/src/modes/rpc/rpc-mode.ts`
- **Deviations / honest deferrals (documented in-file):**
  - `ExtensionUIContext` is a 5-field **struct** (not the TS ~30-method
    interface) → RPC UI wires only Select/Confirm/Input/Notify/Status;
    editor/setWidget/setTitle/theme/setEditorText have no Go field (not emitted)
  - `CommandContextActions` deferred (unwired everywhere in the Go port — same
    as 20.3); only `ShutdownHandler` + `OnError` bound
  - `set_auto_retry` → honest error (no `SetAutoRetryEnabled`); `get_fork_messages`
    → honest error (no `GetUserMessagesForForking`)
  - `get_last_assistant_text` **reimplemented** via backward message scan
    (`lastAssistantText`); `get_state.pendingMessageCount` deferred to 0
    (no queue-length getter)
  - output-guard / `killTrackedDetachedChildren` not ported (same as 20.3);
    stdout writes mutex-guarded; `prompt` is fire-and-forget goroutine so
    turn-triggered UI dialogs don't deadlock the stdin reader
  - `ThinkingLevel` needs `llm.ThinkingLevel(...)` conversion (session returns
    `sdk.ThinkingLevel`); `CycleModel("")` maps to the TS no-arg "forward" default
- **Depends on:** 20.1, 20.2
- **Verify:** `go build ./...` ✓ · `go vet ./packages/coding-agent/src/modes/` clean ✓ · `go test ./packages/coding-agent/src/modes/` PASS ✓
  (pre-existing `core/tools/bash.go` vet notes are outside this package, untouched)

---

### 20.5 — Wire Modes into Entry Point

- [x] Wired `RunPrintMode()` / `RunRPCMode()` into the entry-point dispatch
  - `cmd/ycode/modes.go` rewritten: `runDeferredMode` replaced by
    `runPrintDispatch` (text/json via `toPrintOutputMode` → `modes.RunPrintMode`,
    exits with the runner's code) + `runRPCDispatch` (`modes.RunRPCMode`, blocks
    for the process lifetime) + shared `toAnyImages` helper
  - `cmd/ycode/main.go` switch: `appRPC`→`runRPCDispatch`, `appPrint`/`appJSON`→
    `runPrintDispatch`, default→`launchInteractive`; `core.PrintTimings()` called
    once before dispatch (TS printTimings() in every branch); header deferral
    note updated (print/rpc/migrations no longer deferred)
  - `cmd/ycode/runtime.go`: `launchInteractive` reuses `toAnyImages` (DRY)
- **Depends on:** 20.3, 20.4, 19.8 (all done)
- **Verify:** `go build ./...` ✓ · `go vet ./cmd/ycode/` clean ✓ · `go test ./cmd/ycode/` PASS ✓ ·
  binary smoke: `--version`→0.0.0 exit 0; `-p` (no model) correctly hits the
  non-interactive no-model gate (red "No models available…")

---

### ✅ Phase 20 Checkpoint (FINAL)

**Run:** `go build ./packages/coding-agent/cmd/ycode/ && ./ycode`
**Test all modes:**
1. `ycode` → interactive mode works end-to-end
2. `ycode -p "explain Go"` → print mode outputs text
3. `ycode --mode json "explain Go"` → JSON event stream
4. `ycode --mode rpc` → JSONL protocol responds to commands
5. Full session: prompt → response → tool calls → bash → model switch → session fork → clean exit

**🎉 If all pass — migration complete!**

---

## Quick Reference: Total Steps per Phase

| Phase | Steps | Files | Est. Lines |
|-------|-------|-------|-----------|
| 14 (Foundation) | 19 | ~15 | ~2,400 |
| 15 (Components) | 15 | ~14 | ~2,150 |
| 16 (Selectors) | 19 | ~17 | ~2,100 |
| 17 (Autocomplete) | 6 | ~5 | ~570 |
| 18 (Extensions) | 5 | ~5 | ~700 |
| 19 (CLI/Config) | 9 | ~9 | ~1,400 |
| 20 (Modes) | 5 | ~4 | ~1,234 |
| **Total** | **78 steps** | **~69 files** | **~10,554** |
