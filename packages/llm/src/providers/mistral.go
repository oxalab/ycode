package providers

import (
	"bytes"
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
	mistralAPIBaseURL        = "https://api.mistral.ai/v1/chat/completions"
	mistralToolCallIDLength = 9
)

// =============================================================================
// Types
// =============================================================================

type MistralOptions struct {
	APIKey         string
	MaxTokens      *int
	Temperature    *float64
	ToolChoice     interface{}
	PromptMode     *string
	ReasoningEffort *string
	SessionID      string
	Headers        map[string]string
	// Callbacks
	OnPayload  func(params interface{}, model src.Model[src.Api]) (interface{}, error)
	OnResponse func(resp interface{}, model src.Model[src.Api])
}

type MistralToolCallIDNormalizer struct {
	idMap      map[string]string
	reverseMap map[string]string
}

// =============================================================================
// Provider registration
// =============================================================================

// StreamMistral creates an async streaming function for Mistral Conversations API
func StreamMistral(model src.Model[src.Api], context src.ChatContext, options *MistralOptions) func() <-chan src.AssistantMessageEvent {
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

			resp, err := doMistralRequest(client, model, params, options)
			if err != nil {
				handleMistralError(ch, output, err, false)
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
			if err := processMistralStream(resp.Body, ch, output, model, normalizer); err != nil {
				handleMistralError(ch, output, err, true)
				return
			}

			if output.StopReason == src.StopReasonError || output.StopReason == src.StopReasonAborted {
				handleMistralError(ch, output, fmt.Errorf("stream ended with error: %s", output.StopReason), false)
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

func doMistralRequest(client *http.Client, model src.Model[src.Api], params interface{}, options *MistralOptions) (*http.Response, error) {
	baseURL := mistralAPIBaseURL
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
		return nil, fmt.Errorf("No API key for provider: %s", model.Provider)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Add model headers
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}

	// Add session affinity header
	if options != nil && options.SessionID != "" {
		hasAffinity := false
		if options.Headers != nil {
			if _, ok := options.Headers["x-affinity"]; ok {
				hasAffinity = true
			}
		}
		if !hasAffinity {
			req.Header.Set("x-affinity", options.SessionID)
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
		params["maxTokens"] = *options.MaxTokens
	}

	if len(context.Tools) > 0 {
		params["tools"] = toMistralFunctionTools(context.Tools)
	}

	if options != nil && options.ToolChoice != nil {
		params["toolChoice"] = mapMistralToolChoice(options.ToolChoice)
	}

	if options != nil && options.PromptMode != nil {
		params["promptMode"] = *options.PromptMode
	}

	if options != nil && options.ReasoningEffort != nil {
		params["reasoningEffort"] = *options.ReasoningEffort
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
							"imageUrl": "data:" + c.MimeType + ";base64," + c.Data,
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
				"role": "assistant",
			}

			if len(contentParts) > 0 {
				assistantMsg["content"] = contentParts
			}
			if len(toolCalls) > 0 {
				assistantMsg["toolCalls"] = toolCalls
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
							"imageUrl": "data:" + ic.MimeType + ";base64," + ic.Data,
						})
					}
				}
			}

			result = append(result, map[string]interface{}{
				"role":       "tool",
				"toolCallId": tr.ToolCallID,
				"name":       tr.ToolName,
				"content":    toolContent,
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
			return "auto"
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

	return "auto"
}

// =============================================================================
// Stream processing
// =============================================================================

func processMistralStream(body io.ReadCloser, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], normalizer *MistralToolCallIDNormalizer) error {
	decoder := json.NewDecoder(body)

	var currentBlock struct {
		Type  string
		Text  string
		delta string
	}
	currentBlock.Type = ""

	toolBlocksByKey := make(map[string]int)

	for decoder.More() {
		var chunk map[string]interface{}
		if err := decoder.Decode(&chunk); err != nil {
			return err
		}

		// Set response ID
		if id, ok := chunk["id"].(string); ok && output.ResponseID == "" {
			output.ResponseID = id
		}

		// Parse usage
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			output.Usage.Input = int(getFloatValue(usage, "promptTokens"))
			output.Usage.Output = int(getFloatValue(usage, "completionTokens"))
			output.Usage.TotalTokens = int(getFloatValue(usage, "totalTokens"))
			if output.Usage.TotalTokens == 0 {
				output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output
			}
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
								idx := len(output.Content) - 1
								ch <- src.AssistantMessageEvent{Type: "text_delta", ContentIndex: &idx, Delta: textDelta, Partial: output}
							}
						}
					}
				}
			}
		}

		// Tool calls
		if toolCalls, ok := delta["toolCalls"].([]interface{}); ok {
			for _, tc := range toolCalls {
				if toolCall, ok := tc.(map[string]interface{}); ok {
					finishCurrentMistralBlock(ch, output, &currentBlock)
					currentBlock.Type = ""

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

					// Get existing block
					var toolCallBlock *struct {
						Type        string
						ID          string
						Name        string
						Arguments   map[string]interface{}
						PartialArgs string
					}

					if existingIndex, ok := toolBlocksByKey[key]; ok {
						if existing, ok := output.Content[existingIndex].(*src.ToolCall); ok {
							toolCallBlock = &struct {
								Type        string
								ID          string
								Name        string
								Arguments   map[string]interface{}
								PartialArgs string
							}{
								Type:        "toolCall",
								ID:          existing.ID,
								Name:        existing.Name,
								Arguments:   existing.Arguments,
								PartialArgs: "",
							}
						}
					}

					// Get function info
					var argsDelta string
					if fn, ok := toolCall["function"].(map[string]interface{}); ok {
						if name, ok := fn["name"].(string); ok && toolCallBlock == nil {
							toolCallBlock = &struct {
								Type        string
								ID          string
								Name        string
								Arguments   map[string]interface{}
								PartialArgs string
							}{
								Type:        "toolCall",
								ID:          callId,
								Name:        name,
								Arguments:   map[string]interface{}{},
								PartialArgs: "",
							}
						}
						if args, ok := fn["arguments"].(string); ok {
							argsDelta = args
						} else if argsMap, ok := fn["arguments"].(map[string]interface{}); ok {
							if argsJson, err := json.Marshal(argsMap); err == nil {
								argsDelta = string(argsJson)
							}
						}
					}

					if toolCallBlock == nil {
						continue
					}

					toolCallBlock.PartialArgs += argsDelta
					toolCallBlock.Arguments = parseStreamingJson(toolCallBlock.PartialArgs)

					// Add new block if needed
					if _, ok := toolBlocksByKey[key]; !ok {
						newBlock := &src.ToolCall{
							Type:      "toolCall",
							ID:        toolCallBlock.ID,
							Name:      toolCallBlock.Name,
							Arguments: toolCallBlock.Arguments,
						}
						output.Content = append(output.Content, newBlock)
						idx := len(output.Content) - 1
						toolBlocksByKey[key] = idx
						ch <- src.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: &idx, Partial: output}
					} else {
						// Update existing block
						idx := toolBlocksByKey[key]
						if existing, ok := output.Content[idx].(*src.ToolCall); ok {
							existing.Arguments = toolCallBlock.Arguments
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
		}
	}

	finishCurrentMistralBlock(ch, output, &currentBlock)

	// Finalize tool calls
	for _, index := range toolBlocksByKey {
		if index >= len(output.Content) {
			continue
		}
		if toolCall, ok := output.Content[index].(*src.ToolCall); ok {
			ch <- src.AssistantMessageEvent{
				Type:         "toolcall_end",
				ContentIndex: &index,
				ToolCall:     toolCall,
				Partial:      output,
			}
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
