# TUI + CLI + Modes Migration Plan

> Last updated: 2026-06-07
> Branch: `workspace`
> Framework: **Bubble Tea** (Charm ecosystem)
> CLI Parser: **cobra**
> Scope: **All Phases 14–20**

## Context

All core business logic (Phases 11–13, Sprints 1–5) is **complete and compiling** in `packages/coding-agent/src/core/`. The remaining work is the **presentation layer**: the terminal UI, CLI entry points, and non-interactive modes (print, RPC). This is ~18,725 lines of TypeScript across ~55 files that needs to be migrated to Go using the **Bubble Tea** framework (Charm ecosystem).

The TS code uses `@earendil-works/pi-tui`, a custom TUI framework with class-based components (`Container`, `Text`, `Markdown`, `Spacer`, `Input`, `Loader`). In Go, Bubble Tea's **Elm Architecture** (Model-View-Update) replaces this — components become sub-models exposing their own `Init/Update/View`, and events flow as typed messages through a central Update loop.

---

## Framework Choice: Bubble Tea (Charm Ecosystem)

**Why Bubble Tea:**
- Most mature Go TUI framework with large community
- Elm Architecture maps naturally to the event-driven agent session model
- Rich component library (Bubbles): viewport, textarea, spinner, list, table, help
- Lip Gloss for styling with theme support (hex colors, 256-color fallback, adaptive light/dark)
- Huh for forms (login dialogs, confirmations)
- Built-in async command pattern (`tea.Cmd`) — perfect for bridging agent events to UI
- Mouse support, alt screen, window resize handling out of the box

**Key Libraries:**
| Library | Purpose | Replaces TS |
|---------|---------|------------|
| `bubbletea` | Core TUI framework | `@earendil-works/pi-tui` TUI/ProcessTerminal |
| `bubbles/viewport` | Scrollable content | Chat container |
| `bubbles/textarea` | Multi-line input | CustomEditor |
| `bubbles/spinner` | Loading animation | Loader |
| `bubbles/list` | Fuzzy-searchable list | Model/session/theme selectors |
| `bubbles/key` | Keybinding management | KeybindingsManager |
| `bubbles/help` | Keybinding help display | keybinding-hints |
| `lipgloss` | Styling/colors/layout | theme.fg/theme.bg/theme.bold |
| `huh` | Forms/dialogs | LoginDialog, OAuth flow |
| `bubblezone` | Mouse zones | Mouse click handling |
| `glamour` | Markdown rendering | Markdown component |
| `alecthomas/chroma` | Syntax highlighting | cli-highlight |

---

## File & Folder Structure

