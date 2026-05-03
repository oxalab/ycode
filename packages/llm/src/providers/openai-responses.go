package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	src "pigo.dev/packages/llm/src"
)

// =============================================================================
// Constants
// =============================================================================

const (
	openAIResponsesBaseURL = "https://api.openai.com/v1/responses"
)

// =============================================================================
// Types
// =============================================================================

type OpenAIResponsesOptions struct {
	APIKey             string
	MaxTokens          *int
	MaxOutputTokens    *int
	Temperature        *float64
	ServiceTier        *string
	ReasoningEffort    *string
	ReasoningSummary   *string
	Headers            map[string]string
	SessionID          string
	CacheRetention     *src.CacheRetention
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Provider registration
// =============================================================================

// StreamOpenAIResponses creates an async streaming function for OpenAI Responses API
func StreamOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *OpenAIResponsesOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			output := &src.AssistantMessage{
				Role:       src.RoleAssistant,
				Content:    []src.ContentBlock{},
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			// Build and send request
			client := &http.Client{}
			params := buildOpenAIResponsesParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doOpenAIResponsesRequest(client, model, context, params, options)
			if err != nil {
				handleOpenAIResponsesError(ch, output, err, false)
				return
			}

			if options != nil && options.OnResponse != nil {
				options.OnResponse(map[string]interface{}{
					"status": resp.StatusCode,
					"headers": resp.Header,
				}, model)
			}

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			// Process streaming response
			if err := processOpenAIResponsesStream(resp.Body, ch, output, model, options); err != nil {
				handleOpenAIResponsesError(ch, output, err, true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleOpenAIResponsesError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// =============================================================================
// Request handling
// =============================================================================

func doOpenAIResponsesRequest(client *http.Client, model src.Model[src.Api], context src.ChatContext, params interface{}, options *OpenAIResponsesOptions) (*http.Response, error) {
	baseURL := openAIResponsesBaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	apiKey := ""
	if options != nil && options.APIKey != "" {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}
	if apiKey == "" {
		apiKey = getEnvApiKey("openai")
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Add Copilot dynamic headers if applicable
	if model.Provider == src.GithubCopilot {
		hasImages := HasCopilotVisionInput(contextMessagesParam(context))
		copilotHeaders := BuildCopilotDynamicHeaders(contextMessagesParam(context), hasImages)
		for k, v := range copilotHeaders {
			req.Header.Set(k, v)
		}
	}

	// Add session ID headers
	if options != nil && options.SessionID != "" {
		retention := src.CacheRetentionShort
		if options.CacheRetention != nil {
			retention = *options.CacheRetention
		}
		if retention != src.CacheRetentionNone {
			req.Header.Set("session_id", options.SessionID)
			req.Header.Set("x-client-request-id", options.SessionID)
		}
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	return client.Do(req)
}

func handleOpenAIResponsesError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
	output.StopReason = src.StopReasonError
	if isAborted {
		output.StopReason = src.StopReasonAborted
	}
	output.ErrorMessage = err.Error()
	ch <- src.AssistantMessageEvent{Type: "error", Reason: output.StopReason, Error: output}
}

// =============================================================================
// Parameter building
// =============================================================================

func buildOpenAIResponsesParams(model src.Model[src.Api], context src.ChatContext, options *OpenAIResponsesOptions) interface{} {
	allowedToolCallProviders := map[string]bool{
		"openai":         true,
		"openai-codex":   true,
		"opencode":       true,
	}

	convertOptions := &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt: true,
	}

	messages := convertResponsesMessages(model, context, allowedToolCallProviders, convertOptions)

	params := map[string]interface{}{
		"model":  model.ID,
		"input":  messages,
		"stream": true,
		"store":  false,
	}

	// Max tokens
	maxTokens := 0
	if options != nil && options.MaxOutputTokens != nil {
		maxTokens = *options.MaxOutputTokens
	} else if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	if maxTokens > 0 {
		params["max_output_tokens"] = maxTokens
	}

	// Temperature
	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	// Service tier
	if options != nil && options.ServiceTier != nil {
		params["service_tier"] = *options.ServiceTier
	}

	// Tools
	if len(context.Tools) > 0 {
		convertToolsOptions := &ConvertResponsesToolsOptions{}
		params["tools"] = convertResponsesTools(context.Tools, convertToolsOptions)
	}

	// Reasoning
	if model.Reasoning {
		effort := "medium"
		summary := "auto"

		if options != nil && options.ReasoningEffort != nil {
			effort = *options.ReasoningEffort
		}
		if options != nil && options.ReasoningSummary != nil {
			summary = *options.ReasoningSummary
		}

		// Only set reasoning if explicitly configured
		if options != nil && (options.ReasoningEffort != nil || options.ReasoningSummary != nil) {
			params["reasoning"] = map[string]interface{}{
				"effort":  effort,
				"summary": summary,
			}
			params["include"] = []string{"reasoning.encrypted_content"}
		} else if model.Provider != src.GithubCopilot {
			params["reasoning"] = map[string]interface{}{
				"effort": "none",
			}
		}
	}

	// Prompt cache
	cacheRetention := src.CacheRetentionShort
	if options != nil && options.CacheRetention != nil {
		cacheRetention = *options.CacheRetention
	}
	if cacheRetention != src.CacheRetentionNone {
		if options != nil && options.SessionID != "" {
			params["prompt_cache_key"] = options.SessionID
		}
		if cacheRetention == src.CacheRetentionLong {
			params["prompt_cache_retention"] = "24h"
		}
	}

	return params
}

// =============================================================================
// Stream processing
// =============================================================================

func processOpenAIResponsesStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], options *OpenAIResponsesOptions) error {
	decoder := json.NewDecoder(body)

	var currentItem interface{}
	var currentBlockIndex *int

	for decoder.More() {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			return err
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "response.created":
			if response, ok := event["response"].(map[string]interface{}); ok {
				if id, ok := response["id"].(string); ok {
					output.ResponseID = id
				}
			}

		case "response.output_item.added":
			if item, ok := event["item"].(map[string]interface{}); ok {
				itemType, _ := item["type"].(string)
				currentItem = item

				switch itemType {
				case "reasoning":
					newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					currentBlockIndex = &idx
					ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}

				case "message":
					newBlock := &src.TextContent{Type: "text", Text: ""}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					currentBlockIndex = &idx
					ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}

				case "function_call":
					callId, _ := item["call_id"].(string)
					id, _ := item["id"].(string)
					name, _ := item["name"].(string)

					newBlock := &src.ToolCall{
						Type:      "toolCall",
						ID:        callId + "|" + id,
						Name:      name,
						Arguments: map[string]interface{}{},
					}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					currentBlockIndex = &idx
					ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
				}
			}

		case "response.content_part.added":
			if currentItem != nil {
				if item, ok := currentItem.(map[string]interface{}); ok {
					if item["type"] == "message" {
						if content, ok := item["content"].([]map[string]interface{}); !ok {
							item["content"] = []map[string]interface{}{}
						} else if part, ok := event["part"].(map[string]interface{}); ok {
							partType, _ := part["type"].(string)
							if partType == "output_text" || partType == "refusal" {
								item["content"] = append(content, part)
							}
						}
					}
				}
			}

		case "response.output_text.delta":
			if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.TextContent); ok {
					delta, _ := event["delta"].(string)
					tc.Text += delta
					ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.refusal.delta":
			if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if tc, ok := output.Content[*currentBlockIndex].(*src.TextContent); ok {
					delta, _ := event["delta"].(string)
					tc.Text += delta
					ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.function_call_arguments.delta":
			if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if toolCall, ok := output.Content[*currentBlockIndex].(*src.ToolCall); ok {
					delta, _ := event["delta"].(string)
					toolCall.Arguments = parseStreamingJson(toolCall.Arguments, delta)
					ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: currentBlockIndex, Delta: delta, Partial: output}
				}
			}

		case "response.function_call_arguments.done":
			if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
				if toolCall, ok := output.Content[*currentBlockIndex].(*src.ToolCall); ok {
					if arguments, ok := event["arguments"].(string); ok {
						toolCall.Arguments = parseStreamingJson(toolCall.Arguments, arguments)
					}
				}
			}

		case "response.output_item.done":
			if item, ok := event["item"].(map[string]interface{}); ok {
				itemType, _ := item["type"].(string)

				switch itemType {
				case "reasoning":
					if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
						if tc, ok := output.Content[*currentBlockIndex].(*src.ThinkingContent); ok {
							if summary, ok := item["summary"].([]map[string]interface{}); ok {
								var parts []string
								for _, s := range summary {
									if text, ok := s["text"].(string); ok {
										parts = append(parts, text)
									}
								}
								tc.Thinking = strings.Join(parts, "\n\n")
							}
							sig, _ := json.Marshal(item)
							tc.ThinkingSignature = string(sig)
							ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: currentBlockIndex, Content: tc.Thinking, Partial: output}
						}
					}
					currentBlockIndex = nil

				case "message":
					if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
						if tc, ok := output.Content[*currentBlockIndex].(*src.TextContent); ok {
							if content, ok := item["content"].([]map[string]interface{}); ok {
								var textParts []string
								for _, c := range content {
									if t, ok := c["text"].(string); ok {
										textParts = append(textParts, t)
									}
								}
								tc.Text = strings.Join(textParts, "")
							}
							id, _ := item["id"].(string)
							phase, _ := item["phase"].(string)
							tc.TextSignature = encodeTextSignatureV1(id, &phase)
							ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: currentBlockIndex, Content: tc.Text, Partial: output}
						}
					}
					currentBlockIndex = nil

				case "function_call":
					if currentBlockIndex != nil && *currentBlockIndex < len(output.Content) {
						if toolCall, ok := output.Content[*currentBlockIndex].(*src.ToolCall); ok {
							callId, _ := item["call_id"].(string)
							id, _ := item["id"].(string)
							name, _ := item["name"].(string)
							toolCall.ID = callId + "|" + id
							toolCall.Name = name
							if arguments, ok := item["arguments"].(string); ok {
								toolCall.Arguments = parseStreamingJson(map[string]interface{}{}, arguments)
							}
							ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: currentBlockIndex, ToolCall: toolCall, Partial: output}
						}
					}
					currentBlockIndex = nil
				}
			}

		case "response.completed":
			if response, ok := event["response"].(map[string]interface{}); ok {
				if id, ok := response["id"].(string); ok {
					output.ResponseID = id
				}
				if usage, ok := response["usage"].(map[string]interface{}); ok {
					cachedTokens := 0.0
					if details, ok := usage["input_tokens_details"].(map[string]interface{}); ok {
						if ct, ok := details["cached_tokens"].(float64); ok {
							cachedTokens = ct
						}
					}

					inputTokens, _ := usage["input_tokens"].(float64)
					outputTokens, _ := usage["output_tokens"].(float64)

					output.Usage.Input = int(inputTokens - cachedTokens)
					output.Usage.Output = int(outputTokens)
					output.Usage.CacheRead = int(cachedTokens)
					output.Usage.TotalTokens = int(inputTokens)

// 					// Apply service tier pricing if applicable
// 					if options != nil && options.ApplyServiceTierPricing != nil {
// 						serviceTier := options.ServiceTier
// 						if options.ResolveServiceTier != nil {
// 							if responseTier, ok := response["service_tier"].(string); ok {
// 								serviceTier = options.ResolveServiceTier(&responseTier, serviceTier)
// 							}
// 						}
// 						options.ApplyServiceTierPricing(&output.Usage, serviceTier)
// 					}
				}

				status, _ := response["status"].(string)
				output.StopReason = mapOpenAIResponsesStopReason(status)
				if hasToolCalls(output.Content) && output.StopReason == src.StopReasonStop {
					output.StopReason = src.StopReasonToolUse
				}
			}

		case "error":
			code, _ := event["code"].(string)
			message, _ := event["message"].(string)
			return fmt.Errorf("error Code %s: %s", code, message)

		case "response.failed":
			if response, ok := event["response"].(map[string]interface{}); ok {
				if err, ok := response["error"].(map[string]interface{}); ok {
					code, _ := err["code"].(string)
					msg, _ := err["message"].(string)
					return fmt.Errorf("%s: %s", code, msg)
				}
				if details, ok := response["incomplete_details"].(map[string]interface{}); ok {
					if reason, ok := details["reason"].(string); ok {
						return fmt.Errorf("incomplete: %s", reason)
					}
				}
			}
		}
	}

	return nil
}

// =============================================================================
// Helpers
// =============================================================================

func contextMessagesParam(context src.ChatContext) []src.Message {
	return context.Messages
}

func hasToolCalls(content []src.ContentBlock) bool {
	for _, c := range content {
		if _, ok := c.(*src.ToolCall); ok {
			return true
		}
	}
	return false
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleOpenAIResponses provides a simplified streaming interface for OpenAI Responses
func StreamSimpleOpenAIResponses(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	opts := buildOpenAIResponsesSimpleOptions(options)
	return StreamOpenAIResponses(model, context, opts)
}

func buildOpenAIResponsesSimpleOptions(options *src.SimpleStreamOptions) *OpenAIResponsesOptions {
	if options == nil {
		return nil
	}

	reasoningEffort := ""
	if options.Reasoning != "" {
		reasoningEffort = string(options.Reasoning)
	}

	return &OpenAIResponsesOptions{
		APIKey:          options.APIKey,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		ReasoningEffort: &reasoningEffort,
		Headers:         options.Headers,
	}
}
