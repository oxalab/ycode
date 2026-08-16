package tui

import (
	"strings"
	"time"

	"ycode/packages/coding-agent/src/core/tui/components"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	llm "ycode/packages/llm/src"
)

// Update handles incoming messages and updates the App model accordingly.
// This is the central message router for the Bubble Tea Elm Architecture.
//
// Phase 15: full agent event handling — streaming, tool calls, bash output,
// compaction, retry, editor input, and key routing.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// When an overlay is active, it owns keyboard + tick messages. The App
	// still processes the result/cancel/preview messages overlays emit.
	if a.overlay != OverlayNone {
		switch m := msg.(type) {
		case selectorDoneMsg:
			return a.handleSelectorDone(m.result)
		case selectorCancelledMsg:
			a.closeOverlay()
			return a, nil
		case themePreviewMsg:
			return a.handleThemePreview(m.name)
		}
		// Forward everything else (KeyMsg, spinner/timer TickMsg) to the overlay.
		if model, cmd, ok := (&a).forwardOverlayMsg(msg); ok {
			return model, cmd
		}
	}

	switch msg := msg.(type) {

	// --- Window resize ---
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.header.SetWidth(msg.Width)
		a.footer.SetWidth(msg.Width)
		a.editor.SetWidth(msg.Width)
		// Size the chat to fit header(2) + footer(2) + the editor, reserving
		// rows for the autocomplete popup when it's open so the layout never
		// overflows the terminal. (Extension widget heights are still not
		// subtracted — widgets are empty today; see app_view.go note.)
		a.resizeChatForPopup()
		a.rebuildChat()
		if a.overlayModel != nil {
			a.overlayModel.SetWidth(a.overlayWidth())
		}
		return a, nil

	// --- Key events ---
	case tea.KeyMsg:
		return a.handleKeyMsg(msg)

	// --- Spinner tick ---
	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd

	// --- Agent event bridge messages ---
	case agentEventMsg:
		// Dispatch the raw agent event into a typed tea.Msg, then process it.
		cmd := handleAgentEvent(msg.event)
		if cmd != nil {
			result := cmd()
			if result != nil {
				return a.Update(result)
			}
		}
		return a, nil

	case agentEventError:
		// Bridge error — surface as an error entry in the chat.
		a.addStaticEntry(a.theme.Fg("error", "Event bridge error: "+msg.err.Error()))
		return a, nil

	// --- Session events ---
	case sessionEventMsg:
		cmd := handleSessionEvent(msg)
		if cmd != nil {
			result := cmd()
			if result != nil {
				return a.Update(result)
			}
		}
		return a, nil

	// --- Streaming messages ---
	case streamStartMsg:
		return a.handleStreamStart(msg), nil
	case streamUpdateMsg:
		return a.handleStreamUpdate(msg), nil
	case streamEndMsg:
		return a.handleStreamEnd(msg), nil

	// --- Tool execution messages ---
	case toolCallStartMsg:
		return a.handleToolCallStart(msg), nil
	case toolCallUpdateMsg:
		return a.handleToolCallUpdate(msg), nil
	case toolCallEndMsg:
		return a.handleToolCallEnd(msg), nil

	// --- Bash messages ---
	case bashOutputMsg:
		return a.handleBashOutput(msg), nil
	case bashCompleteMsg:
		return a.handleBashComplete(msg), nil

	// --- Compaction messages ---
	case compactionStartMsg:
		a.isCompacting = true
		a.workingMessage = "Compacting context..."
		a.workingVisible = true
		return a, nil
	case compactionEndMsg:
		a.isCompacting = false
		a.workingVisible = false
		return a, nil

	// --- Retry messages ---
	case retryStartMsg:
		a.isRetrying = true
		a.retryCountdownMs = msg.delayMs
		a.workingMessage = "Retrying..."
		a.workingVisible = true
		return a, nil
	case retryEndMsg:
		a.isRetrying = false
		a.workingVisible = false
		return a, nil
	case retryCountdownTickMsg:
		a.retryCountdownMs = int(msg.remaining / time.Millisecond)
		return a, nil
	case retryAbortedMsg:
		a.isRetrying = false
		a.workingVisible = false
		return a, nil

	// --- Editor messages ---
	case components.SubmitMsg:
		return a.handleSubmitSubmit(msg.Text)
	case components.BashSubmitMsg:
		return a.handleSubmitBash(msg.Command, msg.ExcludedFromContext)
	case submitMsg:
		// Legacy internal submit msg — delegate.
		if strings.TrimSpace(msg.text) != "" {
			return a.handleSubmitSubmit(msg.text)
		}
		return a, nil
	case bashSubmitMsg:
		if strings.TrimSpace(msg.command) != "" {
			return a.handleSubmitBash(msg.command, false)
		}
		return a, nil

	// --- Selector messages (Phase 16) ---
	case selectorDoneMsg:
		return a, nil
	case selectorCancelledMsg:
		return a, nil

	// --- Lifecycle messages ---
	case initCompleteMsg:
		a.isInitialized = true
		return a, nil

	case shutdownMsg:
		a.shutdownRequested = true
		return a, tea.Quit

	// --- Theme change ---
	case themeChangedMsg:
		a.theme = msg.theme
		return a, nil

	// --- Slash-command async results (export/import/clone/new/copy/reload) ---
	case commandResultMsg:
		style := a.theme.Fg("muted", msg.message)
		if !msg.ok {
			style = a.theme.Fg("error", msg.message)
		}
		a.addStaticEntry(style)
		return a, nil
	}

	return a, nil
}