```
packages/coding-agent/
├── cmd/
│   └── ycode/
│       └── main.go                              # Entry point (replaces main.ts + cli.ts)
│
└── src/
    ├── cli/                                     # CLI utilities package (new)
    │   ├── args.go                              # Argument parsing (replaces cli/args.ts) — ~354 lines
    │   ├── file_processor.go                    # @file processing (replaces cli/file-processor.ts) — ~100 lines
    │   ├── initial_message.go                   # Initial message building (replaces cli/initial-message.ts) — ~43 lines
    │   ├── list_models.go                       # Model listing (replaces cli/list-models.ts) — ~111 lines
    │   ├── session_picker.go                    # Session selection (replaces cli/session-picker.ts) — ~52 lines
    │   └── config_selector.go                   # Config selection (replaces cli/config-selector.ts) — ~52 lines
    │
    ├── config/                                  # Configuration package (new)
    │   ├── config.go                            # Config loading, paths, env vars (replaces config.ts) — ~500 lines
    │   └── migrations.go                        # Settings migration system (replaces migrations.ts) — ~315 lines
    │
    ├── modes/                                   # Mode implementations package (new)
    │   ├── print_mode.go                        # Print mode (replaces modes/print-mode.ts) — ~158 lines
    │   ├── rpc_mode.go                          # RPC mode (replaces modes/rpc/rpc-mode.ts) — ~754 lines
    │   ├── rpc_types.go                         # RPC types (replaces modes/rpc/rpc-types.ts) — ~264 lines
    │   └── jsonl.go                             # JSONL serialization (replaces modes/rpc/jsonl.ts) — ~58 lines
    │
    └── core/
        ├── (existing files — no changes)
        │
        └── tui/                                 # TUI package (new, sub-package of core)
            │
            ├── app.go                           # App model struct + constructor — ~300 lines
            ├── app_types.go                     # All message types, constants, state enums — ~250 lines
            ├── app_update.go                    # Central Update() + key routing — ~600 lines
            ├── app_view.go                      # View() layout composition — ~300 lines
            ├── app_events.go                    # Agent event → tea.Msg bridge — ~400 lines
            ├── app_init.go                      # Init(), startup sequence — ~200 lines
            ├── app_selectors.go                 # Selector overlay management — ~350 lines
            ├── app_autocomplete.go              # Autocomplete provider wiring — ~250 lines
            ├── app_extensions.go                # Extension UI integration — ~300 lines
            │
            ├── components/                      # Reusable TUI components
            │   ├── chat.go                      # Chat viewport (messages list + auto-scroll) — ~200 lines
            │   ├── editor.go                    # Input editor (textarea + autocomplete overlay) — ~400 lines
            │   ├── footer.go                    # Status bar (pwd, tokens, model, context %) — ~220 lines
            │   ├── header.go                    # Header (logo + keybinding hints) — ~100 lines
            │   ├── assistant_message.go         # Assistant message (markdown + thinking blocks) — ~150 lines
            │   ├── user_message.go              # User message bubble — ~50 lines
            │   ├── custom_message.go            # Custom/extension messages — ~100 lines
            │   ├── tool_execution.go            # Tool call/result with expand/collapse — ~350 lines
            │   ├── bash_execution.go            # Bash command output with streaming — ~220 lines
            │   ├── diff.go                      # Diff rendering for edit tools — ~150 lines
            │   ├── markdown.go                  # Markdown renderer (glamour wrapper) — ~80 lines
            │   ├── selector.go                  # Base fuzzy-search selector (reusable) — ~250 lines
            │   ├── model_selector.go            # Model picker (extends selector) — ~150 lines
            │   ├── session_selector.go          # Session picker with tree view — ~250 lines
            │   ├── session_selector_search.go   # Session search variant — ~200 lines
            │   ├── theme_selector.go            # Theme picker — ~70 lines
            │   ├── thinking_selector.go         # Thinking level picker — ~75 lines
            │   ├── settings_selector.go         # Settings picker — ~200 lines
            │   ├── scoped_models_selector.go    # Scoped models picker — ~100 lines
            │   ├── config_selector.go           # Config picker — ~150 lines
            │   ├── show_images_selector.go      # Show images toggle — ~50 lines
            │   ├── user_message_selector.go     # User message picker — ~155 lines
            │   ├── tree_selector.go             # Tree navigation selector — ~300 lines
            │   ├── login_dialog.go              # Auth/login dialog — ~200 lines
            │   ├── oauth_selector.go            # OAuth provider picker — ~210 lines
            │   ├── extension_editor.go          # Extension custom editor — ~150 lines
            │   ├── extension_input.go           # Extension input component — ~90 lines
            │   ├── extension_selector.go        # Extension selector — ~110 lines
            │   ├── skill_invocation.go          # Skill invocation message — ~60 lines
            │   ├── branch_summary.go            # Branch summary message — ~60 lines
            │   ├── compaction_summary.go        # Compaction summary message — ~60 lines
            │   ├── dynamic_border.go            # Border line component — ~30 lines
            │   ├── bordered_loader.go           # Loader with border — ~70 lines
            │   ├── countdown_timer.go           # Countdown timer for retry — ~40 lines
            │   ├── visual_truncate.go           # Visual line truncation — ~50 lines
            │   ├── keybinding_hints.go          # Key hint formatting helpers — ~50 lines
            │   └── earendil_announcement.go      # Startup announcement — ~55 lines
            │
            ├── theme/                           # Theme system
            │   ├── theme.go                     # Theme type, color utilities, fg/bg — ~400 lines
            │   ├── schema.go                    # Theme JSON loading + validation — ~200 lines
            │   ├── colors.go                    # Hex↔RGB, 256-color conversion — ~200 lines
            │   ├── dark.json                    # Default dark theme (embedded)
            │   ├── light.json                   # Default light theme (embedded)
            │   └── theme_test.go                # Color conversion tests — ~100 lines
            │
            └── autocomplete/                    # Autocomplete system
                ├── provider.go                  # AutocompleteProvider interface — ~60 lines
                ├── combined.go                  # Combined provider (slash + templates + extensions) — ~100 lines
                └── slash.go                     # Slash command items — ~80 lines
```

