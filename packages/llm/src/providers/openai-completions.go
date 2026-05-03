package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	src "pigo.dev/packages/llm/src"
	"pigo.dev/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	openAICompletionsBaseURL = "https://api.openai.com/v1/chat/completions"
)

// =============================================================================
// Types
// =============================================================================

type OpenAICompletionsOptions struct {
	APIKey          string
	MaxTokens       *int
	Temperature     *float64
	ToolChoice      interface{}
	ReasoningEffort *string
	Headers         map[string]string
	SessionID       string
	CacheRetention  *src.CacheRetention
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

type OpenAICompletionsCompat struct {
	SupportsStore                      bool
	SupportsDeveloperRole              bool
	SupportsReasoningEffort            bool
	ReasoningEffortMap                 map[string]string
	SupportsUsageInStreaming           bool
	MaxTokensField                     string
	RequiresToolResultName             bool
	RequiresAssistantAfterToolResult   bool
	RequiresThinkingAsText             bool
	RequiresReasoningContentOnAssistant bool
	ThinkingFormat                     string
	OpenRouterRouting                  map[string]string
	VercelGatewayRouting               map[string]interface{}
	ZaiToolStream                      bool
	SupportsStrictMode                 bool
	CacheControlFormat                 string
	SendSessionAffinityHeaders         bool
	SupportsLongCacheRetention         bool
}

// =============================================================================
// Provider registration
// =============================================================================

// StreamOpenAICompletions creates an async streaming function for OpenAI Completions API
func StreamOpenAICompletions(model src.Model[src.Api], context src.ChatContext, options *OpenAICompletionsOptions) func() <-chan src.AssistantMessageEvent {
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
			compat := detectOpenAICompat(model)
			params := buildOpenAICompletionsParams(model, context, options, compat)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doOpenAICompletionsRequest(client, model, context, params, options, compat)
			if err != nil {
				handleOpenAICompletionsError(ch, output, err, false)
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
			if err := processOpenAICompletionsStream(resp.Body, ch, output, model, compat); err != nil {
				handleOpenAICompletionsError(ch, output, err, true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleOpenAICompletionsError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doOpenAICompletionsRequest(client *http.Client, model src.Model[src.Api], context src.ChatContext, params interface{}, options *OpenAICompletionsOptions, compat *OpenAICompletionsCompat) (*http.Response, error) {
	baseURL := openAICompletionsBaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}

	// Handle Cloudflare Workers AI special case
	if IsCloudflareProvider(string(model.Provider)) {
		baseURL = ResolveCloudflareBaseUrl(model)
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
	if options != nil && options.SessionID != "" && compat.SendSessionAffinityHeaders {
		req.Header.Set("session_id", options.SessionID)
		req.Header.Set("x-client-request-id", options.SessionID)
		req.Header.Set("x-session-affinity", options.SessionID)
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	return client.Do(req)
}

func handleOpenAICompletionsError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
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

func buildOpenAICompletionsParams(model src.Model[src.Api], context src.ChatContext, options *OpenAICompletionsOptions, compat *OpenAICompletionsCompat) interface{} {
	cacheRetention := src.CacheRetentionShort
	if options != nil && options.CacheRetention != nil {
		cacheRetention = *options.CacheRetention
	}

	messages := convertOpenAICompletionsMessages(model, context, compat)
	cacheControl := getOpenAICompatCacheControl(compat, cacheRetention)

	params := map[string]interface{}{
		"model":    model.ID,
		"messages": messages,
		"stream":   true,
	}

	// Prompt cache
	if (strings.Contains(model.BaseURL, "api.openai.com") && cacheRetention != src.CacheRetentionNone) ||
		(cacheRetention == src.CacheRetentionLong && compat.SupportsLongCacheRetention) {
		if options != nil && options.SessionID != "" {
			params["prompt_cache_key"] = options.SessionID
		}
	}
	if cacheRetention == src.CacheRetentionLong && compat.SupportsLongCacheRetention {
		params["prompt_cache_retention"] = "24h"
	}

	// Stream options for usage
	if compat.SupportsUsageInStreaming {
		params["stream_options"] = map[string]interface{}{
			"include_usage": true,
		}
	}

	// Store
	if compat.SupportsStore {
		params["store"] = false
	}

	// Max tokens
	if options != nil && options.MaxTokens != nil {
		if compat.MaxTokensField == "max_tokens" {
			params["max_tokens"] = *options.MaxTokens
		} else {
			params["max_completion_tokens"] = *options.MaxTokens
		}
	}

	// Temperature
	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	// Tools
	if len(context.Tools) > 0 {
		params["tools"] = convertOpenAICompletionsTools(context.Tools, compat)
		if compat.ZaiToolStream {
			params["tool_stream"] = true
		}
	} else if hasToolHistory(context.Messages) {
		params["tools"] = []interface{}{}
	}

	// Apply cache control
	if cacheControl != nil {
		applyOpenAICompatCacheControl(messages, params["tools"], cacheControl)
	}

	// Tool choice
	if options != nil && options.ToolChoice != nil {
		params["tool_choice"] = options.ToolChoice
	}

	// Reasoning configuration
	if model.Reasoning {
		switch compat.ThinkingFormat {
		case "zai":
			if options != nil && options.ReasoningEffort != nil {
				params["enable_thinking"] = true
			}
		case "qwen":
			if options != nil && options.ReasoningEffort != nil {
				params["enable_thinking"] = true
			}
		case "qwen-chat-template":
			if options != nil && options.ReasoningEffort != nil {
				params["chat_template_kwargs"] = map[string]interface{}{
					"enable_thinking":   true,
					"preserve_thinking": true,
				}
			}
		case "deepseek":
			thinkingType := "disabled"
			if options != nil && options.ReasoningEffort != nil {
				thinkingType = "enabled"
			}
			params["thinking"] = map[string]interface{}{
				"type": thinkingType,
			}
			if options != nil && options.ReasoningEffort != nil {
				effort := mapOpenAIReasoningEffort(*options.ReasoningEffort, compat.ReasoningEffortMap)
				params["reasoning_effort"] = effort
			}
		case "openrouter":
			reasoning := map[string]interface{}{
				"effort": "none",
			}
			if options != nil && options.ReasoningEffort != nil {
				effort := mapOpenAIReasoningEffort(*options.ReasoningEffort, compat.ReasoningEffortMap)
				reasoning["effort"] = effort
			}
			params["reasoning"] = reasoning
		default:
			if options != nil && options.ReasoningEffort != nil && compat.SupportsReasoningEffort {
				effort := mapOpenAIReasoningEffort(*options.ReasoningEffort, compat.ReasoningEffortMap)
				params["reasoning_effort"] = effort
			}
		}
	}

	// OpenRouter routing
	if strings.Contains(model.BaseURL, "openrouter.ai") && len(compat.OpenRouterRouting) > 0 {
		params["provider"] = compat.OpenRouterRouting
	}

	// Vercel AI Gateway routing
	if strings.Contains(model.BaseURL, "ai-gateway.vercel.sh") && len(compat.VercelGatewayRouting) > 0 {
		gatewayOptions := map[string]interface{}{}
		if only, ok := compat.VercelGatewayRouting["only"].([]string); ok {
			gatewayOptions["only"] = only
		}
		if order, ok := compat.VercelGatewayRouting["order"].([]string); ok {
			gatewayOptions["order"] = order
		}
		if len(gatewayOptions) > 0 {
			params["providerOptions"] = map[string]interface{}{
				"gateway": gatewayOptions,
			}
		}
	}

	return params
}

func convertOpenAICompletionsMessages(model src.Model[src.Api], context src.ChatContext, compat *OpenAICompletionsCompat) []map[string]interface{} {
	var params []map[string]interface{}

	normalizeToolCallId := func(id string) string {
		if strings.Contains(id, "|") {
			parts := strings.Split(id, "|")
			callId := parts[0]
			// Sanitize and truncate
			var result strings.Builder
			for _, r := range callId {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
					result.WriteRune(r)
				} else {
					result.WriteRune('_')
				}
			}
			sanitized := result.String()
			if len(sanitized) > 40 {
				sanitized = sanitized[:40]
			}
			return sanitized
		}

		if model.Provider == "openai" {
			if len(id) > 40 {
				return id[:40]
			}
			return id
		}
		return id
	}

	transformedMessages := TransformMessages(context.Messages, model, func(id string, _ src.Model[src.Api], _ src.AssistantMessage) string {
		return normalizeToolCallId(id)
	})

	// System prompt
	if context.SystemPrompt != "" {
		role := "system"
		if model.Reasoning && compat.SupportsDeveloperRole {
			role = "developer"
		}
		params = append(params, map[string]interface{}{
			"role":    role,
			"content": utils.SanitizeSurrogates(context.SystemPrompt),
		})
	}

	lastRole := ""

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]
		role := msgRole(msg)

		// Insert synthetic assistant message after tool results if needed
		if compat.RequiresAssistantAfterToolResult && lastRole == "toolResult" && role == src.RoleUser {
			params = append(params, map[string]interface{}{
				"role":    "assistant",
				"content": "I have processed the tool results.",
			})
		}

		if role == src.RoleUser {
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}

			// Check if content is a simple string
			isSimpleString := len(um.Content) == 1
			if isSimpleString {
				if _, ok := um.Content[0].(*src.TextContent); !ok {
					isSimpleString = false
				}
			}

			if isSimpleString {
				params = append(params, map[string]interface{}{
					"role":    "user",
					"content": utils.SanitizeSurrogates(um.Content[0].(*src.TextContent).Text),
				})
			} else {
				var content []map[string]interface{}
				for _, item := range um.Content {
					switch c := item.(type) {
					case *src.TextContent:
						content = append(content, map[string]interface{}{
							"type": "text",
							"text": utils.SanitizeSurrogates(c.Text),
						})
					case *src.ImageContent:
						content = append(content, map[string]interface{}{
							"type": "image_url",
							"image_url": map[string]string{
								"url": "data:" + c.MimeType + ";base64," + c.Data,
							},
						})
					}
				}
				if len(content) > 0 {
					params = append(params, map[string]interface{}{
						"role":    "user",
						"content": content,
					})
				}
			}
		} else if role == src.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}

			assistantMsg := map[string]interface{}{
				"role": "assistant",
			}

			if compat.RequiresAssistantAfterToolResult {
				assistantMsg["content"] = ""
			}

			// Process text content
			var textParts []string
			for _, block := range am.Content {
				if tc, ok := block.(*src.TextContent); ok && strings.TrimSpace(tc.Text) != "" {
					textParts = append(textParts, utils.SanitizeSurrogates(tc.Text))
				}
			}

			// Process thinking content
			var thinkingBlocks []*src.ThinkingContent
			for _, block := range am.Content {
				if tc, ok := block.(*src.ThinkingContent); ok && strings.TrimSpace(tc.Thinking) != "" {
					thinkingBlocks = append(thinkingBlocks, tc)
				}
			}

			if len(thinkingBlocks) > 0 {
				if compat.RequiresThinkingAsText {
					var thinkingTexts []string
					for _, tb := range thinkingBlocks {
						thinkingTexts = append(thinkingTexts, utils.SanitizeSurrogates(tb.Thinking))
					}
					assistantMsg["content"] = append([]interface{}{
						map[string]interface{}{
							"type": "text",
							"text": strings.Join(thinkingTexts, "\n\n"),
						},
					}, convertToTextParts(textParts)...)
				} else {
					if len(textParts) > 0 {
						assistantMsg["content"] = strings.Join(textParts, "")
					}
					// Use signature from first thinking block
					if thinkingBlocks[0].ThinkingSignature != "" {
						signatureKey := thinkingBlocks[0].ThinkingSignature
						var thinkingTexts []string
						for _, tb := range thinkingBlocks {
							thinkingTexts = append(thinkingTexts, tb.Thinking)
						}
						assistantMsg[signatureKey] = strings.Join(thinkingTexts, "\n")
					}
				}
			} else if len(textParts) > 0 {
				assistantMsg["content"] = strings.Join(textParts, "")
			}

			// Process tool calls
			var toolCalls []map[string]interface{}
			var reasoningDetails []map[string]interface{}
			for _, block := range am.Content {
				if tc, ok := block.(*src.ToolCall); ok {
					argsJson, _ := json.Marshal(tc.Arguments)
					toolCall := map[string]interface{}{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      tc.Name,
							"arguments": string(argsJson),
						},
					}
					toolCalls = append(toolCalls, toolCall)

					if tc.ThoughtSignature != "" {
						var detail map[string]interface{}
						if err := json.Unmarshal([]byte(tc.ThoughtSignature), &detail); err == nil {
							reasoningDetails = append(reasoningDetails, detail)
						}
					}
				}
			}

			if len(toolCalls) > 0 {
				assistantMsg["tool_calls"] = toolCalls
				if len(reasoningDetails) > 0 {
					assistantMsg["reasoning_details"] = reasoningDetails
				}
			}

			// Some providers require reasoning_content field
			if compat.RequiresReasoningContentOnAssistant && model.Reasoning {
				if _, ok := assistantMsg["reasoning_content"]; !ok {
					assistantMsg["reasoning_content"] = ""
				}
			}

			// Skip empty assistant messages
			hasContent := false
			if content, ok := assistantMsg["content"]; ok {
				switch v := content.(type) {
				case string:
					hasContent = v != ""
				case []interface{}:
					hasContent = len(v) > 0
				}
			}
			if !hasContent && len(toolCalls) == 0 {
				continue
			}

			params = append(params, assistantMsg)
		} else if role == src.RoleTool {
			// Collect consecutive tool results
			var toolResults []map[string]interface{}
			var imageBlocks []map[string]interface{}

			for j := i; j < len(transformedMessages) && msgRole(transformedMessages[j]) == src.RoleTool; j++ {
				toolMsg := transformedMessages[j].(*src.ToolResultMessage[any])

				// Extract text content
				var textParts []string
				hasImages := false
				for _, c := range toolMsg.Content {
					if tc, ok := c.(*src.TextContent); ok {
						textParts = append(textParts, tc.Text)
					}
					if _, ok := c.(*src.ImageContent); ok {
						hasImages = true
					}
				}
				textResult := strings.Join(textParts, "\n")

				toolResultMsg := map[string]interface{}{
					"role":         "tool",
					"content":      utils.SanitizeSurrogates(textResult),
					"tool_call_id": toolMsg.ToolCallID,
				}
				if compat.RequiresToolResultName && toolMsg.ToolName != "" {
					toolResultMsg["name"] = toolMsg.ToolName
				}
				toolResults = append(toolResults, toolResultMsg)

				// Extract images
				if hasImages && hasImageInput(model) {
					for _, c := range toolMsg.Content {
						if ic, ok := c.(*src.ImageContent); ok {
							imageBlocks = append(imageBlocks, map[string]interface{}{
								"type": "image_url",
								"image_url": map[string]string{
									"url": "data:" + ic.MimeType + ";base64," + ic.Data,
								},
							})
						}
					}
				}

				i = j
			}

			// Add tool results
			for _, tr := range toolResults {
				params = append(params, tr)
			}

			// Add images in separate user message
			if len(imageBlocks) > 0 {
				if compat.RequiresAssistantAfterToolResult {
					params = append(params, map[string]interface{}{
						"role":    "assistant",
						"content": "I have processed the tool results.",
					})
				}

				imageContent := []map[string]interface{}{
					{"type": "text", "text": "Attached image(s) from tool result:"},
				}
				imageContent = append(imageContent, imageBlocks...)

				params = append(params, map[string]interface{}{
					"role":    "user",
					"content": imageContent,
				})
				lastRole = "user"
			} else {
				lastRole = "toolResult"
			}
		} else {
			lastRole = string(role)
		}
	}

	return params
}

