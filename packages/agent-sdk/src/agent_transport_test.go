package sdk

import (
	"testing"

	llm "ycode/packages/llm/src"
)

// The agent transport default must be "auto" (TS settings-manager.ts:659
// `?? "auto"`) so the codex provider's WS-first transport is on by default,
// with its SSE fallback still in play. Story 39 flipped this from "sse".
func TestAgentTransportDefaultAuto(t *testing.T) {
	agent := NewAgent(AgentOptions{})
	if agent.transport != llm.TransportAuto {
		t.Fatalf("agent default transport = %q, want auto", agent.transport)
	}
	agent2 := NewAgent(AgentOptions{Transport: llm.TransportSSE})
	if agent2.transport != llm.TransportSSE {
		t.Fatalf("explicit transport = %q, want sse", agent2.transport)
	}
}
