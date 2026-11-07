package providers

import (
	"bufio"
	"bytes"
	gocontext "context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	mistralDefaultBaseURL  = "https://api.mistral.ai"
	mistralChatCompletionsPath = "/v1/chat/completions"
	mistralToolCallIDLength    = 9
	mistralMaxErrorBodyChars   = 4000
)

// =============================================================================
// Types
// =============================================================================

// MistralOptions mirrors MistralOptions in mistral.ts plus the Go Context seam.
type MistralOptions struct {
	APIKey          string
	MaxTokens       *int
	Temperature     *float64
	ToolChoice      interface{}
	PromptMode      *string
	ReasoningEffort *string
	SessionID       string
	Headers         map[string]string
	Context         gocontext.Context
	// Callbacks
	OnPayload func(params interface{}, model src.Model[src.Api]) (interface{}, error)
}

type MistralToolCallIDNormalizer struct {
	idMap      map[string]string
	reverseMap map[string]string
}

// =============================================================================
// Provider entry points
// =============================================================================

// StreamMistral creates an async streaming function for Mistral Conversations API
func StreamMistral(model src.Model[src.Api], context src.ChatContext, options *MistralOptions) func() <-chan src.AssistantMessageEvent {
	return func() <-chan src.AssistantMessageEvent {
		ch := make(chan src.AssistantMessageEvent, 256)

		go func() {
			defer close(ch)

			ctx := gocontext.Background()
			if options != nil && options.Context != nil {
				ctx = options.Context
			}

			output := &src.AssistantMessage{
				Role:       src.RoleAssistant,
				Content:    []src.ContentBlock{},
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      src.Usage{Cost: src.UsageCost{}},
				StopReason: src.StopReasonStop,
			}

			apiKey := ""
			if options != nil && options.APIKey != "" {
				apiKey = options.APIKey
			}
			if apiKey == "" {
				apiKey = getEnvApiKey(string(model.Provider))
			}
			if apiKey == "" {
				handleMistralError(ch, output, fmt.Errorf("No API key for provider: %s", model.Provider), false)
				return
			}

			normalizer := NewMistralToolCallIDNormalizer()
			transformedMessages := TransformMessages(context.Messages, model, func(id string, _ src.Model[src.Api], _ src.AssistantMessage) string {
				return normalizer.Normalize(id)
			})

			params := buildMistralParams(model, context, transformedMessages, options)

			if options != nil && options.OnPayload != nil {
				if nextParams, e := options.OnPayload(params, model); e == nil && nextParams != nil {
					params = nextParams
				}
			}

			resp, err := doMistralRequest(ctx, model, params, options, apiKey)
			if err != nil {
				if ctx.Err() != nil {
					handleMistralError(ch, output, fmt.Errorf("Request was aborted"), true)
				} else {
					handleMistralError(ch, output, err, false)
				}
				return
			}
			defer resp.Body.Close()

			ch <- src.AssistantMessageEvent{Type: "start", Partial: output}

			if err := processMistralStream(resp.Body, ch, output, model); err != nil {
				if ctx.Err() != nil {
					handleMistralError(ch, output, fmt.Errorf("Request was aborted"), true)
				} else {
					handleMistralError(ch, output, err, false)
				}
				return
			}

			if ctx.Err() != nil {
				handleMistralError(ch, output, fmt.Errorf("Request was aborted"), true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleMistralError(ch, output, fmt.Errorf("An unknown error occurred"), false)
				return
			}

			ch <- src.AssistantMessageEvent{Type: "done", Reason: output.StopReason, Message: output}
		}()

		return ch
	}
}

// StreamSimpleMistral maps provider-agnostic SimpleStreamOptions to Mistral
// options (streamSimpleMistral in mistral.ts).
func StreamSimpleMistral(model src.Model[src.Api], context src.ChatContext, options *src.SimpleStreamOptions) func() <-chan src.AssistantMessageEvent {
	apiKey := ""
	if options != nil {
		apiKey = options.APIKey
	}
	if apiKey == "" {
		apiKey = getEnvApiKey(string(model.Provider))
	}
	if apiKey == "" {
		return noKeyErrorStream(model, fmt.Errorf("No API key for provider: %s", model.Provider))
	}

	base := BuildBaseOptions(model, options, "")
	base.APIKey = apiKey

	opts := &MistralOptions{
		APIKey:      base.APIKey,
		MaxTokens:   base.MaxTokens,
		Temperature: base.Temperature,
		Headers:     base.Headers,
		SessionID:   base.SessionID,
		Context:     base.Context,
	}

	level := ""
	if options != nil && options.Reasoning != "" {
		clamped := src.ClampThinkingLevel(&model, options.Reasoning)
		if clamped != src.Off {
			level = string(clamped)
		}
	}
	shouldUseReasoning := model.Reasoning && level != ""

	if shouldUseReasoning && !usesMistralReasoningEffort(model) {
		promptMode := "reasoning"
		opts.PromptMode = &promptMode
	}
	if shouldUseReasoning && usesMistralReasoningEffort(model) {
		opts.ReasoningEffort = mapMistralReasoningEffort(model, level)
	}

	return StreamMistral(model, context, opts)
}

// =============================================================================
// Request handling
// =============================================================================

func doMistralRequest(ctx gocontext.Context, model src.Model[src.Api], params interface{}, options *MistralOptions, apiKey string) (*http.Response, error) {
	baseURL := mistralDefaultBaseURL
	if model.BaseURL != "" {
		baseURL = strings.TrimRight(model.BaseURL, "/")
	}
	endpoint := baseURL + mistralChatCompletionsPath

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Mistral infrastructure uses `x-affinity` for KV-cache reuse (prefix
	// caching). Respect explicit caller-provided header values.
	sessionID := ""
	if options != nil {
		sessionID = options.SessionID
	}
	if sessionID != "" {
		hasAffinity := false
		if _, ok := model.Headers["x-affinity"]; ok {
			hasAffinity = true
		}
		if options != nil {
			if _, ok := options.Headers["x-affinity"]; ok {
				hasAffinity = true
			}
		}
		if !hasAffinity {
			req.Header.Set("x-affinity", sessionID)
		}
	}

	// Add options headers
	if options != nil {
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		bodyText := strings.TrimSpace(string(raw))
		if bodyText != "" {
			return nil, fmt.Errorf("Mistral API error (%d): %s", resp.StatusCode, truncateMistralErrorText(bodyText, mistralMaxErrorBodyChars))
		}
		return nil, fmt.Errorf("Mistral API error (%d): Response returned an error code", resp.StatusCode)
	}

	return resp, nil
}

func truncateMistralErrorText(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return fmt.Sprintf("%s... [truncated %d chars]", text[:maxChars], len(text)-maxChars)
}

func handleMistralError(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, err error, isAborted bool) {
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

func buildMistralParams(model src.Model[src.Api], context src.ChatContext, transformedMessages []src.Message, options *MistralOptions) interface{} {
	supportsImages := hasImageInput(model)

	messages := toMistralChatMessages(transformedMessages, supportsImages)

	// Prepend system prompt
	if context.SystemPrompt != "" {
		systemMsg := map[string]interface{}{
			"role":    "system",
			"content": utils.SanitizeSurrogates(context.SystemPrompt),
		}
		messages = append([]map[string]interface{}{systemMsg}, messages...)
	}

	params := map[string]interface{}{
		"model":    model.ID,
		"stream":   true,
		"messages": messages,
	}

	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	if options != nil && options.MaxTokens != nil {
		params["max_tokens"] = *options.MaxTokens
	}

	if len(context.Tools) > 0 {
		params["tools"] = toMistralFunctionTools(context.Tools)
	}

	if options != nil && options.ToolChoice != nil {
		params["tool_choice"] = mapMistralToolChoice(options.ToolChoice)
	}

	if options != nil && options.PromptMode != nil {
		params["prompt_mode"] = *options.PromptMode
	}

	if options != nil && options.ReasoningEffort != nil {
		params["reasoning_effort"] = *options.ReasoningEffort
	}

	return params
}

func toMistralChatMessages(messages []src.Message, supportsImages bool) []map[string]interface{} {
	var result []map[string]interface{}

	for _, msg := range messages {
		role := msgRole(msg)

		if role == src.RoleUser {
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}

			// Simple string content
			if len(um.Content) == 1 {
				if tc, ok := um.Content[0].(*src.TextContent); ok {
					result = append(result, map[string]interface{}{
						"role":    "user",
						"content": utils.SanitizeSurrogates(tc.Text),
					})
					continue
				}
			}

			// Array content
			var content []map[string]interface{}
			hadImages := false
			for _, item := range um.Content {
				switch c := item.(type) {
				case *src.TextContent:
					content = append(content, map[string]interface{}{
						"type": "text",
						"text": utils.SanitizeSurrogates(c.Text),
					})
				case *src.ImageContent:
					hadImages = true
					if supportsImages {
						content = append(content, map[string]interface{}{
							"type":      "image_url",
							"image_url": "data:" + c.MimeType + ";base64," + c.Data,
						})
					}
				}
			}

			if len(content) > 0 {
				result = append(result, map[string]interface{}{
					"role":    "user",
					"content": content,
				})
				continue
			}

			if hadImages && !supportsImages {
				result = append(result, map[string]interface{}{
					"role":    "user",
					"content": "(image omitted: model does not support images)",
				})
			}

		} else if role == src.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}

			var contentParts []map[string]interface{}
			var toolCalls []map[string]interface{}

			for _, block := range am.Content {
				switch b := block.(type) {
				case *src.TextContent:
					if strings.TrimSpace(b.Text) != "" {
						contentParts = append(contentParts, map[string]interface{}{
							"type": "text",
							"text": utils.SanitizeSurrogates(b.Text),
						})
					}
				case *src.ThinkingContent:
					if strings.TrimSpace(b.Thinking) != "" {
						contentParts = append(contentParts, map[string]interface{}{
							"type": "thinking",
							"thinking": []map[string]interface{}{
								{
									"type": "text",
									"text": utils.SanitizeSurrogates(b.Thinking),
								},
							},
						})
					}
				case *src.ToolCall:
					argsJson, _ := json.Marshal(b.Arguments)
					toolCalls = append(toolCalls, map[string]interface{}{
						"id":   b.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      b.Name,
							"arguments": string(argsJson),
						},
					})
				}
			}

			assistantMsg := map[string]interface{}{
				"role":   "assistant",
				"prefix": false,
			}

			if len(contentParts) > 0 {
				assistantMsg["content"] = contentParts
			}
			if len(toolCalls) > 0 {
				assistantMsg["tool_calls"] = toolCalls
			}

			if len(contentParts) > 0 || len(toolCalls) > 0 {
				result = append(result, assistantMsg)
			}

		} else if role == src.RoleTool {
			tr, ok := msg.(*src.ToolResultMessage[any])
			if !ok {
				continue
			}

			var toolContent []map[string]interface{}

			// Text content
			var textParts []string
			for _, c := range tr.Content {
				if tc, ok := c.(*src.TextContent); ok {
					textParts = append(textParts, tc.Text)
				}
			}
			textResult := strings.Join(textParts, "\n")

			// Check for images
			hasImages := false
			for _, c := range tr.Content {
				if _, ok := c.(*src.ImageContent); ok {
					hasImages = true
					break
				}
			}

			toolText := buildMistralToolResultText(textResult, hasImages, supportsImages, tr.IsError)
			toolContent = append(toolContent, map[string]interface{}{
				"type": "text",
				"text": toolText,
			})

			// Add images if supported
			if supportsImages {
				for _, c := range tr.Content {
					if ic, ok := c.(*src.ImageContent); ok {
						toolContent = append(toolContent, map[string]interface{}{
							"type":      "image_url",
							"image_url": "data:" + ic.MimeType + ";base64," + ic.Data,
						})
					}
				}
			}

			result = append(result, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tr.ToolCallID,
				"name":         tr.ToolName,
				"content":      toolContent,
			})
		}
	}

	return result
}

