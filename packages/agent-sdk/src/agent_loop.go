package sdk

import (
	"context"
	"fmt"

	"ycode/packages/llm/src"
)

// AgentLoop: Starts an Agent loop with a new prompt message
func AgentLoop(
	prompts 	[]AgentMessage,
	context 	AgentContext,
	config 		AgentLoopConfig,
	signal 		context.Context,
	streamFn 	StreamFn,
) *EventStream {
	stream := NewEventStream()
	go func() {
		messages, err := RunAgentLoop(prompts, context, config, func(event AgentEvent) error {
			stream.Push(event)
			return nil
		}, signal, streamFn)
		if err != nil {
			stream.EndError(err)
			return
		}
		stream.End(messages)
	}()
	return stream
}

// AgentLoopContinue: continues and agent loop from the current context
func AgentLoopContinue(
	context 	AgentContext,
	config 		AgentLoopConfig,
	signal 		context.Context,
	streamFn 	StreamFn,
) *EventStream {
	if len(context.Messages) == 0 {
		stream := NewEventStream()
		stream.EndError(fmt.Errorf("cannot continue: no messages in context"))
		return stream
	}
	lastMessage := context.Messages[len(context.Messages)-1]
	if msg, ok := lastMessage.(llm.AssistantMessage); ok && msg.Role == llm.RoleAssistant {
		stream := NewEventStream()
		stream.EndError(fmt.Errorf("cannot continue from message role: assistant"))
		return stream
	}
	stream := NewEventStream()
	go func() {
		messages, err := RunAgentLoopContinue(context, config, func(event AgentEvent) error {
			stream.Push(event)
			return nil
		},signal, streamFn)
		if err != nil {
			stream.EndError(err)
			return
		}
		stream.End(messages)
	} ()
	return stream
}

// RunAgentLoop: Runs the agent loop with new prompt
func RunAgentLoop(
	prompts 	[]AgentMessage,
	context 	AgentContext,
	config 		AgentLoopConfig, 
	emit 		func(AgentEvent) error,
	signal 		context.Context,
	streamFn 	StreamFn,
) ([]AgentMessage, error) {
	newMessages := make([]AgentMessage, len(prompts))
	copy(newMessages, prompts)

	currentContext := AgentContext{
		SystemPrompt: context.SystemPrompt,
		Messages: 	  append([]AgentMessage{}, context.Messages...),
		Tools: 	      append([]AgentTool[any]{}, context.Tools...),
	}
	currentContext.Messages = append(currentContext.Messages, prompts...)
	emit(NewAgentStartEvent())
	emit(NewTurnStartEvent())
	for _, prompt := range prompts {
		emit(NewMessageStartEvent(prompt))
		emit(NewMessageEndEvent(prompt))
	}
	if err := runLoop(&currentContext, &newMessages, config, signal, emit, streamFn); err != nil {
		return nil, err
	}
	emit(NewAgentEndEvent(newMessages))
	return newMessages, nil
}

// RunAgentLoopContinue: Runs the agent loop from existing context
func RunAgentLoopContinue(
	context AgentContext,
	config AgentLoopConfig,
	emit func(AgentEvent) error,
	signal context.Context,
	streamFn StreamFn,
) ([]AgentMessage, error) {
	if len(context.Messages) == 0 {
		return nil, fmt.Errorf("Cannot continue: no messages in context")
	}
	lastMessage := context.Messages[len(context.Messages)-1]
	if msg, ok := lastMessage.(llm.AssistantMessage); ok && msg.Role == llm.RoleAssistant {
		return nil, fmt.Errorf("cannot continue from message role: assistant")
	}
	newMessages := []AgentMessage{}
	currentContext := AgentContext{
		SystemPrompt: context.SystemPrompt,
		Messages:	  append([]AgentMessage{}, context.Messages...),
		Tools: 		  append([]AgentTool[any]{}, context.Tools...),
	}
	emit(NewAgentStartEvent())
	emit(NewTurnStartEvent())

	if err := runLoop(&currentContext, &newMessages, config, signal, emit, streamFn); err != nil {
		return nil, err
	}
	emit(NewAgentEndEvent(newMessages))
	return newMessages, nil
}

