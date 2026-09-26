package ycode

import (
	"context"
	"fmt"
	"time"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// ============================================================================
// Phase 11.5: Compaction Wiring
//
// Migrated from reference/coding-agent/src/core/agent-session.ts lines 1605-2033
//
// Three compaction triggers:
//  1. Manual — user calls Compact()
//  2. Overflow — LLM returned context overflow error, auto-compact + retry
//  3. Threshold — context exceeds threshold, auto-compact without retry
//
// All three share the same core flow:
//   prepare → extension hook → compact → persist → extension notify → emit event
//
// NOTE: AgentMessage = llm.Message (type alias in agent-sdk). So s.Agent.Messages()
// returns []llm.Message directly — no conversion needed.
// ============================================================================

// --- Public API ---

// Compact manually triggers session compaction.
// Aborts current agent operation first.
// Mirrors TS: AgentSession.compact() lines 1610-1738 — the TS runs cleanup +
// reconnection in a finally block, so they happen on error AND cancel paths
// too. The port previously reconnected only on success, leaving the session
// permanently disconnected from agent events after one failed /compact.
func (s *AgentSession) Compact(customInstructions string) (*compaction.CompactionResult, error) {
	s.disconnectFromAgent()
	s.Agent.Abort()

	// Real cancel function so AbortCompaction() actually cancels (the TS
	// aborts an AbortController; the old `func(){}` made it a no-op).
	ctx, cancel := context.WithCancel(context.Background())
	s.compactionAbort = cancel

	s.emit(AgentSessionEvent{
		EventType:       "compaction_start",
		CompactionReason: CompactionManual,
	})

	// finally-equivalent: cleanup + reconnect on every path.
	defer func() {
		cancel()
		s.compactionAbort = nil
		s.reconnectToAgent()
	}()

	result, err := s.runCompaction(ctx, CompactionManual, customInstructions)
	if err != nil {
		aborted := err.Error() == "Compaction cancelled"
		s.emit(AgentSessionEvent{
			EventType:        "compaction_end",
			CompactionReason: CompactionManual,
			CompactionAborted: aborted,
			ComapctionError:  conditionalStr(!aborted, fmt.Sprintf("Compaction failed: %s", err.Error())),
		})
		return nil, err
	}

	s.emit(AgentSessionEvent{
		EventType:        "compaction_end",
		CompactionReason: CompactionManual,
		CompactionResult: result,
	})

	// Clear overflow recovery flag on successful manual compaction
	s.overflowRecoveryAttempted = false

	return result, nil
}

// AbortCompaction cancels in-progress compaction (manual or auto).
func (s *AgentSession) AbortCompaction() {
	if s.compactionAbort != nil {
		s.compactionAbort()
	}
	if s.autoCompactionAbort != nil {
		s.autoCompactionAbort()
	}
}

// SetAutoCompactionEnabled toggles auto-compaction.
func (s *AgentSession) SetAutoCompactionEnabled(enabled bool) {
	s.SettingsManager.SetCompactionEnabled(enabled)
}

// IsAutoCompactionEnabled reports whether auto-compaction is enabled.
func (s *AgentSession) IsAutoCompactionEnabled() bool {
	return s.SettingsManager.GetCompactionEnabled()
}

// Note: IsCompacting() is defined in agent_session.go — includes branchSummaryAbort check.

// --- Auto-Compaction Check (called from processAgentEvent / prompt) ---

// checkCompaction is called after agent_end and before prompt submission.
// Two cases:
//   1. Overflow: LLM returned context overflow error → compact + auto-retry
//   2. Threshold: context exceeds threshold → compact without retry
//
// Mirrors TS: _checkCompaction() lines 1766-1844
func (s *AgentSession) checkCompaction(assistantMessage llm.AssistantMessage, skipAbortedCheck bool) {
	settings := s.getCompactionSettings()
	if !settings.Enabled {
		return
	}

	// Skip if message was aborted (user cancelled) — unless skipAbortedCheck is false
	if skipAbortedCheck && assistantMessage.StopReason == llm.StopReasonAborted {
		return
	}

	contextWindow := 0
	if model := s.GetModel(); model.ID != "" {
		contextWindow = model.ContextWindow
	}

	// Skip compaction checks if this assistant message is older than the latest
	// compaction boundary. Prevents stale pre-compaction usage/error from retriggering.
	compactionEntry := GetLatestCompactionEntry(s.SessionManager.GetBranch(""))
	if compactionEntry != nil && assistantMessage.Timestamp <= compactionEntry.Timestamp {
		return
	}

	// Skip overflow check if the message came from a different model.
	sameModel := false
	if model := s.GetModel(); model.ID != "" {
		sameModel = model.ID == assistantMessage.Model && string(model.Provider) == string(assistantMessage.Provider)
	}

	// Case 1: Overflow — LLM returned context overflow error
	if sameModel && utils.IsContextOverflow(assistantMessage, contextWindow) {
		if s.overflowRecoveryAttempted {
			s.emit(AgentSessionEvent{
				EventType:        "compaction_end",
				CompactionReason: CompactionOverflow,
				ComapctionError:  "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model.",
			})
			return
		}
		s.overflowRecoveryAttempted = true

		// Remove the error message from agent state (saved in session for history,
		// but not in context for retry)
		messages := s.Agent.Messages()
		if len(messages) > 0 {
			if _, ok := messages[len(messages)-1].(llm.AssistantMessage); ok {
				s.Agent.SetMessages(messages[:len(messages)-1])
			}
		}

		s.runAutoCompaction(CompactionOverflow, true)
		return
	}

	// Case 2: Threshold — context is getting large
	// For error messages (no usage data), estimate from last successful response.
	var contextTokens int
	if assistantMessage.StopReason == llm.StopReasonError {
		messages := s.Agent.Messages()
		estimate := compaction.EstimateContextTokens(messages)
		if estimate.LastUsageIndex < 0 {
			return // No usage data at all
		}
		// Verify the usage source is post-compaction. Kept pre-compaction messages
		// have stale usage reflecting the old (larger) context and would falsely
		// trigger compaction right after one just finished.
		usageMsg := messages[estimate.LastUsageIndex]
		if compactionEntry != nil {
			if am, ok := usageMsg.(llm.AssistantMessage); ok {
				if am.Timestamp <= compactionEntry.Timestamp {
					return
				}
			}
		}
		contextTokens = estimate.Tokens
	} else {
		contextTokens = compaction.CalculateContextTokens(assistantMessage.Usage)
	}

	if compaction.ShouldCompact(contextTokens, contextWindow, settings) {
		s.runAutoCompaction(CompactionThreshold, false)
	}
}

// --- Internal Auto-Compaction Runner ---

// runAutoCompaction runs auto-compaction with proper events.
// Mirrors TS: _runAutoCompaction() lines 1849-2021
func (s *AgentSession) runAutoCompaction(reason CompactionReason, willRetry bool) {
	s.emit(AgentSessionEvent{
		EventType:        "compaction_start",
		CompactionReason: reason,
	})

	// Create abort context
	ctx, cancel := context.WithCancel(context.Background())
	s.autoCompactionAbort = cancel
	defer func() { s.autoCompactionAbort = nil }()

	model := s.GetModel()
	if model.ID == "" {
		s.emit(AgentSessionEvent{
			EventType:        "compaction_end",
			CompactionReason: reason,
		})
		return
	}

	// Get auth
	authResult := s.modelRegistry.GetApiKeyAndHeaders(ctx, &model)
	if authResult == nil || !authResult.Ok || authResult.ApiKey == "" {
		s.emit(AgentSessionEvent{
			EventType:        "compaction_end",
			CompactionReason: reason,
		})
		return
	}

	settings := s.getCompactionSettings()
	result, err := s.runCompactionCore(ctx, reason, model, authResult.ApiKey, authResult.Headers, settings, "")
	if err != nil {
		errorMsg := err.Error()
		if reason == CompactionOverflow {
			errorMsg = fmt.Sprintf("Context overflow recovery failed: %s", errorMsg)
		} else {
			errorMsg = fmt.Sprintf("Auto-compaction failed: %s", errorMsg)
		}
		s.emit(AgentSessionEvent{
			EventType:        "compaction_end",
			CompactionReason: reason,
			ComapctionError:  errorMsg,
		})
		return
	}

	s.emit(AgentSessionEvent{
		EventType:           "compaction_end",
		CompactionReason:    reason,
		CompactionResult:    result,
		CompactionWillRetry: willRetry,
	})

	// If overflow retry, remove trailing error message and continue
	if willRetry {
		messages := s.Agent.Messages()
		if len(messages) > 0 {
			if _, ok := messages[len(messages)-1].(llm.AssistantMessage); ok {
				s.Agent.SetMessages(messages[:len(messages)-1])
			}
		}
		// Schedule retry after short delay
		go func() {
			time.Sleep(100 * time.Millisecond)
				s.Agent.Continue(context.Background())
		}()
	}
}

// --- Core Compaction Flow (shared by manual + auto) ---

// runCompaction is the shared flow for both manual and auto compaction.
// Mirrors the inner logic of TS compact() and _runAutoCompaction().
// ctx carries cancellation from AbortCompaction (manual) or the auto-compaction
// abort context.
func (s *AgentSession) runCompaction(ctx context.Context, reason CompactionReason, customInstructions string) (*compaction.CompactionResult, error) {
	model := s.GetModel()
	if model.ID == "" {
		return nil, fmt.Errorf("no model selected")
	}

	authResult := s.modelRegistry.GetApiKeyAndHeaders(ctx, &model)
	if authResult == nil || !authResult.Ok || authResult.ApiKey == "" {
		return nil, fmt.Errorf("unable to get API credentials for compaction")
	}

	settings := s.getCompactionSettings()
	return s.runCompactionCore(ctx, reason, model, authResult.ApiKey, authResult.Headers, settings, customInstructions)
}

// runCompactionCore executes the compaction: prepare → extension hook → compact → persist → notify.
func (s *AgentSession) runCompactionCore(
	ctx context.Context,
	reason CompactionReason,
	model llm.Model[llm.Api],
	apiKey string,
	headers map[string]string,
	settings compaction.CompactionSettings,
	customInstructions string,
) (*compaction.CompactionResult, error) {
	pathEntries := s.SessionManager.GetBranch("")
	sessionMessages := s.SessionManager.BuildSessionContext().Messages

	deps := s.getCompactionDeps()

	preparation := compaction.PrepareCompaction(pathEntries, sessionMessages, settings, deps)
	if preparation == nil {
		// Check why we can't compact
		if len(pathEntries) > 0 && pathEntries[len(pathEntries)-1].Type == compaction.EntryTypeCompaction {
			return nil, fmt.Errorf("already compacted")
		}
		return nil, fmt.Errorf("nothing to compact (session too small)")
	}

	// Extension hook: session_before_compact
	var extensionCompaction *compaction.CompactionResult
	fromExtension := false

	if s.extensionRunner != nil && s.extensionRunner.HasHandlers("session_before_compact") {
		extResult, err := s.extensionRunner.Emit(ctx, map[string]any{
			"type":               "session_before_compact",
			"preparation":        preparation,
			"branchEntries":      pathEntries,
			"customInstructions": customInstructions,
		})
		if err == nil {
			if m, ok := extResult.(map[string]any); ok {
				if cancel, _ := m["cancel"].(bool); cancel {
					return nil, fmt.Errorf("Compaction cancelled")
				}
				if comp, ok := m["compaction"].(*compaction.CompactionResult); ok && comp != nil {
					extensionCompaction = comp
					fromExtension = true
				}
			}
		}
	}

	var summary string
	var firstKeptEntryID string
	var tokensBefore int
	var details any

	thinkingLevel := s.GetThinkingLevel()

	if extensionCompaction != nil {
		// Extension provided compaction content
		summary = extensionCompaction.Summary
		firstKeptEntryID = extensionCompaction.FirstKeptEntry.ID
		tokensBefore = extensionCompaction.TokensBefore
		details = &compaction.CompactionDetails{ReadFiles: extensionCompaction.ReadFiles, ModifiedFiles: extensionCompaction.ModifiedFiles}
	} else {
		// Generate compaction result
		result, err := compaction.Compact(ctx, deps, preparation, &model, apiKey, headers, customInstructions, llm.ThinkingLevel(thinkingLevel))
		if err != nil {
			return nil, err
		}
		summary = result.Summary
		tokensBefore = result.TokensBefore
		details = &compaction.CompactionDetails{
			ReadFiles:     result.ReadFiles,
			ModifiedFiles: result.ModifiedFiles,
		}
		firstKeptEntryID = preparation.FirstKeptEntryID
	}

	// Persist to session
	s.SessionManager.AppendCompaction(summary, firstKeptEntryID, tokensBefore, details, fromExtension)

	// Update agent state with new messages (AgentMessage = llm.Message, no conversion)
	sessionContext := s.SessionManager.BuildSessionContext()
	s.Agent.SetMessages(sessionContext.Messages)

	// Notify extension: session_compact
	newEntries := s.SessionManager.GetEntries()
	var savedCompactionEntry *compaction.SessionEntry
	for _, e := range newEntries {
		if e.Type == compaction.EntryTypeCompaction && e.Summary == summary {
			savedCompactionEntry = e
			break
		}
	}

	if s.extensionRunner != nil && savedCompactionEntry != nil {
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":            "session_compact",
			"compactionEntry": savedCompactionEntry,
			"fromExtension":   fromExtension,
		})
	}

	return &compaction.CompactionResult{
		Summary:       summary,
		TokensBefore:  tokensBefore,
		FirstKeptEntry: savedCompactionEntry,
	}, nil
}

