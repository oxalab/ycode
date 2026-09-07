package ycode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// ============================================================================
// AgentSession
// ============================================================================

// AgentSession is the core abstraction for agent lifecycle and session management.
// Shared between all run modes (interactive, print, rpc).
type AgentSession struct {
	Agent           sdk.AgentState
	SessionManager  *SessionManager
	SettingsManager *SettingsManager

	scopedModels []ScopedModelEntry

	// Event subscription state
	unsubscribeAgent func()
	configSubscribe    func(listener func(event sdk.AgentEvent, ctx context.Context) error) func()
	eventListeners    []AgentSessionEventListener
	eventListenerIDs  []uint64 // parallel to eventListeners; registration IDs for unsubscribe
	eventListenerSeq  uint64
	agentEventQueue  chan func()
	eventQueueOnce   sync.Once

	// Queue state
	steeringMessages        []string
	followUpMessages        []string
	pendingNextTurnMessages []any // CustomMessage

	// Compaction state
	compactionAbort        context.CancelFunc
	autoCompactionAbort    context.CancelFunc
	overflowRecoveryAttempted bool

	// Branch summarization state
	branchSummaryAbort context.CancelFunc

	// Retry state
	retryMu       sync.Mutex
	retryAttempt  int
	retryChan     chan struct{}

	// Bash execution state
	bashAbortMu       sync.Mutex // guards bashAbort (touched from RPC handler goroutines)
	bashAbort         context.CancelFunc
	pendingBashMessages []any // BashExecutionMessage

	// Extension system
	extensionRunner                *extensions.ExtensionRunner
	turnIndex                      int
	extensionRunnerRef             *ExtensionRunnerRef
	extensionUIContext             extensions.ExtensionUIContext
	extensionCommandContextActions extensions.ExtensionCommandContextActions
	extensionShutdownHandler       extensions.ShutdownHandler
	extensionErrorListener         extensions.ExtensionErrorListener
	extensionErrorUnsubscriber     func()

	// Resource & config
	resourceLoader       ResourceLoader
	customTools          []extensions.ToolDefinition
	baseToolDefinitions  map[string]extensions.ToolDefinition
	cwd                  string
	initialActiveToolNames []string
	allowedToolNames     map[string]bool
	baseToolsOverride    map[string]any // map of AgentTool
	sessionStartEvent    extensions.SessionStartEvent
	modelRegistry        *ModelRegistry

	// Runtime session-replacement callbacks — injected by AgentSessionRuntime after creation
	onNewSession    func(opts map[string]any) (map[string]bool, error)
	onFork          func(entryID string, opts map[string]any) (map[string]bool, error)
	onSwitchSession func(sessionPath string, opts map[string]any) (map[string]bool, error)

	// Tool registry
	toolRegistry        map[string]any // map of AgentTool
	toolDefinitions     map[string]ToolDefinitionEntry
	toolPromptSnippets  map[string]string
	toolPromptGuidelines map[string][]string

	// System prompt
	baseSystemPrompt        string
	baseSystemPromptOptions BuildSystemPromptOptions

	// Track last assistant for auto-compaction
	lastAssistantMessage any // AssistantMessage
}

// ============================================================================
// Constructor
// ============================================================================

func NewAgentSession(config AgentSessionConfig) *AgentSession {
	session := &AgentSession{
		Agent:                config.Agent,
		SessionManager:       config.SessionManager,
		configSubscribe:       config.SubscribeFunc,
		SettingsManager:      config.SettingsManager,
		scopedModels:         config.ScopedModels,
		resourceLoader:       config.ResourceLoader,
		customTools:          config.CustomTools,
		cwd:                  config.Cwd,
		modelRegistry:        config.ModelRegistry,
		extensionRunnerRef:   config.ExtensionRunnerRef,
		initialActiveToolNames: config.InitialActiveToolNames,
		allowedToolNames:     makeMapSet(config.AllowedToolNames),
		baseToolsOverride:    nil, // TODO: wire from config
		sessionStartEvent:    derefOr(config.SessionStartEvent, extensions.SessionStartEvent{
			Reason: "startup",
		}),
		agentEventQueue:    make(chan func(), 256),
		// retryChan starts nil (TS: _retryPromise starts undefined). It is
		// created only when a retry is actually initiated and cleared when the
		// retry settles — see createRetryPromiseForAgentEnd / resolveRetry.
		// Buffering a token at construction caused every retry wait to consume
		// a stale token and self-cancel.
		retryChan:          nil,
		baseToolDefinitions: make(map[string]extensions.ToolDefinition),
		toolRegistry:       make(map[string]any),
		toolDefinitions:    make(map[string]ToolDefinitionEntry),
		toolPromptSnippets: make(map[string]string),
		toolPromptGuidelines: make(map[string][]string),
		onNewSession:    config.OnNewSession,
		onFork:          config.OnFork,
		onSwitchSession: config.OnSwitchSession,
	}

	// Start event processing goroutine
	go session.processEventQueue()

	// Subscribe to agent events
	session.unsubscribeAgent = config.SubscribeFunc(session.handleAgentEvent)

	session.installAgentToolHooks()
	session.buildRuntime(session.initialActiveToolNames, nil, true)

	return session
}