---

## TS → Go File Mapping (Complete)

### Entry Points & Config

| TS File | Lines | Go File | Notes |
|---------|-------|---------|-------|
| `main.ts` | 727 | `cmd/ycode/main.go` | Mode selection, session init, runtime creation |
| `cli.ts` | 22 | (merged into `main.go`) | Bootstrap → just `main()` |
| `config.ts` | 500 | `src/config/config.go` | Path resolution, env vars, auth paths |
| `migrations.ts` | 315 | `src/config/migrations.go` | Settings/auth/session migrations |
| `index.ts` | 353 | (not needed in Go) | TS-specific re-exports |

### CLI Utilities

| TS File | Lines | Go File | Notes |
|---------|-------|---------|-------|
| `cli/args.ts` | 354 | `src/cli/args.go` | Use `cobra` for arg parsing |
| `cli/file-processor.ts` | 100 | `src/cli/file_processor.go` | @file expansion, stdin reading |
| `cli/initial-message.ts` | 43 | `src/cli/initial_message.go` | Build initial prompt from args |
| `cli/list-models.ts` | 111 | `src/cli/list_models.go` | Print available models |
| `cli/session-picker.ts` | 52 | `src/cli/session_picker.go` | Interactive session selection |
| `cli/config-selector.ts` | 52 | `src/cli/config_selector.go` | Config selection UI |

### Modes

| TS File | Lines | Go File | Notes |
|---------|-------|---------|-------|
| `modes/print-mode.ts` | 158 | `src/modes/print_mode.go` | Single-shot text/JSON output |
| `modes/rpc/rpc-mode.ts` | 754 | `src/modes/rpc_mode.go` | JSONL stdin/stdout protocol |
| `modes/rpc/rpc-types.ts` | 264 | `src/modes/rpc_types.go` | Command/response types |
| `modes/rpc/jsonl.ts` | 58 | `src/modes/jsonl.go` | Line reader + serializer |
| `modes/rpc/rpc-client.ts` | 515 | (defer or make internal) | RPC client library |

### Interactive Mode (Main TUI)

The 5475-line `interactive-mode.ts` splits into 9 `app_*.go` files:

| TS Section | Lines | Go File | What |
|------------|-------|---------|------|
| Constructor, fields, accessors | ~350 | `app.go` | App struct, NewApp(), field accessors |
| Types, messages, state | ~300 | `app_types.go` | All tea.Msg types, OverlayState enum |
| Key handling, Update() | ~1500 | `app_update.go` | Central Update(), key routing, state machine |
| View(), layout | ~800 | `app_view.go` | View() composition: header+chat+pending+editor+footer |
| Agent event subscription | ~600 | `app_events.go` | Subscribe() → tea.Msg bridge, streaming updates |
| Startup, Init() | ~300 | `app_init.go` | Init(), notices, changelog, extension binding |
| Selectors (model, session, theme, etc.) | ~800 | `app_selectors.go` | Overlay open/close, selector lifecycle |
| Autocomplete wiring | ~400 | `app_autocomplete.go` | Slash commands, templates, extensions, skills |
| Extension UI (editors, inputs, widgets) | ~425 | `app_extensions.go` | Extension editor/input/selector, widgets, custom footer/header |

### Components (35 files)