// runLoop: main loop logic
func runLoop(
	currentContext 	*AgentContext,
	newMessages 	*[]AgentMessage,
	config 			AgentLoopConfig,
	signal 			context.Context,
	emit 			func(AgentEvent) error,
	streamFn 		StreamFn,
) error {
	firstTurn := true
	// Check for steering messages at start (intent)
	pendingMessages := []AgentMessage{}
	if config.GetSteeringMessages != nil {
		if msgs, err := config.GetSteeringMessages(); err == nil && len(msgs) > 0 {
			pendingMessages = msgs
		}
	}

	for {
		hasMoreToolCalls := true

		// InnerLoop: Process tool calls and steering messages
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if !firstTurn {
				emit(NewTurnStartEvent())
			} else {
				firstTurn = false
			}

			// Process pending messages
			if len(pendingMessages) > 0 {
				for _, message := range pendingMessages {
					emit(NewMessageStartEvent(message))
					emit(NewMessageEndEvent(message))
					currentContext.Messages = append(currentContext.Messages, message)
				}
				pendingMessages = []AgentMessage{}
			}

			// Stream Assistant response
			message, err := streamAssistantResponse(*currentContext, config, signal, emit, streamFn)
			if err != nil {
				// Handle error
				errorMsg := createErrorMessage(config, err.Error(), signal)
				*newMessages = append(*newMessages, errorMsg)
				emit(NewTurnEndEvent(errorMsg, []llm.ToolResultMessage[any]{}))
				emit(NewAgentEndEvent(*newMessages))
				return nil
			}
			*newMessages = append(*newMessages, message)
			// The assistant message (with its tool_calls) must join the loop
			// context — streamAssistantResponse mutates a pass-by-value copy,
			// so without this every follow-up request sends orphaned tool
			// results with no preceding assistant tool_calls (the TS reference
			// pushes the partial into context.messages by reference,
			// agent-loop.ts:313-346).
			currentContext.Messages = append(currentContext.Messages, message)
			if message.StopReason == llm.StopReasonError || message.StopReason == llm.StopReasonAborted {
				emit(NewTurnEndEvent(message, []llm.ToolResultMessage[any]{}))
				emit(NewAgentEndEvent(*newMessages))
				return nil
			}

			// Check for tool calls
			toolCalls := extractToolCalls(message)
			toolResults := []llm.ToolResultMessage[any]{}
			hasMoreToolCalls = false
			if len(toolCalls) > 0 {
				batch, err := executeToolCalls(*currentContext, message, toolCalls, config, signal, emit)
				if err != nil {
					// Tool Execution error - continue with error results
				}
				toolResults = append(toolResults, batch.Messages...)
				hasMoreToolCalls = !batch.Terminate

				for _, result := range toolResults {
					currentContext.Messages = append(currentContext.Messages, result)
					*newMessages = append(*newMessages, result)
				}
			}
			emit(NewTurnEndEvent(message, toolResults))

			pendingMessages = []AgentMessage{}
			if config.GetSteeringMessages != nil {
				if msgs, err := config.GetSteeringMessages(); err == nil && len(msgs) > 0 {
					pendingMessages = msgs
				}
			}
		}

		// Check for follow up messages
		if config.GetFollowUpMessages != nil {
			if followUps, err := config.GetFollowUpMessages(); err == nil && len(followUps) > 0 {
				pendingMessages = followUps
				continue
			}
		}
		break
	}
	return nil
}

