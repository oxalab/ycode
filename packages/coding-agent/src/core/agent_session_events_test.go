package ycode

// agent_session_events_test.go pins the agentEventToSessionEvent mapping
// (audit #15): turn_end carries a structured payload and never touches
// RetryError — that field belongs to auto_retry_* events only.

import (
	"testing"

	"ycode/packages/agent-sdk/src"
	llm "ycode/packages/llm/src"
)

func TestTurnEndMappingCarriesStructuredPayload(t *testing.T) {
	toolResult := llm.ToolResultMessage[any]{
		Role:       "toolResult",
		ToolCallID: "call-1",
	}
	event := agentEventToSessionEvent(sdk.EventTurnEnd{
		Message:     llm.AssistantMessage{Role: "assistant"},
		ToolResults: []llm.ToolResultMessage[any]{toolResult},
	})

	if event.EventType != "turn_end" {
		t.Errorf("EventType = %q, want turn_end", event.EventType)
	}
	if event.RetryError != "" {
		t.Errorf("RetryError = %q on turn_end — must stay empty (audit #15)", event.RetryError)
	}
	if event.Message == nil {
		t.Error("Message not carried on turn_end")
	}
	if len(event.ToolResults) != 1 || event.ToolResults[0].ToolCallID != "call-1" {
		t.Errorf("ToolResults = %+v, want the single tool result", event.ToolResults)
	}
}

func TestRetryErrorOnlySetByRetryEvents(t *testing.T) {
	cases := []struct {
		event sdk.AgentEvent
		want  string
	}{
		{sdk.EventAgentStart{}, "agent_start"},
		{sdk.EventAgentEnd{}, "agent_end"},
		{sdk.EventTurnStart{}, "turn_start"},
		{sdk.EventMessageStart{}, "message_start"},
		{sdk.EventMessageEnd{}, "message_end"},
	}
	for _, c := range cases {
		got := agentEventToSessionEvent(c.event)
		if got.EventType != c.want {
			t.Errorf("EventType = %q, want %q", got.EventType, c.want)
		}
		if got.RetryError != "" {
			t.Errorf("event %q: RetryError = %q, want empty", got.EventType, got.RetryError)
		}
	}
}
