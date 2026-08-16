package tui

import (
	"fmt"

	sdk "ycode/packages/agent-sdk/src"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// App is the central Bubble Tea model for the interactive TUI.
// It owns all UI state and delegates rendering to sub-components.
//
// The App struct is defined here with all fields. Logic is split across
// companion files by concern:
//   - app_types.go    — message types, state enums
//   - app_update.go   — Update() method
//   - app_view.go     — View() method
//   - app_events.go   — agent event → tea.Msg bridge
//   - app_init.go     — Init() method, startup sequence
//   - app_selectors.go — selector overlay management (Phase 16)
//   - app_autocomplete.go — autocomplete wiring (Phase 17)
//   - app_extensions.go   — extension UI (Phase 18)
type App struct {
	// Core dependencies
	runtimeHost *core.AgentSessionRuntime

	// Convenience accessors (set in constructor, updated on session replacement)
	session         *core.AgentSession
	sessionManager  *core.SessionManager
	settingsManager *core.SettingsManager

	// Theme
	theme *theme.Theme

	// Window dimensions
	width  int
	height int

	// Sub-components
	header components.Header
	footer components.Footer
	chat   components.Chat
	editor components.Editor
	spinner spinner.Model

	// State — Streaming
	isStreaming    bool
	streamingText  string
	workingMessage string
	workingVisible bool

	// State — Overlay
	overlay      OverlayState
	overlayModel overlayComponent // active selector/dialog model (nil when overlay == OverlayNone)

	// State — Extension UI (Phase 18.4)
	// Widget content rendered above/below the editor. Fed by extension widgets
	// once the SDK's ExtensionUIContext gains a SetWidget field (see app_extensions.go).
	widgetsAbove []extensionWidget
	widgetsBelow []extensionWidget

	// State — Input
	isBashMode bool

	// State — Key tracking
	keys keyTrackingState

	// State — Chat log
	// entries is the ordered list of rendered chat items (user messages,
	// assistant messages, tool calls, bash output, summaries, etc.).
	entries []chatEntry
	// streamingIdx is the index in entries of the currently-streaming
	// assistant message, or -1 when not streaming.
	streamingIdx int
	// toolComponents maps toolCallID → entry index for in-flight tool calls.
	toolComponents map[string]int
	// bashComponents maps toolCallID → bash execution component for live output.
	bashComponents map[string]*components.BashExecutionComponent

	// State — Compaction
	isCompacting          bool
	autoCompactionEnabled bool
	compactionQueued      []compactionQueuedMessage

	// State — Retry
	isRetrying       bool
	retryCountdownMs int

	// State — Thinking block
	hideThinkingBlock bool

	// State — Tool output
	toolOutputExpanded bool

	// State — Shutdown
	shutdownRequested bool

	// State — Startup
	isInitialized     bool
	startupNoticesShown bool

	// State — Session version
	version string

	// Unsubscribe function for agent events
	unsubscribe func()
	program     *tea.Program

	// Signal cleanup handlers
	signalCleanupHandlers []func()

	// Options passed at creation
	options AppOptions
}

// NewApp creates a new App model with the given runtime host and options.
func NewApp(runtimeHost *core.AgentSessionRuntime, options AppOptions) App {
	session := runtimeHost.Session()

	// Initialize theme
	theme.InitTheme("") // empty = auto-detect dark/light
	t := theme.GetTheme()

	app := App{
		runtimeHost:     runtimeHost,
		session:         session,
		sessionManager:  session.SessionManager,
		settingsManager: session.SettingsManager,
		options:         options,
		compactionQueued: make([]compactionQueuedMessage, 0),
		version:         "0.1.0", // TODO: wire from config.VERSION
		theme:           t,
		header:          components.NewHeader(t, "0.1.0"),
		footer:          components.NewFooter(session, core.NewFooterDataProvider(session.SessionManager.GetCwd()), t),
		chat:            components.NewChat(80, 20),
		editor:          components.NewEditor(t, 80),
		spinner:         spinner.New(),
		streamingIdx:    -1,
		toolComponents:  make(map[string]int),
		bashComponents:  make(map[string]*components.BashExecutionComponent),
	}

	// Load settings into state
	app.hideThinkingBlock = session.SettingsManager.GetHideThinkingBlock()
	app.autoCompactionEnabled = session.IsAutoCompactionEnabled()

	// Wire autocomplete (Phase 17.6) once the editor exists.
	app.setupAutocomplete()

	return app
}

// SetProgram stores a reference to the tea.Program so commands can send messages.
// It sets both the App field and the package-level globalProgram — the latter
// is what async goroutines (event bridge) actually use, since the running
// Program holds its own model copy where a.program is nil.
func (a *App) SetProgram(p *tea.Program) {
	a.program = p
	globalProgram = p
}

// ============================================================================
// Convenience Accessors
// ============================================================================

func (a *App) getSession() *core.AgentSession {
	return a.session
}

func (a *App) getAgent() sdk.AgentState {
	return a.session.Agent
}

func (a *App) getSessionManager() *core.SessionManager {
	return a.sessionManager
}

func (a *App) getSettingsManager() *core.SettingsManager {
	return a.settingsManager
}

func (a *App) GetTheme() *theme.Theme {
	return a.theme
}

// ============================================================================
// Session Replacement — called by AgentSessionRuntime after session swap
// ============================================================================

// RebindSession updates the app's references after a session replacement.
func (a *App) RebindSession(session *core.AgentSession) error {
	a.session = session
	a.sessionManager = session.SessionManager
	a.settingsManager = session.SettingsManager
	a.hideThinkingBlock = session.SettingsManager.GetHideThinkingBlock()
	a.autoCompactionEnabled = session.IsAutoCompactionEnabled()
	// Rebuild the autocomplete provider from the new session's sources.
	a.setupAutocomplete()
	return nil
}

// String returns a debug representation of the app state.
func (a *App) String() string {
	return fmt.Sprintf("App{streaming: %v, overlay: %d, width: %d, height: %d}",
		a.isStreaming, a.overlay, a.width, a.height)
}