// streamAssistantResponse: Streams an assistant response from the LLM
func streamAssistantResponse(
	context AgentContext,
	config AgentLoopConfig,
	signal context.Context,
	emit func(AgentEvent) error,
	streamFn StreamFn,
) (llm.AssistantMessage, error) {
	// Apply Context Transform
	messages := context.Messages
	if config.TransformContext != nil {
		transformed, err := config.TransformContext(messages, signal)
		if err == nil && len(transformed) > 0 {
			messages = transformed
		}
	}

	// Convert to LLM-compatible messages
	llmMessages, err := config.ConvertToLlm(messages)
	if err != nil {
		return llm.AssistantMessage{}, err
	}

	// Build LLM Context
	llmContext := llm.ChatContext{
		SystemPrompt: context.SystemPrompt,
		Messages: 	  llmMessages,
		Tools: 	      convertTools(context.Tools),
	}
	
	// Use provided streamFn or get default from registry
	streamFunc := streamFn
	if streamFunc == nil {
		// Get stream function from provider registry
		// TODO: Integrate with llm.GetStreamFunction or similar
		return llm.AssistantMessage{}, fmt.Errorf("streamFn not provided and default not implemented")
	}

	// Resolve API key
	apiKey := ""
	if config.GetApiKey != nil {
		if key, err := config.GetApiKey(string(config.Model.Provider)); err == nil {
			apiKey = key
		}
	}

	// Build stream options
	options := llm.SimpleStreamOptions{
		StreamOptions: llm.StreamOptions {
			Context: signal,
			APIKey:  apiKey,
		},
	}

	// Set reasoning if enabled
	if config.Reasoning != ThinkingOff {
		options.Reasoning = llm.ThinkingLevel(config.Reasoning)
	}

	// Call stream function
	response := streamFunc(signal, config.Model, llmContext, &options)

	var partialMessage *llm.AssistantMessage
	addedPartial := false
	
	// Process streaming events
	for event := range response {
		switch event.Type {
		case "start":
			partialMessage = event.Partial
			context.Messages = append(context.Messages, *partialMessage)
			addedPartial = true
			emit(NewMessageStartEvent(*partialMessage))
		case "text_start", "text_delta", "text_end",
			"thinking_start", "thinking_delta", "thinking_end",
			"toolcall_start", "toolcall_delta", "toolcall_end":
			if partialMessage != nil {
				partialMessage = event.Partial
				context.Messages[len(context.Messages)-1] = *partialMessage
				emit(NewMessageUpdateEvent(*partialMessage, event))
			}
		case "done", "error":
			var finalMessage llm.AssistantMessage
			if event.Message != nil {
				finalMessage = *event.Message
			} else if event.Error != nil {
				finalMessage = *event.Error
			}
			if addedPartial {
				context.Messages[len(context.Messages)-1] = finalMessage
			} else {
				context.Messages = append(context.Messages, finalMessage)
				emit(NewMessageStartEvent(finalMessage))
			}
			emit(NewMessageEndEvent(finalMessage))
			return finalMessage, nil
		}
	}
	return llm.AssistantMessage{}, fmt.Errorf("stream ended without done.error/event")
	// Fallback: Get result from stream
}

// ExecuteToolCallBatch: Represents the result of executing a batch of tool calls
type ExecutedToolCallBatch struct {
	Messages 	[]llm.ToolResultMessage[any]
	Terminate 	bool
}

// executeToolCalls: Represents the result of executing a batch of tool calls
func executeToolCalls(
	context  AgentContext,
	assistantMessage llm.AssistantMessage,
	toolCalls []llm.ToolCall,
	config AgentLoopConfig,
	signal context.Context,
	emit func(AgentEvent) error,
) (ExecutedToolCallBatch, error) {
	hasSequentialToolCall := false
	for _, tc := range toolCalls {
		for _, tool := range context.Tools {
			if tool.Name == tc.Name && tool.ExecutionMode == ToolExecutionSequential {
				hasSequentialToolCall = true
				break
			}
		}
	}

	if config.ToolExecution == ToolExecutionSequential || hasSequentialToolCall {
		return executeToolCallsSequential(context, assistantMessage, toolCalls, config, signal, emit)
	}
	return executeToolCallsParallel(context, assistantMessage, toolCalls, config, signal, emit)
}

