package ycode

import (
	"context"
	"encoding/json"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/export-html"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// ============================================================================
// Phase 11.9: Session Stats, Info, Tree Navigation
//
// Migrated from reference/coding-agent/src/core/agent-session.ts lines 2654-2990
//
// Session management concerns:
//   1. Stats — message counts, token usage, cost
//   2. Context usage — estimated context window utilization
//   3. Session name — display name management
//   4. Tree navigation — branch switching within session
// ============================================================================

// --- Session Stats ---

// GetSessionStats computes session statistics from the current message history.
// Mirrors TS: getSessionStats() lines 2901-2943
func (s *AgentSession) GetSessionStats() SessionStats {
	messages := s.Agent.Messages()

	var userMsgs, assistantMsgs, toolResults, toolCalls int
	var totalInput, totalOutput, totalCacheRead, totalCacheWrite int
	var totalCost float64

	for _, msg := range messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			userMsgs++
		case llm.AssistantMessage:
			assistantMsgs++
			for _, block := range m.Content {
				if _, ok := block.(llm.ToolCall); ok {
					toolCalls++
				}
			}
			totalInput += m.Usage.Input
			totalOutput += m.Usage.Output
			totalCacheRead += m.Usage.CacheRead
			totalCacheWrite += m.Usage.CacheWrite
			totalCost += float64(m.Usage.Cost.Total)
		case llm.ToolResultMessage[any]:
			toolResults++
		}
	}

	return SessionStats{
		SessionFile:      s.GetSessionFile(),
		SessionID:        s.GetSessionID(),
		UserMessages:     userMsgs,
		AssistantMessages: assistantMsgs,
		ToolCalls:        toolCalls,
		ToolResults:      toolResults,
		TotalMessages:    len(messages),
		Tokens: TokenStats{
			Input:      totalInput,
			Output:    totalOutput,
			CacheRead: totalCacheRead,
			CacheWrite: totalCacheWrite,
			Total:     totalInput + totalOutput + totalCacheRead + totalCacheWrite,
		},
		Cost:          totalCost,
		ContextUsage:  s.GetContextUsage(),
	}
}

// GetContextUsage estimates current context window utilization.
// Mirrors TS: getContextUsage() lines 2946-2990
func (s *AgentSession) GetContextUsage() extensions.ContextUsage {
	model := s.GetModel()
	contextWindow := model.ContextWindow
	if contextWindow <= 0 {
		return extensions.ContextUsage{}
	}

	// After compaction, the last assistant usage reflects pre-compaction context size.
	// Only trust usage from an assistant that responded after the latest compaction.
	branchEntries := s.SessionManager.GetBranch("")
	latestCompaction := GetLatestCompactionEntry(branchEntries)

	if latestCompaction != nil {
		// Find compaction index
		compactionIndex := -1
		for i, e := range branchEntries {
			if e == latestCompaction {
				compactionIndex = i
				break
			}
		}

		if compactionIndex >= 0 {
			hasPostCompactionUsage := false
			for i := len(branchEntries) - 1; i > compactionIndex; i-- {
				entry := branchEntries[i]
				if entry.Type == compaction.EntryTypeMessage {
					if am, ok := entry.Message.(llm.AssistantMessage); ok {
						if am.StopReason != llm.StopReasonAborted && am.StopReason != llm.StopReasonError {
							contextTokens := compaction.CalculateContextTokens(am.Usage)
							if contextTokens > 0 {
								hasPostCompactionUsage = true
							}
							break
						}
					}
				}
			}
			if !hasPostCompactionUsage {
				// Context size unknown until next LLM response
				return extensions.ContextUsage{
					ContextWindow: contextWindow,
				}
			}
		}
	}

	estimate := compaction.EstimateContextTokens(s.Agent.Messages())
	// Note: no estimate.Tokens <= 0 guard. A fresh session legitimately
	// estimates to 0 tokens → 0%, shown as "0/205k" in the footer (matches the
	// reference TS, which has no such guard). "?" is reserved for the two
	// genuinely-unknown cases above: no model/context window, and
	// post-compaction before the next LLM response.
	percent := int(float64(estimate.Tokens) / float64(contextWindow) * 100)
	tokens := estimate.Tokens

	return extensions.ContextUsage{
		Tokens:        &tokens,
		ContextWindow: contextWindow,
		Percent:       &percent,
	}
}

// --- Session Name ---

// SetSessionName sets a display name for the current session.
// Mirrors TS: setSessionName() lines 2661-2664
func (s *AgentSession) SetSessionName(name string) {
	s.SessionManager.AppendSessionInfo(name)
	s.emit(AgentSessionEvent{
		EventType:    "session_info_changed",
		SessionName:  s.SessionManager.GetSessionName(),
	})
}

