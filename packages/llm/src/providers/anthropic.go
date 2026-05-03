package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	src "pigo.dev/packages/llm/src"
	"pigo.dev/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	anthropicAPIBaseURL      = "https://api.anthropic.com/v1/messages"
	anthropicVersionHeader   = "anthropic-version"
	anthropicVersion         = "2023-06-01"
	fineGrainedToolStreamingBeta = "fine-grained-tool-streaming-2025-05-14"
	interleavedThinkingBeta  = "interleaved-thinking-2025-05-14"
	claudeCodeVersion        = "2.1.75"
)

// =============================================================================
// Types
// =============================================================================

type AnthropicOptions struct {
	APIKey                string
	MaxTokens             *int
	Temperature           *float64
	ThinkingEnabled       *bool
	ThinkingBudgetTokens  *int
	Effort                *string
	ThinkingDisplay       *string
	InterleavedThinking   *bool
	ToolChoice            interface{}
	Headers               map[string]string
	Metadata              map[string]string
	CacheRetention        *src.CacheRetention
	// Callbacks
	OnPayload func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

// =============================================================================
// Streaming
// =============================================================================

// StreamAnthropic creates an async streaming function for Anthropic Messages API
func StreamAnthropic(model src.Model[src.Api], context src.ChatContext, options *AnthropicOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			output := &src.AssistantMessage{
				Role:      src.RoleAssistant,
				Content:   []src.ContentBlock{},
				Api:       model.Api,
				Provider:  model.Provider,
				Model:     model.ID,
				Usage:     src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			var err error
			var resp *http.Response

			// Build and send request
			client := &http.Client{}
			params := buildAnthropicParams(model, context, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err = doAnthropicRequest(client, model, params, options)
			if err != nil {
				handleAnthropicError(ch, output, err, false)
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
			blockIndexMap := make(map[int]int)

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := strings.TrimPrefix(line, "data: ")

				if data == "[DONE]" {
					break
				}

				var event map[string]interface{}
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					continue
				}

				eventType, _ := event["type"].(string)

				switch eventType {
				case "message_start":
					if msg, ok := event["message"].(map[string]interface{}); ok {
						output.ResponseID, _ = msg["id"].(string)
						if usage, ok := msg["usage"].(map[string]interface{}); ok {
							if inputTokens, ok := usage["input_tokens"].(float64); ok {
								output.Usage.Input = int(inputTokens)
							}
							if outputTokens, ok := usage["output_tokens"].(float64); ok {
								output.Usage.Output = int(outputTokens)
							}
							if cacheRead, ok := usage["cache_read_input_tokens"].(float64); ok {
								output.Usage.CacheRead = int(cacheRead)
							}
							if cacheWrite, ok := usage["cache_creation_input_tokens"].(float64); ok {
								output.Usage.CacheWrite = int(cacheWrite)
							}
							output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
						}
					}

				case "content_block_start":
					index := int(event["index"].(float64))
					if block, ok := event["content_block"].(map[string]interface{}); ok {
						blockType, _ := block["type"].(string)
						switch blockType {
						case "text":
							newBlock := &src.TextContent{Type: "text", Text: ""}
							output.Content = append(output.Content, newBlock)
							blockIndexMap[index] = len(output.Content) - 1
							ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
						case "thinking":
							newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
							output.Content = append(output.Content, newBlock)
							blockIndexMap[index] = len(output.Content) - 1
							ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
						case "redacted_thinking":
							signature, _ := block["data"].(string)
							newBlock := &src.ThinkingContent{Type: "thinking", Thinking: "[Reasoning redacted]", ThinkingSignature: signature}
							output.Content = append(output.Content, newBlock)
							blockIndexMap[index] = len(output.Content) - 1
							ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
						case "tool_use":
							id, _ := block["id"].(string)
							name, _ := block["name"].(string)
							input := map[string]interface{}{}
							if i, ok := block["input"].(map[string]interface{}); ok {
								input = i
							}
							newBlock := &src.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: input}
							output.Content = append(output.Content, newBlock)
							blockIndexMap[index] = len(output.Content) - 1
							ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: ptr(len(output.Content) - 1), Partial: output}
						}
					}

				case "content_block_delta":
					index := int(event["index"].(float64))
					if idx, ok := blockIndexMap[index]; ok && idx < len(output.Content) {
						if delta, ok := event["delta"].(map[string]interface{}); ok {
							deltaType, _ := delta["type"].(string)
							switch deltaType {
							case "text_delta":
								if text, ok := delta["text"].(string); ok {
									if tc, ok := output.Content[idx].(*src.TextContent); ok {
										tc.Text += text
										ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: ptr(idx), Delta: text, Partial: output}
									}
								}
							case "thinking_delta":
								if thinking, ok := delta["thinking"].(string); ok {
									if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
										tc.Thinking += thinking
										ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: ptr(idx), Delta: thinking, Partial: output}
									}
								}
							case "input_json_delta":
								if partialJson, ok := delta["partial_json"].(string); ok {
									if toolCall, ok := output.Content[idx].(*src.ToolCall); ok {
										toolCall.Arguments = parseStreamingJson(toolCall.Arguments, partialJson)
										ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: ptr(idx), Delta: partialJson, Partial: output}
									}
								}
							case "signature_delta":
								if signature, ok := delta["signature"].(string); ok {
									if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
										tc.ThinkingSignature += signature
									}
								}
							}
						}
					}

				case "content_block_stop":
					index := int(event["index"].(float64))
					if idx, ok := blockIndexMap[index]; ok && idx < len(output.Content) {
						delete(blockIndexMap, index)
						switch block := output.Content[idx].(type) {
						case *src.TextContent:
							ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: ptr(idx), Content: block.Text, Partial: output}
						case *src.ThinkingContent:
							ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: ptr(idx), Content: block.Thinking, Partial: output}
						case *src.ToolCall:
							ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: ptr(idx), ToolCall: block, Partial: output}
						}
					}

				case "message_delta":
					if delta, ok := event["delta"].(map[string]interface{}); ok {
						if stopReason, ok := delta["stop_reason"].(string); ok {
							output.StopReason = mapAnthropicStopReason(stopReason)
						}
					}
					if usage, ok := event["usage"].(map[string]interface{}); ok {
						if inputTokens, ok := usage["input_tokens"].(float64); ok {
							output.Usage.Input = int(inputTokens)
						}
						if outputTokens, ok := usage["output_tokens"].(float64); ok {
							output.Usage.Output = int(outputTokens)
						}
						if cacheRead, ok := usage["cache_read_input_tokens"].(float64); ok {
							output.Usage.CacheRead = int(cacheRead)
						}
						if cacheWrite, ok := usage["cache_creation_input_tokens"].(float64); ok {
							output.Usage.CacheWrite = int(cacheWrite)
						}
						output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
					}

				case "error":
					if errorMsg, ok := event["error"].(map[string]interface{}); ok {
						msg, _ := errorMsg["message"].(string)
						handleAnthropicError(ch, output, fmt.Errorf("anthropic error: %s", msg), false)
						return
					}
				}
			}

			if err := scanner.Err(); err != nil {
				handleAnthropicError(ch, output, err, true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleAnthropicError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doAnthropicRequest(client *http.Client, model src.Model[src.Api], params interface{}, options *AnthropicOptions) (*http.Response, error) {
	baseURL := anthropicAPIBaseURL
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
	req.Header.Set(anthropicVersionHeader, anthropicVersion)

	apiKey := ""
	if options != nil && options.APIKey != "" {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}

	if isOAuthToken(apiKey) {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("User-Agent", "claude-cli/"+claudeCodeVersion)
		req.Header.Set("X-App", "cli")
	} else {
		req.Header.Set("x-api-key", apiKey)
	}

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Add beta headers
	betaFeatures := []string{}
	if options != nil && options.InterleavedThinking != nil && *options.InterleavedThinking && !supportsAdaptiveThinking(model.ID) {
		betaFeatures = append(betaFeatures, interleavedThinkingBeta)
	}
	if options != nil && options.ThinkingEnabled != nil && *options.ThinkingEnabled && len(contextTools(params)) > 0 {
		betaFeatures = append(betaFeatures, fineGrainedToolStreamingBeta)
	}
	if len(betaFeatures) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(betaFeatures, ","))
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	return client.Do(req)
}

func handleAnthropicError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
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

func buildAnthropicParams(model src.Model[src.Api], context src.ChatContext, options *AnthropicOptions) interface{} {
	isOAuth := false
	if options != nil && options.APIKey != "" {
		isOAuth = isOAuthToken(options.APIKey)
	}

	cacheControl := getAnthropicCacheControl(model, options)

	params := map[string]interface{}{
		"model":      model.ID,
		"messages":   convertAnthropicMessages(context.Messages, model, isOAuth, cacheControl),
		"max_tokens": getDefaultMaxTokens(model, options),
		"stream":     true,
	}

	// System prompt
	if context.SystemPrompt != "" {
		if isOAuth {
			params["system"] = []map[string]interface{}{
				{
					"type": "text",
					"text": "You are Claude Code, Anthropic's official CLI for Claude.",
				},
				{
					"type": "text",
					"text": utils.SanitizeSurrogates(context.SystemPrompt),
				},
			}
		} else {
			params["system"] = []map[string]interface{}{
				{
					"type": "text",
					"text": utils.SanitizeSurrogates(context.SystemPrompt),
				},
			}
		}
		if cacheControl != nil {
			system := params["system"].([]map[string]interface{})
			system[len(system)-1]["cache_control"] = cacheControl
		}
	}

	// Temperature (incompatible with extended thinking)
	if options != nil && options.Temperature != nil {
		thinkingEnabled := false
		if options.ThinkingEnabled != nil {
			thinkingEnabled = *options.ThinkingEnabled
		}
		if !thinkingEnabled {
			params["temperature"] = *options.Temperature
		}
	}

	// Tools
	if len(context.Tools) > 0 {
		params["tools"] = convertAnthropicTools(context.Tools, isOAuth, cacheControl)
	}

	// Thinking mode
	if model.Reasoning {
		thinkingEnabled := false
		if options != nil && options.ThinkingEnabled != nil {
			thinkingEnabled = *options.ThinkingEnabled
		}

		if thinkingEnabled {
			display := "summarized"
			if options != nil && options.ThinkingDisplay != nil {
				display = *options.ThinkingDisplay
			}

			if supportsAdaptiveThinking(model.ID) {
				thinking := map[string]interface{}{
					"type":   "adaptive",
					"display": display,
				}
				if options != nil && options.Effort != nil {
					thinking["output_config"] = map[string]interface{}{
						"effort": *options.Effort,
					}
				}
				params["thinking"] = thinking
			} else {
				budget := 1024
				if options != nil && options.ThinkingBudgetTokens != nil {
					budget = *options.ThinkingBudgetTokens
				}
				params["thinking"] = map[string]interface{}{
					"type":    "enabled",
					"budget_tokens": budget,
					"display": display,
				}
			}
		} else if options != nil && options.ThinkingEnabled != nil && !*options.ThinkingEnabled {
			params["thinking"] = map[string]interface{}{
				"type": "disabled",
			}
		}
	}

	// Tool choice
	if options != nil && options.ToolChoice != nil {
		params["tool_choice"] = options.ToolChoice
	}

	// Metadata
	if options != nil && options.Metadata != nil {
		if userID, ok := options.Metadata["user_id"]; ok {
			params["metadata"] = map[string]interface{}{
				"user_id": userID,
			}
		}
	}

	return params
}

func convertAnthropicMessages(messages []src.Message, model src.Model[src.Api], isOAuth bool, cacheControl map[string]interface{}) []map[string]interface{} {
	var params []map[string]interface{}

	transformedMessages := TransformMessages(messages, model, normalizeAnthropicToolCallId)

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]
		role := msgRole(msg)

		if role == src.RoleUser {
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}

			if len(um.Content) == 1 {
				if tc, ok := um.Content[0].(*src.TextContent); ok && tc.Text != "" {
					params = append(params, map[string]interface{}{
						"role":    "user",
						"content": utils.SanitizeSurrogates(tc.Text),
					})
					continue
				}
			}

			var blocks []map[string]interface{}
			for _, item := range um.Content {
				switch c := item.(type) {
				case *src.TextContent:
					if c.Text == "" {
						continue
					}
					blocks = append(blocks, map[string]interface{}{
						"type": "text",
						"text": utils.SanitizeSurrogates(c.Text),
					})
				case *src.ImageContent:
					blocks = append(blocks, map[string]interface{}{
						"type": "image",
						"source": map[string]string{
							"type":       "base64",
							"media_type": c.MimeType,
							"data":       c.Data,
						},
					})
				}
			}

			if len(blocks) == 0 {
				continue
			}

			params = append(params, map[string]interface{}{
				"role":    "user",
				"content": blocks,
			})
		} else if role == src.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}

			var blocks []map[string]interface{}
			for _, block := range am.Content {
				switch b := block.(type) {
				case *src.TextContent:
					if b.Text == "" {
						continue
					}
					blocks = append(blocks, map[string]interface{}{
						"type": "text",
						"text": utils.SanitizeSurrogates(b.Text),
					})
				case *src.ThinkingContent:
					if b.Thinking == "" {
						continue
					}
					if b.Redacted != nil && *b.Redacted {
						blocks = append(blocks, map[string]interface{}{
							"type": "redacted_thinking",
							"data": b.ThinkingSignature,
						})
						continue
					}
					if b.ThinkingSignature == "" {
						blocks = append(blocks, map[string]interface{}{
							"type": "text",
							"text": utils.SanitizeSurrogates(b.Thinking),
						})
					} else {
						blocks = append(blocks, map[string]interface{}{
							"type":      "thinking",
							"thinking":  utils.SanitizeSurrogates(b.Thinking),
							"signature": b.ThinkingSignature,
						})
					}
				case *src.ToolCall:
					name := b.Name
					if isOAuth {
						name = toClaudeCodeName(b.Name)
					}
					blocks = append(blocks, map[string]interface{}{
						"type":  "tool_use",
						"id":    b.ID,
						"name":  name,
						"input": b.Arguments,
					})
				}
			}

			if len(blocks) == 0 {
				continue
			}

			params = append(params, map[string]interface{}{
				"role":    "assistant",
				"content": blocks,
			})
		} else if role == src.RoleTool {
			tr, ok := msg.(*src.ToolResultMessage[any])
			if !ok {
				continue
			}

			// Collect consecutive tool results
			var toolResults []map[string]interface{}
			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": tr.ToolCallID,
				"content":     convertAnthropicContentBlocks(tr.Content),
				"is_error":    tr.IsError,
			})

			j := i + 1
			for j < len(transformedMessages) && msgRole(transformedMessages[j]) == src.RoleTool {
				nextMsg := transformedMessages[j]
				if nextTr, ok := nextMsg.(*src.ToolResultMessage[any]); ok {
					toolResults = append(toolResults, map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": nextTr.ToolCallID,
						"content":     convertAnthropicContentBlocks(nextTr.Content),
						"is_error":    nextTr.IsError,
					})
				}
				j++
			}

			i = j - 1

			params = append(params, map[string]interface{}{
				"role":    "user",
				"content": toolResults,
			})
		}
	}

	// Add cache_control to last user message
	if cacheControl != nil && len(params) > 0 {
		lastMsg := params[len(params)-1]
		if lastMsg["role"] == "user" {
			if content, ok := lastMsg["content"]; ok {
				switch c := content.(type) {
				case string:
					lastMsg["content"] = []map[string]interface{}{
						{
							"type":         "text",
							"text":         c,
							"cache_control": cacheControl,
						},
					}
				case []map[string]interface{}:
					if len(c) > 0 {
						c[len(c)-1]["cache_control"] = cacheControl
					}
				}
			}
		}
	}

	return params
}

