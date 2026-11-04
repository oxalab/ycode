package providers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func anthropicTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "claude-test", Name: "Claude Test", Api: src.Api("anthropic-messages"),
		Provider: src.Provider("anthropic"), BaseURL: "https://example.invalid/v1/messages",
		MaxTokens: 3000,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

func runAnthropicSSE(t *testing.T, m src.Model[src.Api], sse string, opts *AnthropicOptions) (*src.AssistantMessage, []src.AssistantMessageEvent, error) {
	t.Helper()
	ch := make(chan src.AssistantMessageEvent, 256)
	output := &src.AssistantMessage{Content: []src.ContentBlock{}, StopReason: src.StopReasonStop}
	err := consumeAnthropicStream(io.NopCloser(strings.NewReader(sse)), ch, output, m, src.ChatContext{}, false, opts)
	close(ch)
	var evts []src.AssistantMessageEvent
	for e := range ch {
		evts = append(evts, e)
	}
	return output, evts, err
}

// --- Streaming loop ---

func TestAnthropicSSEHappyPathCost(t *testing.T) {
	m := anthropicTestModel(func(m *src.Model[src.Api]) { m.Cost = src.ModelCost{Input: 1, Output: 1} })
	sse := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":1000000,\"output_tokens\":2}}}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1000000}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	output, evts, err := runAnthropicSSE(t, m, sse, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if output.ResponseID != "msg_1" {
		t.Fatalf("responseId = %q", output.ResponseID)
	}
	if output.StopReason != src.StopReasonStop {
		t.Fatalf("stopReason = %v", output.StopReason)
	}
	// message_delta overwrote output to 1M tokens: cost = 1 + 1 = 2.
	if output.Usage.Output != 1000000 || output.Usage.Input != 1000000 {
		t.Fatalf("usage = %+v", output.Usage)
	}
	if output.Usage.Cost.Total != 2 {
		t.Fatalf("cost = %+v, want total 2 (CalculateCost wired)", output.Usage.Cost)
	}
	var kinds []string
	for _, e := range evts {
		kinds = append(kinds, e.Type)
	}
	// (start/done are emitted by StreamAnthropic, not consumeAnthropicStream.)
	if got, want := strings.Join(kinds, ","), "text_start,text_delta,text_end"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestAnthropicRedactedThinkingRoundTrip(t *testing.T) {
	m := anthropicTestModel(nil)
	sse := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{}}}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"opaque-payload\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	output, _, err := runAnthropicSSE(t, m, sse, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	tc, ok := output.Content[0].(*src.ThinkingContent)
	if !ok {
		t.Fatalf("block 0 = %T", output.Content[0])
	}
	if tc.Redacted == nil || !*tc.Redacted {
		t.Fatal("streamed redacted block must carry the Redacted flag")
	}

	// Replay must send redacted_thinking, not a thinking/text block.
	msgs := &src.AssistantMessage{
		Role: src.RoleAssistant, Model: m.ID, Provider: m.Provider, Api: m.Api,
		Content: []src.ContentBlock{tc},
	}
	params := convertAnthropicMessages([]src.Message{msgs}, m, false, nil)
	if len(params) != 1 {
		t.Fatalf("params = %d", len(params))
	}
	content := params[0]["content"].([]map[string]interface{})
	if content[0]["type"] != "redacted_thinking" || content[0]["data"] != "opaque-payload" {
		t.Fatalf("replayed block = %#v, want redacted_thinking with data", content[0])
	}
}

// An empty-thinking redacted block still replays as redacted_thinking
// (the redacted check runs before the empty-thinking skip).
func TestAnthropicRedactedEmptyThinkingReplay(t *testing.T) {
	m := anthropicTestModel(nil)
	redacted := true
	msgs := &src.AssistantMessage{
		Role: src.RoleAssistant, Model: m.ID, Provider: m.Provider, Api: m.Api,
		Content: []src.ContentBlock{&src.ThinkingContent{Type: "thinking", Thinking: "", ThinkingSignature: "data", Redacted: &redacted}},
	}
	params := convertAnthropicMessages([]src.Message{msgs}, m, false, nil)
	content := params[0]["content"].([]map[string]interface{})
	if content[0]["type"] != "redacted_thinking" {
		t.Fatalf("replayed block = %#v", content[0])
	}
}

