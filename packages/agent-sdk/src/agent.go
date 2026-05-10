package sdk

import (
	"context"
	"fmt"
	"sync"

	"ycode/packages/llm/src"
)

func defaultConvertToLlm(messages []AgentMessage) ([]llm.Message, error){
	var result []llm.Message
	for _, msg := range messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			result = append(result, m)
		case llm.AssistantMessage:
			result = append(result, m)
		case llm.ToolResultMessage[any]:
			result = append(result, m)
		}
	}
	return result, nil
}

var defaultModel = llm.Model[llm.Api]{
	ID: 		"unknown",
	Name: 		"unknown",
	Api: 		"unknown",
	Provider: 	"unknown",
	BaseURL: 	"unknown",
	Reasoning:  false,
	Input: 		[]llm.InputType{},
	Cost: 		llm.ModelCost{},
	ContextWindow: 0,
	MaxTokens: 	0,
}

// --- Pending Message Queue ---
type PendingMessageQueue struct {
	messages []AgentMessage
	mode 	 QueueMode
}

func NewPendingMessageQueue(mode QueueMode) *PendingMessageQueue {
	return &PendingMessageQueue{
		messages: 	[]AgentMessage{},
		mode: 		mode,
	}
}

func (q *PendingMessageQueue) Enqueue(message AgentMessage) {
	q.messages = append(q.messages, message)
}

func (q *PendingMessageQueue) HasItems() bool {
	return len(q.messages) > 0
}

func (q *PendingMessageQueue) Clear() {
 
}

func (q *PendingMessageQueue) Drain() []AgentMessage {
	if q.mode == QueueModeAll {
		drained := make([]AgentMessage, len(q.messages))
		copy(drained, q.messages)
		q.messages = []AgentMessage{}
		return drained
	}

	if len(q.messages) == 0 {
		return []AgentMessage{}
	}
	first := q.messages[0]
	q.messages = q.messages[1:]
	return []AgentMessage{first}
}

// --- activeRun ---
type activeRun struct {
	cancel context.CancelFunc
	done chan struct{}
}

// --- AgentOptions ---
type AgentOptions struct {
	InititalState   	*PartialAgentState
	ConvertToLlm 		func(messages []AgentMessage) ([]llm.Message, error)
	TransformContext 	func(messages []AgentMessage, signal context.Context) ([]AgentMessage, error)
	StreamFn 			StreamFn
	GetApiKey 			func(provider string) (string, error)
	OnPayload 			func(payload []byte)
	OnResponse 			func(response *llm.StreamOptions)
	BeforeToolCall 		func(ctx BeforeToolCallContext, signal context.Context) (*BeforeToolCallResult, error)
	AfterToolCall 		func(ctx AfterToolCallContext, signal context.Context) (*AfterToolCallResult, error)
	SteeringMode 		QueueMode
	FollowUpMode 		QueueMode
	SessionID 			string
	ThinkingBudgets 	*llm.ThinkingBudgets
	Transport 			llm.Transport
	MaxRetryDelayMs     int
	ToolExecution 		ToolExecutionMode
}

type PartialAgentState struct {
	SystemPrompt 	string
	Model 			*llm.Model[llm.Api]
	ThinkingLevel 	ThinkingLevel
	Tools 			[]AgentTool[any]
	Messages  	  	[]AgentMessage
}


// --- Agent ---