func toMistralFunctionTools(tools []src.Tool[any]) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  stripMistralSymbolKeys(tool.Parameters),
				"strict":      false,
			},
		}
	}
	return result
}

func stripMistralSymbolKeys(value interface{}) interface{} {
	switch v := value.(type) {
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = stripMistralSymbolKeys(item)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, val := range v {
			result[key] = stripMistralSymbolKeys(val)
		}
		return result
	default:
		return v
	}
}

func buildMistralToolResultText(text string, hasImages, supportsImages, isError bool) string {
	trimmed := strings.TrimSpace(text)
	errorPrefix := ""
	if isError {
		errorPrefix = "[tool error] "
	}

	if trimmed != "" {
		imageSuffix := ""
		if hasImages && !supportsImages {
			imageSuffix = "\n[tool image omitted: model does not support images]"
		}
		return errorPrefix + trimmed + imageSuffix
	}

	if hasImages {
		if supportsImages {
			if isError {
				return "[tool error] (see attached image)"
			}
			return "(see attached image)"
		}
		if isError {
			return "[tool error] (image omitted: model does not support images)"
		}
		return "(image omitted: model does not support images)"
	}

	if isError {
		return "[tool error] (no tool output)"
	}
	return "(no tool output)"
}

func mapMistralToolChoice(choice interface{}) interface{} {
	if choice == nil {
		return nil
	}

	switch v := choice.(type) {
	case string:
		switch v {
		case "auto", "none", "any", "required":
			return v
		default:
			return v
		}
	case map[string]interface{}:
		if typ, ok := v["type"].(string); ok && typ == "function" {
			if fn, ok := v["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok {
					return map[string]interface{}{
						"type": "function",
						"function": map[string]interface{}{
							"name": name,
						},
					}
				}
			}
		}
	}

	return nil
}