// executeToolCallsSequential: Executes tool calls one at a time
func executeToolCallsSequential(
	context AgentContext,
	assistantMessage llm.AssistantMessage,
	toolCalls []llm.ToolCall,
	config AgentLoopConfig,
	signal context.Context,
	emit func(AgentEvent) error,
) (ExecutedToolCallBatch, error) {
	var finalizedCalls []finalizedToolCallOutcome
	var messages []llm.ToolResultMessage[any]

	for _, toolCall := range toolCalls {
		emit(NewToolExecutionStartEvent(toolCall.ID, toolCall.Name, toolCall.Arguments))
		
		preparation := prepareToolCall(context, assistantMessage, toolCall, config, signal)
		var finalized finalizedToolCallOutcome

		if preparation.Kind == preparationImmediate {
			finalized = finalizedToolCallOutcome{
				ToolCall: preparation.ToolCall,
				Result:   preparation.Result,
				IsError:  preparation.IsError,
			}	
		} else {
			tool := findTool(context.Tools, toolCall.Name)
			prepped := preparedToolCall{
				ToolCall: toolCall,
				Tool: *tool,
				Args: toolCall.Arguments,
			}
			executed := executePreparedToolCall(prepped, signal, emit)
			finalized = finalizeExecutedToolCall(context, assistantMessage, prepped, executed, config, signal)
		}

		// Execute concurrently
		emitToolExecutionEnd(finalized, emit)
		toolResultMessage := createToolResultMessage(finalized)
		emitToolResultMessage(toolResultMessage, emit)
		finalizedCalls = append(finalizedCalls, finalized)
		messages = append(messages, toolResultMessage)
	}
	return ExecutedToolCallBatch {
		Messages: messages,
		Terminate: shouldTerminateToolBatch(finalizedCalls),
	}, nil
}


func executeToolCallsParallel(
	context AgentContext,
	assistantMessage llm.AssistantMessage,
	toolCalls []llm.ToolCall,
	config AgentLoopConfig,
	signal context.Context,
	emit func(AgentEvent) error,
) (ExecutedToolCallBatch, error) {
	type futureResult struct {
		value finalizedToolCallOutcome
	}
	futures := make([]chan futureResult, len(toolCalls))
	for i, toolCall := range toolCalls {
		emit(NewToolExecutionStartEvent(toolCall.ID, toolCall.Name,toolCall.Arguments))
		futures[i] = make(chan futureResult, 1)

		preparation := prepareToolCall(context, assistantMessage, toolCall, config, signal)
		if preparation.Kind == preparationImmediate {
			finalized := finalizedToolCallOutcome{
				ToolCall: preparation.ToolCall,
				Result:   preparation.Result,
				IsError:  preparation.IsError,
			}
			emitToolExecutionEnd(finalized, emit)
			futures[i] <- futureResult{value: finalized}
			continue
		}

		prepped := preparedToolCall{
			ToolCall: preparation.ToolCall,
			Tool: 	  preparation.Tool,
			Args: 	  preparation.Args,
		}
		go func(idx int, prep preparedToolCall){
			executed := executePreparedToolCall(prep, signal, emit)
			finalized := finalizeExecutedToolCall(context, assistantMessage, prep, executed, config, signal)
			emitToolExecutionEnd(finalized,emit)
			futures[idx] <- futureResult{value: finalized}
		}(i, prepped)
	}
	var finalizedCalls []finalizedToolCallOutcome
	var messages []llm.ToolResultMessage[any]
	for _, future := range futures {
		result := <- future
		finalizedCalls = append(finalizedCalls, result.value)
		toolResultMessage := createToolResultMessage(result.value)
		emitToolResultMessage(toolResultMessage, emit)
		messages = append(messages, toolResultMessage)
	}
	return ExecutedToolCallBatch{
		Messages: messages,
		Terminate: shouldTerminateToolBatch(finalizedCalls),
	}, nil
}

// Internal Types for tool execution
type preparedToolCall struct {
	Kind string
	ToolCall llm.ToolCall
	Tool AgentTool[any]
	Args any
}