type Agent struct {
	mu sync.RWMutex

	// Mutable state (Alterable State)
	systemPrompt  	 string
	model 			 llm.Model[llm.Api]
	thinkingLevel 	 ThinkingLevel
	tools 			 []AgentTool[any]
	messages 		 []AgentMessage
	isStreaming 	 bool
	streamingMessage AgentMessage
	pendingToolCalls map[string]bool
	errorMessage 	 string

	// Callbacks
	convertToLlm 	 func(messages []AgentMessage) ([]llm.Message, error)
	transformContext func(messages []AgentMessage, signal context.Context) ([]AgentMessage, error)
	streamFn 		 StreamFn
	getApiKey 		 func(provider string) (string, error)
	onPayload 		 func(payload []byte)
	onResponse 		 func(response *llm.StreamOptions)
	beforeToolCall 	 func(ctx BeforeToolCallContext, signal context.Context) (*BeforeToolCallResult, error)
	afterToolCall 	 func(ctx AfterToolCallContext, signal context.Context) (*AfterToolCallResult, error)
	
	// Session config
	sessionID 		 string
	thinkingBudgets  *llm.ThinkingBudgets
	transport 		 llm.Transport
	maxRetryDelayMs  int
	toolExecution 	 ToolExecutionMode

	// Queues
	steeringQueue 	 *PendingMessageQueue
	followUpQueue    *PendingMessageQueue

	// Listeners
	listeners 		 []func(event AgentEvent, signal context.Context) error

	// Active run
	run *activeRun
}

func NewAgent(options AgentOptions) *Agent {
	model := defaultModel
	if options.InititalState != nil && options.InititalState.Model != nil {
		model = *options.InititalState.Model
	}

	thinkingLevel := ThinkingOff
	if options.InititalState != nil && options.InititalState.ThinkingLevel != "" {
		thinkingLevel = options.InititalState.ThinkingLevel
	}

	var tools []AgentTool[any]
	if options.InititalState != nil && options.InititalState.Tools != nil {
		tools = make([]AgentTool[any], len(options.InititalState.Tools))
		copy(tools, options.InititalState.Tools)
	}

	var messages []AgentMessage
	if options.InititalState != nil && options.InititalState.Messages != nil {
		messages = make([]AgentMessage, len(options.InititalState.Messages))
		copy(messages, options.InititalState.Messages)
	}

	convertToLlm := options.ConvertToLlm
	if convertToLlm == nil {
		convertToLlm = defaultConvertToLlm
	}

	steeringMode := options.SteeringMode
	if steeringMode == "" {
		steeringMode = QueueModeOneAtATime
	}

	followUpMode := options.FollowUpMode
	if followUpMode == "" {
		followUpMode = QueueModeOneAtATime
	}

	transport := options.Transport
	if transport == "" {
		transport = "sse"
	}

	toolExecution := options.ToolExecution
	if toolExecution == "" {
		toolExecution = ToolExecutionParallel
	}

	return &Agent{
		systemPrompt: func() string {
			if options.InititalState != nil {
				return options.InititalState.SystemPrompt
			}
			return ""
		}(),
		model: 		   	model,
		thinkingLevel:	thinkingLevel,
		tools: 			tools,
		messages: 		messages,
		isStreaming:  	false,
		pendingToolCalls: make(map[string]bool),

		convertToLlm: 	convertToLlm,
		transformContext: options.TransformContext,
		streamFn: 		options.StreamFn,
		getApiKey: 		options.GetApiKey,
		onPayload: 		options.OnPayload,
		onResponse: 	options.OnResponse,
		beforeToolCall: options.BeforeToolCall,
		afterToolCall:  options.AfterToolCall,

		sessionID: 		options.SessionID,
		thinkingBudgets: options.ThinkingBudgets,
		transport: 		transport,
		maxRetryDelayMs: options.MaxRetryDelayMs,
		toolExecution: 	toolExecution,

		steeringQueue: 	NewPendingMessageQueue(steeringMode),
		followUpQueue: 	NewPendingMessageQueue(followUpMode),
		listeners: 		[]func(event AgentEvent, signal context.Context) error{},
	}
}


// --- Subscribe ---

func (a *Agent) Subscribe(listener func(event AgentEvent, signal context.Context) error) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listeners = append(a.listeners, listener)
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for i, l := range a.listeners {
			if &l == &listener {
				a.listeners = append(a.listeners[:i], a.listeners[i+1:]...)
				break
			}
		}
	}
}

// --- State Getters ---
func (a *Agent) SystemPrompt() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.systemPrompt
}

func (a *Agent) Model() llm.Model[llm.Api] {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

func (a *Agent) ThinkingLevel() ThinkingLevel {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinkingLevel
}

func (a *Agent) Tools() []AgentTool[any] {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tools
}

func (a *Agent) Messages() []AgentMessage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.messages
}

