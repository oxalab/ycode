package providers

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func mistralTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "mistral-large-latest", Name: "Mistral Large (latest)", Api: src.Api("mistral-conversations"),
		Provider: src.Provider("mistral"), BaseURL: "https://api.mistral.ai",
		Reasoning: false, Input: []src.InputType{src.InputText, src.InputImage},
		Cost:          src.ModelCost{Input: 0.5, Output: 1.5},
		ContextWindow: 262144, MaxTokens: 262144,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

func mistralSSE(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func collectMistralStream(t *testing.T, m src.Model[src.Api], opts *MistralOptions) (*src.AssistantMessage, []src.AssistantMessageEvent, string) {
	t.Helper()
	return collectMistralStreamWithHistory(t, m, src.ChatContext{}, opts)
}

func collectMistralStreamWithHistory(t *testing.T, m src.Model[src.Api], chatCtx src.ChatContext, opts *MistralOptions) (*src.AssistantMessage, []src.AssistantMessageEvent, string) {
	t.Helper()
	if opts == nil {
		opts = &MistralOptions{}
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.APIKey == "" {
		opts.APIKey = "test-key"
	}
	var final *src.AssistantMessage
	var events []src.AssistantMessageEvent
	errMsg := ""
	for ev := range StreamMistral(m, chatCtx, opts)() {
		events = append(events, ev)
		if ev.Type == "error" && ev.Error != nil {
			final = ev.Error
			errMsg = ev.Error.ErrorMessage
		}
		if ev.Type == "done" {
			final = ev.Message
		}
	}
	return final, events, errMsg
}

// --- Request shape: URL, auth, snake_case body ---

func TestMistralRequestShape(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	var gotPath, gotAuth, gotAccept string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		_, _ = w.Write([]byte(mistralSSE(`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
	}))
	defer srv.Close()

	m := mistralTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Input = []src.InputType{src.InputText}
	})
	temp := 0.7
	maxTok := 512
	auto := "auto"
	effort := "high"
	mode := "reasoning"
	_, _, errMsg := collectMistralStream(t, m, &MistralOptions{
		Temperature: &temp, MaxTokens: &maxTok, ToolChoice: auto,
		ReasoningEffort: &effort, PromptMode: &mode,
	})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if body["model"] != "mistral-large-latest" || body["stream"] != true {
		t.Fatalf("model/stream = %v/%v", body["model"], body["stream"])
	}
	if body["max_tokens"] != float64(512) {
		t.Fatalf("max_tokens = %v (must be snake_case)", body["max_tokens"])
	}
	if _, has := body["maxTokens"]; has {
		t.Fatal("camelCase maxTokens must not be sent")
	}
	if body["temperature"] != 0.7 {
		t.Fatalf("temperature = %v", body["temperature"])
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", body["tool_choice"])
	}
	if body["prompt_mode"] != "reasoning" {
		t.Fatalf("prompt_mode = %v", body["prompt_mode"])
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", body["reasoning_effort"])
	}
}

func TestMistralHeadersAndAffinity(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		_, _ = w.Write([]byte(mistralSSE(`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
	}))
	defer srv.Close()

	m := mistralTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Headers = map[string]string{"x-model-h": "mv"}
	})
	// Pre-set x-affinity in options headers must be respected.
	_, _, errMsg := collectMistralStream(t, m, &MistralOptions{
		SessionID: "sess-1",
		Headers:   map[string]string{"x-affinity": "explicit", "x-opt-h": "ov"},
	})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if gotHeaders.Get("x-affinity") != "explicit" {
		t.Fatalf("x-affinity = %q, want explicit preserved", gotHeaders.Get("x-affinity"))
	}
	if gotHeaders.Get("x-model-h") != "mv" || gotHeaders.Get("x-opt-h") != "ov" {
		t.Fatalf("header merge = %v", gotHeaders)
	}

	// Without a pre-set value, sessionId populates x-affinity.
	gotHeaders = nil
	_, _, errMsg = collectMistralStream(t, m, &MistralOptions{SessionID: "sess-2"})
	if errMsg != "" {
		t.Fatalf("stream2: %s", errMsg)
	}
	if gotHeaders.Get("x-affinity") != "sess-2" {
		t.Fatalf("x-affinity = %q, want sess-2", gotHeaders.Get("x-affinity"))
	}
}