| TS File | Lines | Go File | Bubble Tea Primitive |
|---------|-------|---------|---------------------|
| `assistant-message.ts` | 147 | `assistant_message.go` | Custom model (lipgloss layout) |
| `user-message.ts` | 35 | `user_message.go` | Custom model (styled text) |
| `custom-message.ts` | 99 | `custom_message.go` | Custom model |
| `tool-execution.ts` | 352 | `tool_execution.go` | Custom model (expand/collapse) |
| `bash-execution.ts` | 218 | `bash_execution.go` | Custom model (streaming output) |
| `diff.ts` | 147 | `diff.go` | Pure function + lipgloss |
| `footer.ts` | 220 | `footer.go` | Custom model (stats bar) |
| `model-selector.ts` | 338 | `model_selector.go` | bubbles/list + fuzzy search |
| `session-selector.ts` | 1023 | `session_selector.go` | Custom tree + search |
| `session-selector-search.ts` | 194 | `session_selector_search.go` | Search variant |
| `tree-selector.ts` | 1247 | `tree_selector.go` | Custom tree navigator |
| `config-selector.ts` | 618 | `config_selector.go` | bubbles/list |
| `settings-selector.ts` | 533 | `settings_selector.go` | bubbles/list |
| `scoped-models-selector.ts` | 355 | `scoped_models_selector.go` | bubbles/list |
| `show-images-selector.ts` | 50 | `show_images_selector.go` | bubbles/list |
| `user-message-selector.ts` | 155 | `user_message_selector.go` | bubbles/list |
| `thinking-selector.ts` | 74 | `thinking_selector.go` | bubbles/list |
| `theme-selector.ts` | 67 | `theme_selector.go` | bubbles/list |
| `extension-selector.ts` | 107 | `extension_selector.go` | bubbles/list |
| `extension-editor.ts` | 147 | `extension_editor.go` | Custom model |
| `extension-input.ts` | 87 | `extension_input.go` | bubbles/textarea wrapper |
| `login-dialog.ts` | 196 | `login_dialog.go` | huh.Form |
| `oauth-selector.ts` | 208 | `oauth_selector.go` | bubbles/list |
| `skill-invocation-message.ts` | 55 | `skill_invocation.go` | Custom model |
| `branch-summary-message.ts` | 58 | `branch_summary.go` | Custom model |
| `compaction-summary-message.ts` | 59 | `compaction_summary.go` | Custom model |
| `dynamic-border.ts` | 25 | `dynamic_border.go` | Lip gloss border |
| `bordered-loader.ts` | 68 | `bordered_loader.go` | Custom model + spinner |
| `countdown-timer.ts` | 38 | `countdown_timer.go` | bubbles/timer |
| `visual-truncate.ts` | 50 | `visual_truncate.go` | Pure function |
| `keybinding-hints.ts` | 48 | `keybinding_hints.go` | Pure formatting helpers |
| `earendil-announcement.ts` | 53 | `earendil_announcement.go` | Static text |
| `daxnuts.ts` | 164 | (evaluate if needed) | Easter egg component |
| `armin.ts` | 382 | (evaluate if needed) | Easter egg component |
| `custom-editor.ts` | 80 | `editor.go` (part of) | Base editor config |

### Theme System

| TS File | Lines | Go File | Notes |
|---------|-------|---------|-------|
| `theme/theme.ts` | 1143 | `theme/theme.go` + `theme/colors.go` + `theme/schema.go` | Split into 3 files |
| `theme/dark.json` | - | `theme/dark.json` | Embedded via `//go:embed` |
| `theme/light.json` | - | `theme/light.json` | Embedded via `//go:embed` |
| `theme/theme-schema.json` | - | `theme/schema.go` | Validation via Go struct tags |

---

## Architecture: Bubble Tea ↔ AgentSession Bridge

The critical design challenge is bridging the **imperative** agent event system (AgentSession.Subscribe) with Bubble Tea's **functional** message loop (Update). Here's the pattern:

```
┌─────────────────────────────────────────────────────┐
│                    Bubble Tea Program                │
│                                                      │
│  ┌──────────┐   tea.Msg    ┌──────────┐            │
│  │ tea.Cmd  │─────────────▶│ Update() │            │
│  │ (async)  │              │          │             │
│  └──────────┘              └────┬─────┘            │
│       ▲                         │                   │
│       │                         ▼                   │
│  ┌──────────┐             ┌──────────┐             │
│  │ Agent    │             │  View()  │             │
│  │ Event    │             │          │             │
│  │ Bridge   │             └──────────┘             │
│  └──────────┘                                       │
│       │                                             │
└───────┼─────────────────────────────────────────────┘
        │
        ▼
┌───────────────────┐
│  AgentSession     │  (existing, no changes)
│  .Subscribe()     │
└───────────────────┘
```

**Implementation:**