func convertOpenAICompletionsTools(tools []src.Tool[any], compat *OpenAICompletionsCompat) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		}
		if compat.SupportsStrictMode {
			result[i]["function"].(map[string]interface{})["strict"] = false
		}
	}
	return result
}

func convertToTextParts(textParts []string) []interface{} {
	result := make([]interface{}, len(textParts))
	for i, text := range textParts {
		result[i] = map[string]interface{}{
			"type": "text",
			"text": text,
		}
	}
	return result
}

// =============================================================================
// Cache control
// =============================================================================

func getOpenAICompatCacheControl(compat *OpenAICompletionsCompat, retention src.CacheRetention) map[string]interface{} {
	if compat.CacheControlFormat != "anthropic" || retention == src.CacheRetentionNone {
		return nil
	}

	cacheControl := map[string]interface{}{
		"type": "ephemeral",
	}

	if retention == src.CacheRetentionLong && compat.SupportsLongCacheRetention {
		cacheControl["ttl"] = "1h"
	}

	return cacheControl
}

func applyOpenAICompatCacheControl(messages []map[string]interface{}, tools interface{}, cacheControl map[string]interface{}) {
	addCacheControlToSystemPrompt(messages, cacheControl)
	addCacheControlToLastTool(tools, cacheControl)
	addCacheControlToLastConversationMessage(messages, cacheControl)
}