func TestMistralBodyHistoryPins(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		_, _ = w.Write([]byte(mistralSSE(`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
	}))
	defer srv.Close()

	chatCtx := src.ChatContext{
		SystemPrompt: "sys prompt",
		Tools: []src.Tool[any]{
			{Name: "doit", Description: "does it", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		},
		Messages: []src.Message{
			&src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{
				&src.TextContent{Type: "text", Text: "look"},
				&src.ImageContent{Type: "image", MimeType: "image/png", Data: "aGk="},
			}},
			&src.AssistantMessage{
				Role: src.RoleAssistant, Provider: src.Provider("mistral"), Api: src.Api("mistral-conversations"), Model: "mistral-large-latest",
				Content: []src.ContentBlock{
					&src.ThinkingContent{Type: "thinking", Thinking: "hmm"},
					&src.TextContent{Type: "text", Text: "hi"},
					&src.ToolCall{Type: "toolCall", ID: "call_abc123def", Name: "doit", Arguments: map[string]interface{}{"x": float64(1)}},
				},
			},
			&src.ToolResultMessage[any]{Role: src.RoleTool, ToolCallID: "call_abc123def", ToolName: "doit", Content: []src.ContentBlock{
				&src.TextContent{Type: "text", Text: "result"},
			}},
		},
	}
	_, _, errMsg := collectMistralStreamWithHistory(t, mistralTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL }), chatCtx, nil)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}

	msgs, _ := body["messages"].([]interface{})
	if len(msgs) != 4 {
		t.Fatalf("messages = %d (system + 3), body: %v", len(msgs), body)
	}
	if sys, _ := msgs[0].(map[string]interface{}); sys["role"] != "system" || sys["content"] != "sys prompt" {
		t.Fatalf("system = %v", sys)
	}

	user, _ := msgs[1].(map[string]interface{})
	userContent, _ := user["content"].([]interface{})
	imgChunk, _ := userContent[1].(map[string]interface{})
	if imgChunk["type"] != "image_url" {
		t.Fatalf("image chunk = %v", imgChunk)
	}
	if imgChunk["image_url"] != "data:image/png;base64,aGk=" {
		t.Fatalf("image_url key/value = %v (must be snake_case image_url)", imgChunk)
	}
	if _, has := imgChunk["imageUrl"]; has {
		t.Fatal("camelCase imageUrl must not be sent")
	}

	assistant, _ := msgs[2].(map[string]interface{})
	if assistant["prefix"] != false {
		t.Fatalf("assistant prefix = %v, want false (SDK outbound default)", assistant["prefix"])
	}
	toolCalls, _ := assistant["tool_calls"].([]interface{})
	if len(toolCalls) != 1 {
		t.Fatalf("assistant tool_calls = %v (must be snake_case tool_calls)", assistant["tool_calls"])
	}
	tc, _ := toolCalls[0].(map[string]interface{})
	if tc["type"] != "function" {
		t.Fatalf("tool call = %v", tc)
	}
	// Same-model history keeps the raw tool call ID (TS normalizes only
	// cross-model history — see TestMistralToolCallIDNormalization).
	if tc["id"] != "call_abc123def" {
		t.Fatalf("same-model tool call id = %v, want raw", tc["id"])
	}
	assistantContent, _ := assistant["content"].([]interface{})
	if len(assistantContent) != 2 {
		t.Fatalf("assistant content parts = %v", assistantContent)
	}
	thinking, _ := assistantContent[0].(map[string]interface{})
	if thinking["type"] != "thinking" {
		t.Fatalf("thinking part = %v", thinking)
	}

	toolMsg, _ := msgs[3].(map[string]interface{})
	if toolMsg["tool_call_id"] == nil {
		t.Fatalf("tool message = %v (must be snake_case tool_call_id)", toolMsg)
	}
	if toolMsg["name"] != "doit" {
		t.Fatalf("tool name = %v", toolMsg["name"])
	}

	tools, _ := body["tools"].([]interface{})
	tool0, _ := tools[0].(map[string]interface{})
	fn, _ := tool0["function"].(map[string]interface{})
	if fn["strict"] != false || fn["name"] != "doit" {
		t.Fatalf("function tool = %v", fn)
	}
}

// --- Errors ---

func TestMistralStatus401(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	_, _, errMsg := collectMistralStream(t, mistralTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL }), nil)
	if errMsg != `Mistral API error (401): {"message":"Unauthorized"}` {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestMistralMissingKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request must not be sent without a key")
	}))
	defer srv.Close()

	// Call StreamMistral directly: the collect helper force-fills a test key.
	errMsg := ""
	for ev := range StreamMistral(mistralTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL }), src.ChatContext{}, &MistralOptions{})() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	if errMsg != "No API key for provider: mistral" {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

// --- SSE happy path: text + thinking + usage + cost ---

func TestMistralSSEHappyPath(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mistralSSE(
			`{"id":"resp-1","choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
			`{"id":"resp-1","choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			`{"id":"resp-1","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"pondering"}]}]}}]}`,
			`{"id":"resp-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`,
		)))
	}))
	defer srv.Close()

	final, events, errMsg := collectMistralStream(t, mistralTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL }), nil)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if final == nil {
		t.Fatal("no done event")
	}
	if final.ResponseID != "resp-1" {
		t.Fatalf("responseId = %q", final.ResponseID)
	}
	if final.Usage.Input != 100 || final.Usage.Output != 10 || final.Usage.TotalTokens != 110 {
		t.Fatalf("usage = %+v", final.Usage)
	}
	if final.Usage.CacheRead != 0 || final.Usage.CacheWrite != 0 {
		t.Fatalf("cache usage must be forced to 0: %+v", final.Usage)
	}
	// cost = (0.5/1e6)*100 + (1.5/1e6)*10
	wantIn := (0.5 / 1e6) * 100
	wantOut := (1.5 / 1e6) * 10
	if math.Abs(final.Usage.Cost.Input-wantIn) > 1e-12 || math.Abs(final.Usage.Cost.Output-wantOut) > 1e-12 {
		t.Fatalf("cost = %+v, want in=%v out=%v", final.Usage.Cost, wantIn, wantOut)
	}
	if final.StopReason != src.StopReasonStop {
		t.Fatalf("stopReason = %q", final.StopReason)
	}

	var sawTextStart, sawThinkingStart, sawThinkingDelta bool
	for _, ev := range events {
		switch ev.Type {
		case "text_start":
			sawTextStart = true
		case "thinking_start":
			sawThinkingStart = true
		case "thinking_delta":
			sawThinkingDelta = true
			if ev.Delta != "pondering" {
				t.Fatalf("thinking delta = %q", ev.Delta)
			}
		}
	}
	if !sawTextStart || !sawThinkingStart || !sawThinkingDelta {
		t.Fatalf("missing block events: textStart=%v thinkingStart=%v thinkingDelta=%v", sawTextStart, sawThinkingStart, sawThinkingDelta)
	}
	// Content: text block first, thinking block second.
	if len(final.Content) != 2 {
		t.Fatalf("content blocks = %d", len(final.Content))
	}
	if txt, ok := final.Content[0].(*src.TextContent); !ok || txt.Text != "Hello" {
		t.Fatalf("text block = %+v", final.Content[0])
	}
	if th, ok := final.Content[1].(*src.ThinkingContent); !ok || th.Thinking != "pondering" {
		t.Fatalf("thinking block = %+v", final.Content[1])
	}
}