```go
// app_events.go — Bridge pattern
func subscribeToAgent(session *core.AgentSession, p *tea.Program) tea.Cmd {
    return func() tea.Msg {
        // Block until next event
        event, err := session.Subscribe()
        if err != nil {
            return agentEventError{err}
        }
        // Convert to typed message
        return agentEventMsg{event}
    }
}

// In Update():
case agentEventMsg:
    // Process event, update state, return next subscribe command
    cmd := handleAgentEvent(msg.event)
    return m, tea.Batch(cmd, subscribeToAgent(m.session, m.p))

// In Init():
return tea.Batch(
    subscribeToAgent(m.session, m.p),
    spinner.Tick,
)
```

This creates a **self-sustaining event loop**: each processed event re-subscribes for the next one.

---

## Message Types (app_types.go)

```go
// State machine for overlays
type OverlayState int
const (
    OverlayNone OverlayState = iota
    OverlayModelSelector
    OverlaySessionSelector
    OverlayThemeSelector
    OverlayThinkingSelector
    OverlaySettingsSelector
    OverlayLoginDialog
    OverlayOAuthSelector
    OverlayExtensionSelector
    OverlayConfigSelector
)

// Agent event messages
type agentEventMsg struct { event core.AgentSessionEvent }
type agentEventError struct { err error }

// Streaming messages
type streamStartMsg struct{}
type streamDeltaMsg struct { content string; blockType string }
type streamEndMsg struct { stopReason string; errorMsg string }

// Tool execution messages
type toolCallStartMsg struct { id, name string; args any }
type toolCallDeltaMsg struct { id string; args any }
type toolResultMsg struct { id string; content any; isError bool }

// Bash execution messages
type bashOutputMsg struct { toolCallID string; chunk string }
type bashCompleteMsg struct { toolCallID string; exitCode int; cancelled bool }

// Compaction/retry messages
type compactionStartMsg struct{}
type compactionEndMsg struct{}
type retryCountdownMsg struct { remaining time.Duration }
type retryAbortedMsg struct{}

// Editor messages
type submitMsg struct { text string }
type bashSubmitMsg struct { command string }

// Selector messages
type selectorDoneMsg struct { result any }
type selectorCancelledMsg struct{}

// Lifecycle messages
type initCompleteMsg struct{}
type shutdownMsg struct{}
```

---

## Component Design Pattern

Every component follows the **sub-model pattern**:

```go
// Example: footer.go
type Footer struct {
    session     *core.AgentSession
    footerData  *core.FooterDataProvider
    width       int
    theme       *theme.Theme
}

func NewFooter(session *core.AgentSession, data *core.FooterDataProvider, t *theme.Theme) Footer {
    return Footer{session: session, footerData: data, theme: t}
}

func (f Footer) Init() tea.Cmd { return nil }

func (f Footer) Update(msg tea.Msg) (Footer, tea.Cmd) {
    switch msg.(type) {
    case tea.WindowSizeMsg:
        f.width = msg.(tea.WindowSizeMsg).Width
    }
    return f, nil
}

func (f Footer) View() string {
    // Build 2-line footer: pwd line + stats line
    pwdLine := f.renderPwdLine()
    statsLine := f.renderStatsLine()
    return lipgloss.JoinVertical(lipgloss.Left, pwdLine, statsLine)
}
```

**Selector pattern** (reused by all picker components):

```go
// selector.go — Base fuzzy-search selector
type Selector[T any] struct {
    items       []T
    filtered    []T
    cursor      int
    search      textinput.Model
    width       int
    height      int
    filterFn    func(T, string) bool
    renderFn    func(T, bool) string
    onCancel    func() tea.Cmd
    onSelect    func(T) tea.Cmd
}
```

---

## Phased Implementation Plan

### Phase 14: Foundation (TUI Infrastructure)

**Goal:** Get a blank TUI running with the App shell, theme system, and agent bridge.

| Step | What | Files |
|------|------|-------|
| 14.1 | Install dependencies (`go get bubbletea, lipgloss, bubbles, glamour, huh, bubblezone, cobra`) | `go.mod` |
| 14.2 | Theme system: type, color utils, JSON loading, embedded themes | `tui/theme/*.go` + `dark.json` + `light.json` |
| 14.3 | Message types and state enums | `tui/app_types.go` |
| 14.4 | App struct + constructor (fields only, no logic yet) | `tui/app.go` |
| 14.5 | Minimal View() — just header + editor + footer placeholders | `tui/app_view.go` |
| 14.6 | Minimal Update() — quit on Ctrl+C, window resize | `tui/app_update.go` |
| 14.7 | Agent event bridge (subscribe loop) | `tui/app_events.go` |
| 14.8 | Header component (logo + version) | `tui/components/header.go` |
| 14.9 | Footer component (pwd, model name) | `tui/components/footer.go` |
| 14.10 | Entry point that wires App + AgentSession | `cmd/ycode/main.go` |