// ============================================================================
// Event processing goroutine
// ============================================================================

// processEventQueue drains the agentEventQueue sequentially,
// matching TS's _agentEventQueue behavior (serialized async processing).
func (s *AgentSession) processEventQueue() {
	for fn := range s.agentEventQueue {
		fn()
	}
}

// ============================================================================
// Event subscription
// ============================================================================

// Subscribe registers a session event listener and returns an unsubscribe
// function. Removal is by registration ID: Go func values aren't comparable
// with == (and the old address-of-loop-copy comparison `&l == &listener` was
// always false), so identity-based removal like the TS indexOf/splice
// (agent-session.ts:713-723) needs an explicit handle.
func (s *AgentSession) Subscribe(listener AgentSessionEventListener) func() {
	s.eventListenerSeq++
	id := s.eventListenerSeq
	s.eventListeners = append(s.eventListeners, listener)
	s.eventListenerIDs = append(s.eventListenerIDs, id)
	return func() {
		for i, existing := range s.eventListenerIDs {
			if existing == id {
				s.eventListeners = append(s.eventListeners[:i], s.eventListeners[i+1:]...)
				s.eventListenerIDs = append(s.eventListenerIDs[:i], s.eventListenerIDs[i+1:]...)
				return
			}
		}
	}
}

func (s *AgentSession) emit(event AgentSessionEvent) {
	for _, l := range s.eventListeners {
		l(event)
	}
}

func (s *AgentSession) emitQueueUpdate() {
	steering := make([]string, len(s.steeringMessages))
	copy(steering, s.steeringMessages)
	followUp := make([]string, len(s.followUpMessages))
	copy(followUp, s.followUpMessages)
	s.emit(AgentSessionEvent{
		EventType: "queue_update",
		Steering:  steering,
		FollowUp:  followUp,
	})
}

// ============================================================================
// Agent event handler
// ============================================================================

func (s *AgentSession) handleAgentEvent(event sdk.AgentEvent, ctx context.Context) error {
	// Create retry promise synchronously before queueing
	s.createRetryPromiseForAgentEnd(event)

	// Queue serialized processing
	s.agentEventQueue <- func() {
		s.processAgentEvent(event)
	}
	return nil
}

func (s *AgentSession) processAgentEvent(event sdk.AgentEvent) {
	switch e := event.(type) {
	case sdk.EventMessageStart:
		if e.Message != nil {
			// Reference (agent-session.ts:503) resets the overflow-recovery flag
			// only on USER messages — resetting on any message_start let the
			// retry's own assistant stream clear the flag, re-enabling
			// compact→retry→compact loops on a persistently-overflowing model.
			if getRole(e.Message) == "user" {
				s.overflowRecoveryAttempted = false
			}
			text := getUserMessageText(e.Message)
			if text != "" {
				if s.removeFromSteering(text) || s.removeFromFollowUp(text) {
					s.emitQueueUpdate()
				}
			}
		}

	case sdk.EventMessageEnd:
		if e.Message != nil {
			// Persist message
			s.SessionManager.AppendMessage(e.Message)

			// Track assistant for auto-compaction
			role := getRole(e.Message)
			if role == "assistant" {
				s.lastAssistantMessage = e.Message
				// Reference (agent-session.ts:554-565): both the overflow-recovery
				// flag and the retry counter reset ONLY on non-error responses.
				// Resetting on error responses made persistent 529/500 loops
				// retry forever (counter never reached max) and let the retry's
				// own error stream clear overflowRecoveryAttempted, enabling
				// compact→retry→compact loops.
				if am, ok := e.Message.(llm.AssistantMessage); ok && am.StopReason != llm.StopReasonError {
					s.overflowRecoveryAttempted = false
					s.retryMu.Lock()
					if s.retryAttempt > 0 {
						s.emit(AgentSessionEvent{
							EventType:   "auto_retry_end",
							RetrySuccess: true,
							RetryAttempt: s.retryAttempt,
						})
						s.retryAttempt = 0
					}
					s.retryMu.Unlock()
				}
			}
		}

	case sdk.EventAgentEnd:
		if s.lastAssistantMessage != nil {
			msg := s.lastAssistantMessage
			s.lastAssistantMessage = nil

			// Retry logic (Phase 11.7)
			if am, ok := msg.(llm.AssistantMessage); ok {
				if s.isRetryableError(am) {
					if s.handleRetryableError(am) {
						return // retry initiated, skip compaction
					}
				}
				// Auto-compaction check (Phase 11.5)
				s.checkCompaction(am, true)
			}

			s.resolveRetry()
		}
	}

	// Emit to extension runner
	s.emitExtensionEvent(event)

	// Notify listeners
	// Convert core event to session event and emit
	s.emit(agentEventToSessionEvent(event))
}

