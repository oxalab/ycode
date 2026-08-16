package tui

import (
	"context"
	"fmt"

	sdk "ycode/packages/agent-sdk/src"
	core "ycode/packages/coding-agent/src/core"

	tea "github.com/charmbracelet/bubbletea"
)

// ============================================================================
// Agent Event → tea.Msg Bridge
// ============================================================================
//
// The existing AgentSession uses a synchronous callback-based event system:
//
//	session.Subscribe(func(event AgentSessionEvent) { ... })
//
// Bubble Tea requires all state changes to flow through tea.Msg in the
// Update() loop. The bridge works as follows:
//
//  1. startEventBridge() subscribes to both the sdk.Agent (core events like
//     streaming, tool calls) and the AgentSession (session events like
//     compaction, retry, queue updates).
//
//  2. Each callback calls program.Send() to inject the event as a tea.Msg.
//
//  3. Update() receives the typed message and updates state.
//
// This avoids goroutine races — all UI state mutations happen in the
// single-threaded Bubble Tea Update loop.

// startEventBridge subscribes to agent and session events for the given
// session, forwarding them into the Bubble Tea message loop via the
// package-level globalProgram. Returns an unsubscribe function to call on
// shutdown.
//
// It is standalone (not a method on App) so it can be invoked from a command
// captured at Init time without relying on mutable model state.
func startEventBridge(session *core.AgentSession) func() {
	var unsubSession func()
	var unsubAgent func()

	// Subscribe to AgentSession-level events (compaction, retry, queue)
	if session != nil {
		unsubSession = session.Subscribe(func(event core.AgentSessionEvent) {
			sendToProgram(sessionEventMsg{
				eventType:         event.EventType,
				compactionReason:  string(event.CompactionReason),
				compactionAborted: event.CompactionAborted,
				compactionError:   event.ComapctionError,
				retryAttempt:      event.RetryAttempt,
				retryMaxAttempt:   event.RetryMaxAttempt,
				retryDelayMs:      event.RetryDelayMs,
				retryError:        event.RetryError,
				retrySuccess:      event.RetrySuccess,
				steering:          event.Steering,
				followUp:          event.FollowUp,
			})
		})
	}

	// Subscribe to core agent events (streaming, tool calls, turn lifecycle)
	if session != nil && session.Agent != nil {
		unsubAgent = session.Agent.Subscribe(func(event sdk.AgentEvent, ctx context.Context) error {
			sendToProgram(agentEventMsg{event: event})
			return nil
		})
	}

	return func() {
		if unsubSession != nil {
			unsubSession()
		}
		if unsubAgent != nil {
			unsubAgent()
		}
	}
}

// ============================================================================
// Agent Event Dispatcher
// ============================================================================

// handleAgentEvent converts an sdk.AgentEvent into the appropriate
// typed tea.Msg and returns it for processing in Update().
//
// Phase 14: basic type switch stub that logs and returns nil.
// Phase 15 will expand this to return proper streaming/tool/bash messages.
func handleAgentEvent(event sdk.AgentEvent) tea.Cmd {
	switch event.(type) {

	case sdk.EventAgentStart:
		return func() tea.Msg { return streamStartMsg{} }

	case sdk.EventAgentEnd:
		return func() tea.Msg { return streamEndMsg{} }

	case sdk.EventTurnStart:
		// New turn begins — will be expanded in Phase 15
		return nil

	case sdk.EventTurnEnd:
		// Turn complete — will be expanded in Phase 15
		return nil

	case sdk.EventMessageStart:
		e := event.(sdk.EventMessageStart)
		return func() tea.Msg { return streamStartMsg{message: e.Message} }

	case sdk.EventMessageUpdate:
		e := event.(sdk.EventMessageUpdate)
		return func() tea.Msg {
			return streamUpdateMsg{
				message:           e.Message,
				assistantMsgEvent: e.AssistantMessageEvent,
			}
		}

	case sdk.EventMessageEnd:
		e := event.(sdk.EventMessageEnd)
		return func() tea.Msg { return streamEndMsg{message: e.Message} }

	case sdk.EventToolExecutionStart:
		e := event.(sdk.EventToolExecutionStart)
		return func() tea.Msg {
			return toolCallStartMsg{
				toolCallID: e.ToolCallID,
				toolName:   e.ToolName,
				args:       e.Args,
			}
		}

	case sdk.EventToolExecutionUpdate:
		e := event.(sdk.EventToolExecutionUpdate)
		return func() tea.Msg {
			return toolCallUpdateMsg{
				toolCallID:    e.ToolCallID,
				toolName:      e.ToolName,
				args:          e.Args,
				partialResult: e.PartialResult,
			}
		}

	case sdk.EventToolExecutionEnd:
		e := event.(sdk.EventToolExecutionEnd)
		return func() tea.Msg {
			return toolCallEndMsg{
				toolCallID: e.ToolCallID,
				toolName:   e.ToolName,
				result:     e.Result,
				isError:    e.IsError,
			}
		}

	default:
		// Unknown event type — ignore
		return nil
	}
}

// ============================================================================
// Session Event Dispatcher
// ============================================================================

// handleSessionEvent processes AgentSessionEvents (compaction, retry, queue)
// and returns appropriate tea.Cmd values.
//
// Phase 14: stub returning nil for all events.
// Phase 15 will wire compaction/retry UI states.
func handleSessionEvent(msg sessionEventMsg) tea.Cmd {
	switch msg.eventType {

	case "compaction_start":
		return func() tea.Msg {
			return compactionStartMsg{reason: msg.compactionReason}
		}

	case "compaction_end":
		return func() tea.Msg {
			return compactionEndMsg{
				aborted:   msg.compactionAborted,
				willRetry: false, // not available in current event
				err:       msg.compactionError,
			}
		}

	case "auto_retry_start":
		return func() tea.Msg {
			return retryStartMsg{
				attempt:    msg.retryAttempt,
				maxAttempt: msg.retryMaxAttempt,
				delayMs:    msg.retryDelayMs,
				err:        msg.retryError,
			}
		}

	case "auto_retry_end":
		return func() tea.Msg {
			return retryEndMsg{success: msg.retrySuccess}
		}

	case "queue_update":
		// Queue update — will be expanded in Phase 15 to show pending messages
		return nil

	case "session_info_changed":
		return nil

	case "thinking_level_changed":
		return nil

	default:
		return nil
	}
}

// ============================================================================
// Helper: bridge error message
// ============================================================================

// bridgeErrorCmd creates a tea.Cmd that returns an error message.
// Used when the event bridge encounters an unexpected error.
func bridgeErrorCmd(err error) tea.Cmd {
	return func() tea.Msg {
		return agentEventError{err: fmt.Errorf("event bridge: %w", err)}
	}
}

// Ensure context.Context is available for the Subscribe callback.
var _ context.Context = nil