// =============================================================================
// Stream processing
// =============================================================================

func processMistralStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api]) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var currentBlock struct {
		Type  string
		Text  string
		delta string
	}
	currentBlock.Type = ""

	toolBlocksByKey := make(map[string]int)
	partialArgsByKey := make(map[string]string)
	var toolKeyOrder []string

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("Could not parse Mistral SSE event: %v", err)
		}

		// Set response ID
		if id, ok := chunk["id"].(string); ok && output.ResponseID == "" {
			output.ResponseID = id
		}

		// Parse usage
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			output.Usage.Input = int(getFloatValue(usage, "prompt_tokens"))
			output.Usage.Output = int(getFloatValue(usage, "completion_tokens"))
			output.Usage.CacheRead = 0
			output.Usage.CacheWrite = 0
			output.Usage.TotalTokens = int(getFloatValue(usage, "total_tokens"))
			if output.Usage.TotalTokens == 0 {
				output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output
			}
			output.Usage.Cost = usageCostFromModelCost(src.CalculateCost(&model, &output.Usage))
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
			output.StopReason = mapMistralStopReason(finishReason)
		}

		// Parse delta
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		// Content delta
		if content, ok := delta["content"]; ok && content != nil {
			var contentItems []interface{}
			switch c := content.(type) {
			case string:
				contentItems = []interface{}{c}
			case []interface{}:
				contentItems = c
			}

			for _, item := range contentItems {
				if itemStr, ok := item.(string); ok {
					textDelta := utils.SanitizeSurrogates(itemStr)
					if currentBlock.Type != "text" {
						finishCurrentMistralBlock(ch, output, &currentBlock)
						currentBlock.Type = "text"
						currentBlock.Text = ""
						newBlock := &src.TextContent{Type: "text", Text: ""}
						output.Content = append(output.Content, newBlock)
						idx := len(output.Content) - 1
						ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}
					}

					currentBlock.Text += textDelta
					if tc, ok := output.Content[len(output.Content)-1].(*src.TextContent); ok {
						tc.Text = currentBlock.Text
					}
					idx := len(output.Content) - 1
					ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: textDelta, Partial: output}
				}

				if itemMap, ok := item.(map[string]interface{}); ok {
					if itemType, ok := itemMap["type"].(string); ok {
						switch itemType {
						case "thinking":
							if thinking, ok := itemMap["thinking"].([]interface{}); ok {
								var thinkingText strings.Builder
								for _, part := range thinking {
									if partMap, ok := part.(map[string]interface{}); ok {
										if text, ok := partMap["text"].(string); ok {
											thinkingText.WriteString(text)
										}
									}
								}
								thinkingDelta := utils.SanitizeSurrogates(thinkingText.String())
								if thinkingDelta == "" {
									continue
								}

								if currentBlock.Type != "thinking" {
									finishCurrentMistralBlock(ch, output, &currentBlock)
									currentBlock.Type = "thinking"
									currentBlock.Text = ""
									newBlock := &src.ThinkingContent{Type: "thinking", Thinking: ""}
									output.Content = append(output.Content, newBlock)
									idx := len(output.Content) - 1
									ch <- src.AssistantMessageEvent{Type: "thinking_start", ContentIndex: &idx, Partial: output}
								}

								currentBlock.Text += thinkingDelta
								idx := len(output.Content) - 1
								if tc, ok := output.Content[idx].(*src.ThinkingContent); ok {
									tc.Thinking = currentBlock.Text
								}
								ch <- src.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: &idx, Delta: thinkingDelta, Partial: output}
							}

						case "text":
							if text, ok := itemMap["text"].(string); ok {
								textDelta := utils.SanitizeSurrogates(text)
								if currentBlock.Type != "text" {
									finishCurrentMistralBlock(ch, output, &currentBlock)
									currentBlock.Type = "text"
									currentBlock.Text = ""
									newBlock := &src.TextContent{Type: "text", Text: ""}
									output.Content = append(output.Content, newBlock)
									idx := len(output.Content) - 1
									ch <- src.AssistantMessageEvent{Type: "text_start", ContentIndex: &idx, Partial: output}
								}

								currentBlock.Text += textDelta
								if tc, ok := output.Content[len(output.Content)-1].(*src.TextContent); ok {
									tc.Text = currentBlock.Text
								}
								idx := len(output.Content) - 1
								ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: textDelta, Partial: output}
							}
						}
					}
				}
			}
		}

		// Tool calls
		if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tc := range toolCalls {
				toolCall, ok := tc.(map[string]interface{})
				if !ok {
					continue
				}

				finishCurrentMistralBlock(ch, output, &currentBlock)

				// Get tool call ID
				callId := ""
				if id, ok := toolCall["id"].(string); ok && id != "" && id != "null" {
					callId = id
				} else {
					index := 0
					if idx, ok := toolCall["index"].(float64); ok {
						index = int(idx)
					}
					callId = deriveMistralToolCallId(fmt.Sprintf("toolcall:%d", index), 0)
				}

				index := 0
				if idx, ok := toolCall["index"].(float64); ok {
					index = int(idx)
				}
				key := fmt.Sprintf("%s:%d", callId, index)

				// Get function info
				name := ""
				var argsDelta string
				if fn, ok := toolCall["function"].(map[string]interface{}); ok {
					name, _ = fn["name"].(string)
					if args, ok := fn["arguments"].(string); ok {
						argsDelta = args
					} else if argsMap, ok := fn["arguments"].(map[string]interface{}); ok {
						if argsJson, err := json.Marshal(argsMap); err == nil {
							argsDelta = string(argsJson)
						}
					}
				}

				if _, exists := toolBlocksByKey[key]; !exists {
					newBlock := &src.ToolCall{
						Type:      "toolCall",
						ID:        callId,
						Name:      name,
						Arguments: map[string]interface{}{},
					}
					output.Content = append(output.Content, newBlock)
					idx := len(output.Content) - 1
					toolBlocksByKey[key] = idx
					toolKeyOrder = append(toolKeyOrder, key)
					ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
				} else if name != "" {
					if existing, ok := output.Content[toolBlocksByKey[key]].(*src.ToolCall); ok && existing.Name == "" {
						existing.Name = name
					}
				}

				// Arguments accumulate as one concatenated buffer, re-parsed
				// each chunk (partialArgs in mistral.ts).
				partialArgsByKey[key] += argsDelta
				parsed := parseStreamingJson(partialArgsByKey[key])
				idx := toolBlocksByKey[key]
				if block, ok := output.Content[idx].(*src.ToolCall); ok {
					block.Arguments = parsed
				}
				ch <- src.AssistantMessageEvent{
					Type:         "toolcall_delta",
					ContentIndex: &idx,
					Delta:        argsDelta,
					Partial:      output,
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	finishCurrentMistralBlock(ch, output, &currentBlock)

	// Finalize tool calls in insertion order (TS iterates the Map's values),
	// re-parsing each accumulated buffer and dropping the scratch state.
	for _, key := range toolKeyOrder {
		idx, ok := toolBlocksByKey[key]
		if !ok || idx >= len(output.Content) {
			continue
		}
		toolCall, ok := output.Content[idx].(*src.ToolCall)
		if !ok {
			continue
		}
		toolCall.Arguments = parseStreamingJson(partialArgsByKey[key])
		ch <- src.AssistantMessageEvent{
			Type:         "toolcall_end",
			ContentIndex: &idx,
			ToolCall:     toolCall,
			Partial:      output,
		}
	}

	return nil
}

func finishCurrentMistralBlock(ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, block *struct {
	Type  string
	Text  string
	delta string
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
	}

	block.Type = ""
	block.Text = ""
}

func mapMistralStopReason(reason string) src.StopReason {
	switch reason {
	case "stop":
		return src.StopReasonStop
	case "length", "model_length":
		return src.StopReasonLength
	case "tool_calls":
		return src.StopReasonToolUse
	case "error":
		return src.StopReasonError
	default:
		return src.StopReasonStop
	}
}

// =============================================================================
// Simple wrapper reasoning routing
// =============================================================================

func usesMistralReasoningEffort(model src.Model[src.Api]) bool {
	return model.ID == "mistral-small-2603" || model.ID == "mistral-small-latest" || model.ID == "mistral-medium-3.5"
}

func mapMistralReasoningEffort(model src.Model[src.Api], level string) *string {
	effort := "high"
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
			effort = string(*mapped)
		}
	}
	return &effort
}