func (s *AgentSession) resolveRetry() {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	// No active retry → no-op (TS: resolveRetry() no-ops when _retryPromise is
	// undefined). The channel is drained/cleared by the retry goroutine.
	if s.retryChan == nil {
		return
	}
	select {
	case s.retryChan <- struct{}{}:
	default:
	}
}

// Extension event dispatch — see agent_session_extensions.go (Phase 11.6)

// ============================================================================
// Agent tool hooks
// ============================================================================

// installAgentToolHooks wires the agent's BeforeToolCall/AfterToolCall hooks
// (reference agent-session.ts:378-428) to the extension runner's
// EmitToolCall/EmitToolResult pipeline. Extensions that register `tool_call`
// handlers can observe or block tool calls; `tool_result` handlers can observe
// or modify tool results.
//
// The closures capture `s` and dereference s.extensionRunner at call time so
// extensions loaded after session construction are picked up. If no runner is
// configured, or no handlers are registered for the event, the hooks are
// no-ops (return nil → agent loop proceeds normally).
func (s *AgentSession) installAgentToolHooks() {
	s.Agent.SetBeforeToolCall(func(ctx sdk.BeforeToolCallContext, signal context.Context) (*sdk.BeforeToolCallResult, error) {
		runner := s.extensionRunner
		if runner == nil || !runner.HasHandlers("tool_call") {
			return nil, nil
		}
		event := &extensions.ToolCallEvent{
			Type:       "tool_call",
			ToolCallID: ctx.ToolCall.ID,
			ToolName:   ctx.ToolCall.Name,
			Args:       ctx.Args,
		}
		result, err := runner.EmitToolCall(signal, event)
		if err != nil {
			return nil, fmt.Errorf("extension tool_call handler failed: %w", err)
		}
		if result == nil {
			return nil, nil
		}
		return &sdk.BeforeToolCallResult{
			Block:  result.Block,
			Reason: result.Reason,
		}, nil
	})

	s.Agent.SetAfterToolCall(func(ctx sdk.AfterToolCallContext, signal context.Context) (*sdk.AfterToolCallResult, error) {
		runner := s.extensionRunner
		if runner == nil || !runner.HasHandlers("tool_result") {
			return nil, nil
		}
		event := &extensions.ToolResultEvent{
			Type:       "tool_result",
			ToolCallID: ctx.ToolCall.ID,
			ToolName:   ctx.ToolCall.Name,
			Args:       ctx.Args,
			Result:     ctx.Result.Details,
			IsError:    ctx.IsError,
		}
		result, err := runner.EmitToolResult(signal, event)
		if err != nil {
			return nil, fmt.Errorf("extension tool_result handler failed: %w", err)
		}
		if result == nil {
			return nil, nil
		}
		out := &sdk.AfterToolCallResult{
			Details: result.Details,
			IsError: result.IsError,
		}
		// Convert the []any Content back into []llm.ContentBlock. Fall back to
		// JSON-stringifying anything we can't map directly so the agent always
		// sees something when an extension reports a modified content value.
		if len(result.Content) > 0 {
			blocks := make([]llm.ContentBlock, 0, len(result.Content))
			for _, item := range result.Content {
				if cb, ok := item.(llm.ContentBlock); ok {
					blocks = append(blocks, cb)
					continue
				}
				if str, ok := item.(string); ok {
					blocks = append(blocks, llm.TextContent{Type: "text", Text: str})
					continue
				}
				if m, ok := item.(map[string]any); ok {
					if t, _ := m["type"].(string); t == "text" {
						if txt, _ := m["text"].(string); txt != "" {
							blocks = append(blocks, llm.TextContent{Type: "text", Text: txt})
							continue
						}
					}
				}
				// Last-resort: JSON-encode so the model still sees the value.
				if encoded, err := json.Marshal(item); err == nil {
					blocks = append(blocks, llm.TextContent{Type: "text", Text: string(encoded)})
				}
			}
			out.Content = blocks
		}
		return out, nil
	})
}

