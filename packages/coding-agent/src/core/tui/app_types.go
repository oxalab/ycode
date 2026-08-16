package tui

import (
	"context"
	"time"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// ============================================================================
// Overlay State
// ============================================================================

// OverlayState tracks which selector/overlay is currently active.
type OverlayState int

const (
	OverlayNone OverlayState = iota
	OverlayModelSelector
	OverlaySessionSelector
	OverlaySessionSearch
	OverlayThemeSelector
	OverlayThinkingSelector
	OverlaySettingsSelector
	OverlayScopedModelsSelector
	OverlayConfigSelector
	OverlayShowImagesSelector
	OverlayUserMessageSelector
	OverlayExtensionSelector
	OverlayLoginDialog
	OverlayOAuthSelector
	OverlayTreeSelector
)

// ============================================================================
// App Mode
// ============================================================================

// AppMode determines the overall application mode.
type AppMode int

const (
	AppModeInteractive AppMode = iota
	AppModePrint
	AppModePrintJSON
	AppModeRPC
)

// ============================================================================
// Agent Event Messages — bridge from AgentSession.Subscribe to tea.Msg
// ============================================================================

// agentEventMsg wraps an sdk.AgentEvent for the Bubble Tea message loop.
type agentEventMsg struct {
	event sdk.AgentEvent
}

// agentEventError wraps an error from the agent event subscription.
type agentEventError struct {
	err error
}

// sessionEventMsg wraps an AgentSessionEvent (session-level events like
// compaction, retry, queue updates) for the Bubble Tea message loop.
type sessionEventMsg struct {
	eventType string
	// Compaction fields
	compactionReason  string
	compactionAborted bool
	compactionError   string
	// Retry fields
	retryAttempt    int
	retryMaxAttempt int
	retryDelayMs    int
	retryError      string
	retrySuccess    bool
	// Queue fields
	steering []string
	followUp []string
}

// ============================================================================
// Streaming Messages
// ============================================================================

// streamStartMsg is sent when a new assistant message starts streaming.
type streamStartMsg struct {
	message sdk.AgentMessage
}

// streamUpdateMsg is sent on each streaming delta.
type streamUpdateMsg struct {
	message           sdk.AgentMessage
	assistantMsgEvent any // llm.AssistantMessageEvent
}

// streamEndMsg is sent when streaming completes.
type streamEndMsg struct {
	message sdk.AgentMessage
}

// ============================================================================
// Tool Execution Messages
// ============================================================================

// toolCallStartMsg is sent when a tool begins executing.
type toolCallStartMsg struct {
	toolCallID string
	toolName   string
	args       any
}

// toolCallUpdateMsg is sent on partial tool execution updates.
type toolCallUpdateMsg struct {
	toolCallID    string
	toolName      string
	args          any
	partialResult any
}

// toolCallEndMsg is sent when a tool finishes executing.
type toolCallEndMsg struct {
	toolCallID string
	toolName   string
	result     any
	isError    bool
}

// ============================================================================
// Bash Execution Messages
// ============================================================================

// bashOutputMsg carries a chunk of bash command output.
type bashOutputMsg struct {
	toolCallID string
	chunk      string
}

// bashCompleteMsg is sent when a bash command finishes.
type bashCompleteMsg struct {
	toolCallID string
	exitCode   int
	cancelled  bool
}

// ============================================================================
// Compaction Messages
// ============================================================================

// compactionStartMsg is sent when compaction begins.
type compactionStartMsg struct {
	reason string
}

// compactionEndMsg is sent when compaction finishes.
type compactionEndMsg struct {
	aborted   bool
	willRetry bool
	err       string
}

// ============================================================================
// Retry Messages
// ============================================================================

// retryStartMsg is sent when auto-retry begins.
type retryStartMsg struct {
	attempt    int
	maxAttempt int
	delayMs    int
	err        string
}

// retryEndMsg is sent when auto-retry ends.
type retryEndMsg struct {
	success bool
}

// retryCountdownTickMsg is sent on each countdown tick during retry delay.
type retryCountdownTickMsg struct {
	remaining time.Duration
}

// retryAbortedMsg is sent when the user aborts a pending retry.
type retryAbortedMsg struct{}

// ============================================================================
// Editor Messages
// ============================================================================

// submitMsg is sent when the user submits the editor input.
type submitMsg struct {
	text string
}

// bashSubmitMsg is sent when the user submits a bash command (! prefix).
type bashSubmitMsg struct {
	command string
}

// ============================================================================
// Selector Messages
// ============================================================================

// selectorDoneMsg is sent when a selector overlay completes with a selection.
type selectorDoneMsg struct {
	result any
}

// selectorCancelledMsg is sent when a selector overlay is cancelled.
type selectorCancelledMsg struct{}

// ============================================================================
// Lifecycle Messages
// ============================================================================

// initCompleteMsg is sent when the app initialization finishes.
type initCompleteMsg struct{}

// shutdownMsg is sent to trigger a clean shutdown.
type shutdownMsg struct{}

// windowSizeMsg is a convenience wrapper for tea.WindowSizeMsg.
type windowSizeMsg struct {
	width  int
	height int
}

// ============================================================================
// Theme Messages
// ============================================================================

// themeChangedMsg is sent when the active theme changes.
type themeChangedMsg struct {
	theme *theme.Theme
}

// ============================================================================
// Key Tracking State
// ============================================================================

// keyTrackingState tracks consecutive key presses for double-tap detection
// (e.g., double Ctrl+C to force quit, double Escape to cancel).
type keyTrackingState struct {
	lastCtrlCTime  time.Time
	lastEscapeTime time.Time
}

// ============================================================================
// App Options
// ============================================================================

// AppOptions contains options for creating the interactive App.
type AppOptions struct {
	// Providers that were migrated to auth.json (shows warning)
	MigratedProviders []string
	// Warning message if session model couldn't be restored
	ModelFallbackMessage string
	// Initial message to send on startup
	InitialMessage string
	// Images to attach to the initial message
	InitialImages []any
	// Additional messages to send after the initial message
	InitialMessages []string
	// Force verbose startup (overrides quietStartup setting)
	Verbose bool
}

// ============================================================================
// Compaction Queue
// ============================================================================

// compactionQueuedMessage represents a message queued during compaction.
type compactionQueuedMessage struct {
	text string
	mode string // "steer" or "followUp"
}

// ============================================================================
// Context helper
// ============================================================================

// subscribeCmd creates a tea.Cmd that blocks until the next agent event,
// bridging the imperative agent event system into the Bubble Tea loop.
type subscribeCmd struct {
	agent    sdk.AgentState
	listener func(event sdk.AgentEvent, ctx context.Context) error
}

// Execute runs the subscription and returns a tea.Msg.
func (s subscribeCmd) Execute(ctx context.Context) tea.Msg {
	// This is a placeholder — the actual implementation is in app_events.go
	// where we use a channel-based subscription bridge.
	return nil
}