// --- Helpers ---

// getCompactionSettings builds a compaction.CompactionSettings from SettingsManager.
// SettingsManager exposes individual getters; we compose them into the struct.
func (s *AgentSession) getCompactionSettings() compaction.CompactionSettings {
	return compaction.CompactionSettings{
		Enabled:          s.SettingsManager.GetCompactionEnabled(),
		ReserveTokens:    s.SettingsManager.GetCompactionReserveTokens(),
		KeepRecentTokens: s.SettingsManager.GetCompactionKeepRecentTokens(),
	}
}

// getCompactionDeps builds the Deps struct needed by the compaction package.
// These functions break the circular import between ycode ↔ compaction.
func (s *AgentSession) getCompactionDeps() *compaction.Deps {
	return &compaction.Deps{
		ComvertToLlm:                  ConvertToLlm,
		CompleteSimple:                 s.completeSimple,
		CreateBranchSummaryMessage:     createBranchSummaryMessage,
		CreateCompactionSummaryMessage: createCompactionSummaryMessage,
		CreateCustomMessage:            createCustomMessage,
	}
}

// conditionalStr returns s if cond is true, empty string otherwise.
func conditionalStr(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

// completeSimple is the Deps.CompleteSimple implementation.
// Performs a non-streaming LLM call by wrapping the streaming infrastructure.
// Collects events from the stream channel and extracts the final AssistantMessage.
func (s *AgentSession) completeSimple(
	ctx context.Context,
	model *llm.Model[llm.Api],
	systemPrompt string,
	messages []llm.Message,
	apiKey string,
	headers map[string]string,
	maxTokens int,
	thinkingLevel llm.ThinkingLevel,
) (*compaction.CompletionResult, error) {
	// 1. Look up provider
	provider := llm.GetApiProvider(model.Api)
	if provider == nil || provider.StreamSimple == nil {
		return nil, fmt.Errorf("no provider registered for api: %s", model.Api)
	}

	// 2. Build options with auth + retry settings
	providerRetry := s.SettingsManager.GetProviderRetrySettings()
	maxRetries := 1
	opts := &llm.SimpleStreamOptions{
		StreamOptions: llm.StreamOptions{
			APIKey:          apiKey,
			Headers:         headers,
			TimeoutMs:       derefInt(providerRetry.TimeoutMs, 300000),
			MaxRetries:      &maxRetries,
			MaxRetryDelayMs: derefInt(providerRetry.MaxRetryDelayMs, 60000),
			Context:         ctx,
		},
		Reasoning: thinkingLevel,
	}
	// Honor the caller's output-token budget (TS passes options.maxTokens
	// through; providers default only when it's absent). Guarded on >0 so a
	// zero budget falls back to provider defaults, matching TS's falsy `||`
	// in the Anthropic max_tokens path.
	if maxTokens > 0 {
		opts.StreamOptions.MaxTokens = &maxTokens
	}

	// 3. Build chat context
	chatCtx := llm.ChatContext{
		SystemPrompt: systemPrompt,
		Messages:     messages,
	}

	// 4. Call streaming and collect the final message
	ch := provider.StreamSimple(ctx, *model, chatCtx, opts)
	var lastMessage *llm.AssistantMessage
	for event := range ch {
		if event.Message != nil {
			lastMessage = event.Message
		}
		if event.Error != nil {
			return &compaction.CompletionResult{
				StopReason:   string(llm.StopReasonError),
				ErrorMessage: event.Error.ErrorMessage,
			}, nil
		}
	}

	if lastMessage == nil {
		return nil, fmt.Errorf("completeSimple: no response received")
	}

	return &compaction.CompletionResult{
		Content:      lastMessage.Content,
		StopReason:   string(lastMessage.StopReason),
		ErrorMessage: lastMessage.ErrorMessage,
	}, nil
}

// derefInt dereferences an *int with a fallback.
func derefInt(p *int, fallback int) int {
	if p != nil {
		return *p
	}
	return fallback
}

// --- Message Factory Functions (for compaction Deps) ---

func createBranchSummaryMessage(summary, fromID string, timestamp int64) llm.Message {
	return llm.UserMessage{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			llm.TextContent{Text: fmt.Sprintf("[Branch summary from %s]\n%s", fromID, summary)},
		},
		Timestamp: timestamp,
	}
}