// ============================================================================
// Disconnect / Reconnect
// ============================================================================

func (s *AgentSession) disconnectFromAgent() {
	if s.unsubscribeAgent != nil {
		s.unsubscribeAgent()
		s.unsubscribeAgent = nil
	}
}

func (s *AgentSession) reconnectToAgent() {
	if s.unsubscribeAgent != nil {
		return
	}
	s.unsubscribeAgent = s.configSubscribe(s.handleAgentEvent)
}

func (s *AgentSession) Dispose() {
	if s.extensionRunner != nil {
		s.extensionRunner.Invalidate("session disposed")
	}
	s.disconnectFromAgent()
	s.eventListeners = nil
	s.eventListenerIDs = nil
	close(s.agentEventQueue)
}

// ============================================================================
// buildRuntime, refreshToolRegistry — see agent_session_extensions.go (Phase 11.6)

// ============================================================================
// Read-only State Access
// ============================================================================

func (s *AgentSession) GetModel() llm.Model[llm.Api] {
	return s.Agent.Model()
}

// GetModelRegistry returns the model registry backing this session (available
// models, auth status). Used by the TUI's model + scoped-models selectors.
func (s *AgentSession) GetModelRegistry() *ModelRegistry {
	return s.modelRegistry
}

func (s *AgentSession) GetThinkingLevel() sdk.ThinkingLevel {
	return s.Agent.ThinkingLevel()
}

func (s *AgentSession) IsStreaming() bool {
	return s.Agent.IsStreaming()
}

func (s *AgentSession) GetSystemPrompt() string {
	return s.Agent.SystemPrompt()
}

func (s *AgentSession) GetCwd() string {
	return s.cwd
}

func (s *AgentSession) GetActiveToolNames() []string {
	toolList := s.Agent.Tools()
	names := make([]string, len(toolList))
	for i, t := range toolList {
		names[i] = t.Name
	}
	return names
}

func (s *AgentSession) GetAllTools() []ToolDefinitionEntry {
	result := make([]ToolDefinitionEntry, 0, len(s.toolDefinitions))
	for _, entry := range s.toolDefinitions {
		result = append(result, entry)
	}
	return result
}

func (s *AgentSession) GetToolDefinition(name string) *extensions.ToolDefinition {
	if entry, ok := s.toolDefinitions[name]; ok {
		return &entry.Definition
	}
	return nil
}

func (s *AgentSession) setActiveToolsByName(toolNames []string) {
	s.baseSystemPrompt = s.rebuildSystemPrompt(toolNames)
	s.Agent.SetSystemPrompt(s.baseSystemPrompt)
	// Map toolNames → AgentTool from the registry and set them on the agent state.
	// Without this the agent carries no structured tool schemas, so the provider
	// sends no `tools` array and the model invents text-based tool calls.
	tools := make([]sdk.AgentTool[any], 0, len(toolNames))
	for _, name := range toolNames {
		if t, ok := s.toolRegistry[name]; ok {
			if at, ok := t.(sdk.AgentTool[any]); ok {
				tools = append(tools, at)
			}
		}
	}
	s.Agent.SetTools(tools)
}

func (s *AgentSession) IsCompacting() bool {
	return s.autoCompactionAbort != nil || s.compactionAbort != nil || s.branchSummaryAbort != nil
}

func (s *AgentSession) GetSessionFile() string {
	return s.SessionManager.GetSessionFile()
}

func (s *AgentSession) GetSessionID() string {
	return s.SessionManager.GetSessionID()
}


func (s *AgentSession) GetScopedModels() []ScopedModelEntry {
	return s.scopedModels
}

func (s *AgentSession) SetScopedModels(models []ScopedModelEntry) {
	s.scopedModels = models
}