// ============================================================================
// Key handling
// ============================================================================

// handleKeyMsg processes keyboard input.
func (a App) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()

	// Ctrl+C: double-tap within 1s to force quit; single tap aborts streaming.
	if keyStr == "ctrl+c" {
		now := time.Now()
		if now.Sub(a.keys.lastCtrlCTime) < time.Second {
			return a, tea.Quit
		}
		a.keys.lastCtrlCTime = now
		if a.isStreaming {
			a.isStreaming = false
			a.workingVisible = false
			if a.session != nil && a.session.Agent != nil {
				a.session.Agent.Abort()
			}
			return a, nil
		}
		return a, tea.Quit
	}

	// Escape: cancel overlay or current operation; double-tap to quit.
	if keyStr == "escape" {
		now := time.Now()
		if a.overlay != OverlayNone {
			a.closeOverlay()
			return a, nil
		}
		if now.Sub(a.keys.lastEscapeTime) < time.Second {
			return a, tea.Quit
		}
		a.keys.lastEscapeTime = now
		// During compaction, Escape cancels it.
		if a.isCompacting {
			a.session.AbortCompaction()
			return a, nil
		}
		return a, nil
	}

	// Overlay-opening key bindings — only when no overlay is active and the
	// editor holds focus. Provisional Phase 16 bindings; slash commands
	// (/model, /theme, …) supersede these in Phase 17 and the keybindings
	// system reconciles them in Phase 19.
	if a.overlay == OverlayNone && !a.isStreaming {
		switch keyStr {
		case "ctrl+l", "ctrl+m": // ctrl+l = TS app.model.select; ctrl+m = plan mnemonic
			return a.openModelSelector()
		case "ctrl+s": // plan mnemonic: session selector
			return a.openSessionSelector()
		case "ctrl+e": // Settings (nests theme + thinking + warnings submenus)
			return a.openSettingsSelector()
		case "ctrl+k": // scoped-models (Ctrl+P cycling list)
			return a.openScopedModelsSelector()
		case "ctrl+i": // show images
			return a.openShowImagesSelector()
		case "ctrl+u": // fork from message
			return a.openUserMessageSelector()
		case "ctrl+b": // resource configuration
			return a.openConfigSelector()
		case "ctrl+y": // login / OAuth provider picker
			return a.openOAuthSelector(components.OAuthModeLogin)
		}
	}

	// Scroll keys route to the chat viewport when not focused the editor input.
	switch keyStr {
	case "pgup":
		a.chat.Update(msg)
		return a, nil
	case "pgdown":
		a.chat.Update(msg)
		return a, nil
	}

	// Route everything else to the editor.
	var cmd tea.Cmd
	a.editor, cmd = a.editor.Update(msg)
	// The editor may have just opened/closed the autocomplete popup — resize the
	// chat so the popup's rows are reserved and nothing overflows the terminal.
	a.resizeChatForPopup()
	return a, cmd
}