**Verify:** Run `go run cmd/ycode/main.go` → see header + empty chat + input + footer. Ctrl+C exits.

### Phase 15: Core Components

**Goal:** Render real messages, tool executions, and bash output.

| Step | What | Files |
|------|------|-------|
| 15.1 | Chat viewport (scrollable message list with auto-scroll) | `tui/components/chat.go` |
| 15.2 | Editor component (textarea with submit) | `tui/components/editor.go` |
| 15.3 | Assistant message component (markdown + thinking blocks) | `tui/components/assistant_message.go` |
| 15.4 | Markdown renderer (glamour wrapper with theme colors) | `tui/components/markdown.go` |
| 15.5 | User message component (styled bubble) | `tui/components/user_message.go` |
| 15.6 | Tool execution component (call + result + expand/collapse) | `tui/components/tool_execution.go` |
| 15.7 | Bash execution component (streaming output + borders) | `tui/components/bash_execution.go` |
| 15.8 | Diff renderer (added/removed/context lines) | `tui/components/diff.go` |
| 15.9 | Dynamic border + visual truncation | `tui/components/dynamic_border.go` + `visual_truncate.go` |
| 15.10 | Custom message + branch summary + compaction summary | `tui/components/custom_message.go` + `branch_summary.go` + `compaction_summary.go` |
| 15.11 | Full Update() — handle all agent events, streaming, tool calls | `tui/app_update.go` |
| 15.12 | Full View() — chat layout with real messages | `tui/app_view.go` |

**Verify:** Run app → send prompt → see assistant streaming response + tool executions + bash output.

### Phase 16: Selectors & Overlays

**Goal:** All picker/selector components working as overlays.

| Step | What | Files |
|------|------|-------|
| 16.1 | Base selector component (fuzzy search list) | `tui/components/selector.go` |
| 16.2 | Model selector | `tui/components/model_selector.go` |
| 16.3 | Session selector + tree selector | `tui/components/session_selector.go` + `tree_selector.go` |
| 16.4 | Theme selector | `tui/components/theme_selector.go` |
| 16.5 | Thinking level selector | `tui/components/thinking_selector.go` |
| 16.6 | Settings selector | `tui/components/settings_selector.go` |
| 16.7 | Scoped models selector | `tui/components/scoped_models_selector.go` |
| 16.8 | Login dialog (huh.Form) | `tui/components/login_dialog.go` |
| 16.9 | OAuth selector | `tui/components/oauth_selector.go` |
| 16.10 | Remaining selectors (config, images, user-message, extension) | Multiple files |
| 16.11 | Selector overlay management in App | `tui/app_selectors.go` |
| 16.12 | Bordered loader + countdown timer for compaction/retry | `bordered_loader.go` + `countdown_timer.go` |

**Verify:** Open model selector (Ctrl+M), search, select model → footer updates. Same for all selectors.

### Phase 17: Autocomplete + Editor Features

**Goal:** Full editor with slash command autocomplete, bash mode, image paste.

| Step | What | Files |
|------|------|-------|
| 17.1 | Autocomplete provider interface + combined provider | `tui/autocomplete/*.go` |
| 17.2 | Slash command autocomplete items | `tui/autocomplete/slash.go` |
| 17.3 | Autocomplete overlay in editor | `tui/components/editor.go` |
| 17.4 | Bash mode (lines starting with `!`) | `tui/components/editor.go` |
| 17.5 | Skill invocation message + keybinding hints | `skill_invocation.go` + `keybinding_hints.go` |
| 17.6 | Autocomplete wiring in App (commands, templates, extensions, skills) | `tui/app_autocomplete.go` |

**Verify:** Type `/` in editor → see autocomplete popup. Type `!ls` → bash mode.

### Phase 18: Extension UI + Advanced Features

