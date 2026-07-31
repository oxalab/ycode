package sdk

import (
	"context"

	"ycode/packages/llm/src"
)

// ThinkingLevel: Represents the reasoning/intensity level for models that support it
type ThinkingLevel string

const (
	ThinkingOff 		ThinkingLevel = "off"
	ThinkingMinimal 	ThinkingLevel = "minimal"
	ThinkingLow 		ThinkingLevel = "low"
	ThinkingMedium 		ThinkingLevel = "medium"
	ThinkingHigh 		ThinkingLevel = "high"
	ThinkingXHigh 		ThinkingLevel = "xhigh"
)

// ToolExecutionMode: Defines how tool calls are executed
type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel 	ToolExecutionMode = "parallel"
)

// QueueMode: Defines how queued messages are drained
type QueueMode string

const (
	QueueModeAll 		QueueMode = "all"
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// AgentMessage: Union type for all message types in the agent system.
// In Go, we use the Message type from the llm package, which can be extended
// by apps using their own types that embed the standard types
type AgentMessage = llm.Message

// AgentToolResult: Result returned from a tool execution
type AgentToolResult[T any] struct {
	// Content returned to the model
	Content []llm.ContentBlock
	// Arbitary structured details for logs or UI rendering
	Details T
	// Hint that the agent should stop after the current tool batch.
	// Early termination only happens when every finalized tool result in the basets this to true
	Terminate bool
}

// AgentToolUpdateCallback: used by tools to stream partial execution updates
type AgentToolUpdateCallback[T any] func(partialResult AgentToolResult[T])

// AgentTool: Defines a tool that can be called by the agent
type AgentTool[T any] struct {
	// Tool Name (must match schema name)
	Name string
	// Human-readable label for UI display
	Label string
	// Tool Description (for the LLM)
	Description string
	// JSON schema for parameters (can be TypeBox schema converted to JSON)
	Parameters map[string]any
	// Optional compatibility shim for raw tool-call arguments before schema validation.
	// Must return an object that matches Parameters.
	PrepareArguments func(args any) (any, error)
	// Execute the tool call. Return an error on failure (don't encode errors in Content)
	Execute func(
		toolCallID string,
		params any,
		signal context.Context,
		onUpdate AgentToolUpdateCallback[T],
	) (AgentToolResult[T], error)
	// Per-tool execution mode override. If omitted, the default execution mode applies.
	ExecutionMode ToolExecutionMode
}

// BeforeToolCallResult is returned from BeforeToolCall hook
type BeforeToolCallResult struct{
	// Block prevents the tool from executing. The loop emits an error tool result instead.
	Block 	bool
	Reason string // If omitted, a default blocked message is used
}

// AfterToolCallResult is returned from AfterToolCall hook
type AfterToolCallResult struct {
	// Content replaces the tool result content array if provided
	Content []llm.ContentBlock
	// Details replaced the tool result details value if provided
	Details any
	// IsError replaces the tool result error flag if provided
	IsError *bool
	// Terminate replaces the early-termination hint if provided
	Terminate *bool
}

// BeforeToolCallContext is passed to BeforeToolCall hook 
type BeforeToolCallContext struct {
	AssistantMessage 	llm.AssistantMessage
	ToolCall 			llm.ToolCall
	Args 				any
	Context 			AgentContext
}

// AfterToolCallContext is passed to AfterToolCall hook
type AfterToolCallContext struct {
	AssistantMessage 	llm.AssistantMessage
	ToolCall 			llm.ToolCall
	Args 				any
	Result 				AgentToolResult[any]
	IsError 			bool
	Context 			AgentContext
}

// AgentContext: Snapshot of the agent state passed to the loop
type AgentContext struct {
	SystemPrompt string
	Messages 	 []AgentMessage
	Tools 		 []AgentTool[any]
}

// AgentLoopConfig: Configures the agent loop behavior
type AgentLoopConfig struct {
	// Model to use for inference
	Model 	llm.Model[llm.Api]

	// Reasoning Level for model that support it
	Reasoning 	ThinkingLevel
	// Optional session identifier forwarded to providers for cache-aware backend
	SessionID 	string
	// Optional per-level thinking token budgets forwarded to the stream function
	ThinkingBudget 	*llm.ThinkingBudgets
	// Preferred transport forwarded to the stream function
	Transport 	llm.Transport
	// Optional cap for provider-requested retry delays
	MaxRetryDelayMs  int
	// Tool execution mode
	ToolExecution 	ToolExecutionMode
	// Converts AgentMessage[] to LLM-compatible Message[] before each LLM call
	ConvertToLlm 	func(messages []AgentMessage) ([]llm.Message, error)
	// Optional transform applied to context before ConvertToLlm
	TransformContext func(messages []AgentMessage, signal context.Context) ([]AgentMessage, error)
	// Resolves an API key dynamically for each LLM call
	GetApiKey func(provider string) (string, error)
	// Optional: Returns steering messages to inject into the conversation mid-run
	GetSteeringMessages func() ([]AgentMessage, error)
	// Optional: Returns follow-up messages to process after the agent would otherwise stop
	GetFollowUpMessages func() ([]AgentMessage, error)
	// Called before a tool is executed, after arguments have been validated
	BeforeToolCall func(ctx BeforeToolCallContext, signal context.Context) (*BeforeToolCallResult, error)
	// Called after a tool finishes executing, before tool_execution_end and message events are emitted
	AfterToolCall func(ctx AfterToolCallContext, signal context.Context) (*AfterToolCallResult, error)
	// Optional callbacks forwarded to the stream function
	OnPayload func(payload []byte)
	OnResponse func(response *llm.StreamOptions)
}

// StreamFn is the function type for streaming LLM response 
type StreamFn func(
    ctx context.Context,
    model llm.Model[llm.Api],
    chatCtx llm.ChatContext,
    options *llm.SimpleStreamOptions,
) <-chan llm.AssistantMessageEvent

// AgentEvent: Represents all event emitted by the agent
type AgentEvent interface {
	agentEvent()
}

// Event Implementation
type EventAgentStart  struct{}

func (e EventAgentStart) agentEvent() {}

type EventAgentEnd struct {
	Messages 	[]AgentMessage
}

func (e EventAgentEnd) agentEvent() {}

type EventTurnStart struct{}

func (e EventTurnStart) agentEvent() {}

type EventTurnEnd struct {
	Message 	AgentMessage
	ToolResults []llm.ToolResultMessage[any]
}

func (e EventTurnEnd) agentEvent() {}

type EventMessageStart struct {
	Message AgentMessage
}

func (e EventMessageStart) agentEvent() {}

type EventMessageUpdate struct {
	Message 			AgentMessage
	AssistantMessageEvent 	llm.AssistantMessageEvent
}

func (e EventMessageUpdate) agentEvent() {}

type EventMessageEnd struct {
	Message AgentMessage
}

func (e EventMessageEnd) agentEvent() {}

type EventToolExecutionStart struct {
	ToolCallID 	string
	ToolName 	string
	Args 		any
}

func (e EventToolExecutionStart) agentEvent() {}

type EventToolExecutionUpdate struct {
	ToolCallID 	string
	ToolName 	string
	Args 	 	any
	PartialResult any
}

func (e EventToolExecutionUpdate) agentEvent() {}

type EventToolExecutionEnd struct {
	ToolCallID 	string
	ToolName 	string
	Result 		any
	IsError 	bool
}

func (e EventToolExecutionEnd) agentEvent() {}

// AgentState represents the public agent state and lifecycle control.
// All methods on this interface are implemented by *Agent.
type AgentState interface {
	// --- State accessors ---

	// SystemPrompt returns the system prompt sent with each model request
	SystemPrompt() string
	// SetSystemPrompt sets the system prompt
	SetSystemPrompt(prompt string)
	// Model returns the active model
	Model() llm.Model[llm.Api]
	// SetModel sets the active model
	SetModel(model llm.Model[llm.Api])
	// ThinkingLevel returns the reasoning level
	ThinkingLevel() ThinkingLevel
	// SetThinkingLevel sets the reasoning level
	SetThinkingLevel(level ThinkingLevel)
	// Tools returns available tools
	Tools() []AgentTool[any]
	// SetTools sets available tools (copies the array)
	SetTools(tools []AgentTool[any])
	// Messages returns the conversation transcript
	Messages() []AgentMessage
	// SetMessages sets the conversation transcript (copies the array)
	SetMessages(messages []AgentMessage)
	// SetBeforeToolCall installs a pre-tool-call hook (nil clears it).
	// Extensions use this to observe or block tool calls.
	SetBeforeToolCall(fn func(ctx BeforeToolCallContext, signal context.Context) (*BeforeToolCallResult, error))
	// SetAfterToolCall installs a post-tool-call hook (nil clears it).
	// Extensions use this to observe or modify tool results.
	SetAfterToolCall(fn func(ctx AfterToolCallContext, signal context.Context) (*AfterToolCallResult, error))
	// IsStreaming returns true while agent is processing
	IsStreaming() bool
	// IsStreamingMessage returns the partial assistant message if streaming
	IsStreamingMessage() AgentMessage
	// PendingToolCalls returns the tool call IDs currently executing
	PendingToolCalls() map[string]bool
	// ErrorMessage returns the error from the most recent failed/aborted turn
	ErrorMessage() string

	// --- Lifecycle ---

	// Prompt sends a new user prompt to the agent and starts processing.
	Prompt(ctx context.Context, input interface{}, images ...llm.ImageContent) error
	// Continue resumes the agent loop after an abort or error.
	Continue(ctx context.Context) error
	// Abort cancels the current agent run.
	Abort()
	// WaitForIdle blocks until the agent finishes processing.
	WaitForIdle()

	// --- Queue operations ---

	// Steer enqueues a steering message to inject mid-run.
	Steer(message AgentMessage)
	// FollowUp enqueues a follow-up message to process after the run would stop.
	FollowUp(message AgentMessage)
	// SetSteeringMode sets the drain mode for the steering queue.
	SetSteeringMode(mode QueueMode)
	// SteeringMode returns the current steering queue mode.
	SteeringMode() QueueMode
	// SetFollowUpMode sets the drain mode for the follow-up queue.
	SetFollowUpMode(mode QueueMode)
	// FollowUpMode returns the current follow-up queue mode.
	FollowUpMode() QueueMode
	// HasQueuedMessages returns true if either queue has pending items.
	HasQueuedMessages() bool
	// ClearAllQueues removes all pending steering and follow-up messages.
	ClearAllQueues()

	// --- Subscription ---

	// Subscribe registers an event listener and returns an unsubscribe function.
	Subscribe(listener func(event AgentEvent, signal context.Context) error) func()

	// --- Reset ---

	// Reset clears all messages, queues, and streaming state.
	Reset()
}

// AgentEventSink is the callback type for emitting agent events
type AgentEventSink func(event AgentEvent) error

// EventStream is used to return a stream of event from agent functions
type EventStream struct {
	events 	 chan AgentEvent
	result   chan []AgentMessage
	error 	 chan error
	done   	 chan struct{}
	isDone 	 bool
	endValue []AgentMessage
	endError error
}

// NewEventStream creates a new event stream
func NewEventStream() *EventStream {
	return &EventStream {
		events: make(chan AgentEvent, 100),
		result: make(chan []AgentMessage, 1),
		error:  make(chan error, 1),
		done:   make(chan struct{}),
	}
}

// Push emits an event to the stream
func (s *EventStream) Push(event AgentEvent) {
	if !s.isDone { 
		s.events <- event
	}
}

// End closes the stream with a final result
func(s *EventStream) End(messages []AgentMessage) {
	if !s.isDone {
		s.isDone = true
		s.endValue = messages
		close(s.events)
		s.result <- messages
		close(s.result)
		close(s.done)
	}
}

// EndError closes the stream with an error
func (s *EventStream) EndError(err error) {
	if !s.isDone {
		s.isDone = true
		s.endError = err
		close(s.events)
		s.error <- err
		close(s.error)
		close(s.done)
	}
}

// Events returns the channel of events 
func (s *EventStream) Events() <- chan AgentEvent {
	return s.events
}

// Result returns the final result, blocking until the stream ends
func (s *EventStream) Result() ([]AgentMessage, error) {
	<- s.done
	if s.endError != nil {
		return nil, s.endError
	}
	return s.endValue, nil
}

// Done returns a channel that closes when the stream ends
func (s *EventStream) Done() <- chan struct{} {
	return s.done
}

// IsDone returns true if the stream has ended
func (s *EventStream) IsDone() bool {
	return s.isDone
}

// Helper functions for creating events
func NewAgentStartEvent() AgentEvent { return EventAgentStart{} }
func NewAgentEndEvent(messages []AgentMessage) AgentEvent { 
	return EventAgentEnd{Messages: messages}
}
func NewTurnStartEvent() AgentEvent { return EventTurnStart{} }
func NewTurnEndEvent(msg AgentMessage, results []llm.ToolResultMessage[any]) AgentEvent {
	return EventTurnEnd{Message: msg, ToolResults: results}
}

func NewMessageStartEvent(msg AgentMessage) AgentEvent {
	return EventMessageStart{Message: msg}
}
func NewMessageUpdateEvent(msg AgentMessage, evt llm.AssistantMessageEvent) AgentEvent{
    return EventMessageUpdate{Message: msg, AssistantMessageEvent: evt}
}
func NewMessageEndEvent(msg AgentMessage) AgentEvent { 
	return EventMessageEnd{Message: msg} 
}
func NewToolExecutionStartEvent(toolCallID, toolName string, args any) AgentEvent {
    return EventToolExecutionStart{ToolCallID: toolCallID, ToolName: toolName, Args: args }
}
func NewToolExecutionUpdateEvent(toolCallID, toolName string, args, partial any) AgentEvent {
    return EventToolExecutionUpdate{ToolCallID: toolCallID, ToolName: toolName, Args: args, PartialResult: partial}
}
func NewToolExecutionEndEvent(toolCallID, toolName string, result any, isError bool) AgentEvent {
    return EventToolExecutionEnd{ToolCallID: toolCallID, ToolName: toolName, Result: result, IsError: isError}
}