// GetSessionName returns the current session display name.
func (s *AgentSession) GetSessionName() string {
	return s.SessionManager.GetSessionName()
}

// --- Tree Navigation ---

// NavigateTree switches to a different node in the session tree.
// Unlike Fork which creates a new session file, this stays in the same file.
// Returns the user message text at the target if available.
// Mirrors TS: navigateTree() lines 2681+
func (s *AgentSession) NavigateTree(targetID string) {
	// Branch to target
	s.SessionManager.Branch(targetID)

	// Rebuild agent state from new branch
	sessionContext := s.SessionManager.BuildSessionContext()
	s.Agent.SetMessages(sessionContext.Messages)

	// Rebuild system prompt
	s.baseSystemPrompt = s.rebuildSystemPrompt(s.GetActiveToolNames())
	s.Agent.SetSystemPrompt(s.baseSystemPrompt)
}

// --- Reload ---

// Reload reloads settings and resources, then rebuilds the runtime.
// Mirrors TS: reload() lines 2383-2404
func (s *AgentSession) Reload() error {
	// Emit session shutdown event for reload
	if s.extensionRunner != nil {
		s.extensionRunner.Emit(context.Background(), extensions.SessionShutdownEvent{
			Type:   "session_shutdown",
			Reason: "reload",
		})
	}

	// Reload settings and resources
	s.SettingsManager.Reload()
	if err := s.resourceLoader.Reload(); err != nil {
		return err
	}

	// Rebuild runtime with previous tool names
	previousTools := s.GetActiveToolNames()
	previousFlags := make(map[string]any)
	if s.extensionRunner != nil {
		for k, v := range s.extensionRunner.GetFlagValues() {
			previousFlags[k] = v
		}
	}
	s.buildRuntime(previousTools, previousFlags, true)

	// Emit session start for reload
	if s.extensionRunner != nil {
		s.extensionRunner.Emit(context.Background(), extensions.SessionStartEvent{
			Type:   "session_start",
			Reason: "reload",
		})
	}

	return nil
}

// ============================================================================
// HTML Export
// ============================================================================

// ExportToHtml exports the session to a standalone HTML file.
// Mirrors TS AgentSession.exportToHtml() (agent-session.ts:2997-3020).
func (s *AgentSession) ExportToHtml(outputPath string) (string, error) {
	// Collect tool definitions for export
	var tools []exporthtml.ToolExportDef
	for _, entry := range s.GetAllTools() {
		params, _ := json.Marshal(entry.Definition.Parameters)
		tools = append(tools, exporthtml.ToolExportDef{
			Name:        entry.Definition.Name,
			Description: entry.Definition.Description,
			Parameters:  params,
		})
	}

	// Build a session manager adapter
	sm := &sessionManagerAdapter{
		sm:       s.SessionManager,
		entries:  s.SessionManager.GetBranch(""),
	}

	opts := &exporthtml.ExportOptions{
		OutputPath: outputPath,
	}

	return exporthtml.ExportSessionToHtml(sm, s.Agent.SystemPrompt(), tools, opts)
}

// sessionManagerAdapter wraps SessionManager to implement the export-html interface.
type sessionManagerAdapter struct {
	sm      *SessionManager
	entries []*compaction.SessionEntry
}

func (a *sessionManagerAdapter) GetSessionFile() string {
	return a.sm.GetSessionFile()
}

func (a *sessionManagerAdapter) GetHeader() json.RawMessage {
	header := a.sm.GetHeader()
	if header == nil {
		return nil
	}
	data, _ := json.Marshal(header)
	return data
}

func (a *sessionManagerAdapter) GetEntries() []json.RawMessage {
	var entries []json.RawMessage
	for _, e := range a.entries {
		data, err := json.Marshal(e)
		if err == nil {
			entries = append(entries, data)
		}
	}
	return entries
}

func (a *sessionManagerAdapter) GetLeafID() string {
	return a.sm.GetLeafId()
}

/*
KEYNOTES:
 1. GetSessionStats iterates messages using type assertion (Go interface dispatch)
    instead of TS's `.filter(m => m.role === ...).length`.

 2. GetContextUsage checks for post-compaction usage to avoid reporting stale
    context sizes. After compaction, the pre-compaction assistant usage reflects
    the old (larger) context — we can only trust usage from after the boundary.

 3. NavigateTree calls SessionManager.Branch(targetID) to switch the active
    leaf, then rebuilds agent state from the new branch.

 4. Reload emits session_shutdown/reload events, refreshes settings and resources,
    then rebuilds the runtime. The TS version also calls resetApiProviders() —
    that will be wired in Phase 12/13.

 5. extensions.ContextUsage has Tokens, ContextWindow, Percent as int/int/float64.
    Zero-value Tokens means "unknown" (null in TS).
*/