**Goal:** Extension custom editors, inputs, selectors, widgets, custom header/footer.

| Step | What | Files |
|------|------|-------|
| 18.1 | Extension editor component | `tui/components/extension_editor.go` |
| 18.2 | Extension input component | `tui/components/extension_input.go` |
| 18.3 | Extension selector component | `tui/components/extension_selector.go` |
| 18.4 | Extension widget containers (above/below editor) | `tui/app_extensions.go` |
| 18.5 | Custom header/footer from extensions | `tui/app_extensions.go` |
| 18.6 | Full extension UI wiring in App | `tui/app_extensions.go` |

**Verify:** Extension provides custom editor → renders in input area.

### Phase 19: CLI + Entry Points + Config

**Goal:** Full CLI with argument parsing, config loading, mode selection.

| Step | What | Files |
|------|------|-------|
| 19.1 | Config package (paths, env vars, auth) | `src/config/config.go` |
| 19.2 | CLI args (flags, positional args, help) | `src/cli/args.go` |
| 19.3 | File processor (@file expansion, stdin) | `src/cli/file_processor.go` |
| 19.4 | Initial message builder | `src/cli/initial_message.go` |
| 19.5 | Model listing | `src/cli/list_models.go` |
| 19.6 | Session picker | `src/cli/session_picker.go` |
| 19.7 | Config selector | `src/cli/config_selector.go` |
| 19.8 | Main entry point (mode selection, runtime creation) | `cmd/ycode/main.go` |
| 19.9 | Migration system | `src/config/migrations.go` |

**Verify:** Run `ycode -p "hello"` → print mode. Run `ycode` → interactive mode.

### Phase 20: Non-Interactive Modes

**Goal:** Print mode and RPC mode working.

| Step | What | Files |
|------|------|-------|
| 20.1 | Print mode (text output + JSON event stream) | `src/modes/print_mode.go` |
| 20.2 | JSONL serialization | `src/modes/jsonl.go` |
| 20.3 | RPC types | `src/modes/rpc_types.go` |
| 20.4 | RPC mode (full JSONL protocol) | `src/modes/rpc_mode.go` |

**Verify:** `ycode -p "explain Go"` → text output. `ycode --mode rpc` → JSONL protocol.

---

## Key Design Decisions

### 1. App as Central Orchestrator (not God Object)
The TS `InteractiveMode` is 5475 lines in one class. We split it by **concern** into 9 files sharing one `App` struct. The `App` struct holds all state but delegates logic to focused files.

### 2. Agent Events → tea.Cmd Bridge
Agent events arrive asynchronously via `AgentSession.Subscribe()`. The bridge pattern wraps each event in a `tea.Msg` and re-subscribes after processing. This keeps all UI updates on the Bubble Tea main loop — no goroutine races.

### 3. Selectors as Overlays (not separate screens)
All selectors render as overlays on top of the main view. The `OverlayState` enum tracks which (if any) selector is active. When active, `Update()` routes keys to the selector and `View()` renders it on top.

### 4. Chat as Viewport (not custom scroll)
Use `bubbles/viewport` for the chat message list. Messages are pre-rendered as styled strings, concatenated, and set as viewport content. Auto-scroll to bottom on new messages, but allow manual scroll-up.

### 5. Theme as Lip Gloss Styles (not raw ANSI)
The theme system converts JSON colors to `lipgloss.AdaptiveColor` or `lipgloss.CompleteColor` (truecolor + 256 fallback). Components receive theme as a `*Theme` struct and use it for all styling — no raw ANSI codes in component code.

### 6. Editor as Custom Component (not just textarea)
The editor wraps `bubbles/textarea` but adds: autocomplete overlay, bash mode detection, image handling, submit handling. These are managed in the editor's own `Update()` method.

### 7. Config/CLI/Modes as Separate Packages
Following Go convention, `cli/`, `config/`, and `modes/` are separate packages imported by `main.go`. They don't depend on `tui/` (except `modes/print_mode.go` uses agent session directly).

---

## Existing Code to Reuse