func convertAnthropicContentBlocks(content []src.ContentBlock) interface{} {
	if len(content) == 1 {
		if tc, ok := content[0].(*src.TextContent); ok {
			return utils.SanitizeSurrogates(tc.Text)
		}
	}

	var blocks []map[string]interface{}
	for _, c := range content {
		switch block := c.(type) {
		case *src.TextContent:
			blocks = append(blocks, map[string]interface{}{
				"type": "text",
				"text": utils.SanitizeSurrogates(block.Text),
			})
		case *src.ImageContent:
			blocks = append(blocks, map[string]interface{}{
				"type": "image",
				"source": map[string]string{
					"type":       "base64",
					"media_type": block.MimeType,
					"data":       block.Data,
				},
			})
		}
	}

	if len(blocks) == 0 {
		return ""
	}

	return blocks
}

func convertAnthropicTools(tools []src.Tool[any], isOAuth bool, cacheControl map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		name := tool.Name
		if isOAuth {
			name = toClaudeCodeName(name)
		}

		parameters, ok := tool.Parameters.(map[string]interface{})
		var properties map[string]interface{}
		var required []string
		if ok {
			properties, _ = parameters["properties"].(map[string]interface{})
			if req, ok := parameters["required"].([]interface{}); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						required = append(required, s)
					}
				}
			}
		}

		toolDef := map[string]interface{}{
			"name":        name,
			"description": tool.Description,
			"input_schema": map[string]interface{}{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}

		if cacheControl != nil && i == len(tools)-1 {
			toolDef["cache_control"] = cacheControl
		}

		result[i] = toolDef
	}
	return result
}

