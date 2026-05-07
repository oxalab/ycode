package providers

import (
	"encoding/json"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Constants
// =============================================================================

const (
	skipThoughtSignatureValidator = "skip_thought_signature_validator"
)

// =============================================================================
// Types
// =============================================================================

type GoogleApiType string

const (
	GoogleGenerativeAI  GoogleApiType = "google-generative-ai"
	GoogleGeminiCLI      GoogleApiType = "google-gemini-cli"
	GoogleVertex          GoogleApiType = "google-vertex"
)

// =============================================================================
// Utilities
// =============================================================================

// isThinkingPart determines whether a streamed Part should be treated as "thinking"
func isThinkingPart(thought *bool, thoughtSignature string) bool {
	return thought != nil && *thought
}

// retainThoughtSignature preserves the last non-empty signature for the current block
func retainThoughtSignature(existing, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

// resolveThoughtSignature only keeps signatures from the same provider/model with valid base64
func resolveThoughtSignature(isSameProviderAndModel bool, signature string) string {
	if !isSameProviderAndModel || signature == "" {
		return ""
	}
	if len(signature)%4 != 0 {
		return ""
	}
	// Basic base64 validation
	for _, c := range signature {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return ""
		}
	}
	return signature
}

// requiresToolCallId checks if a model requires explicit tool call IDs
func requiresToolCallId(modelId string) bool {
	return strings.HasPrefix(modelId, "claude-") || strings.HasPrefix(modelId, "gpt-oss-")
}

// getGeminiMajorVersion extracts the major version from a model ID
func getGeminiMajorVersion(modelId string) *int {
	lower := strings.ToLower(modelId)
	idx := strings.Index(lower, "gemini")
	if idx == -1 {
		return nil
	}

	rest := lower[idx+6:]
	if strings.HasPrefix(rest, "-live-") {
		rest = rest[6:]
	}

	if strings.HasPrefix(rest, "-") {
		rest = rest[1:]
	}

	var versionStr string
	for i, c := range rest {
		if c >= '0' && c <= '9' {
			versionStr += string(c)
		} else {
			break
		}
		if i > 2 { // Max 3 digits for version
			break
		}
	}

	var version int
	if  err := json.Unmarshal([]byte(versionStr), &version); err == nil {
		return &version
	}
	return nil
}

// supportsMultimodalFunctionResponse checks if a model supports multimodal function responses
func supportsMultimodalFunctionResponse(modelId string) bool {
	version := getGeminiMajorVersion(modelId)
	if version != nil {
		return *version >= 3
	}
	return true
}

// =============================================================================
// Message conversion
// =============================================================================

// convertMessages converts internal messages to Gemini Content[] format
func convertGoogleMessages(model src.Model[src.Api], context src.ChatContext) []map[string]interface{} {
	var contents []map[string]interface{}

	normalizeToolCallId := func(id string) string {
		if !requiresToolCallId(model.ID) {
			return id
		}
		// Sanitize to alphanumeric, underscore, hyphen
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

	transformedMessages := TransformMessages(context.Messages, model, func(id string, _ src.Model[src.Api], _ src.AssistantMessage) string {
		return normalizeToolCallId(id)
	})

	for _, msg := range transformedMessages {
		role := msgRole(msg)
		if role == src.RoleUser {
			um, ok := msg.(*src.UserMessage)
			if !ok {
				continue
			}

			var parts []map[string]interface{}
			for _, item := range um.Content {
				switch c := item.(type) {
				case *src.TextContent:
					parts = append(parts, map[string]interface{}{
						"text": utils.SanitizeSurrogates(c.Text),
					})
				case *src.ImageContent:
					parts = append(parts, map[string]interface{}{
						"inlineData": map[string]interface{}{
							"mimeType": c.MimeType,
							"data":     c.Data,
						},
					})
				}
			}

			if len(parts) == 0 {
				continue
			}

			contents = append(contents, map[string]interface{}{
				"role":  "user",
				"parts": parts,
			})
		} else if role == src.RoleAssistant {
			am := okAssistant(msg)
			if am == nil {
				continue
			}

			var parts []map[string]interface{}
			isSameProviderAndModel := am.Provider == model.Provider && am.Model == model.ID

			for _, block := range am.Content {
				switch b := block.(type) {
				case *src.TextContent:
					if b.Text == "" || strings.TrimSpace(b.Text) == "" {
						continue
					}
					thoughtSignature := resolveThoughtSignature(isSameProviderAndModel, b.TextSignature)
					part := map[string]interface{}{
						"text": utils.SanitizeSurrogates(b.Text),
					}
					if thoughtSignature != "" {
						part["thoughtSignature"] = thoughtSignature
					}
					parts = append(parts, part)

				case *src.ThinkingContent:
					if b.Thinking == "" || strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					if isSameProviderAndModel {
						thoughtSignature := resolveThoughtSignature(isSameProviderAndModel, b.ThinkingSignature)
						part := map[string]interface{}{
							"thought": true,
							"text":    utils.SanitizeSurrogates(b.Thinking),
						}
						if thoughtSignature != "" {
							part["thoughtSignature"] = thoughtSignature
						}
						parts = append(parts, part)
					} else {
						parts = append(parts, map[string]interface{}{
							"text": utils.SanitizeSurrogates(b.Thinking),
						})
					}

				case *src.ToolCall:
					thoughtSignature := resolveThoughtSignature(isSameProviderAndModel, b.ThoughtSignature)
					isGemini3 := strings.Contains(strings.ToLower(model.ID), "gemini-3")
					effectiveSignature := thoughtSignature
					if effectiveSignature == "" && isGemini3 {
						effectiveSignature = skipThoughtSignatureValidator
					}

					functionCall := map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": b.Name,
							"args": b.Arguments,
						},
					}
					if requiresToolCallId(model.ID) {
						functionCall["functionCall"].(map[string]interface{})["id"] = b.ID
					}
					if effectiveSignature != "" {
						functionCall["thoughtSignature"] = effectiveSignature
					}
					parts = append(parts, functionCall)
				}
			}

			if len(parts) == 0 {
				continue
			}

			contents = append(contents, map[string]interface{}{
				"role":  "model",
				"parts": parts,
			})
		} else if role == src.RoleTool {
			tr, ok := msg.(*src.ToolResultMessage[any])
			if !ok {
				continue
			}

			var textParts []string
			for _, c := range tr.Content {
				if tc, ok := c.(*src.TextContent); ok {
					textParts = append(textParts, tc.Text)
				}
			}
			textResult := strings.Join(textParts, "\n")

			var images []src.ImageContent
			if hasImageInput(model) {
				for _, c := range tr.Content {
					if ic, ok := c.(*src.ImageContent); ok {
						images = append(images, *ic)
					}
				}
			}

			hasText := textResult != ""
			hasImages := len(images) > 0
			modelSupportsMultimodalFunctionResponse := supportsMultimodalFunctionResponse(model.ID)

			responseValue := textResult
			if !hasText && hasImages {
				responseValue = "(see attached image)"
			}

			var imageParts []map[string]interface{}
			for _, img := range images {
				imageParts = append(imageParts, map[string]interface{}{
					"inlineData": map[string]interface{}{
						"mimeType": img.MimeType,
						"data":     img.Data,
					},
				})
			}

			includeId := requiresToolCallId(model.ID)
			functionResponsePart := map[string]interface{}{
				"functionResponse": map[string]interface{}{
					"name": tr.ToolName,
				},
			}

			if tr.IsError {
				functionResponsePart["functionResponse"].(map[string]interface{})["response"] = map[string]interface{}{
					"error": utils.SanitizeSurrogates(responseValue),
				}
			} else {
				functionResponsePart["functionResponse"].(map[string]interface{})["response"] = map[string]interface{}{
					"output": utils.SanitizeSurrogates(responseValue),
				}
			}

			if hasImages && modelSupportsMultimodalFunctionResponse && len(imageParts) > 0 {
				functionResponsePart["functionResponse"].(map[string]interface{})["parts"] = imageParts
			}
			if includeId {
				functionResponsePart["functionResponse"].(map[string]interface{})["id"] = tr.ToolCallID
			}

			// Cloud Code Assist API requires all function responses to be in a single user turn
			lastContent := contents[len(contents)-1]
			if lastContent != nil && lastContent["role"] == "user" {
				if parts, ok := lastContent["parts"].([]map[string]interface{}); ok {
					hasFunctionResponse := false
					for _, p := range parts {
						if _, ok := p["functionResponse"]; ok {
							hasFunctionResponse = true
							break
						}
					}
					if hasFunctionResponse {
						lastContent["parts"] = append(parts, functionResponsePart)
						continue
					}
				}
			}

			contents = append(contents, map[string]interface{}{
				"role":  "user",
				"parts": []map[string]interface{}{functionResponsePart},
			})

			// For Gemini < 3, add images in a separate user message
			if hasImages && !modelSupportsMultimodalFunctionResponse {
				imagePartsMessage := []map[string]interface{}{
					{"text": "Tool result image:"},
				}
				imagePartsMessage = append(imagePartsMessage, imageParts...)
				contents = append(contents, map[string]interface{}{
					"role":  "user",
					"parts": imagePartsMessage,
				})
			}
		}
	}

	return contents
}

