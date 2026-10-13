package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	src "ycode/packages/llm/src"
)

// Agent-loop secondary 5c (docs/agent-loop-context-bug.md §5c): when a
// provider streams the tool call's id (or name) in a later chunk than the
// first delta, it must reach the finished ToolCall block — the reference
// backfills the live block in place. Previously only the throwaway accumulator
// saw it and the block serialized with an empty id, orphaning the tool result
// on replay.

func runToolCallStream(t *testing.T, chunks []string) (*src.AssistantMessage, []src.AssistantMessageEvent) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	model := src.Model[src.Api]{
		ID: "glm-test", Name: "GLM Test", Api: src.Api("openai-completions"),
		Provider: src.Provider("zai"), BaseURL: server.URL,
	}
	ch := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
		&OpenAICompletionsOptions{APIKey: "test-key"})()

	var (
		final     *src.AssistantMessage
		toolCalls []src.AssistantMessageEvent
	)
	for ev := range ch {
		if ev.Type == "toolcall_end" {
			toolCalls = append(toolCalls, ev)
		}
		if (ev.Type == "done" || ev.Type == "error") && ev.Message != nil {
			final = ev.Message
		}
	}
	if final == nil {
		t.Fatal("stream produced no final message")
	}
	return final, toolCalls
}

func finalToolCall(t *testing.T, final *src.AssistantMessage) *src.ToolCall {
	t.Helper()
	for i := len(final.Content) - 1; i >= 0; i-- {
		if tc, ok := final.Content[i].(*src.ToolCall); ok {
			return tc
		}
	}
	t.Fatalf("no ToolCall block in final content (%d blocks)", len(final.Content))
	return nil
}

func TestLateToolCallIDAndNameReachFinishedBlock(t *testing.T) {
	final, ends := runToolCallStream(t, []string{
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"fi"}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_late","function":{"name":"read","arguments":"le\":\"x\"}"}}]}}]}`,
	})

	tc := finalToolCall(t, final)
	if tc.ID != "call_late" {
		t.Errorf("late-streamed id lost: ToolCall.ID = %q, want call_late", tc.ID)
	}
	if tc.Name != "read" {
		t.Errorf("late-streamed name lost: ToolCall.Name = %q, want read", tc.Name)
	}
	if tc.Arguments["file"] != "x" {
		t.Errorf("arguments = %v, want {file: x}", tc.Arguments)
	}
	if len(ends) != 1 || ends[0].ToolCall == nil || ends[0].ToolCall.ID != "call_late" {
		t.Errorf("toolcall_end event must carry the backfilled block, got %+v", ends)
	}
}

func TestFirstToolCallIDWinsOverLaterIds(t *testing.T) {
	final, _ := runToolCallStream(t, []string{
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_first","function":{"name":"read","arguments":""}}]}}]}`,
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_other","function":{"arguments":"{}"}}]}}]}`,
	})

	tc := finalToolCall(t, final)
	if tc.ID != "call_first" {
		t.Errorf("first-seen id must win (reference parity): got %q, want call_first", tc.ID)
	}
}

func TestNeverDeliveredToolCallIDStaysEmpty(t *testing.T) {
	final, _ := runToolCallStream(t, []string{
		`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"read","arguments":"{}"}}]}}]}`,
	})

	tc := finalToolCall(t, final)
	if tc.ID != "" {
		t.Errorf("id without any id delta should stay empty (TS parity), got %q", tc.ID)
	}
	if tc.Name != "read" {
		t.Errorf("name = %q, want read", tc.Name)
	}
}