| What | Go File | Used By |
|------|---------|---------|
| `AgentSession` | `core/agent_session.go` | App, all event handling |
| `AgentSessionRuntime` | `core/agent_session_runtime.go` | main.go (session replacement) |
| `SessionManager` | `core/session_manager.go` | Session selector, footer |
| `SettingsManager` | `core/settings_manager.go` | All settings, theme, editor config |
| `FooterDataProvider` | `core/footer_data_provider.go` | Footer component |
| `KeybindingsManager` | `core/keybindings.go` | App key handling |
| `BuiltinSlashCommands` | `core/slash_commands.go` | Autocomplete |
| `tools.CreateAllToolDefinitions` | `core/tools/` | Tool execution rendering |
| `tools/truncate` | `core/tools/truncate.go` | Bash output truncation |
| `CompactionSettings` | `core/compaction/` | Compaction UI |
| `ResourceLoader` | `core/resource_loader.go` | Themes, skills, prompt templates |
| `ModelRegistry` | `core/model_registry.go` | Model selector |
| `exporthtml` | `core/export-html/` | /export command |
| `intercept.go` (output-guard) | `core/intercept.go` | Print/RPC modes |
| `LLM types` | `packages/llm/src/` | All message rendering |

---

## Dependencies to Add

```
github.com/charmbracelet/bubbletea      # Core TUI
github.com/charmbracelet/bubbles        # Components (viewport, textarea, spinner, list, etc.)
github.com/charmbracelet/lipgloss       # Styling
github.com/charmbracelet/huh            # Forms (login dialog)
github.com/charmbracelet/glamour        # Markdown rendering
github.com/lrstanley/bubblezone         # Mouse zones
github.com/alecthomas/chroma            # Syntax highlighting (for diffs, code blocks)
github.com/muesli/termenv               # Terminal capability detection (transitive via lipgloss)
github.com/spf13/cobra                  # CLI argument parsing (confirmed choice)
```

---

## Estimated Scope

| Category | Files | Est. Lines |
|----------|-------|-----------|
| App core (9 files) | 9 | ~2,950 |
| Components (32 files) | 32 | ~4,350 |
| Theme (6 files) | 6 | ~900 |
| Autocomplete (3 files) | 3 | ~240 |
| CLI (6 files) | 6 | ~710 |
| Config (2 files) | 2 | ~815 |
| Modes (4 files) | 4 | ~1,234 |
| Entry point (1 file) | 1 | ~727 |
| **Total** | **~63** | **~11,926** |

(Actual will likely be higher due to Go verbosity vs TypeScript, but Bubble Tea's built-in components reduce custom rendering code.)

---

## Cobra CLI Structure

```
cmd/ycode/main.go
├── rootCmd        # ycode [flags] [prompt]  — default: interactive mode
├── versionCmd     # ycode --version / -v
├── helpCmd        # ycode --help / -h
└── flags on root:
    ├── -p, --print          # Print mode (non-interactive)
    ├── --mode string        # "interactive" | "print" | "json" | "rpc"
    ├── -m, --model string   # Model to use (provider/id)
    ├── --thinking string    # Thinking level
    ├── -s, --session string # Session ID to resume
    ├── -c, --config string  # Config file path
    ├── --continue           # Continue last session
    ├── --verbose            # Verbose startup
    ├── --no-input           # Skip stdin check
    ├── --allowedTools       # Restrict tool set
    ├── --append-system-prompt
    ├── --cwd string         # Working directory
    └── [prompt]             # Positional: initial prompt text
```

---

## Verification Plan

After each phase:

1. **Phase 14:** `go build ./cmd/ycode && ./ycode` → see blank TUI shell
2. **Phase 15:** Run interactive → send prompt → streaming response renders
3. **Phase 16:** Ctrl+M opens model selector → fuzzy search works → model switches
4. **Phase 17:** Type `/` → autocomplete appears → select command
5. **Phase 18:** Extension provides custom editor → renders correctly
6. **Phase 19:** `ycode -p "test"` → print mode works. `ycode --help` → shows help
7. **Phase 20:** `ycode --mode rpc` → JSONL protocol responds to commands

End-to-end test: full interactive session with multiple prompts, tool calls, bash execution, model switching, session forking, and clean exit.

---

## Scope: All Phases (14–20)

Implementation order: 14 → 15 → 16 → 17 → 18 → 19 → 20. Each phase builds on the previous and is independently verifiable. The goal is a **complete working application** across all modes (interactive, print, JSON, RPC) with full feature parity.
