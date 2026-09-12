package ycode

// agent_session_model_test.go covers GetAvailableThinkingLevels (audit #16):
// with no model selected the full list is offered (TS returns THINKING_LEVELS
// when this.model is undefined); only a selected non-reasoning model narrows
// the list to "off".

import (
	"testing"

	"ycode/packages/agent-sdk/src"
	llm "ycode/packages/llm/src"
)

func thinkingSession(model llm.Model[llm.Api]) *AgentSession {
	agent := sdk.NewAgent(sdk.AgentOptions{
		InititalState: &sdk.PartialAgentState{Model: &model},
	})
	return &AgentSession{Agent: agent}
}

func TestAvailableThinkingLevelsNoModelReturnsFullList(t *testing.T) {
	// Zero-value model = "no model selected" (GetModel().ID == "").
	s := thinkingSession(llm.Model[llm.Api]{})

	got := s.GetAvailableThinkingLevels()
	if len(got) != len(ThinkingLevels) {
		t.Fatalf("no-model list = %v, want all %d levels", got, len(ThinkingLevels))
	}
}

func TestAvailableThinkingLevelsNonReasoningModelOffOnly(t *testing.T) {
	s := thinkingSession(llm.Model[llm.Api]{ID: "plain", Reasoning: false})

	got := s.GetAvailableThinkingLevels()
	if len(got) != 1 || got[0] != sdk.ThinkingOff {
		t.Fatalf("non-reasoning list = %v, want [off]", got)
	}
}

func TestAvailableThinkingLevelsReasoningModelFullList(t *testing.T) {
	s := thinkingSession(llm.Model[llm.Api]{ID: "reasoner", Reasoning: true})

	got := s.GetAvailableThinkingLevels()
	if len(got) != len(ThinkingLevels) {
		t.Fatalf("reasoning-model list = %v, want all %d levels", got, len(ThinkingLevels))
	}
}
