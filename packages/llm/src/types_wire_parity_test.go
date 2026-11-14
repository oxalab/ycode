package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
Wire-parity pins for the catalog spellings (TS reference: pi/packages/ai/src/types.ts).

These values reach session JSONL files, the agent-sdk event stream, and the
export-html template (`msg.stopReason !== 'toolUse'`), so a misspelled constant
or JSON tag silently diverges from TS-written data on every tool-calling
response. Story 37 fixed: "tooluse"→"toolUse", thoughSignature→thoughtSignature,
beedrock/mistral-converations Api values, allow_fallback→allow_fallbacks.
*/

func TestStopReasonToolUseWireValue(t *testing.T) {
	if StopReasonToolUse != "toolUse" {
		t.Fatalf("StopReasonToolUse = %q, want %q (TS types.ts:269)", StopReasonToolUse, "toolUse")
	}
	msg := AssistantMessage{
		Role:       RoleAssistant,
		Provider:   Anthropic,
		Model:      "claude-opus-4-6",
		StopReason: StopReasonToolUse,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"stopReason":"toolUse"`) {
		t.Fatalf("marshaled stopReason = %s, want \"stopReason\":\"toolUse\"", data)
	}
	if strings.Contains(string(data), `"tooluse"`) {
		t.Fatalf("marshaled message still contains the misspelled stop reason: %s", data)
	}
}

func TestStopReasonDecodeFromTSFormat(t *testing.T) {
	// A session entry written by TS (or post-fix Go) must decode into the constant.
	var msg AssistantMessage
	if err := json.Unmarshal([]byte(`{"role":"assistant","stopReason":"toolUse","usage":{},"timestamp":0}`), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != StopReasonToolUse {
		t.Fatalf("decoded stopReason = %q, want it to equal StopReasonToolUse (%q)", msg.StopReason, StopReasonToolUse)
	}
}

func TestThoughtSignatureWireKey(t *testing.T) {
	call := ToolCall{Type: "toolCall", ID: "call_1", Name: "read", Arguments: map[string]any{"a": 1}, ThoughtSignature: "c2ln"}
	data, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["thoughtSignature"]; !ok {
		t.Fatalf("marshaled toolCall missing \"thoughtSignature\" key: %s", data)
	}
	if _, ok := generic["thoughSignature"]; ok {
		t.Fatalf("marshaled toolCall still contains the misspelled \"thoughSignature\" key: %s", data)
	}
}

func TestToolCallDecodeWithThoughtSignature(t *testing.T) {
	// TS-written toolCall blocks carry camelCase "thoughtSignature" — must
	// populate the field (pre-fix Go could not round-trip TS sessions).
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"type":"toolCall","id":"call_1","name":"read","arguments":{"a":1},"thoughtSignature":"c2ln"}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.ThoughtSignature != "c2ln" {
		t.Fatalf("decoded ThoughtSignature = %q, want %q", call.ThoughtSignature, "c2ln")
	}
}

func TestApiConstantsMatchTS(t *testing.T) {
	if MistralConversations != "mistral-conversations" {
		t.Fatalf("MistralConversations = %q, want %q (TS types.ts KnownApi)", MistralConversations, "mistral-conversations")
	}
	if BedrockConverseStream != "bedrock-converse-stream" {
		t.Fatalf("BedrockConverseStream = %q, want %q (TS types.ts KnownApi)", BedrockConverseStream, "bedrock-converse-stream")
	}
	// The registry/catalog register these spellings — a constant that matched
	// nothing made the add-provider chooser write un-dispatchable models.json.
	for _, api := range []Api{OpenAICompletions, MistralConversations, OpenAIResponses, AzureOpenAIResponses, OpenAICodexResponses, AnthropicMessages, BedrockConverseStream, GoogleGenerativeAI, GoogleVertexAI} {
		if api == "" {
			t.Fatal("empty Api constant")
		}
	}
}

func TestOpenRouterRoutingFallbacksTag(t *testing.T) {
	yes := true
	data, err := json.Marshal(OpenRouterRouting{AllowFallbacks: &yes})
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["allow_fallbacks"]; !ok {
		t.Fatalf("marshaled routing missing \"allow_fallbacks\" key (TS types.ts:448): %s", data)
	}
	if _, ok := generic["allow_fallback"]; ok {
		t.Fatalf("marshaled routing still contains the misspelled \"allow_fallback\" key: %s", data)
	}
}
