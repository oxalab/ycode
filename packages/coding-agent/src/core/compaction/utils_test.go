package compaction

// utils_test.go pins the summarizer-facing conversation serialization
// (audit #30): a tool call renders as name(key=value, …) with only the
// per-argument values stringified — never the whole ToolCall struct.

import (
	"strings"
	"testing"

	llm "ycode/packages/llm/src"
)

func TestSerializeConversation_ToolCallArgs(t *testing.T) {
	msgs := []llm.Message{
		llm.AssistantMessage{
			Role: llm.RoleAssistant,
			Content: []llm.ContentBlock{
				llm.ToolCall{
					Type:      "toolCall",
					ID:        "tc_1",
					Name:      "read",
					Arguments: map[string]any{"path": "/a.go", "limit": 10},
				},
			},
		},
	}

	got := SerializeConversation(msgs)
	if !strings.Contains(got, `read(limit=10, path="/a.go")`) {
		t.Errorf("SerializeConversation tool call = %q, want read(limit=10, path=%q)", got, "/a.go")
	}
	// The whole-struct regression: struct JSON (type/id/arguments nesting)
	// must never leak into the serialized line.
	if strings.Contains(got, `"toolCall"`) || strings.Contains(got, "tc_1") {
		t.Errorf("SerializeConversation leaked the ToolCall struct: %q", got)
	}
}