type immediateToolCallOutcome struct {
	Kind string
	ToolCall llm.ToolCall
	Tool AgentTool[any]
	Args any
	Result AgentToolResult[any]
	IsError bool
}

type executedToolCallOutcome struct {
	Result AgentToolResult[any]
	IsError bool
}

type finalizedToolCallOutcome struct {
	ToolCall llm.ToolCall
	Result AgentToolResult[any]
	IsError bool
}

const (
	preparationKind = "prepared"
	preparationImmediate = "immediate"
)

func shouldTerminateToolBatch(finalizedCalls []finalizedToolCallOutcome) bool {
	if len(finalizedCalls) == 0 {
		return false
	}
	for _, fc := range finalizedCalls {
		if !fc.Result.Terminate {
			return false
		}
	}
	return true
}

func prepareToolCall(
	context AgentContext,
	assistantMessage llm.AssistantMessage,
	toolCall llm.ToolCall,
	config AgentLoopConfig,
	signal context.Context,
) immediateToolCallOutcome {
	// Find tool
	var tool *AgentTool[any]
	for i := range context.Tools {
		if context.Tools[i].Name == toolCall.Name {
			tool = &context.Tools[i]
			break
		}
	}

	if tool == nil {
		return immediateToolCallOutcome{
			Kind: preparationImmediate,
			ToolCall: toolCall,
			Result: AgentToolResult[any]{
				Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: fmt.Sprintf("Tool %s not found",toolCall.Name)}},
				Details: struct{}{},
			},
			IsError: true,
		}
	}

	// Validate Arguments
	var args any = toolCall.Arguments
	if tool.PrepareArguments != nil {
		prepared, err := tool.PrepareArguments(toolCall.Arguments)
		if err != nil {
			return immediateToolCallOutcome{
				Kind:    preparationImmediate,
				ToolCall: toolCall,
				Result: AgentToolResult[any]{
					Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: err.Error()}},
					Details: struct{}{},
				},
				IsError: true,
			}
		}
		args = prepared
	}

	// Call beforeToolCall hook
	if config.BeforeToolCall != nil {
		beforeCtx := BeforeToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall: toolCall,
			Args: args,
			Context: context,
		}
		result, err := config.BeforeToolCall(beforeCtx, signal)
		if err != nil {
			return immediateToolCallOutcome{
				Kind: 	preparationImmediate,
				ToolCall: toolCall,
				Result: AgentToolResult[any]{
					Content: []llm.ContentBlock{llm.TextContent{Type:"text", Text: err.Error()}},
					Details: struct{}{},
				},
				IsError: true,
			}
		}
		if result != nil && result.Block {
			return immediateToolCallOutcome{
				Kind: preparationImmediate,
				ToolCall: toolCall,
				Result: AgentToolResult[any]{
					Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: result.Reason}},
					Details: struct{}{},
				},
				IsError: true,
			}
		}
	}
	return immediateToolCallOutcome{
		Kind: preparationKind,
		ToolCall: toolCall,
		Tool: *tool,
		Args: args,
	}
}

func executePreparedToolCall(
	prep preparedToolCall,
	signal context.Context,
	emit func(AgentEvent) error,
) executedToolCallOutcome {
	result, err := prep.Tool.Execute(
		prep.ToolCall.ID,
		prep.Args,
		signal,
		func(partial AgentToolResult[any]) {
			// Live emission matches the reference, where onUpdate fires during
			// execution (not after the tool settles). The parallel path already
			// emits ToolExecutionEnd from tool goroutines, so this adds no new
			// concurrency class.
			_ = emit(NewToolExecutionUpdateEvent(prep.ToolCall.ID, prep.ToolCall.Name, prep.Args, partial))
		},
	)
	if err != nil {
		return executedToolCallOutcome{
			Result: AgentToolResult[any]{
				Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: err.Error()}},
				Details: struct{}{},
			},
			IsError: true,
		}
	}
	return executedToolCallOutcome{
		Result:  result,
		IsError: false,
	}
}



