package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"ycode/packages/llm/src"
)

// ProxyAssistantMessageEvent represents SSE events from the proxy server.
// The server strips the partial field from delta events to reduce bandwidth.
type ProxyAssistantMessageEvent struct {
	Type 				string 			`json:"type"`
	ContentIndex		*int 			`json:"contentIndex,omitempty"`
	Delta 				string 			`json:"detla,omitempty"`
	ContentSignature	string 			`json:"contentSignature,omitempty"`
	ID 					string 			`json:"id,omitempty"`
	ToolName 			string 			`json:"toolName,omitempty"`
	Reason 				llm.StopReason 	`json:"reason,omitempty"`
	ErrorMessage 		string 			`json:"errorMessage,omitempty"`
	Usage 				*llm.Usage  	`json:"usage,omitempty"`
}

// ProxySerializationOptions is the subset of SimpleStreamOptions sent to the proxy server
type proxySerializableOptions struct {
	Temperature 	*float64 			`json:"temperature,omitempty"`
	MaxTokens 		*int 				`json:"maxTokens,omitempty"`
	Reasoning 		llm.ThinkingLevel 	`json:"reasoning,omitempty"`
	CacheRetention 	llm.CacheRetention 	`json:"cacheRetention,omitempty"`
	SessionID 		string 				`json:"sessionId,omitempty"`
	Headers 		map[string]string 	`json:"headers,omitempty"`
	Metadata 		map[string]any 		`json:"metadata,omitempty"`
	Transport 		llm.Transport 		`json:"transport,omitempty"`
	ThinkingBudgets llm.ThinkingBudgets `json:"thinkingBudgets,omitempty"`
	MaxRetryDelayMs int 				`json:"maxRetryDelayMs,omitempty"`
}

// ProxyStreamOptions extends serializable stream options with proxy-specific fields.
type ProxyStreamOptions struct {
	Temperature 	*float64 			`json:"temperature,omitempty"`
	MaxTokens 		*int 				`json:"maxTokens,omitempty"`
	Reasoning 		llm.ThinkingLevel 	`json:"thinkingLevel,omitempty"`
	CacheRetention 	llm.CacheRetention 	`json:"cacheRetention,omitempty"`
	SessionID 		string 				`json:"sessionId,omitempty"`
	Headers 		map[string]string 	`json:"headers,omitempty"`
	Metadata 		map[string]any 		`json:"metadata,omitempty"`
	Transport 		llm.Transport 		`json:"transport,omitempty"`
	ThinkingBudgets llm.ThinkingBudgets `json:"thinkingBudgets,omitempty"`
	MaxRetryDelayMs int 				`json:"maxRetryDelayMs,omitempty"`

	// Proxy-specific
	Ctx 		context.Context
	AuthToken	string
	ProxyURL 	string
}

func buildProxyRequestOptions(options *ProxyStreamOptions) proxySerializableOptions {
	return proxySerializableOptions{
		Temperature: 		options.Temperature,
		MaxTokens: 			options.MaxTokens,
		Reasoning: 			options.Reasoning,
		CacheRetention: 	options.CacheRetention,
		SessionID: 			options.SessionID,
		Headers: 			options.Headers,
		Metadata: 			options.Metadata,
		Transport: 			options.Transport,
		ThinkingBudgets: 	options.ThinkingBudgets,
		MaxRetryDelayMs: 	options.MaxRetryDelayMs,
	}
}