func TestAnthropicSSEIntegrity(t *testing.T) {
	m := anthropicTestModel(nil)

	// SSE-level event: error → raw data wording.
	sse := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded\",\"message\":\"Overloaded\"}}\n\n"
	_, _, err := runAnthropicSSE(t, m, sse, nil)
	if err == nil || !strings.Contains(err.Error(), "Overloaded") {
		t.Fatalf("event:error err = %v", err)
	}

	// Malformed JSON → diagnostic error (old code silently skipped).
	sse = "data: {not json}\n\n"
	if _, _, err := runAnthropicSSE(t, m, sse, nil); err == nil || !strings.Contains(err.Error(), "Could not parse Anthropic SSE event") {
		t.Fatalf("parse err = %v", err)
	}

	// Stream ends after message_start without message_stop → error.
	sse = "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{}}}\n\n"
	if _, _, err := runAnthropicSSE(t, m, sse, nil); err == nil || !strings.Contains(err.Error(), "ended before message_stop") {
		t.Fatalf("truncated err = %v", err)
	}
}

// --- Request/auth ---

func TestAnthropicStatusCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()

	m := anthropicTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	_, err := doAnthropicRequest(&http.Client{}, m, src.ChatContext{}, map[string]interface{}{}, "bad-key", nil)
	if err == nil || !strings.Contains(err.Error(), "Anthropic API error (401)") {
		t.Fatalf("err = %v, want status error", err)
	}
}

func captureAnthropicRequest(t *testing.T, m src.Model[src.Api], opts *AnthropicOptions, context src.ChatContext, apiKey string) *http.Request {
	t.Helper()
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	m.BaseURL = srv.URL
	if _, err := doAnthropicRequest(&http.Client{}, m, context, map[string]interface{}{}, apiKey, opts); err != nil {
		t.Fatalf("request: %v", err)
	}
	return captured
}

func TestAnthropicAuthBranches(t *testing.T) {
	// OAuth: Bearer + CC identity headers + CC betas + UA/X-App.
	m := anthropicTestModel(func(m *src.Model[src.Api]) { m.ID = "claude-sonnet-4-5" }) // non-adaptive
	req := captureAnthropicRequest(t, m, nil, src.ChatContext{}, "sk-ant-oat01-test")
	if got := req.Header.Get("Authorization"); got != "Bearer sk-ant-oat01-test" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("anthropic-beta"); got != "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14" {
		t.Fatalf("anthropic-beta = %q", got)
	}
	if req.Header.Get("X-App") != "cli" || req.Header.Get("User-Agent") != "claude-cli/2.1.75" {
		t.Fatalf("identity headers missing: UA=%q X-App=%q", req.Header.Get("User-Agent"), req.Header.Get("X-App"))
	}

	// Non-OAuth non-adaptive model: interleaved beta defaults ON, no CC betas.
	m2 := anthropicTestModel(func(m *src.Model[src.Api]) { m.ID = "claude-sonnet-4-5" })
	req2 := captureAnthropicRequest(t, m2, nil, src.ChatContext{}, "plain-key")
	if got := req2.Header.Get("anthropic-beta"); got != "interleaved-thinking-2025-05-14" {
		t.Fatalf("default interleaved beta = %q", got)
	}
	if got := req2.Header.Get("x-api-key"); got != "plain-key" {
		t.Fatalf("x-api-key = %q", got)
	}

	// Adaptive model: no interleaved beta. Tools + eager support: no fine-grained beta.
	m3 := anthropicTestModel(func(m *src.Model[src.Api]) { m.ID = "claude-opus-4.6" })
	tools := []src.Tool[any]{{Name: "read", Description: "d", Parameters: map[string]interface{}{"type": "object"}}}
	req3 := captureAnthropicRequest(t, m3, nil, src.ChatContext{Tools: tools}, "plain-key")
	if got := req3.Header.Get("anthropic-beta"); got != "" {
		t.Fatalf("adaptive+eager beta = %q, want empty", got)
	}

	// Fireworks (no eager streaming) with tools: fine-grained beta appears,
	// plus the default interleaved beta (non-adaptive model id).
	m4 := anthropicTestModel(func(m *src.Model[src.Api]) { m.Provider = src.Provider("fireworks") })
	req4 := captureAnthropicRequest(t, m4, nil, src.ChatContext{Tools: tools}, "plain-key")
	if got := req4.Header.Get("anthropic-beta"); got != fineGrainedToolStreamingBeta+","+interleavedThinkingBeta {
		t.Fatalf("fireworks beta = %q", got)
	}
}

