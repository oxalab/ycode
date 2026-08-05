package ycode

import (
	"context"
	"regexp"
	"time"
	"ycode/packages/agent-sdk/src"
	"ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// ============================================================================
// Phase 11.7: Retry Logic
//
// Migrated from reference/coding-agent/src/core/agent-session.ts lines 470-487, 2414-2527
//
// Retry handles transient errors (rate limits, overloaded, network) with
// exponential backoff. Context overflow errors are NOT retried — those
// go through compaction instead.
//
// Flow:
//   agent_end → createRetryPromise (if retryable) → waitForRetry (backoff) → agent.continue()
// ============================================================================

// retryablePattern matches transient errors that should be retried.
// Mirrors TS: _isRetryableError() regex pattern
var retryablePattern = regexp.MustCompile(
	`(?i)overloaded|provider.?returned.?error|rate.?limit|too many requests|429|500|502|503|504|` +
		`service.?unavailable|server.?error|internal.?error|network.?error|connection.?error|` +
		`connection.?refused|connection.?lost|websocket.?closed|websocket.?error|other side closed|` +
		`fetch failed|upstream.?connect|reset before headers|socket hang up|ended without|` +
		`http2 request did not get a response|timed? out|timeout|terminated|retry delay`,
)

// isRetryableError checks if an assistant message represents a retryable error.
// Context overflow errors are NOT retryable (handled by compaction instead).
func (s *AgentSession) isRetryableError(message llm.AssistantMessage) bool {
	if message.StopReason != llm.StopReasonError || message.ErrorMessage == "" {
		return false
	}

	// Context overflow is handled by compaction, not retry
	contextWindow := 0
	if model := s.GetModel(); model.ID != "" {
		contextWindow = model.ContextWindow
	}
	if utils.IsContextOverflow(message, contextWindow) {
		return false
	}

	return retryablePattern.MatchString(message.ErrorMessage)
}

// createRetryPromiseForAgentEnd checks if agent_end event has a retryable error
// and creates a retry promise if so. Called synchronously from handleAgentEvent.
// Mirrors TS: _createRetryPromiseForAgentEnd() lines 470-487
func (s *AgentSession) createRetryPromiseForAgentEnd(event sdk.AgentEvent) {
	if _, ok := event.(sdk.EventAgentEnd); !ok {
		return
	}

	settings := s.getRetrySettings()
	if !settings.Enabled {
		return
	}

	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	if s.retryChan != nil {
		return // already created
	}

	// Find last assistant message in event messages
	agentEnd, _ := event.(sdk.EventAgentEnd)
	lastAssistant := s.findLastAssistantInMessages(agentEnd.Messages)
	if lastAssistant == nil || !s.isRetryableError(*lastAssistant) {
		return
	}

	s.retryChan = make(chan struct{}, 1)
}

// handleRetryableError executes retry with exponential backoff.
// Returns true if retry was initiated, false if max retries exceeded or disabled.
// Mirrors TS: _handleRetryableError() lines 2432-2506
func (s *AgentSession) handleRetryableError(message llm.AssistantMessage) bool {
	settings := s.getRetrySettings()
	if !settings.Enabled {
		s.resolveRetry()
		return false
	}

	// Ensure retry channel exists (defensive fallback)
	s.retryMu.Lock()
	if s.retryChan == nil {
		s.retryChan = make(chan struct{}, 1)
	}
	s.retryMu.Unlock()

	s.retryMu.Lock()
	s.retryAttempt++
	s.retryMu.Unlock()

	if s.retryAttempt > settings.MaxRetries {
		// Max retries exceeded — emit final failure and reset
		s.emit(AgentSessionEvent{
			EventType:   "auto_retry_end",
			RetrySuccess: false,
			RetryAttempt: s.retryAttempt - 1,
			RetryError:  message.ErrorMessage,
		})
		s.retryMu.Lock()
		s.retryAttempt = 0
		s.retryMu.Unlock()
		s.resolveRetry()
		return false
	}

	delayMs := settings.BaseDelayMs
	for i := 1; i < s.retryAttempt; i++ {
		delayMs *= 2
	}

	s.emit(AgentSessionEvent{
		EventType:    "auto_retry_start",
		RetryAttempt: s.retryAttempt,
		RetryMaxAttempt: settings.MaxRetries,
		RetryDelayMs: delayMs,
		RetryError:   message.ErrorMessage,
	})

	// Remove error message from agent state (keep in session for history)
	messages := s.Agent.Messages()
	if len(messages) > 0 {
		if _, ok := messages[len(messages)-1].(llm.AssistantMessage); ok {
			s.Agent.SetMessages(messages[:len(messages)-1])
		}
	}

	// Wait with exponential backoff (abortable)
	go func() {
		select {
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
			// Retry via continue — use goroutine to break out of event handler chain
				s.Agent.Continue(context.Background())
		case <-s.retryChan:
			// Aborted during wait
			s.retryMu.Lock()
			attempt := s.retryAttempt
			s.retryAttempt = 0
			s.retryMu.Unlock()

			s.retryAbort = nil
			s.emit(AgentSessionEvent{
				EventType:   "auto_retry_end",
				RetrySuccess: false,
				RetryAttempt: attempt,
				RetryError:  "Retry cancelled",
			})
			s.resolveRetry()
		}
	}()

	return true
}

// AbortRetry cancels in-progress retry.
func (s *AgentSession) AbortRetry() {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	if s.retryAbort != nil {
		s.retryAbort()
	}
	s.resolveRetry()
}

// --- Helpers ---

// retrySettings holds computed retry settings.
type retrySettings struct {
	Enabled     bool
	MaxRetries  int
	BaseDelayMs int
}

// getRetrySettings reads retry settings from SettingsManager.
func (s *AgentSession) getRetrySettings() retrySettings {
	return retrySettings{
		Enabled:     s.SettingsManager.GetRetryEnabled(),
		MaxRetries:  s.SettingsManager.GetRetryMaxRetries(),
		BaseDelayMs: s.SettingsManager.GetRetryBaseDelayMs(),
	}
}

// findLastAssistantInMessages finds the last AssistantMessage in a message slice.
func (s *AgentSession) findLastAssistantInMessages(messages []sdk.AgentMessage) *llm.AssistantMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if am, ok := messages[i].(llm.AssistantMessage); ok {
			return &am
		}
	}
	return nil
}

/*
KEYNOTES:
 1. isRetryableError uses a single compiled regex instead of TS's inline regex.
    Pattern is identical — matches overloaded, rate limit, 5xx, network errors, etc.

 2. Context overflow is explicitly excluded — those go through compaction (Phase 11.5).

 3. Exponential backoff: delayMs = baseDelayMs * 2^(attempt-1).
    TS: settings.baseDelayMs * 2 ** (this._retryAttempt - 1)

 4. createRetryPromiseForAgentEnd is called synchronously from handleAgentEvent,
    BEFORE processAgentEvent runs. This ensures the retry promise exists when
    processAgentEvent calls handleRetryableError.

 5. The retry wait uses a goroutine with select on time.After vs retryChan.
    This mirrors TS's sleep() with AbortController signal.

 6. resolveRetry is defined in agent_session.go — sends to retryChan.
*/