// =============================================================================
// MistralToolCallIDNormalizer
// =============================================================================

func NewMistralToolCallIDNormalizer() *MistralToolCallIDNormalizer {
	return &MistralToolCallIDNormalizer{
		idMap:      make(map[string]string),
		reverseMap: make(map[string]string),
	}
}

func (n *MistralToolCallIDNormalizer) Normalize(id string) string {
	if existing, ok := n.idMap[id]; ok {
		return existing
	}

	for attempt := 0; ; attempt++ {
		candidate := deriveMistralToolCallId(id, attempt)
		if owner, ok := n.reverseMap[candidate]; !ok || owner == id {
			n.idMap[id] = candidate
			n.reverseMap[candidate] = id
			return candidate
		}
	}
}

func deriveMistralToolCallId(id string, attempt int) string {
	// Remove non-alphanumeric characters
	var normalized strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		}
	}

	if attempt == 0 && normalized.Len() == mistralToolCallIDLength {
		return normalized.String()
	}

	seedBase := normalized.String()
	if seedBase == "" {
		seedBase = id
	}

	seed := seedBase
	if attempt > 0 {
		seed = fmt.Sprintf("%s:%d", seedBase, attempt)
	}

	hash := utils.ShortHash(seed)

	// Remove non-alphanumeric from hash
	var result strings.Builder
	for _, r := range hash {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		}
	}

	if result.Len() > mistralToolCallIDLength {
		return result.String()[:mistralToolCallIDLength]
	}
	return result.String()
}
