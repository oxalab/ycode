package providers

import (
	"encoding/json"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Types
// =============================================================================

type TextSignatureV1 struct {
	V     int     `json:"v"`
	ID    string  `json:"id"`
	Phase *string `json:"phase,omitempty"`
}

type OpenAIResponsesStreamOptions struct {
	ServiceTier           *string
	ResolveServiceTier    func(responseServiceTier, requestServiceTier *string) *string
	ApplyServiceTierPricing func(usage *src.Usage, serviceTier *string)
}

type ConvertResponsesMessagesOptions struct {
	IncludeSystemPrompt bool
}

type ConvertResponsesToolsOptions struct {
	Strict *bool
}

// =============================================================================
// Utilities
// =============================================================================

func encodeTextSignatureV1(id string, phase *string) string {
	payload := TextSignatureV1{V: 1, ID: id}
	if phase != nil {
		payload.Phase = phase
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func parseTextSignature(signature string) *struct{ ID string; Phase *string } {
	if signature == "" {
		return nil
	}
	if strings.HasPrefix(signature, "{") {
		var parsed TextSignatureV1
		if err := json.Unmarshal([]byte(signature), &parsed); err == nil {
			if parsed.V == 1 && parsed.ID != "" {
				if parsed.Phase != nil && (*parsed.Phase == "commentary" || *parsed.Phase == "final_answer") {
					return &struct{ ID string; Phase *string }{ID: parsed.ID, Phase: parsed.Phase}
				}
				return &struct{ ID string; Phase *string }{ID: parsed.ID}
			}
		}
		// Fall through to legacy plain-string handling
	}
	return &struct{ ID string; Phase *string }{ID: signature}
}

// =============================================================================
// Message conversion
// =============================================================================

func convertResponsesMessages(
	model src.Model[src.Api],
	context src.ChatContext,
	allowedToolCallProviders map[string]bool,
	options *ConvertResponsesMessagesOptions,
) []interface{} {
	var messages []interface{}
	msgIndex := 0

	normalizeIdPart := func(part string) string {
		// Simple sanitization
		var result strings.Builder
		for _, r := range part {
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
		return strings.TrimRight(sanitized, "_")
	}

	buildForeignResponsesItemId := func(itemId string) string {
		normalized := "fc_" + utils.ShortHash(itemId)
		if len(normalized) > 64 {
			normalized = normalized[:64]
		}
		return normalized
	}

	normalizeToolCallId := func(id string, _ src.Model[src.Api], source src.AssistantMessage) string {
		if !allowedToolCallProviders[string(model.Provider)] {
			return normalizeIdPart(id)
		}
		parts := strings.Split(id, "|")
		if len(parts) != 2 {
			return normalizeIdPart(id)
		}
		callId, itemId := parts[0], parts[1]
		normalizedCallId := normalizeIdPart(callId)
		isForeignToolCall := source.Provider != model.Provider || source.Api != model.Api

		var normalizedItemId string
		if isForeignToolCall {
			normalizedItemId = buildForeignResponsesItemId(itemId)
		} else {
			normalizedItemId = normalizeIdPart(itemId)
		}

		// OpenAI Responses API requires item id to start with "fc"
		if !strings.HasPrefix(normalizedItemId, "fc_") {
			normalizedItemId = normalizeIdPart("fc_" + normalizedItemId)
		}

		return normalizedCallId + "|" + normalizedItemId
	}

	transformedMessages := TransformMessages(context.Messages, model, normalizeToolCallId)

	includeSystemPrompt := true
	if options != nil {
		includeSystemPrompt = options.IncludeSystemPrompt
	}

	if includeSystemPrompt && context.SystemPrompt != "" {
		role := "system"
		if model.Reasoning {
			role = "developer"
		}
		messages = append(messages, map[string]interface{}{
			"role":    role,
			"content": utils.SanitizeSurrogates(context.SystemPrompt),
		})
	}

	for _, msg := range transformedMessages {
		role := msgRole(msg)
		if role == src.RoleUser {
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}
			if len(um.Content) == 1 {
				if tc, ok := um.Content[0].(*src.TextContent); ok {
					messages = append(messages, map[string]interface{}{
						"role":    "user",
						"content": []map[string]interface{}{{"type": "input_text", "text": utils.SanitizeSurrogates(tc.Text)}},
					})
					continue
				}
			}

			var content []map[string]interface{}
			for _, item := range um.Content {
				switch c := item.(type) {
				case *src.TextContent:
					content = append(content, map[string]interface{}{
						"type": "input_text",
						"text": utils.SanitizeSurrogates(c.Text),
					})
				case *src.ImageContent:
					content = append(content, map[string]interface{}{
						"type":  "input_image",
						"image_url": "data:" + c.MimeType + ";base64," + c.Data,
					})
				}
			}
			if len(content) > 0 {
				messages = append(messages, map[string]interface{}{
					"role":    "user",
					"content": content,
				})
			}
		} else if role == src.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}
			var output []interface{}
			isDifferentModel := am.Model != model.ID && am.Provider == model.Provider && am.Api == model.Api

			for _, block := range am.Content {
				switch b := block.(type) {
				case *src.ThinkingContent:
					if b.ThinkingSignature != "" {
						var reasoningItem map[string]interface{}
						if err := json.Unmarshal([]byte(b.ThinkingSignature), &reasoningItem); err == nil {
							output = append(output, reasoningItem)
						}
					}
				case *src.TextContent:
					parsedSig := parseTextSignature(b.TextSignature)
					msgId := "msg_" + string(rune(msgIndex))
					if parsedSig != nil && parsedSig.ID != "" {
						msgId = parsedSig.ID
						if len(msgId) > 64 {
							msgId = "msg_" + utils.ShortHash(msgId)
						}
					}
					outputMsg := map[string]interface{}{
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"id":     msgId,
						"content": []map[string]interface{}{
							{
								"type":        "output_text",
								"text":        utils.SanitizeSurrogates(b.Text),
								"annotations": []interface{}{},
							},
						},
					}
					if parsedSig != nil && parsedSig.Phase != nil {
						outputMsg["phase"] = *parsedSig.Phase
					}
					output = append(output, outputMsg)
				case *src.ToolCall:
					parts := strings.Split(b.ID, "|")
					callId := parts[0]
					itemId := ""
					if len(parts) > 1 {
						itemId = parts[1]
					}

					// For different-model messages, omit id to avoid pairing validation
					if isDifferentModel && strings.HasPrefix(itemId, "fc_") {
						itemId = ""
					}

					argsJson, _ := json.Marshal(b.Arguments)
					functionCall := map[string]interface{}{
						"type":     "function_call",
						"id":       itemId,
						"call_id":  callId,
						"name":     b.Name,
						"arguments": string(argsJson),
					}
					if itemId == "" {
						delete(functionCall, "id")
					}
					output = append(output, functionCall)
				}
			}

			if len(output) > 0 {
				messages = append(messages, output...)
			}
		} else if role == src.RoleTool {
			tr, ok := msg.(*src.ToolResultMessage[any])
			if !ok {
				continue
			}

			var textParts []string
			var hasImages bool
			for _, c := range tr.Content {
				if tc, ok := c.(*src.TextContent); ok {
					textParts = append(textParts, tc.Text)
				}
				if _, ok := c.(*src.ImageContent); ok {
					hasImages = true
				}
			}
			textResult := strings.Join(textParts, "\n")

			parts := strings.Split(tr.ToolCallID, "|")
			callId := parts[0]

			var output interface{}
			if hasImages && hasImageInput(model) {
				var contentParts []map[string]interface{}
				if textResult != "" {
					contentParts = append(contentParts, map[string]interface{}{
						"type": "input_text",
						"text": utils.SanitizeSurrogates(textResult),
					})
				}
				for _, block := range tr.Content {
					if ic, ok := block.(*src.ImageContent); ok {
						contentParts = append(contentParts, map[string]interface{}{
							"type":  "input_image",
							"image_url": "data:" + ic.MimeType + ";base64," + ic.Data,
						})
					}
				}
				output = contentParts
			} else {
				if textResult != "" {
					output = utils.SanitizeSurrogates(textResult)
				} else {
					output = "(see attached image)"
				}
			}

			messages = append(messages, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": callId,
				"output":  output,
			})
		}
		msgIndex++
	}

	return messages
}

// =============================================================================
// Tool conversion
// =============================================================================

func convertResponsesTools(tools []src.Tool[any], options *ConvertResponsesToolsOptions) []map[string]interface{} {
	strict := false
	if options != nil && options.Strict != nil {
		strict = *options.Strict
	}

	result := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		result[i] = map[string]interface{}{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"strict":      strict,
		}
	}
	return result
}

// =============================================================================
// Stop reason mapping
// =============================================================================

func mapOpenAIResponsesStopReason(status string) src.StopReason {
	switch status {
	case "completed":
		return src.StopReasonStop
	case "incomplete":
		return src.StopReasonLength
	case "failed", "cancelled":
		return src.StopReasonError
	case "in_progress", "queued":
		return src.StopReasonStop
	default:
		return src.StopReasonStop
	}
}