// resizeChatForPopup sizes the chat viewport for the current layout, reserving
// rows for the autocomplete popup when it is open (the popup renders below the
// editor, which is docked at the bottom of the screen). Without this the popup
// pushes the total layout past the terminal height and its lower rows — where
// the highlight lands while scrolling — get clipped, so the ▸ vanishes.
func (a *App) resizeChatForPopup() {
	if a.height == 0 {
		return
	}
	used := 2 + 2 + a.editor.Height() // header + footer + editor (bordered)
	if a.overlay == OverlayNone {
		used += a.editor.PopupHeight()
	}
	chatHeight := a.height - used
	if chatHeight < 3 {
		chatHeight = 3
	}
	a.chat.SetSize(a.width, chatHeight)
}

// ============================================================================
// Streaming handlers
// ============================================================================

// handleStreamStart begins a new assistant message in the chat.
func (a *App) handleStreamStart(msg streamStartMsg) App {
	assistantMsg, ok := msg.message.(llm.AssistantMessage)
	if !ok {
		return *a
	}
	if assistantMsg.Role != "assistant" {
		return *a
	}

	a.isStreaming = true
	a.workingMessage = ""
	a.workingVisible = false // assistant content is now visible

	comp := components.NewAssistantMessageComponent(
		a.theme,
		a.hideThinkingBlock,
		"Thinking...",
	)
	comp.UpdateContent(assistantMsg)
	a.addAssistantEntry(comp)
	return *a
}

// handleStreamUpdate applies a streaming delta to the current assistant message.
func (a *App) handleStreamUpdate(msg streamUpdateMsg) App {
	assistantMsg, ok := msg.message.(llm.AssistantMessage)
	if !ok {
		return *a
	}
	if a.streamingIdx < 0 || a.streamingIdx >= len(a.entries) {
		return *a
	}

	comp, ok := a.entries[a.streamingIdx].item.(*components.AssistantMessageComponent)
	if !ok {
		return *a
	}
	comp.UpdateContent(assistantMsg)

	// Create tool-call components for any tool calls in the streaming message.
	for _, block := range assistantMsg.Content {
		if tc, isTool := block.(llm.ToolCall); isTool {
			if _, exists := a.toolComponents[tc.ID]; !exists {
				toolComp := a.newToolComponent(tc.Name, tc.ID, tc.Arguments)
				a.addToolEntry(tc.ID, toolComp)
			} else if existing := a.toolEntry(tc.ID); existing != nil {
				existing.UpdateArgs(tc.Arguments)
			}
		}
	}

	a.rebuildChat()
	return *a
}

// handleStreamEnd finalizes the streaming assistant message.
func (a *App) handleStreamEnd(msg streamEndMsg) App {
	assistantMsg, ok := msg.message.(llm.AssistantMessage)
	if ok && a.streamingIdx >= 0 && a.streamingIdx < len(a.entries) {
		if comp, ok := a.entries[a.streamingIdx].item.(*components.AssistantMessageComponent); ok {
			comp.UpdateContent(assistantMsg)
		}
	}

	// On abort/error, mark all in-flight tool calls as errored.
	if ok && (assistantMsg.StopReason == llm.StopReasonAborted || assistantMsg.StopReason == llm.StopReasonError) {
		errMsg := "Operation aborted"
		if assistantMsg.ErrorMessage != "" && assistantMsg.ErrorMessage != "Request was aborted" {
			errMsg = assistantMsg.ErrorMessage
		}
		for id := range a.toolComponents {
			if tc := a.toolEntry(id); tc != nil {
				tc.UpdateResult([]any{map[string]string{"type": "text", "text": errMsg}}, nil, true, false)
			}
		}
	} else {
		// Mark args complete for all pending tool calls.
		for id := range a.toolComponents {
			if tc := a.toolEntry(id); tc != nil {
				tc.SetArgsComplete()
			}
		}
	}

	a.rebuildChat()
	a.finishStreaming()
	return *a
}

// ============================================================================
// Tool execution handlers
// ============================================================================

// handleToolCallStart creates or marks a tool execution component.
func (a *App) handleToolCallStart(msg toolCallStartMsg) App {
	tc := a.toolEntry(msg.toolCallID)
	if tc == nil {
		newComp := a.newToolComponent(msg.toolName, msg.toolCallID, msg.args)
		a.addToolEntry(msg.toolCallID, newComp)
	} else {
		tc.MarkExecutionStarted()
	}
	return *a
}