// =============================================================================
// Helpers
// =============================================================================

func isOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

func supportsAdaptiveThinking(modelId string) bool {
	lower := strings.ToLower(modelId)
	return strings.Contains(lower, "opus-4-6") || strings.Contains(lower, "opus-4.6") ||
		strings.Contains(lower, "opus-4-7") || strings.Contains(lower, "opus-4.7") ||
		strings.Contains(lower, "sonnet-4-6") || strings.Contains(lower, "sonnet-4.6")
}

func normalizeAnthropicToolCallId(id string, _ src.Model[src.Api], _ src.AssistantMessage) string {
	var result strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	sanitized := result.String()
	if len(sanitized) > 64 {
		sanitized = sanitized[:64]
	}
	return sanitized
}

func getAnthropicCacheControl(model src.Model[src.Api], options *AnthropicOptions) map[string]interface{} {
	retention := src.CacheRetentionShort
	if options != nil && options.CacheRetention != nil {
		retention = *options.CacheRetention
	}

	if retention == src.CacheRetentionNone {
		return nil
	}

	cacheControl := map[string]interface{}{
		"type": "ephemeral",
	}

	if retention == src.CacheRetentionLong {
		cacheControl["ttl"] = "1h"
	}

	return cacheControl
}

func getDefaultMaxTokens(model src.Model[src.Api], options *AnthropicOptions) int {
	if options != nil && options.MaxTokens != nil {
		return *options.MaxTokens
	}
	if model.MaxTokens > 0 {
		return model.MaxTokens / 3
	}
	return 4096
}

