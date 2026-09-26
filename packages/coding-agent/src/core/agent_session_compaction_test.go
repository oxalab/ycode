package ycode

// agent_session_compaction_test.go pins completeSimple's option pass-through
// (audit #32): the caller's maxTokens budget must reach the provider options —
// it used to be dropped on the floor, letting provider defaults
// (min(model.maxTokens, 32000) / model.maxTokens/3) win.

import (
	"context"
	"testing"

	llm "ycode/packages/llm/src"
)

func TestCompleteSimple_PassesMaxTokens(t *testing.T) {
	api := llm.Api("test-complete-simple-capture")
	var captured *int
	llm.RegisterApiProvider(llm.ApiProvider{
		Api: api,
		StreamSimple: func(ctx context.Context, model llm.Model[llm.Api], chatCtx llm.ChatContext, options *llm.SimpleStreamOptions) <-chan llm.AssistantMessageEvent {
			if options != nil {
				captured = options.MaxTokens
			}
			ch := make(chan llm.AssistantMessageEvent, 1)
			ch <- llm.AssistantMessageEvent{Type: "done", Message: &llm.AssistantMessage{Role: llm.RoleAssistant}}
			close(ch)
			return ch
		},
	}, "test")

	sm := NewSettingsManager(NewFileSettingsStorage(t.TempDir(), t.TempDir()))
	s := &AgentSession{SettingsManager: sm}
	model := &llm.Model[llm.Api]{ID: "test-model", Api: api}

	// Caller budget reaches the provider options.
	if _, err := s.completeSimple(context.Background(), model, "", nil, "", nil, 4096, llm.ThinkingLevel("")); err != nil {
		t.Fatalf("completeSimple: %v", err)
	}
	if captured == nil || *captured != 4096 {
		t.Errorf("captured MaxTokens = %v, want 4096", captured)
	}

	// Zero budget → nil (provider defaults preserved; matches TS's falsy
	// `options?.maxTokens || default` in the Anthropic path).
	captured = nil
	if _, err := s.completeSimple(context.Background(), model, "", nil, "", nil, 0, llm.ThinkingLevel("")); err != nil {
		t.Fatalf("completeSimple: %v", err)
	}
	if captured != nil {
		t.Errorf("captured MaxTokens = %d, want nil (zero budget → provider defaults)", *captured)
	}
}