func finalizeExecutedToolCall(
	context AgentContext,
	assistantMessage llm.AssistantMessage,
	prep preparedToolCall,
	executed executedToolCallOutcome,
	config AgentLoopConfig,
	signal context.Context,
) finalizedToolCallOutcome{
	result := executed.Result
	isError := executed.IsError

	if config.AfterToolCall != nil {
		afterCtx := AfterToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall: 		  prep.ToolCall,
			Args: 			  prep.Args, // Would be set in prepared path
			Result: 		  result,
			IsError: 		  isError,
			Context: 		  context,
		}
		afterResult, err := config.AfterToolCall(afterCtx, signal)
		if err == nil && afterResult != nil  {
			if afterResult.Content != nil {
				result.Content = afterResult.Content
			}
			if afterResult.Details != nil {
				result.Details = afterResult.Details
			}
			if afterResult.IsError != nil { 
				isError = *afterResult.IsError
			}
			if afterResult.Terminate != nil {
				result.Terminate = *afterResult.Terminate
			}
		}
	}
	return finalizedToolCallOutcome{
		ToolCall: prep.ToolCall,
		Result:   result,
		IsError:  isError,
	}
}


func createToolResultMessage(finalized finalizedToolCallOutcome) llm.ToolResultMessage[any] {
	details := finalized.Result.Details
	return llm.ToolResultMessage[any] {
		Role: llm.RoleTool,
		ToolCallID: finalized.ToolCall.ID,
		ToolName: finalized.ToolCall.Name,
		Content: finalized.Result.Content,
		Details: &details,
		IsError: finalized.IsError,
		Timestamp: 0,  // Set by caller
	}
}

func emitToolExecutionEnd(finalized finalizedToolCallOutcome, emit func(AgentEvent) error){
	// Result stays Content (the output blocks the TUI's extractResultText
	// reads); Details rides in its own field so consumers can pick up typed
	// extras (e.g. the edit diff) — it is nil for most tools.
	emit(NewToolExecutionEndEvent(finalized.ToolCall.ID, finalized.ToolCall.Name, finalized.Result.Content, finalized.Result.Details, finalized.IsError))
}

func emitToolResultMessage(toolResultMessage llm.ToolResultMessage[any], emit func(AgentEvent) error) {
	emit(NewMessageStartEvent(toolResultMessage))
	emit(NewMessageEndEvent(toolResultMessage))
}

// Helper functions
func extractToolCalls(message llm.AssistantMessage) []llm.ToolCall {
	var toolCalls []llm.ToolCall
	for _, content := range message.Content {
		// Providers append *llm.ToolCall (pointer); handle both forms defensively
		// — a value-only assertion silently drops every tool call (the codebase
		// has a history of this value/pointer mismatch).
		switch tc := content.(type) {
		case *llm.ToolCall:
			toolCalls = append(toolCalls, *tc)
		case llm.ToolCall:
			toolCalls = append(toolCalls, tc)
		}
	}
	return toolCalls
}

func convertTools(tools []AgentTool[any]) []llm.Tool[any] {
	if tools == nil {
		return nil
	}
	result := make([]llm.Tool[any], len(tools))
	for i, tool := range tools {
		result[i] = llm.Tool[any]{
			Name: 		 tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		}
	}
	return result
}

func createErrorMessage(config AgentLoopConfig, message string, signal context.Context) llm.AssistantMessage{
	usage := llm.Usage{
		Input:		0,
		Output: 	0,
		CacheRead: 	0,
		CacheWrite: 0,
		TotalTokens:0,
	}
	return llm.AssistantMessage{
		Role: 			llm.RoleAssistant,
		Content: 		[]llm.ContentBlock{llm.TextContent{Type:"text", Text: message}},
		Api: 			config.Model.Api,
		Provider: 		config.Model.Provider,
		Model: 			config.Model.ID,
		Usage: 			usage,
		StopReason: 	llm.StopReasonError,
		Timestamp: 		0,
	}
}

func findTool(tools []AgentTool[any], name string) *AgentTool[any] {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}