// =============================================================================
// Tool conversion
// =============================================================================

// jsonSchemaMetaDeclarations contains meta-declaration keys to strip
var jsonSchemaMetaDeclarations = map[string]bool{
	"$schema": true, "$id": true, "$anchor": true,
	"$dynamicAnchor": true, "$vocabulary": true,
	"$comment": true, "$defs": true, "definitions": true,
}

// sanitizeForOpenApi strips meta-declarations from a schema
func sanitizeForOpenApi(schema interface{}) interface{} {
	switch v := schema.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			if jsonSchemaMetaDeclarations[key] {
				continue
			}
			result[key] = sanitizeForOpenApi(value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = sanitizeForOpenApi(item)
		}
		return result
	default:
		return v
	}
}

// convertGoogleTools converts tools to Gemini function declarations format
func convertGoogleTools(tools []src.Tool[any], useParameters bool) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}

	functionDeclarations := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		var parameters interface{}
		if useParameters {
			parameters = sanitizeForOpenApi(tool.Parameters)
		} else {
			parameters = tool.Parameters
		}

		functionDeclarations[i] = map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
		}

		if useParameters {
			functionDeclarations[i]["parameters"] = parameters
		} else {
			functionDeclarations[i]["parametersJsonSchema"] = parameters
		}
	}

	return []map[string]interface{}{
		{"functionDeclarations": functionDeclarations},
	}
}

// =============================================================================
// Stop reason mapping
// =============================================================================

func mapGoogleStopReason(reason string) src.StopReason {
	switch reason {
	case "STOP":
		return src.StopReasonStop
	case "MAX_TOKENS":
		return src.StopReasonLength
	case "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "SAFETY",
		"IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION",
		"IMAGE_OTHER", "RECITATION", "FINISH_REASON_UNSPECIFIED",
		"OTHER", "LANGUAGE", "MALFORMED_FUNCTION_CALL",
		"UNEXPECTED_TOOL_CALL", "NO_IMAGE":
		return src.StopReasonError
	default:
		return src.StopReasonError
	}
}
