package ycode

import (
	"context"
	"time"
	"ycode/packages/coding-agent/src/core/tools"
	"ycode/packages/llm/src"
)

// ============================================================================
// Phase 11.8: Bash Execution in Session Context
//
// Migrated from reference/coding-agent/src/core/agent-session.ts lines 2559-2652
//
// Provides session-scoped bash execution with:
//   - Shell prefix/path from settings
//   - Abort support
//   - Deferred message recording (waits for streaming to finish)
//   - Flush of pending messages after agent turn
//
// NOTE: BashExecutionMessage is defined in messages.go. It is NOT an llm.Message
// (no IsMessage()). It gets persisted to session via AppendMessage() as a custom
// entry, and converted to llm.UserMessage via ConvertToLlm() when building context.
// ============================================================================

// BashRecordOptions configures how a bash result is recorded.
type BashRecordOptions struct {
	ExcludeFromContext bool
}

// ExecuteBash runs a bash command in the session's cwd with abort support.
// Mirrors TS: executeBash() lines 2559-2587
func (s *AgentSession) ExecuteBash(command string, onChunk func(chunk string)) (*BashResult, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s.bashAbort = cancel

	// Apply command prefix if configured
	prefix := s.SettingsManager.GetShellCommandPrefix()
	shellPath := s.SettingsManager.GetShellPath()
	resolvedCommand := command
	if prefix != "" {
		resolvedCommand = prefix + "\n" + command
	}

	result, err := ExecuteBashWithOperations(
		resolvedCommand,
		s.SessionManager.GetCwd(),
		tools.CreateLocalBashOps(&tools.BashToolOptions{ShellPath: &shellPath}),
		&BashExecutorOptions{
			OnChunk: onChunk,
			Ctx:     ctx,
		},
	)
	s.bashAbort = nil

	if err != nil {
		return nil, err
	}

	// Record in session history
	s.recordBashResult(command, result, nil)
	return result, nil
}

// RecordBashResult records a bash execution result in session history.
// Used by ExecuteBash and by extensions that handle bash execution themselves.
// Mirrors TS: recordBashResult() lines 2593-2617
func (s *AgentSession) recordBashResult(command string, result *BashResult, options *BashRecordOptions) {
	bashMessage := BashExecutionMessage{
		Command:        command,
		Output:         result.Output,
		ExitCode:       result.ExitCode,
		Cancelled:      result.Cancelled,
		Truncated:      result.Truncated,
		FullOutputPath: result.FullOutputPath,
		Timestamp:      time.Now().UnixMilli(),
	}
	if options != nil {
		bashMessage.ExcludeFromContext = options.ExcludeFromContext
	}

	// If agent is streaming, defer adding to avoid breaking tool_use/tool_result ordering
	if s.IsStreaming() {
		s.pendingBashMessages = append(s.pendingBashMessages, bashMessage)
	} else {
		// Save to session as custom entry (BashExecutionMessage is not llm.Message)
		s.SessionManager.AppendMessage(bashMessage)
	}
}

// AbortBash cancels the running bash command.
func (s *AgentSession) AbortBash() {
	if s.bashAbort != nil {
		s.bashAbort()
	}
}

// IsBashRunning returns whether a bash command is currently running.
func (s *AgentSession) IsBashRunning() bool {
	return s.bashAbort != nil
}

// HasPendingBashMessages returns whether there are pending bash messages waiting to be flushed.
func (s *AgentSession) HasPendingBashMessages() bool {
	return len(s.pendingBashMessages) > 0
}

// flushPendingBashMessages flushes pending bash messages to session.
// Called after agent turn completes to maintain proper message ordering.
// Mirrors TS: _flushPendingBashMessages() lines 2640-2652
func (s *AgentSession) flushPendingBashMessages() {
	if len(s.pendingBashMessages) == 0 {
		return
	}

	for _, bashMessage := range s.pendingBashMessages {
		if msg, ok := bashMessage.(llm.Message); ok {
			s.SessionManager.AppendMessage(msg)
		}
	}
	s.pendingBashMessages = nil
}

/*
KEYNOTES:
 1. BashExecutionMessage is in messages.go. It does NOT implement llm.Message
    (no IsMessage()). It's a "coding message" — persisted to session as a custom
    entry type and converted to llm.UserMessage via ConvertToLlm() when building
    the LLM context.

 2. Therefore, bash messages are NOT added to s.Agent.Messages() (which is
    []llm.Message). They go only to SessionManager.AppendMessage(). The agent's
    message list stays as pure llm.Message types.

 3. pendingBashMessages is []any (from the struct def in agent_session.go) to
    hold BashExecutionMessage values. Flush casts them back.

  4. ExecuteBash wraps ExecuteBashWithOperations from bash_executor.go,
    adding session-specific concerns (settings, recording).
*/