func (a *Agent) IsStreaming() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.isStreaming
}

func (a *Agent) IsStreamingMessage() AgentMessage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.streamingMessage
}

func (a *Agent) PendingToolCalls() map[string]bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.pendingToolCalls
}

func (a *Agent) ErrorMessage() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.errorMessage
}


// --- State Setters ---
func (a *Agent) SetSystemPrompt(prompt string)  {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.systemPrompt = prompt
}

func (a *Agent) SetModel(model llm.Model[llm.Api])  {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
}

func (a *Agent) SetThinkingLevel(level ThinkingLevel)  {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingLevel = level
}

func (a *Agent) SetTools(tools []AgentTool[any]) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = make([]AgentTool[any], len(tools))
	copy(a.tools, tools)
}

func (a *Agent) SetMessages(messages []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = make([]AgentMessage, len(messages))
	copy(a.messages, messages)
}


// --- Queue Mode ---
func (a *Agent) SetSteeringMode(mode QueueMode) {
	a.steeringQueue.mode = mode
}

func (a *Agent) SteeringMode() QueueMode {
	return a.steeringQueue.mode
}

func (a *Agent) SetFollowUpMode(mode QueueMode) {
	a.followUpQueue.mode = mode
}

func (a *Agent) FollowUpMode() QueueMode {
	return a.followUpQueue.mode
}

// --- Queue Operations ---

func (a *Agent) Steer(message AgentMessage) {
	a.steeringQueue.Enqueue(message)
}

func (a *Agent) FollowUp(message AgentMessage) {
	a.followUpQueue.Enqueue(message)
}

func (a *Agent) ClearSteeringQueue() {
	a.steeringQueue.Clear()
}

func (a *Agent) ClearFollowUpQueue() {
	a.followUpQueue.Clear()
}

func (a *Agent) ClearAllQueues() {
	a.ClearSteeringQueue();
	a.ClearFollowUpQueue()
}

func (a *Agent) HasQueuedMessages() bool {
	return a.steeringQueue.HasItems() || a.followUpQueue.HasItems()
}


// --- Abort / Wait --
func (a *Agent) Abort() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run != nil {
		a.run.cancel()
	}
}

func (a *Agent) WaitForIdle() {
	a.mu.RLock()
	run := a.run
	a.mu.RUnlock()
	if run != nil {
		<-run.done
	}
}


// --- Reset ---
func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = []AgentMessage{}
	a.isStreaming = false
	a.streamingMessage = nil
	a.pendingToolCalls = make(map[string]bool)
	a.errorMessage = ""
	a.followUpQueue.Clear()
	a.steeringQueue.Clear()
}

// --- Prompt ---
func (a *Agent) Prompt(ctx context.Context, input interface{}, images ...llm.ImageContent) error {
	a.mu.Lock()
	if a.run != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing a prompt. Use Steer() or FollowUp() to queue messages, or wait for comppletion")
	}
	a.mu.Unlock()

	messages := a.normalizePromptInput(input, images)
	return a.runPromptMessages(ctx, messages)
}

// --- Continue ---

func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.run != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing. Wait for completion before continuing")
	}
	a.mu.Unlock()

	a.mu.RLock()
	lastIdx := len(a.messages) - 1
	a.mu.RUnlock()

	if lastIdx < 0 {
		return fmt.Errorf("no messages to continue from")
	}
	a.mu.RLock()
	lastMessage := a.messages[lastIdx]
	a.mu.RUnlock()

	if msg, ok := lastMessage.(llm.AssistantMessage); ok && msg.Role == llm.RoleAssistant {
		queuedSteering := a.steeringQueue.Drain()
		if len(queuedSteering) > 0 {
			return a.runPromptMessagesSkipSteering(ctx, queuedSteering)
		}
		queuedFollowUps := a.followUpQueue.Drain()
		if len(queuedFollowUps) > 0 {
			return a.runPromptMessages(ctx, queuedFollowUps)
		}
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	return a.runContinuation(ctx)
}