func contextTools(params interface{}) []src.Tool[any] {
	if p, ok := params.(map[string]interface{}); ok {
		if tools, ok := p["tools"].([]src.Tool[any]); ok {
			return tools
		}
	}
	return nil
}

func mapAnthropicStopReason(reason string) src.StopReason {
	switch reason {
	case "end_turn":
		return src.StopReasonStop
	case "max_tokens":
		return src.StopReasonLength
	case "tool_use":
		return src.StopReasonToolUse
	case "refusal":
		return src.StopReasonError
	case "pause_turn", "stop_sequence":
		return src.StopReasonStop
	default:
		return src.StopReasonStop
	}
}

func ptr(i int) *int {
	return &i
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleAnthropic provides a simplified streaming interface for Anthropic
func StreamSimpleAnthropic(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	opts := buildAnthropicSimpleOptions(options)
	return StreamAnthropic(model, context, opts)
}

func buildAnthropicSimpleOptions(options *src.SimpleStreamOptions) *AnthropicOptions {
	if options == nil {
		return nil
	}

	thinkingEnabled := false
	if options.Reasoning != "" {
		thinkingEnabled = true
	}

	return &AnthropicOptions{
		APIKey:          options.APIKey,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		ThinkingEnabled: &thinkingEnabled,
		Headers:         options.Headers,
	}
}