func createCompactionSummaryMessage(summary string, tokensBefore int, timestamp int64) llm.Message {
	return llm.UserMessage{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			llm.TextContent{Text: fmt.Sprintf("[Compacted conversation (%d tokens)]\n%s", tokensBefore, summary)},
		},
		Timestamp: timestamp,
	}
}

func createCustomMessage(customType string, content any, display bool, details any, timestamp int64) llm.Message {
	text := fmt.Sprintf("[%s: %v]", customType, content)
	return llm.UserMessage{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			llm.TextContent{Text: text},
		},
		Timestamp: timestamp,
	}
}

/*
KEYNOTES:
 1. AgentMessage = llm.Message (type alias). s.Agent.Messages() returns []llm.Message
    directly and SetMessages takes []llm.Message. No conversion functions needed.

 2. runCompactionCore is the shared inner function for both manual Compact() and
    auto-compaction. The TS code duplicates this logic between compact() and
    _runAutoCompaction(); we extract into one function.

 3. SettingsManager has individual getters but no GetCompactionSettings().
    We compose via getCompactionSettings().

 4. completeSimple is stubbed — depends on non-streaming LLM call (Phase 13).

 5. Context cancellation: Go uses context.Context instead of TS AbortController.
    compactionAbort / autoCompactionAbort are CancelFuncs.

 6. IsContextOverflow lives in llm/src/utils/overflow.go — already migrated.
*/