func (s *AgentSession) GetPromptTemplates() []PromptTemplate {
	return s.resourceLoader.GetPrompts().Prompts
}

// GetExtensionCommands returns the commands registered by loaded extensions,
// for autocomplete and slash-command UIs. Returns nil when no runner is bound.
func (s *AgentSession) GetExtensionCommands() []*extensions.ResolvedCommand {
	if s.extensionRunner == nil {
		return nil
	}
	return s.extensionRunner.GetRegisteredCommands()
}

// GetSkills returns the skills discovered by the resource loader, for autocomplete
// (the "/skill:<name>" entries) and other UIs. Returns nil when no loader is bound.
func (s *AgentSession) GetSkills() []Skill {
	if s.resourceLoader == nil {
		return nil
	}
	return s.resourceLoader.GetSkills().Skills
}

// GetContextFiles returns the loaded memory/context files (AGENTS.md, CLAUDE.md,
// etc.) from the resource loader. Used by the /context breakdown to attribute
// tokens to the memory-files category. Returns nil when no loader is bound.
func (s *AgentSession) GetContextFiles() []ContextFile {
	if s.resourceLoader == nil {
		return nil
	}
	if r := s.resourceLoader.GetAgentsFiles(); r != nil {
		return r.AgentsFiles
	}
	return nil
}

// CreateReplacedSessionContext builds a fresh command-capable context bound
// to this session, for use by withSession callbacks after session replacement.
// Mirrors TS agent-session.ts createReplacedSessionContext (line 3087).
func (s *AgentSession) CreateReplacedSessionContext() *extensions.ReplacedSessionContext {
	ctx := &extensions.ReplacedSessionContext{
		Actions: &s.extensionCommandContextActions,
		SendMessage: func(msg any) error {
			s.SendCustomMessage("", msg, true, nil, false, "nextTurn")
			return nil
		},
		SendUserMessage: func(content string, images []any) error {
			return s.SendUserMessage(content, images)
		},
	}
	return ctx
}

// ============================================================================
// Helpers
// ============================================================================

func (s *AgentSession) removeFromSteering(text string) bool {
	for i, msg := range s.steeringMessages {
		if msg == text {
			s.steeringMessages = append(s.steeringMessages[:i], s.steeringMessages[i+1:]...)
			return true
		}
	}
	return false
}

func (s *AgentSession) removeFromFollowUp(text string) bool {
	for i, msg := range s.followUpMessages {
		if msg == text {
			s.followUpMessages = append(s.followUpMessages[:i], s.followUpMessages[i+1:]...)
			return true
		}
	}
	return false
}

func makeMapSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func derefOr(ptr *extensions.SessionStartEvent, fallback extensions.SessionStartEvent) extensions.SessionStartEvent {
	if ptr != nil {
		return *ptr
	}
	return fallback
}

func getUserMessageText(msg any) string {
	switch m := msg.(type) {
	case llm.UserMessage:
		var parts []string
		for _, block := range m.Content {
			if tc, ok := block.(llm.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		result := ""
		for _, p := range parts {
			result += p
		}
		return result
	default:
		return ""
	}
}

func getRole(msg any) string {
	switch m := msg.(type) {
	case llm.UserMessage:
		return string(m.Role)
	case llm.AssistantMessage:
		return string(m.Role)
	case llm.ToolResultMessage[any]:
		return string(m.Role)
	default:
		return ""
	}
}

func agentEventToSessionEvent(event sdk.AgentEvent) AgentSessionEvent {
	// Convert core AgentEvent to AgentSessionEvent for listeners.
	// Session-specific events (queue_update, compaction_*, retry_*) are emitted separately.
	switch e := event.(type) {
	case sdk.EventAgentStart:
		return AgentSessionEvent{EventType: "agent_start"}
	case sdk.EventAgentEnd:
		return AgentSessionEvent{EventType: "agent_end"}
	case sdk.EventTurnStart:
		return AgentSessionEvent{EventType: "turn_start"}
	case sdk.EventTurnEnd:
		return AgentSessionEvent{EventType: "turn_end", RetryError: fmt.Sprintf("%v", e.Message)}
	case sdk.EventMessageStart:
		return AgentSessionEvent{EventType: "message_start"}
	case sdk.EventMessageEnd:
		return AgentSessionEvent{EventType: "message_end"}
	case sdk.EventToolExecutionStart:
		return AgentSessionEvent{EventType: "tool_execution_start"}
	default:
		return AgentSessionEvent{EventType: "unknown"}
	}
}

