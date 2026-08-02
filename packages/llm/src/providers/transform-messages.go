package providers

import (
	"strings"
	"time"

	src "ycode/packages/llm/src"
)

const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
)

func replaceImagesWithPlaceholder(content []src.ContentBlock, placeholder string) []src.ContentBlock {
	var result []src.ContentBlock
	prevWasPlaceholder := false
	for _, block := range content {
		if _, ok := block.(*src.ImageContent); ok {
			if !prevWasPlaceholder {
				result = append(result, &src.TextContent{Type: "text", Text: placeholder})
			}
			prevWasPlaceholder = true
			continue
		}
		if tc, ok := block.(*src.TextContent); ok && tc.Text == placeholder {
			prevWasPlaceholder = true
		} else {
			prevWasPlaceholder = false
		}
		result = append(result, block)
	}
	return result
}

func hasImageInput(model src.Model[src.Api]) bool {
	for _, t := range model.Input {
		if t == src.InputImage {
			return true
		}
	}
	return false
}

func downgradeUnsupportedImages(messages []src.Message, model src.Model[src.Api]) []src.Message {
	if hasImageInput(model) {
		return messages
	}
	result := make([]src.Message, len(messages))
	for i, msg := range messages {
		role := msgRole(msg)
		switch role {
		case src.RoleUser:
			um := msg.(*src.UserMessage)
			replaced := replaceImagesWithPlaceholder(um.Content, nonVisionUserImagePlaceholder)
			result[i] = &src.UserMessage{Role: um.Role, Content: replaced, Timestamp: um.Timestamp}
		case src.RoleTool:
			tr := msg.(*src.ToolResultMessage[any])
			replaced := replaceImagesWithPlaceholder(tr.Content, nonVisionToolImagePlaceholder)
			result[i] = &src.ToolResultMessage[any]{
				Role: tr.Role, ToolCallID: tr.ToolCallID, ToolName: tr.ToolName,
				Content: replaced, Details: tr.Details, IsError: tr.IsError, Timestamp: tr.Timestamp,
			}
		default:
			result[i] = msg
		}
	}
	return result
}

type NormalizeToolCallIDFunc func(id string, model src.Model[src.Api], source src.AssistantMessage) string

// normalizeMessagePtrs converts value-form messages (llm.UserMessage, etc., as
// the agent-sdk passes them) to pointer-form AND normalizes their content blocks
// (which the agent also passes as values, e.g. llm.TextContent), leaving existing
// pointers untouched. The transform + provider code asserts *T for both messages
// and content blocks in many places, so normalizing once at the entry avoids
// value/pointer mismatches (panics + silently-dropped content) at each site.
func normalizeMessagePtrs(messages []src.Message) []src.Message {
	out := make([]src.Message, len(messages))
	for i, m := range messages {
		out[i] = normalizeMessagePtr(m)
	}
	return out
}

func normalizeMessagePtr(m src.Message) src.Message {
	switch v := m.(type) {
	case src.UserMessage:
		v.Content = normalizeContentPtrs(v.Content)
		return &v
	case *src.UserMessage:
		cp := *v
		cp.Content = normalizeContentPtrs(cp.Content)
		return &cp
	case src.AssistantMessage:
		v.Content = normalizeContentPtrs(v.Content)
		return &v
	case *src.AssistantMessage:
		cp := *v
		cp.Content = normalizeContentPtrs(cp.Content)
		return &cp
	case src.ToolResultMessage[any]:
		v.Content = normalizeContentPtrs(v.Content)
		return &v
	case *src.ToolResultMessage[any]:
		cp := *v
		cp.Content = normalizeContentPtrs(cp.Content)
		return &cp
	default:
		return m
	}
}

// normalizeContentPtrs converts value-form content blocks to pointer-form.
func normalizeContentPtrs(blocks []src.ContentBlock) []src.ContentBlock {
	out := make([]src.ContentBlock, len(blocks))
	for i, b := range blocks {
		switch v := b.(type) {
		case src.TextContent:
			out[i] = &v
		case src.ImageContent:
			out[i] = &v
		case src.ThinkingContent:
			out[i] = &v
		case src.ToolCall:
			out[i] = &v
		default:
			out[i] = b // already a pointer (or unknown) — leave as-is
		}
	}
	return out
}