func addCacheControlToSystemPrompt(messages []map[string]interface{}, cacheControl map[string]interface{}) {
	for _, msg := range messages {
		if role, ok := msg["role"].(string); ok && (role == "system" || role == "developer") {
			addCacheControlToMessage(msg, cacheControl)
			return
		}
	}
}

func addCacheControlToLastConversationMessage(messages []map[string]interface{}, cacheControl map[string]interface{}) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if role, ok := msg["role"].(string); ok && (role == "user" || role == "assistant") {
			if addCacheControlToMessage(msg, cacheControl) {
				return
			}
		}
	}
}

func addCacheControlToLastTool(tools interface{}, cacheControl map[string]interface{}) {
	if tools == nil {
		return
	}

	if toolList, ok := tools.([]map[string]interface{}); ok && len(toolList) > 0 {
		lastTool := toolList[len(toolList)-1]
		lastTool["cache_control"] = cacheControl
	}
}

func addCacheControlToMessage(msg map[string]interface{}, cacheControl map[string]interface{}) bool {
	content, ok := msg["content"]
	if !ok {
		return false
	}

	switch c := content.(type) {
	case string:
		if c == "" {
			return false
		}
		msg["content"] = []map[string]interface{}{
			{
				"type":         "text",
				"text":         c,
				"cache_control": cacheControl,
			},
		}
		return true

	case []interface{}:
		for i := len(c) - 1; i >= 0; i-- {
			if part, ok := c[i].(map[string]interface{}); ok {
				if partType, ok := part["type"].(string); ok && partType == "text" {
					part["cache_control"] = cacheControl
					return true
				}
			}
		}

	case []map[string]interface{}:
		for i := len(c) - 1; i >= 0; i-- {
			if c[i]["type"] == "text" {
				c[i]["cache_control"] = cacheControl
				return true
			}
		}
	}

	return false
}

