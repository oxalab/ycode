package sdk

import (
	"encoding/json"
	"strings"
	"testing"

	"ycode/packages/llm/src"
)

// The proxy SSE wire must match the TS proxy format
// (pi/packages/agent/src/proxy.ts:39-45 uses "delta"). The pre-fix
// `json:"detla"` tag silently dropped every text/thinking/toolcall delta
// decoded from a TS-format server.
func TestProxyAssistantMessageEventDeltaTag(t *testing.T) {
	event := ProxyAssistantMessageEvent{
		Type:   "text_delta",
		Delta:  "hello",
		Reason: llm.StopReasonToolUse,
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	if _, ok := generic["delta"]; !ok {
		t.Fatalf("marshaled proxy event missing \"delta\" key: %s", data)
	}
	if _, ok := generic["detla"]; ok {
		t.Fatalf("marshaled proxy event still contains the misspelled \"detla\" key: %s", data)
	}
	if !strings.Contains(string(data), `"reason":"toolUse"`) {
		t.Fatalf("marshaled proxy event stop reason = %s, want \"reason\":\"toolUse\"", data)
	}
}