func TransformMessages(messages []src.Message, model src.Model[src.Api], normalizeID NormalizeToolCallIDFunc) []src.Message {
	messages = normalizeMessagePtrs(messages)
	toolCallIDMap := make(map[string]string)
	imageAware := downgradeUnsupportedImages(messages, model)

	var transformed []src.Message
	for _, msg := range imageAware {
		role := msgRole(msg)

		if role == src.RoleUser {
			transformed = append(transformed, msg)
			continue
		}

		if role == src.RoleTool {
			tr := msg.(*src.ToolResultMessage[any])
			if normalizedID, ok := toolCallIDMap[tr.ToolCallID]; ok && normalizedID != tr.ToolCallID {
				transformed = append(transformed, &src.ToolResultMessage[any]{
					Role: tr.Role, ToolCallID: normalizedID, ToolName: tr.ToolName,
					Content: tr.Content, Details: tr.Details, IsError: tr.IsError, Timestamp: tr.Timestamp,
				})
			} else {
				transformed = append(transformed, msg)
			}
			continue
		}

		if role == src.RoleAssistant {
			am := msg.(*src.AssistantMessage)
			isSameModel := am.Provider == model.Provider && string(am.Api) == string(model.Api) && am.Model == model.ID
			var newContent []src.ContentBlock

			for _, block := range am.Content {
				switch b := block.(type) {
				case *src.ThinkingContent:
					if b.Redacted != nil && *b.Redacted {
						if isSameModel {
							newContent = append(newContent, b)
						}
						continue
					}
					if isSameModel && b.ThinkingSignature != "" {
						newContent = append(newContent, b)
						continue
					}
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					if isSameModel {
						newContent = append(newContent, b)
						continue
					}
					newContent = append(newContent, &src.TextContent{Type: "text", Text: b.Thinking})

				case *src.TextContent:
					if isSameModel {
						newContent = append(newContent, b)
					} else {
						newContent = append(newContent, &src.TextContent{Type: "text", Text: b.Text})
					}

				case *src.ToolCall:
					tc := &src.ToolCall{
						Type: b.Type, ID: b.ID, Name: b.Name,
						Arguments: b.Arguments, ThoughtSignature: b.ThoughtSignature,
					}
					if !isSameModel && tc.ThoughtSignature != "" {
						tc.ThoughtSignature = ""
					}
					if !isSameModel && normalizeID != nil {
						normalizedID := normalizeID(tc.ID, model, *am)
						if normalizedID != tc.ID {
							toolCallIDMap[tc.ID] = normalizedID
							tc.ID = normalizedID
						}
					}
					newContent = append(newContent, tc)

				default:
					newContent = append(newContent, block)
				}
			}
			transformed = append(transformed, &src.AssistantMessage{
				Role: am.Role, Content: newContent, Api: am.Api, Provider: am.Provider,
				Model: am.Model, ResponseID: am.ResponseID, Usage: am.Usage,
				StopReason: am.StopReason, ErrorMessage: am.ErrorMessage, Timestamp: am.Timestamp,
			})
			continue
		}

		transformed = append(transformed, msg)
	}

	// Second pass: insert synthetic tool results for orphaned tool calls
	var result []src.Message
	var pendingToolCalls []*src.ToolCall
	existingResultIDs := make(map[string]bool)

	insertSynthetic := func() {
		for _, tc := range pendingToolCalls {
			if !existingResultIDs[tc.ID] {
				result = append(result, &src.ToolResultMessage[any]{
					Role: src.RoleTool, ToolCallID: tc.ID, ToolName: tc.Name,
					Content:      []src.ContentBlock{&src.TextContent{Type: "text", Text: "No result provided"}},
					IsError:      true,
					Timestamp:    time.Now().UnixMilli(),
				})
			}
		}
		pendingToolCalls = nil
		existingResultIDs = make(map[string]bool)
	}

	for _, msg := range transformed {
		role := msgRole(msg)
		switch role {
		case src.RoleAssistant:
			insertSynthetic()
			am := msg.(*src.AssistantMessage)
			if am.StopReason == src.StopReasonError || am.StopReason == src.StopReasonAborted {
				continue
			}
			var tcs []*src.ToolCall
			for _, b := range am.Content {
				if tc, ok := b.(*src.ToolCall); ok {
					tcs = append(tcs, tc)
				}
			}
			if len(tcs) > 0 {
				pendingToolCalls = tcs
				existingResultIDs = make(map[string]bool)
			}
			result = append(result, msg)
		case src.RoleTool:
			tr := msg.(*src.ToolResultMessage[any])
			existingResultIDs[tr.ToolCallID] = true
			result = append(result, msg)
		case src.RoleUser:
			insertSynthetic()
			result = append(result, msg)
		default:
			result = append(result, msg)
		}
	}
	insertSynthetic()

	return result
}