func TestMistralStopReasons(t *testing.T) {
	cases := map[string]src.StopReason{
		"stop":         src.StopReasonStop,
		"length":       src.StopReasonLength,
		"model_length": src.StopReasonLength,
		"tool_calls":   src.StopReasonToolUse,
		"error":        src.StopReasonError,
		"weird":        src.StopReasonStop,
	}
	for wire, want := range cases {
		if got := mapMistralStopReason(wire); got != want {
			t.Fatalf("mapMistralStopReason(%q) = %q, want %q", wire, got, want)
		}
	}
}

// --- Tool call deltas: accumulation + deterministic end order ---

func TestMistralToolCallAccumulation(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mistralSSE(
			`{"id":"r","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_1","index":0,"function":{"name":"doit","arguments":"{\"x\":"}}]}}]}`,
			`{"id":"r","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_1","index":0,"function":{"arguments":"1,\"y\":2}"}}]}}]}`,
			`{"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		)))
	}))
	defer srv.Close()

	final, events, errMsg := collectMistralStream(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Input = []src.InputType{src.InputText}
	}), nil)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if final == nil || final.StopReason != src.StopReasonToolUse {
		t.Fatalf("final/stopReason = %v/%v", final, final.StopReason)
	}
	if len(final.Content) != 1 {
		t.Fatalf("content = %d blocks", len(final.Content))
	}
	tc, ok := final.Content[0].(*src.ToolCall)
	if !ok {
		t.Fatalf("block = %T", final.Content[0])
	}
	if tc.ID != "call_1" || tc.Name != "doit" {
		t.Fatalf("tool call id/name = %q/%q", tc.ID, tc.Name)
	}
	if tc.Arguments["x"] != float64(1) || tc.Arguments["y"] != float64(2) {
		t.Fatalf("accumulated arguments = %v (args must accumulate across chunks)", tc.Arguments)
	}

	ends := 0
	for _, ev := range events {
		if ev.Type == "toolcall_end" {
			ends++
			if ev.ToolCall == nil || ev.ToolCall.ID != "call_1" {
				t.Fatalf("toolcall_end = %+v", ev.ToolCall)
			}
		}
	}
	if ends != 1 {
		t.Fatalf("toolcall_end count = %d, want 1", ends)
	}
}

func TestMistralToolCallEndOrder(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mistralSSE(
			`{"id":"r","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_B","index":1,"function":{"name":"second","arguments":"{}"}}]}}]}`,
			`{"id":"r","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_A","index":0,"function":{"name":"first","arguments":"{}"}}]}}]}`,
			`{"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		)))
	}))
	defer srv.Close()

	final, events, errMsg := collectMistralStream(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Input = []src.InputType{src.InputText}
	}), nil)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	_ = final
	var endOrder []string
	for _, ev := range events {
		if ev.Type == "toolcall_end" && ev.ToolCall != nil {
			endOrder = append(endOrder, ev.ToolCall.ID)
		}
	}
	// Insertion order (call_B seen first), not map-iteration order.
	if len(endOrder) != 2 || endOrder[0] != "call_B" || endOrder[1] != "call_A" {
		t.Fatalf("toolcall_end order = %v, want [call_B call_A] (insertion order)", endOrder)
	}
}