// StreamProxy creates a stream that proxies LLM calls through a server
// The server manages auth and proxies requests to LLM providers
// Returns a channel of AssistantMessageEvent
func StreamProxy(
	model 	llm.Model[llm.Api],
	chatCtx llm.ChatContext,
	options *ProxyStreamOptions,
) <- chan llm.AssistantMessageEvent {
	ch := make(chan llm.AssistantMessageEvent, 256)

	go func() {
		defer close(ch)

		// Initialize partial message
		partial := llm.AssistantMessage{
			Role: llm.RoleAssistant,
			StopReason: llm.StopReasonStop,
			Content: []llm.ContentBlock{},
			Api: model.Api,
			Provider: model.Provider,
			Model: model.ID,
			Usage: llm.Usage{
				Cost: llm.UsageCost{},
			},
			Timestamp: time.Now().UnixMilli(),
		}
		ctx := options.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		// Track partialJson for tool calls (keyed by content index)
		partialJsons := map[int]string{}

		// Build request body
		requestBody := map[string]any{
			"model": model,
			"context": chatCtx,
			"options": buildProxyRequestOptions(options),
		}

		bodyBytes, err := json.Marshal(requestBody)
		if err != nil {
			pushError(ch, &partial, fmt.Sprintf("failed to marshal request: %s", err.Error()), false)
			return
		}
		req, err := http.NewRequestWithContext(ctx, "POST", options.ProxyURL+"/api/stream", strings.NewReader(string(bodyBytes)))
		if err != nil {
			pushError(ch, &partial, fmt.Sprintf("failed to create request: %s", err.Error()), false)
			return
		}
		req.Header.Set("Authorization", "Bearer "+options.AuthToken)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			aborted := ctx.Err() != nil
			msg := err.Error()
			if aborted {
				msg = "Request aborted by user"
			}
			pushError(ch, &partial, msg, aborted)
			return 
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errorMessage := fmt.Sprintf("Proxt error: %d %s", resp.StatusCode, resp.Status)
			body, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				var errorData  struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(body, &errorData) == nil && errorData.Error != "" {
					errorMessage = fmt.Sprintf("Proxy error: %s", errorData.Error)
				}
			}
			pushError(ch, &partial, errorMessage, false)
			return
		}

		// Read SSE events
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimSpace(line[6:])
			if data == ""{
				continue
			}
			var proxyEvent ProxyAssistantMessageEvent
			if err := json.Unmarshal([]byte(data), &proxyEvent); err != nil {
				continue
			}
			event := processProxyEvent(&proxyEvent, &partial, partialJsons)
			if event != nil {
				ch <- *event
			}
		}

		// Check of context was cancelled during reading
		if ctx.Err() != nil {
			
			partial.StopReason = llm.StopReasonAborted
			partial.ErrorMessage = "Request Aborted by User"
			ch <- llm.AssistantMessageEvent{
				Type: "error",
				Reason: llm.StopReasonAborted,
				Error: &partial,
			}
			return
		}
	}()
	return ch
}

func pushError(ch chan<- llm.AssistantMessageEvent, partial *llm.AssistantMessage, message string, aborted bool) {
	reason := llm.StopReasonError
	if aborted {
		reason = llm.StopReasonAborted
	}
	partial.StopReason = reason
	partial.ErrorMessage = message
	ch <- llm.AssistantMessageEvent{
		Type: "error",
		Reason: reason,
		Error: partial,
	}
}