// handleToolCallUpdate applies a partial tool result.
func (a *App) handleToolCallUpdate(msg toolCallUpdateMsg) App {
	tc := a.toolEntry(msg.toolCallID)
	if tc != nil {
		// partialResult may carry partial content; mark as partial.
		content := extractResultContent(msg.partialResult)
		tc.UpdateResult(content, msg.partialResult, false, true)
		a.rebuildChat()
	}
	return *a
}

// handleToolCallEnd finalizes a tool execution.
func (a *App) handleToolCallEnd(msg toolCallEndMsg) App {
	tc := a.toolEntry(msg.toolCallID)
	if tc != nil {
		content := extractResultContent(msg.result)
		tc.UpdateResult(content, msg.result, msg.isError, false)
		a.removeToolEntry(msg.toolCallID)
		a.rebuildChat()
	}
	return *a
}

// ============================================================================
// Bash handlers
// ============================================================================

// bashComponents maps toolCallID → bash execution component for live output.
// (Stored as a field would be cleaner; kept local to these handlers via a
// package-level map indexed by tool call id.)
//
// handleBashOutput appends a streaming chunk to the bash output.
func (a *App) handleBashOutput(msg bashOutputMsg) App {
	be := a.bashComponent(msg.toolCallID)
	if be != nil {
		be.AppendOutput(msg.chunk)
		a.rebuildChat()
	}
	return *a
}

// handleBashComplete marks a bash command finished.
func (a *App) handleBashComplete(msg bashCompleteMsg) App {
	be := a.bashComponent(msg.toolCallID)
	if be != nil {
		exitCode := msg.exitCode
		be.SetComplete(&exitCode, msg.cancelled, nil, "")
		a.rebuildChat()
	}
	return *a
}

// ============================================================================
// Submit dispatch
// ============================================================================

// handleSubmitSubmit handles a normal prompt submit from the editor.
func (a App) handleSubmitSubmit(text string) (tea.Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return a, nil
	}
	// Slash commands: route to the dispatcher. An unrecognized "/foo" is
	// reported as "Unknown command" rather than sent as a prompt (TS parity).
	if strings.HasPrefix(text, "/") {
		model, cmd, handled := a.handleSlashCommand(text)
		if handled {
			return model, cmd
		}
		a.addStaticEntry(a.theme.Fg("error", "Unknown command: "+text))
		return a, nil
	}
	a.addUserText(text)
	a.isStreaming = true
	a.workingMessage = "Thinking..."
	a.workingVisible = true
	go func() {
		_ = a.session.Prompt(text, nil)
	}()
	return a, nil
}

// handleSubmitBash handles a bash command submit (! or !! prefix).
func (a App) handleSubmitBash(command string, excludedFromContext bool) (tea.Model, tea.Cmd) {
	command = strings.TrimSpace(command)
	if command == "" {
		return a, nil
	}
	// Phase 15/17.5: render the bash command as a static entry; full execution
	// wiring (streaming output) is handled via tool events when the agent invokes
	// the bash tool. A user-initiated bash shortcut is deferred. excludedFromContext
	// (the "!!" form) will keep the output out of the agent context once execution
	// is wired.
	rendered := "$ " + command
	if excludedFromContext {
		rendered += "  (excluded from context)"
	}
	a.addStaticEntry(a.theme.Fg("muted", rendered))
	return a, nil
}

// ============================================================================
// Helpers
// ============================================================================

// extractResultContent normalizes a tool result (any) into a []any content slice
// suitable for ToolExecutionComponent.UpdateResult.
func extractResultContent(result any) []any {
	if result == nil {
		return nil
	}
	switch r := result.(type) {
	case []any:
		return r
	case map[string]any:
		if content, ok := r["content"].([]any); ok {
			return content
		}
		return []any{r}
	case []llm.ContentBlock:
		out := make([]any, len(r))
		for i, b := range r {
			out[i] = b
		}
		return out
	default:
		return []any{map[string]any{"type": "text", "text": toString(result)}}
	}
}

// toString is a best-effort string conversion for fallback rendering.
func toString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	}
	return ""
}
