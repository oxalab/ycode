package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	src "ycode/packages/llm/src"
)

// Agent-loop secondary 5a (docs/agent-loop-context-bug.md §5a): GLM streams
// thinking on reasoning_content/reasoning/reasoning_text. The field NAME must
// be stored in ThinkingContent.ThinkingSignature so the replay path emits the
// thinking back under the same wire key (TS ensureThinkingBlock stores
// foundReasoningField; previously Go never set it, silently dropping past
// turns' thinking from every replayed request).

func TestStreamReasoningContentRecordsFieldSignature(t *testing.T) {
	run := func(wireField string) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"" + wireField + "\":\"deep \"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"" + wireField + "\":\"thought\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		model := src.Model[src.Api]{
			ID: "glm-test", Name: "GLM Test", Api: src.Api("openai-completions"),
			Provider: src.Provider("zai"), BaseURL: server.URL,
		}
		ch := StreamOpenAICompletions(model, src.ChatContext{Messages: []src.Message{}},
			&OpenAICompletionsOptions{APIKey: "test-key"})()

		var final *src.AssistantMessage
		for ev := range ch {
			if ev.Type == "done" || ev.Type == "error" {
				if ev.Message != nil {
					final = ev.Message
				}
			}
		}
		if final == nil {
			t.Fatalf("[%s] no final message", wireField)
		}
		var thinking *src.ThinkingContent
		for _, b := range final.Content {
			if tk, ok := b.(*src.ThinkingContent); ok {
				thinking = tk
			}
		}
		if thinking == nil {
			t.Fatalf("[%s] no thinking block in final content: %#v", wireField, final.Content)
		}
		if thinking.Thinking != "deep thought" {
			t.Errorf("[%s] thinking = %q, want %q", wireField, thinking.Thinking, "deep thought")
		}
		if thinking.ThinkingSignature != wireField {
			t.Errorf("[%s] ThinkingSignature = %q, want %q (the wire field name)", wireField, thinking.ThinkingSignature, wireField)
		}
	}
	run("reasoning_content")
	run("reasoning")
	run("reasoning_text")
}

// The replay side must emit the thinking under the signature (the wire field
// name) as a top-level assistant message key, and drop unsigned thinking (TS
// parity — pi openai-completions.ts:818-846).
func TestConvertMessagesReplaysThinkingUnderSignatureKey(t *testing.T) {
	model := src.Model[src.Api]{ID: "glm-test", Api: src.Api("openai-completions"), Provider: src.Provider("zai")}

	assistant := src.AssistantMessage{
		Role:     src.RoleAssistant,
		Api:      src.Api("openai-completions"),
		Provider: src.Provider("zai"),
		Model:    "glm-test",
		Content: []src.ContentBlock{
			&src.ThinkingContent{Type: "thinking", Thinking: "line one\nline two", ThinkingSignature: "reasoning_content"},
			&src.TextContent{Type: "text", Text: "the answer"},
		},
		StopReason: src.StopReasonStop,
	}
	msgs := convertOpenAICompletionsMessages(model, src.ChatContext{
		Messages: []src.Message{
			src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{&src.TextContent{Type: "text", Text: "q"}}},
			assistant,
		},
	}, &OpenAICompletionsCompat{})

	var assistantMsg map[string]interface{}
	for _, m := range msgs {
		if r, ok := m["role"].(string); ok && r == "assistant" {
			assistantMsg = m
		}
	}
	if assistantMsg == nil {
		t.Fatal("no assistant message in converted output")
	}
	if got, ok := assistantMsg["reasoning_content"].(string); !ok || got != "line one\nline two" {
		t.Errorf("assistant[reasoning_content] = %v, want the joined thinking", assistantMsg["reasoning_content"])
	}
	if content, ok := assistantMsg["content"].(string); !ok || content != "the answer" {
		t.Errorf("assistant content = %v, want %q", assistantMsg["content"], "the answer")
	}

	// Unsigned thinking is dropped (no key emitted).
	unsigned := src.AssistantMessage{
		Role:       src.RoleAssistant,
		Content:    []src.ContentBlock{&src.ThinkingContent{Type: "thinking", Thinking: "no sig"}},
		StopReason: src.StopReasonStop,
	}
	msgs = convertOpenAICompletionsMessages(model, src.ChatContext{
		Messages: []src.Message{unsigned},
	}, &OpenAICompletionsCompat{})
	assistantMsg = nil
	for _, m := range msgs {
		if r, ok := m["role"].(string); ok && r == "assistant" {
			assistantMsg = m
		}
	}
	if assistantMsg == nil {
		t.Fatal("no assistant message for unsigned case")
	}
	if _, exists := assistantMsg["reasoning_content"]; exists {
		t.Errorf("unsigned thinking must not be replayed, got %v", assistantMsg["reasoning_content"])
	}
}