// --- Simple wrapper ---

func captureSimpleMistralBody(t *testing.T, m src.Model[src.Api], o *src.SimpleStreamOptions) (map[string]interface{}, string) {
	t.Helper()
	var body map[string]interface{}
	var errMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		_, _ = w.Write([]byte(mistralSSE(`{"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
	}))
	defer srv.Close()

	mutated := m
	mutated.BaseURL = srv.URL
	for ev := range StreamSimpleMistral(mutated, src.ChatContext{}, o)() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	return body, errMsg
}

func TestMistralSimpleWrapper(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")

	// No key -> immediate error event.
	errMsg := ""
	for ev := range StreamSimpleMistral(mistralTestModel(nil), src.ChatContext{}, &src.SimpleStreamOptions{})() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	if errMsg != "No API key for provider: mistral" {
		t.Fatalf("no-key errMsg = %q", errMsg)
	}

	withKey := func() *src.SimpleStreamOptions {
		o := &src.SimpleStreamOptions{}
		o.APIKey = "test-key"
		return o
	}

	// Non-reasoning model: neither prompt_mode nor reasoning_effort.
	body, errMsg := captureSimpleMistralBody(t, mistralTestModel(nil), withKey())
	if errMsg != "" {
		t.Fatalf("non-reasoning: %s", errMsg)
	}
	if _, has := body["prompt_mode"]; has {
		t.Fatalf("non-reasoning prompt_mode = %v", body["prompt_mode"])
	}
	if _, has := body["reasoning_effort"]; has {
		t.Fatalf("non-reasoning reasoning_effort = %v", body["reasoning_effort"])
	}

	reasoningOpts := func(level src.ThinkingLevel) *src.SimpleStreamOptions {
		o := withKey()
		o.Reasoning = level
		return o
	}

	// Prompt-mode reasoning model (magistral-medium-latest).
	body, errMsg = captureSimpleMistralBody(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.ID = "magistral-medium-latest"
		m.Reasoning = true
	}), reasoningOpts(src.Medium))
	if errMsg != "" {
		t.Fatalf("magistral: %s", errMsg)
	}
	if body["prompt_mode"] != "reasoning" {
		t.Fatalf("magistral prompt_mode = %v", body["prompt_mode"])
	}
	if _, has := body["reasoning_effort"]; has {
		t.Fatalf("magistral reasoning_effort must be absent, got %v", body["reasoning_effort"])
	}

	// Reasoning-effort model (mistral-small-latest): map default "high".
	body, errMsg = captureSimpleMistralBody(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.ID = "mistral-small-latest"
		m.Reasoning = true
	}), reasoningOpts(src.Medium))
	if errMsg != "" {
		t.Fatalf("small-latest: %s", errMsg)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("small-latest reasoning_effort = %v, want high (map default)", body["reasoning_effort"])
	}
	if _, has := body["prompt_mode"]; has {
		t.Fatalf("small-latest prompt_mode must be absent, got %v", body["prompt_mode"])
	}

	// thinkingLevelMap overrides the "high" default.
	body, errMsg = captureSimpleMistralBody(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.ID = "mistral-small-latest"
		m.Reasoning = true
		m.ThinkingLevelMap = map[string]*src.ThinkingLevel{
			"medium": src.ThinkingLevelPtr(src.ThinkingLevel("none")),
		}
	}), reasoningOpts(src.Medium))
	if errMsg != "" {
		t.Fatalf("map override: %s", errMsg)
	}
	if body["reasoning_effort"] != "none" {
		t.Fatalf("map override reasoning_effort = %v, want none", body["reasoning_effort"])
	}

	// "off" disables reasoning: neither field.
	body, errMsg = captureSimpleMistralBody(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.ID = "magistral-medium-latest"
		m.Reasoning = true
	}), reasoningOpts(src.Off))
	if errMsg != "" {
		t.Fatalf("off: %s", errMsg)
	}
	if _, has := body["prompt_mode"]; has {
		t.Fatalf("off prompt_mode = %v", body["prompt_mode"])
	}
	if _, has := body["reasoning_effort"]; has {
		t.Fatalf("off reasoning_effort = %v", body["reasoning_effort"])
	}
}

// --- Registry & catalog wiring ---

func TestMistralToolCallIDNormalization(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		_, _ = w.Write([]byte(mistralSSE(`{"id":"r1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
	}))
	defer srv.Close()

	// Cross-model history: the 9-char alnum normalizer rewrites IDs.
	chatCtx := src.ChatContext{
		Messages: []src.Message{
			&src.AssistantMessage{
				Role: src.RoleAssistant, Provider: src.Provider("openai"), Api: src.Api("openai-completions"), Model: "gpt-x",
				Content: []src.ContentBlock{
					&src.ToolCall{Type: "toolCall", ID: "call_abc123def456", Name: "doit", Arguments: map[string]interface{}{}},
				},
			},
			&src.ToolResultMessage[any]{Role: src.RoleTool, ToolCallID: "call_abc123def456", ToolName: "doit", Content: []src.ContentBlock{
				&src.TextContent{Type: "text", Text: "result"},
			}},
		},
	}
	_, _, errMsg := collectMistralStreamWithHistory(t, mistralTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Input = []src.InputType{src.InputText}
	}), chatCtx, nil)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}

	msgs, _ := body["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, body: %v", len(msgs), body)
	}
	assistant, _ := msgs[0].(map[string]interface{})
	toolCalls, _ := assistant["tool_calls"].([]interface{})
	tc, _ := toolCalls[0].(map[string]interface{})
	normalizedID, _ := tc["id"].(string)
	if len(normalizedID) != 9 {
		t.Fatalf("normalized tool call id = %q, want 9 alnum chars", normalizedID)
	}
	toolMsg, _ := msgs[1].(map[string]interface{})
	if toolMsg["tool_call_id"] != normalizedID {
		t.Fatalf("tool result tool_call_id = %v, want %q (paired rewrite)", toolMsg["tool_call_id"], normalizedID)
	}
}

