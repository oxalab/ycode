package providers

import src "ycode/packages/llm/src"

func InferCopilotInitiator(messages []src.Message) string {
	if len(messages) == 0 {
		return "user"
	}
	last := messages[len(messages)-1]
	if msgRole(last) != src.RoleUser {
		return "agent"
	}
	return "user"
}

func HasCopilotVisionInput(messages []src.Message) bool {
	for _, msg := range messages {
		role := msgRole(msg)
		if role == src.RoleUser || role == src.RoleTool {
			for _, c := range msgContent(msg) {
				if _, ok := c.(*src.ImageContent); ok {
					return true
				}
			}
		}
	}
	return false
}

func BuildCopilotDynamicHeaders(messages []src.Message, hasImages bool) map[string]string {
	h := map[string]string{
		"X-Initiator":   InferCopilotInitiator(messages),
		"Openai-Intent": "conversation-edits",
	}
	if hasImages {
		h["Copilot-Vision-Request"] = "true"
	}
	return h
}

func msgRole(m src.Message) src.Role {
	switch v := m.(type) {
	case *src.UserMessage:
		return v.Role
	case *src.AssistantMessage:
		return v.Role
	case *src.ToolResultMessage[any]:
		return v.Role
	default:
		return ""
	}
}

func msgContent(m src.Message) []src.ContentBlock {
	switch v := m.(type) {
	case *src.UserMessage:
		return v.Content
	case *src.ToolResultMessage[any]:
		return v.Content
	default:
		return nil
	}
}