// =============================================================================
// Stream processing
// =============================================================================

func processOpenAICompletionsStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], compat *OpenAICompletionsCompat) error {
	decoder := json.NewDecoder(body)

	var currentBlock struct {
		Type        string
		Text        string
		Thinking    string
		ID          string
		Name        string
		Arguments   map[string]interface{}
		PartialArgs string
		StreamIndex *int
	}

	currentBlock.Type = ""

	for decoder.More() {
		var chunk map[string]interface{}
		if err := decoder.Decode(&chunk); err != nil {
			return err
		}

		// Set response ID from first chunk
		if id, ok := chunk["id"].(string); ok && output.ResponseID == "" {
			output.ResponseID = id
		}

		// Parse usage
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			output.Usage = parseOpenAICompletionsUsage(usage, model)
		}

		// Get choice
		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}

		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		// Parse finish reason
		if finishReason, ok := choice["finish_reason"].(string); ok {
			result := mapOpenAICompletionsStopReason(finishReason)
			output.StopReason = result.StopReason
			if result.ErrorMessage != "" {
				output.ErrorMessage = result.ErrorMessage
			}
		}

		// Parse delta
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		// Content delta
		if content, ok := delta["content"].(string); ok && content != "" {
			if currentBlock.Type != "text" {
				finishCurrentOpenAICompletionsBlock(ch, output, &currentBlock)
				currentBlock.Type = "text"
				currentBlock.Text = ""
				newBlock := &src.TextContent{Type: "text", Text: ""}
				output.Content = append(output.Content, newBlock)
				idx := len(output.Content) - 1
				ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}
			}

			currentBlock.Text += content
			idx := len(output.Content) - 1
			ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: content, Partial: output}
		}

		// Reasoning content (llama.cpp, other openai compatible)
		reasoningFields := []string{"reasoning_content", "reasoning", "reasoning_text"}
		for _, field := range reasoningFields {
			if reasoning, ok := delta[field].(string); ok && reasoning != "" {
				if currentBlock.Type != "thinking" {
					finishCurrentOpenAICompletionsBlock(ch, output, &currentBlock)
					currentBlock.Type = "thinking"
					currentBlock.Thinking = ""
					newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}
				}

				currentBlock.Thinking += reasoning
				idx := len(output.Content) - 1
				ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &idx, Delta: reasoning, Partial: output}
				break
			}
		}

		// Tool calls
		if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tc := range toolCalls {
				if toolCall, ok := tc.(map[string]interface{}); ok {
					streamIndex := int(0)
					if idx, ok := toolCall["index"].(float64); ok {
						streamIndex = int(idx)
					}

					sameToolCall := currentBlock.Type == "toolCall" &&
						((currentBlock.StreamIndex != nil && *currentBlock.StreamIndex == streamIndex) ||
							(currentBlock.ID != "" && toolCall["id"] != nil && currentBlock.ID == toolCall["id"].(string)))

					if !sameToolCall {
						finishCurrentOpenAICompletionsBlock(ch, output, &currentBlock)
						currentBlock.Type = "toolCall"
						currentBlock.ID = ""
						currentBlock.Name = ""
						currentBlock.Arguments = map[string]interface{}{}
						currentBlock.PartialArgs = ""
						currentBlock.StreamIndex = &streamIndex

						var toolCallName string
						if fn, ok := toolCall["function"].(map[string]interface{}); ok {
							toolCallName = getStringValue(fn, "name")
						}

						newBlock := &src.ToolCall{
							Type:      "toolCall",
							ID:        getStringValue(toolCall, "id"),
							Name:      toolCallName,
							Arguments: map[string]interface{}{},
						}
						output.Content = append(output.Content, newBlock)
						idx := len(output.Content) - 1
						ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
					}

					// Update tool call fields
					if currentBlock.ID == "" {
						if id, ok := toolCall["id"].(string); ok {
							currentBlock.ID = id
						}
					}
					if currentBlock.Name == "" {
						if name, ok := toolCall["function"].(map[string]interface{}); ok {
							if n, ok := name["name"].(string); ok {
								currentBlock.Name = n
							}
						}
					}
					if currentBlock.StreamIndex == nil {
						currentBlock.StreamIndex = &streamIndex
					}

					var delta string
					if fn, ok := toolCall["function"].(map[string]interface{}); ok {
						if args, ok := fn["arguments"].(string); ok {
							delta = args
							currentBlock.PartialArgs += args
							currentBlock.Arguments = parseStreamingJson(currentBlock.Arguments, currentBlock.PartialArgs)
						}
					}

					idx := len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: &idx, Delta: delta, Partial: output}
				}
			}
		}

		// Reasoning details for tool calls
		if reasoningDetails, ok := delta["reasoning_details"].([]interface{}); ok {
			for _, detail := range reasoningDetails {
				if d, ok := detail.(map[string]interface{}); ok {
					if detailType, ok := d["type"].(string); ok && detailType == "reasoning.encrypted" {
						if id, ok := d["id"].(string); ok {
							for _, block := range output.Content {
								if tc, ok := block.(*src.ToolCall); ok && tc.ID == id {
									if detailJson, err := json.Marshal(d); err == nil {
										tc.ThoughtSignature = string(detailJson)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	finishCurrentOpenAICompletionsBlock(ch, output, &currentBlock)
	return nil
}

func finishCurrentOpenAICompletionsBlock(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, block *struct {
	Type        string
	Text        string
	Thinking    string
	ID          string
	Name        string
	Arguments   map[string]interface{}
	PartialArgs string
	StreamIndex *int
}) {
	switch block.Type {
	case "text":
		if len(output.Content) > 0 {
			idx := len(output.Content) - 1
			if tc, ok := output.Content[idx].(*src.TextContent); ok {
				ch <- src.AssistantMessageEvent{Type: "text_end", ContentIndex: &idx, Content: tc.Text, Partial: output}
			}
		}
	case "thinking":
		if len(output.Content) > 0 {
			idx := len(output.Content) - 1
			if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
				ch <- src.AssistantMessageEvent{Type: "thinking_end", ContentIndex: &idx, Content: tc.Thinking, Partial: output}
			}
		}
	case "toolCall":
		if len(output.Content) > 0 {
			idx := len(output.Content) - 1
			if tc, ok := output.Content[idx].(*src.ToolCall); ok {
				tc.Arguments = parseStreamingJson(tc.Arguments, block.PartialArgs)
				ch <- src.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: &idx, ToolCall: tc, Partial: output}
			}
		}
	}

	block.Type = ""
	block.Text = ""
	block.Thinking = ""
	block.ID = ""
	block.Name = ""
	block.Arguments = nil
	block.PartialArgs = ""
	block.StreamIndex = nil
}

func parseOpenAICompletionsUsage(usage map[string]interface{}, model src.Model[src.Api]) src.Usage {
	promptTokens := getFloatValue(usage, "prompt_tokens")
	completionTokens := getFloatValue(usage, "completion_tokens")

	var cachedTokens, cacheWriteTokens float64
	if details, ok := usage["prompt_tokens_details"].(map[string]interface{}); ok {
		cachedTokens = getFloatValue(details, "cached_tokens")
		cacheWriteTokens = getFloatValue(details, "cache_write_tokens")
	}

	// Normalize cache read tokens
	cacheReadTokens := cachedTokens
	if cacheWriteTokens > 0 {
		cacheReadTokens = max(0, cachedTokens-cacheWriteTokens)
	}

	input := max(0.0, promptTokens-cacheReadTokens-cacheWriteTokens)

	return src.Usage{
		Input:       int(input),
		Output:      int(completionTokens),
		CacheRead:   int(cacheReadTokens),
		CacheWrite:  int(cacheWriteTokens),
		TotalTokens: int(input + completionTokens + cacheReadTokens + cacheWriteTokens),
		Cost:        src.UsageCost{},
	}
}

// =============================================================================
// Compat detection
// =============================================================================

func detectOpenAICompat(model src.Model[src.Api]) *OpenAICompletionsCompat {
	compat := &OpenAICompletionsCompat{
		SupportsStore:                    true,
		SupportsDeveloperRole:            true,
		SupportsReasoningEffort:          true,
		ReasoningEffortMap:               map[string]string{},
		SupportsUsageInStreaming:         true,
		MaxTokensField:                   "max_completion_tokens",
		RequiresToolResultName:           false,
		RequiresAssistantAfterToolResult: false,
		RequiresThinkingAsText:           false,
		RequiresReasoningContentOnAssistant: false,
		ThinkingFormat:                   "openai",
		OpenRouterRouting:                map[string]string{},
		VercelGatewayRouting:             map[string]interface{}{},
		ZaiToolStream:                    false,
		SupportsStrictMode:               true,
		CacheControlFormat:               "",
		SendSessionAffinityHeaders:       false,
		SupportsLongCacheRetention:       true,
	}

	provider := model.Provider
	baseURL := model.BaseURL

	isZai := provider == "zai" || strings.Contains(baseURL, "api.z.ai")
	isCloudflareWorkersAI := IsCloudflareProvider(string(provider))
	isNonStandard := provider == "cerebras" || strings.Contains(baseURL, "cerebras.ai") ||
		provider == "xai" || strings.Contains(baseURL, "api.x.ai") ||
		strings.Contains(baseURL, "chutes.ai") || strings.Contains(baseURL, "deepseek.com") ||
		isZai || provider == "opencode" || strings.Contains(baseURL, "opencode.ai") ||
		isCloudflareWorkersAI

	if isNonStandard {
		compat.SupportsStore = false
		compat.SupportsDeveloperRole = false
	}

	if strings.Contains(baseURL, "chutes.ai") {
		compat.MaxTokensField = "max_tokens"
	}

	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	if isGrok {
		compat.SupportsReasoningEffort = false
	}

	isDeepSeek := provider == "deepseek" || strings.Contains(baseURL, "deepseek.com")
	if isDeepSeek {
		compat.ThinkingFormat = "deepseek"
		compat.RequiresReasoningContentOnAssistant = true
		compat.ReasoningEffortMap = map[string]string{
			"minimal": "high",
			"low":     "high",
			"medium":  "high",
			"high":    "high",
			"xhigh":   "max",
		}
	}

	if isZai {
		compat.SupportsReasoningEffort = false
		compat.ThinkingFormat = "zai"
	}

	if strings.Contains(baseURL, "openrouter.ai") {
		compat.ThinkingFormat = "openrouter"
		if strings.HasPrefix(model.ID, "anthropic/") {
			compat.CacheControlFormat = "anthropic"
		}
	}

	if provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai") {
		compat.ThinkingFormat = "openrouter"
	}

	return compat
}

func mapOpenAIReasoningEffort(effort string, effortMap map[string]string) string {
	if mapped, ok := effortMap[effort]; ok {
		return mapped
	}
	return effort
}

func mapOpenAICompletionsStopReason(reason string) struct{ StopReason src.StopReason; ErrorMessage string } {
	switch reason {
	case "stop", "end":
		return struct{ StopReason src.StopReason; ErrorMessage string }{
			StopReason: src.StopReasonStop,
		}
	case "length":
		return struct{ StopReason src.StopReason; ErrorMessage string }{
			StopReason: src.StopReasonLength,
		}
	case "function_call", "tool_calls":
		return struct{ StopReason src.StopReason; ErrorMessage string }{
			StopReason: src.StopReasonToolUse,
		}
	case "content_filter":
		return struct{ StopReason src.StopReason; ErrorMessage string }{
			StopReason:  src.StopReasonError,
			ErrorMessage: "Provider finish_reason: content_filter",
		}
	case "network_error":
		return struct{ StopReason src.StopReason; ErrorMessage string }{
			StopReason:  src.StopReasonError,
			ErrorMessage: "Provider finish_reason: network_error",
		}
	default:
		return struct{ StopReason src.StopReason; ErrorMessage string }{
			StopReason:  src.StopReasonError,
			ErrorMessage: "Provider finish_reason: " + reason,
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

func hasToolHistory(messages []src.Message) bool {
	for _, msg := range messages {
		role := msgRole(msg)
		if role == src.RoleTool {
			return true
		}
		if role == src.RoleAssistant {
			if am := okAssistant(msg); am != nil {
				for _, block := range am.Content {
					if _, ok := block.(*src.ToolCall); ok {
						return true
					}
				}
			}
		}
	}
	return false
}

func getStringValue(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getFloatValue(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch f := v.(type) {
		case float64:
			return f
		case int:
			return float64(f)
		case float32:
			return float64(f)
		}
	}
	return 0
}

// =============================================================================
// Simple stream wrapper
// =============================================================================

// StreamSimpleOpenAICompletions provides a simplified streaming interface for OpenAI Completions
func StreamSimpleOpenAICompletions(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	opts := buildOpenAICompletionsSimpleOptions(options)
	return StreamOpenAICompletions(model, context, opts)
}

func buildOpenAICompletionsSimpleOptions(options *src.SimpleStreamOptions) *OpenAICompletionsOptions {
	if options == nil {
		return nil
	}

	reasoningEffort := ""
	if options.Reasoning != "" {
		reasoningEffort = string(options.Reasoning)
	}

	return &OpenAICompletionsOptions{
		APIKey:          options.APIKey,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		ReasoningEffort: &reasoningEffort,
		Headers:         options.Headers,
	}
}