// processProxyEvent processes a proxy event and updates the partial message.
// Returns nil of no event should be emitted.
func processProxyEvent(
	proxyEvent *ProxyAssistantMessageEvent,
	partial *llm.AssistantMessage,
	partialJsons map[int]string,
) *llm.AssistantMessageEvent {
	switch proxyEvent.Type {
	case "start":
		return &llm.AssistantMessageEvent{
			Type: "start",
			Partial: partial,
		}
	case "text_start":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			ensureContentCapacity(partial, idx)
			partial.Content[idx] = llm.TextContent{Type: "text", Text: ""}
			return &llm.AssistantMessageEvent{
				Type: 	"text_start",
				ContentIndex: proxyEvent.ContentIndex,
				Partial: partial,
			}
		}
	case "text_delta":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			if idx < len(partial.Content) {
				if tc, ok := partial.Content[idx].(llm.TextContent); ok && tc.Type == "text" {
					tc.Text += proxyEvent.Delta
					partial.Content[idx] = tc
					return &llm.AssistantMessageEvent{
						Type: "text_delta",
						ContentIndex: proxyEvent.ContentIndex,
						Delta: proxyEvent.Delta,
						Partial: partial,
					}
				}
			}
		}
	case "text_end":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			if idx < len(partial.Content) {
				if tc, ok := partial.Content[idx].(llm.TextContent); ok && tc.Type == "text" {
					tc.TextSignature = proxyEvent.ContentSignature
					partial.Content[idx] = tc
					return &llm.AssistantMessageEvent{
						Type: "text_end",
						ContentIndex: proxyEvent.ContentIndex,
						Content: tc.Text,
						Partial: partial,
					}
				}
			}
		}
	case "thinking_start":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			ensureContentCapacity(partial, idx)
			partial.Content[idx] = llm.ThinkingContent{Type: "thinking", Thinking: ""}
			return &llm.AssistantMessageEvent{
				Type:  "thinking_start",
				ContentIndex: proxyEvent.ContentIndex,
				Partial: partial,
			}
		}
	case "thinking_delta":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			if idx < len(partial.Content) {
				if tc, ok := partial.Content[idx].(llm.ThinkingContent); ok && tc.Type == "thinking" {
					tc.Thinking += proxyEvent.Delta
					partial.Content[idx] = tc
					return &llm.AssistantMessageEvent{
						Type: "thinking_delta",
						ContentIndex: proxyEvent.ContentIndex,
						Delta: proxyEvent.Delta,
						Partial: partial,
					}
				}
			}
		}
	case "thinking_end":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			if idx < len(partial.Content) {
				if tc, ok := partial.Content[idx].(llm.ThinkingContent); ok && tc.Type == "thinking" {
					tc.ThinkingSignature = proxyEvent.ContentSignature
					partial.Content[idx] = tc
					return &llm.AssistantMessageEvent{
						Type: "thinking_end",
						ContentIndex: proxyEvent.ContentIndex,
						Content: tc.Thinking,
						Partial: partial,
					}
				}
			}
		}
	case "toolcall_start":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			ensureContentCapacity(partial, idx)
			toolCall := llm.ToolCall{
				Type: "toolCall",
				ID: proxyEvent.ID,
				Name: proxyEvent.ToolName,
				Arguments: map[string]any{},
			}
			partial.Content[idx] = toolCall
			partialJsons[idx] = ""
			return &llm.AssistantMessageEvent{
				Type: "toolcall_start",
				ContentIndex: proxyEvent.ContentIndex,
				Partial: partial,
			}
		}
	case "toolcall_delta":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			if idx < len(partial.Content) {
				if tc, ok := partial.Content[idx].(llm.ToolCall); ok && tc.Type == "toolCall" {
					partialJsons[idx] += proxyEvent.Delta
					// Try to parse accumulated JSON
					var args map[string]any
					if err := json.Unmarshal([]byte(partialJsons[idx]), &args); err == nil {
						tc.Arguments = args
					}
					partial.Content[idx] = tc
					return &llm.AssistantMessageEvent{
						Type: "toolcall_delta",
						ContentIndex: proxyEvent.ContentIndex,
						Delta: proxyEvent.Delta,
						Partial: partial,
					}
				}
			}
		}
	case "toolcall_end":
		if proxyEvent.ContentIndex != nil {
			idx := *proxyEvent.ContentIndex
			if idx < len(partial.Content) {
				if tc, ok := partial.Content[idx].(llm.ToolCall); ok && tc.Type == "toolCall" {
					delete(partialJsons, idx)
					return &llm.AssistantMessageEvent{
						Type: "toolcall_end",
						ContentIndex: proxyEvent.ContentIndex,
						ToolCall: &tc,
						Partial: partial,
					}
				}
			}
		}
		return nil
	
	case "done":
		partial.StopReason = proxyEvent.Reason
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &llm.AssistantMessageEvent{
			Type: "done",
			Reason: proxyEvent.Reason,
			Message: partial,
		}
	case "error":
		partial.StopReason = proxyEvent.Reason
		partial.ErrorMessage = proxyEvent.ErrorMessage
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &llm.AssistantMessageEvent{
			Type: "error",
			Reason: proxyEvent.Reason,
			Error: partial,
		}
	}
	return nil
}

// ensureContentCapacity ensure partial.Content has atleast idx+1 elements.
func ensureContentCapacity(partial *llm.AssistantMessage, idx int) {
	for len(partial.Content) <= idx {
		partial.Content = append(partial.Content, nil)
	}
}