// --- Params ---

func TestAnthropicParamsPins(t *testing.T) {
	m := anthropicTestModel(func(m *src.Model[src.Api]) { m.ID = "claude-opus-4.6"; m.Reasoning = true })

	// output_config is a top-level sibling of thinking.
	params := buildAnthropicParams(m, src.ChatContext{}, false, &AnthropicOptions{
		ThinkingEnabled: boolPtr(true), Effort: anthropicEffortPtr("high"),
	}).(map[string]interface{})
	thinking, ok := params["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "adaptive" {
		t.Fatalf("thinking = %#v", params["thinking"])
	}
	if _, nested := thinking["output_config"]; nested {
		t.Fatal("output_config must NOT be nested inside thinking")
	}
	oc, ok := params["output_config"].(map[string]interface{})
	if !ok || oc["effort"] != "high" {
		t.Fatalf("output_config = %#v", params["output_config"])
	}

	// toolChoice string wraps to {type: s}.
	params2 := buildAnthropicParams(m, src.ChatContext{}, false, &AnthropicOptions{ToolChoice: "any"}).(map[string]interface{})
	if tc, ok := params2["tool_choice"].(map[string]interface{}); !ok || tc["type"] != "any" {
		t.Fatalf("tool_choice = %#v", params2["tool_choice"])
	}

	// maxTokens default = model.MaxTokens/3 (1000 for 3000), no 4096 fallback.
	m0 := anthropicTestModel(func(m *src.Model[src.Api]) { m.MaxTokens = 0 })
	params3 := buildAnthropicParams(m0, src.ChatContext{}, false, nil).(map[string]interface{})
	if params3["max_tokens"] != 0 {
		t.Fatalf("max_tokens = %#v, want 0 (TS parity)", params3["max_tokens"])
	}
}

func TestAnthropicConvertContentBlocks(t *testing.T) {
	text := func(s string) src.ContentBlock { return &src.TextContent{Type: "text", Text: s} }
	image := func() src.ContentBlock {
		return &src.ImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"}
	}

	// Text-only → joined string.
	got := convertAnthropicContentBlocks([]src.ContentBlock{text("a"), text("b")})
	if got != "a\nb" {
		t.Fatalf("text-only = %#v", got)
	}

	// Image-only → array with leading placeholder.
	got = convertAnthropicContentBlocks([]src.ContentBlock{image()})
	blocks, ok := got.([]map[string]interface{})
	if !ok || len(blocks) != 2 || blocks[0]["text"] != "(see attached image)" {
		t.Fatalf("image-only = %#v", got)
	}

	// Mixed → array, no placeholder.
	got = convertAnthropicContentBlocks([]src.ContentBlock{text("hi"), image()})
	blocks, ok = got.([]map[string]interface{})
	if !ok || len(blocks) != 2 || blocks[0]["text"] != "hi" {
		t.Fatalf("mixed = %#v", got)
	}
}

