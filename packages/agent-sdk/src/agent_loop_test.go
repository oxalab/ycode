package sdk

import (
	"context"
	"testing"

	llm "ycode/packages/llm/src"
)

// Regression for the agent-loop context bug (docs/agent-loop-context-bug.md):
// streamAssistantResponse receives AgentContext by value, so its internal
// appends never reached the loop's context. runLoop appended tool results but
// NOT the assistant message carrying the tool_calls — every follow-up LLM call
// sent orphaned role:"tool" messages with no preceding assistant tool_calls,
// which lenient backends (GLM/z.ai) fold into the user turn (the model then
// "sees" its tool output as injected into the human turn and loops).

// messageRoles maps a captured request's messages to their roles for asserts.
func messageRoles(t *testing.T, msgs []llm.Message) []llm.Role {
	t.Helper()
	roles := make([]llm.Role, 0, len(msgs))
	for _, m := range msgs {
		switch v := m.(type) {
		case llm.UserMessage:
			roles = append(roles, v.Role)
		case llm.AssistantMessage:
			roles = append(roles, v.Role)
		case *llm.AssistantMessage:
			roles = append(roles, v.Role)
		case llm.ToolResultMessage[any]:
			roles = append(roles, v.Role)
		default:
			t.Fatalf("unexpected message type %T", m)
		}
	}
	return roles
}

func TestAgentLoopKeepsAssistantToolCallsInContext(t *testing.T) {
	var requests [][]llm.Message
	call := 0
	streamFn := StreamFn(func(
		ctx context.Context,
		model llm.Model[llm.Api],
		chatCtx llm.ChatContext,
		options *llm.SimpleStreamOptions,
	) <-chan llm.AssistantMessageEvent {
		ch := make(chan llm.AssistantMessageEvent, 8)
		captured := append([]llm.Message{}, chatCtx.Messages...)
		requests = append(requests, captured)
		call++
		go func() {
			defer close(ch)
			if call == 1 {
				empty := &llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentBlock{}}
				ch <- llm.AssistantMessageEvent{Type: "start", Partial: empty}
				withCall := &llm.AssistantMessage{
					Role:       llm.RoleAssistant,
					StopReason: llm.StopReasonToolUse,
					Content: []llm.ContentBlock{
						&llm.ToolCall{Type: "function", ID: "call-1", Name: "echo", Arguments: map[string]any{"text": "hi"}},
					},
				}
				ch <- llm.AssistantMessageEvent{Type: "toolcall_end", Partial: withCall}
				ch <- llm.AssistantMessageEvent{Type: "done", Message: withCall}
				return
			}
			final := &llm.AssistantMessage{
				Role:       llm.RoleAssistant,
				StopReason: llm.StopReasonStop,
				Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "done"}},
			}
			ch <- llm.AssistantMessageEvent{Type: "start", Partial: final}
			ch <- llm.AssistantMessageEvent{Type: "done", Message: final}
		}()
		return ch
	})

	tool := AgentTool[any]{
		Name:        "echo",
		Description: "echoes",
		Execute: func(toolCallID string, params any, signal context.Context, onUpdate AgentToolUpdateCallback[any]) (AgentToolResult[any], error) {
			return AgentToolResult[any]{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "echoed"}},
			}, nil
		},
	}

	config := AgentLoopConfig{
		Model: llm.Model[llm.Api]{ID: "test-model", Provider: "test-provider"},
		ConvertToLlm: func(msgs []AgentMessage) ([]llm.Message, error) {
			out := make([]llm.Message, len(msgs))
			copy(out, msgs)
			return out, nil
		},
	}

	prompts := []AgentMessage{
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "run the tool"}}},
	}
	newMessages, err := RunAgentLoop(prompts, AgentContext{Tools: []AgentTool[any]{tool}}, config,
		func(AgentEvent) error { return nil }, context.Background(), streamFn)
	if err != nil {
		t.Fatalf("RunAgentLoop error: %v", err)
	}

	if len(newMessages) != 4 {
		t.Fatalf("expected 4 new messages (user, assistant(toolcall), tool, assistant), got %d", len(newMessages))
	}
	if len(requests) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(requests))
	}

	// Call 1: just the user prompt.
	roles1 := messageRoles(t, requests[0])
	if len(roles1) != 1 || roles1[0] != llm.RoleUser {
		t.Fatalf("first request = %v, want [user]", roles1)
	}

	// Call 2 (the regression): must be user → assistant(tool_calls) → tool.
	// Pre-fix this was [user, tool] — the assistant message was missing and
	// the tool result was orphaned.
	roles2 := messageRoles(t, requests[1])
	if len(roles2) != 3 {
		t.Fatalf("second request roles = %v, want [user assistant tool]", roles2)
	}
	if roles2[0] != llm.RoleUser || roles2[1] != llm.RoleAssistant || roles2[2] != llm.RoleTool {
		t.Fatalf("second request roles = %v, want [user assistant tool]", roles2)
	}

	// The assistant turn in request 2 must carry the tool_call, and the tool
	// result must reference its id.
	assistant, ok := requests[1][1].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("second message should be an assistant message, got %T", requests[1][1])
	}
	foundCall := false
	for _, c := range assistant.Content {
		if tc, ok := c.(*llm.ToolCall); ok && tc.ID == "call-1" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Errorf("assistant message in second request lost its tool_call: %+v", assistant.Content)
	}
	toolMsg, ok := requests[1][2].(llm.ToolResultMessage[any])
	if !ok {
		t.Fatalf("third message should be a tool result, got %T", requests[1][2])
	}
	if toolMsg.ToolCallID != "call-1" {
		t.Errorf("tool result id = %q, want call-1", toolMsg.ToolCallID)
	}
}
