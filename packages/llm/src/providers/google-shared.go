package providers

import (
	"regexp"
	"strconv"
	"strings"

	src "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils"
)

// =============================================================================
// Types
// =============================================================================

type GoogleApiType string

const (
	GoogleGenerativeAI GoogleApiType = "google-generative-ai"
	GoogleVertex       GoogleApiType = "google-vertex"
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

// base64SignaturePattern mirrors google-shared.ts: padding only at the end,
// at least one data character, length a multiple of 4.
var base64SignaturePattern = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// resolveThoughtSignature only keeps signatures from the same provider/model with valid base64
func resolveThoughtSignature(isSameProviderAndModel bool, signature string) string {
	if !isSameProviderAndModel || signature == "" {
		return ""
	}
	if len(signature)%4 != 0 {
		return ""
	}
	if !base64SignaturePattern.MatchString(signature) {
		return ""
	}
	return signature
}

// requiresToolCallId checks if a model requires explicit tool call IDs
func requiresToolCallId(modelId string) bool {
	return strings.HasPrefix(modelId, "claude-") || strings.HasPrefix(modelId, "gpt-oss-")
}

// getGeminiMajorVersion extracts the major version from a model ID
// (google-shared.ts: anchored at the start of the id).
var geminiMajorVersionPattern = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)

func getGeminiMajorVersion(modelId string) *int {
	m := geminiMajorVersionPattern.FindStringSubmatch(strings.ToLower(modelId))
	if m == nil {
		return nil
	}
	version, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	return &version
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

					// args ?? {} — never marshal a nil map.
					args := b.Arguments
					if args == nil {
						args = map[string]interface{}{}
					}
					functionCall := map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": b.Name,
							"args": args,
						},
					}
					if requiresToolCallId(model.ID) {
						functionCall["functionCall"].(map[string]interface{})["id"] = b.ID
					}
					if thoughtSignature != "" {
						functionCall["thoughtSignature"] = thoughtSignature
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
			// (TS uses contents.at(-1) with optional chaining — guard the empty case).
			if len(contents) == 0 {
				continue
			}
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

// sanitizeForOpenApi strips meta-declarations from a schema (arrays are
// returned as-is, matching the reference).
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
		return v
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