func TestAnthropicConvertTools(t *testing.T) {
	tools := []src.Tool[any]{{
		Name: "read", Description: "d",
		Parameters: map[string]interface{}{"type": "object"}, // no properties/required
	}}
	out := convertAnthropicTools(tools, false, true, nil)
	schema := out[0]["input_schema"].(map[string]interface{})
	if schema["properties"] == nil || schema["required"] == nil {
		t.Fatalf("schema = %#v, want non-null defaults", schema)
	}
	if out[0]["eager_input_streaming"] != true {
		t.Fatalf("eager_input_streaming = %#v", out[0]["eager_input_streaming"])
	}

	// No eager support → flag omitted.
	out2 := convertAnthropicTools(tools, false, false, nil)
	if _, has := out2[0]["eager_input_streaming"]; has {
		t.Fatal("eager_input_streaming set despite unsupported")
	}

	// OAuth naming: canonical CC casing.
	out3 := convertAnthropicTools(tools, true, true, nil)
	if out3[0]["name"] != "Read" {
		t.Fatalf("oauth name = %#v, want Read", out3[0]["name"])
	}
}

func TestAnthropicStopReasons(t *testing.T) {
	if r, msg := mapAnthropicStopReason("sensitive"); r != src.StopReasonError || msg != "" {
		t.Fatalf("sensitive = %v/%q", r, msg)
	}
	r, msg := mapAnthropicStopReason("brand_new_reason")
	if r != src.StopReasonError || !strings.Contains(msg, "Unhandled stop reason") {
		t.Fatalf("unknown = %v/%q", r, msg)
	}
	if r, _ := mapAnthropicStopReason("pause_turn"); r != src.StopReasonStop {
		t.Fatalf("pause_turn = %v", r)
	}
}

// Simple wrapper: adaptive → effort; budget model → adjusted maxTokens+budget.
func TestAnthropicSimpleWrapper(t *testing.T) {
	adaptive := anthropicTestModel(func(m *src.Model[src.Api]) { m.ID = "claude-opus-4.6" })
	opts := buildAnthropicSimpleOptions(adaptive, &src.SimpleStreamOptions{Reasoning: "high"}, "k")
	if opts.ThinkingEnabled == nil || !*opts.ThinkingEnabled || opts.Effort == nil || *opts.Effort != "high" {
		t.Fatalf("adaptive opts = %+v", opts)
	}

	legacy := anthropicTestModel(func(m *src.Model[src.Api]) { m.ID = "claude-sonnet-4-5" })
	opts2 := buildAnthropicSimpleOptions(legacy, &src.SimpleStreamOptions{Reasoning: "high"}, "k")
	if opts2.ThinkingBudgetTokens == nil || *opts2.ThinkingBudgetTokens != 1976 {
		t.Fatalf("budget = %#v, want 1976 (3000 capped, minus 1024 min output)", opts2.ThinkingBudgetTokens)
	}
	if opts2.MaxTokens == nil || *opts2.MaxTokens != 3000 {
		t.Fatalf("maxTokens = %#v, want 3000 (model cap)", opts2.MaxTokens)
	}

	// No reasoning → thinking disabled.
	opts3 := buildAnthropicSimpleOptions(legacy, &src.SimpleStreamOptions{}, "k")
	if opts3.ThinkingEnabled == nil || *opts3.ThinkingEnabled {
		t.Fatal("no-reasoning must disable thinking")
	}
}

// OAuth streamed tool names map back through the registered tools.
func TestFromClaudeCodeName(t *testing.T) {
	tools := []src.Tool[any]{{Name: "Read"}, {Name: "bash_tool"}}
	if got := fromClaudeCodeName("READ", tools); got != "Read" {
		t.Fatalf("got %q, want Read", got)
	}
	if got := fromClaudeCodeName("unknown_tool", tools); got != "unknown_tool" {
		t.Fatalf("got %q", got)
	}
}