func TestMistralRegistered(t *testing.T) {
	if p := src.GetApiProvider("mistral-conversations"); p == nil {
		t.Fatal("mistral-conversations not registered")
	}

	if len(src.Models["mistral"]) != 28 {
		t.Fatalf("mistral catalog = %d models, want 28", len(src.Models["mistral"]))
	}
	reasoning := 0
	for _, m := range src.Models["mistral"] {
		if m.Api != src.Api("mistral-conversations") || m.Provider != src.Provider("mistral") {
			t.Fatalf("catalog %s api/provider = %s/%s", m.ID, m.Api, m.Provider)
		}
		if m.BaseURL != "https://api.mistral.ai" {
			t.Fatalf("catalog %s BaseURL = %q (must have no /v1)", m.ID, m.BaseURL)
		}
		if m.Reasoning {
			reasoning++
		}
	}
	if reasoning != 7 {
		t.Fatalf("reasoning models = %d, want 7", reasoning)
	}

	large := src.Models["mistral"]["mistral-large-latest"]
	if large == nil || large.MaxTokens != 262144 || large.ContextWindow != 262144 {
		t.Fatalf("mistral-large-latest = %+v", large)
	}
	codestral := src.Models["mistral"]["codestral-latest"]
	if codestral == nil || codestral.Cost.Input != 0.3 || codestral.Cost.Output != 0.9 || codestral.MaxTokens != 4096 {
		t.Fatalf("codestral-latest = %+v", codestral)
	}
	magistral := src.Models["mistral"]["magistral-medium-latest"]
	if magistral == nil || !magistral.Reasoning {
		t.Fatalf("magistral-medium-latest = %+v", magistral)
	}
}
