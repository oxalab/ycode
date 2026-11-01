package providers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func responsesTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "gpt-test", Name: "GPT Test", Api: src.Api("openai-responses"),
		Provider: src.Provider("openai"), BaseURL: "https://example.invalid/v1/responses",
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

func runResponsesSSE(t *testing.T, m src.Model[src.Api], sse string) (*src.AssistantMessage, []src.AssistantMessageEvent, error) {
	t.Helper()
	ch := make(chan src.AssistantMessageEvent, 256)
	output := &src.AssistantMessage{Content: []src.ContentBlock{}, StopReason: src.StopReasonStop}
	err := processOpenAIResponsesStream(io.NopCloser(strings.NewReader(sse)), ch, output, m, nil)
	close(ch)
	var evts []src.AssistantMessageEvent
	for e := range ch {
		evts = append(evts, e)
	}
	return output, evts, err
}

// The SSE framing (event:/data: lines) must parse; the reference's SDK does
// this and the old raw json.Decoder could not.
func TestProcessOpenAIResponsesStreamSSEHappyPath(t *testing.T) {
	m := responsesTestModel(nil)
	sse := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"id\":\"msg_a\"}}\n\n" +
		"data: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"msg_a\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":5,\"total_tokens\":105,\"input_tokens_details\":{\"cached_tokens\":40}}}}\n\n"

	output, evts, err := runResponsesSSE(t, m, sse)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if output.ResponseID != "resp_1" {
		t.Fatalf("responseId = %q", output.ResponseID)
	}
	// Authoritative final content from output_item.done.
	if tc, ok := output.Content[0].(*src.TextContent); !ok || tc.Text != "Hello" {
		t.Fatalf("text = %#v, want Hello", output.Content[0])
	}
	// Usage: input excludes cached, total from total_tokens.
	if output.Usage.Input != 60 || output.Usage.CacheRead != 40 || output.Usage.Output != 5 || output.Usage.TotalTokens != 105 {
		t.Fatalf("usage = %+v", output.Usage)
	}
	if output.StopReason != src.StopReasonStop {
		t.Fatalf("stopReason = %v", output.StopReason)
	}
	// Event sequence: start, text_start, text_delta×2, text_end.
	var kinds []string
	for _, e := range evts {
		kinds = append(kinds, e.Type)
	}
	// ("start" is emitted by StreamOpenAIResponses, not the processor.)
	if got, want := strings.Join(kinds, ","), "text_start,text_delta,text_delta,text_end"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

func TestProcessOpenAIResponsesStreamReasoningSummary(t *testing.T) {
	m := responsesTestModel(nil)
	sse := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_part.added\",\"part\":{\"type\":\"summary_text\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thin\"}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"king\"}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_part.done\",\"part\":{\"type\":\"summary_text\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"thinking\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	output, evts, err := runResponsesSSE(t, m, sse)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if tc, ok := output.Content[0].(*src.ThinkingContent); !ok {
		t.Fatalf("block 0 = %T, want ThinkingContent", output.Content[0])
	} else if tc.Thinking != "thinking" {
		// The authoritative item summary overwrites the live accumulation
		// (reference: summaryText || contentText || currentBlock.thinking).
		t.Fatalf("thinking = %q, want thinking", tc.Thinking)
	}
	var deltas []string
	for _, e := range evts {
		if e.Type == "thinking_delta" {
			deltas = append(deltas, e.Delta)
		}
	}
	if strings.Join(deltas, "|") != "thin|king|\n\n" {
		t.Fatalf("deltas = %q", strings.Join(deltas, "|"))
	}
}

// The part.added gate: summary text before any part must NOT emit deltas.
func TestProcessOpenAIResponsesStreamSummaryGate(t *testing.T) {
	m := responsesTestModel(nil)
	sse := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"orphan\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n"
	output, evts, err := runResponsesSSE(t, m, sse)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for _, e := range evts {
		if e.Type == "thinking_delta" {
			t.Fatal("summary delta emitted without a part.added")
		}
	}
	if tc, _ := output.Content[0].(*src.ThinkingContent); tc == nil || tc.Thinking != "" {
		t.Fatalf("thinking = %#v, want empty", output.Content[0])
	}
}

func TestProcessOpenAIResponsesStreamToolCallArgs(t *testing.T) {
	m := responsesTestModel(nil)
	sse := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"id\":\"fc_1\",\"name\":\"bash\",\"arguments\":\"\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"command\\\":\\\"ls\"}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\" -la\\\"}\"}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.done\",\"arguments\":\"{\\\"command\\\":\\\"ls -la\\\"}\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"id\":\"fc_1\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"ls -la\\\"}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	output, evts, err := runResponsesSSE(t, m, sse)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	// toolUse stop override when tool calls present.
	if output.StopReason != src.StopReasonToolUse {
		t.Fatalf("stopReason = %v, want toolUse", output.StopReason)
	}
	tc, ok := output.Content[0].(*src.ToolCall)
	if !ok {
		t.Fatalf("block 0 = %T", output.Content[0])
	}
	if tc.ID != "call_1|fc_1" || tc.Name != "bash" {
		t.Fatalf("toolCall = %+v", tc)
	}
	if tc.Arguments["command"] != "ls -la" {
		t.Fatalf("arguments = %#v", tc.Arguments)
	}
	// Progressive args arrive live (partial parse), not only at the end.
	var partial string
	for _, e := range evts {
		if e.Type == "toolcall_delta" {
			partial += e.Delta
		}
	}
	if partial != "{\"command\":\"ls" + " -la\"}" {
		t.Fatalf("deltas concatenated = %q", partial)
	}
}

// Refusal deltas only flow when the last added part is a refusal, and the
// authoritative final content includes the refusal text.
func TestProcessOpenAIResponsesStreamRefusal(t *testing.T) {
	m := responsesTestModel(nil)
	sse := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"id\":\"msg_a\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"orphan\"}\n\n" +
		"data: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"refusal\"}}\n\n" +
		"data: {\"type\":\"response.refusal.delta\",\"delta\":\"no can do\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"msg_a\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"no can do\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n"

	output, evts, err := runResponsesSSE(t, m, sse)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	// The orphan output_text delta (before any part) must be dropped.
	for _, e := range evts {
		if e.Type == "text_delta" && e.Delta == "orphan" {
			t.Fatal("output_text delta emitted before a part.added")
		}
	}
	if tc, _ := output.Content[0].(*src.TextContent); tc == nil || tc.Text != "no can do" {
		t.Fatalf("text = %#v, want refusal text", output.Content[0])
	}
}

func TestProcessOpenAIResponsesStreamServiceTierPricing(t *testing.T) {
	m := responsesTestModel(func(m *src.Model[src.Api]) {
		m.Cost = src.ModelCost{Input: 1, Output: 1}
	})
	sse := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"service_tier\":\"flex\",\"usage\":{\"input_tokens\":1000000,\"output_tokens\":1000000,\"total_tokens\":2000000}}}\n\n"
	output, _, _ := runResponsesSSE(t, m, sse)
	// input cost = 1/M tokens * 1M = $1 → flex ×0.5 = 0.5; total recomputed.
	if output.Usage.Cost.Input != 0.5 || output.Usage.Cost.Output != 0.5 || output.Usage.Cost.Total != 1 {
		t.Fatalf("cost = %+v", output.Usage.Cost)
	}
}

func TestProcessOpenAIResponsesStreamErrorEvents(t *testing.T) {
	m := responsesTestModel(nil)

	if _, _, err := runResponsesSSE(t, m, "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"boom\"}\n\n"); err == nil || err.Error() != "Error Code server_error: boom" {
		t.Fatalf("err = %v, want Error Code wording", err)
	}

	if _, _, err := runResponsesSSE(t, m, "data: {\"type\":\"response.failed\",\"response\":{}}\n\n"); err == nil || err.Error() != "Unknown error (no error details in response)" {
		t.Fatalf("err = %v, want fallback wording", err)
	}
}

// Non-2xx responses must fail with status + body, not parse as an empty success.
func TestDoOpenAIResponsesRequestStatusCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "k")
	m := responsesTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	_, err := doOpenAIResponsesRequest(&http.Client{}, m, src.ChatContext{}, map[string]interface{}{"stream": true}, nil)
	if err == nil || !strings.Contains(err.Error(), "Responses API error (401)") {
		t.Fatalf("err = %v, want status error", err)
	}
}

func TestDoOpenAIResponsesRequestMissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	m := responsesTestModel(func(m *src.Model[src.Api]) { m.Provider = src.Provider("no-such-provider-zzz") })
	_, err := doOpenAIResponsesRequest(&http.Client{}, m, src.ChatContext{}, map[string]interface{}{"stream": true}, nil)
	if err == nil || !strings.Contains(err.Error(), "OpenAI API key is required") {
		t.Fatalf("err = %v, want missing-key error", err)
	}
}

// The msg_N fallback must be a decimal counter, not a rune (control char at
// index >= 10 in the old string(rune(msgIndex))).
func TestConvertResponsesMessagesMsgIdFallback(t *testing.T) {
	m := responsesTestModel(nil)
	var messages []src.Message
	for i := 0; i <= 12; i++ {
		messages = append(messages, &src.AssistantMessage{
			Role:    src.RoleAssistant,
			Model:   m.ID, Provider: m.Provider, Api: m.Api,
			Content: []src.ContentBlock{&src.TextContent{Type: "text", Text: "hi", TextSignature: ""}},
		})
	}
	converted := convertResponsesMessages(m, src.ChatContext{Messages: messages}, map[string]bool{"openai": true}, nil)
	// 13 assistant messages → 13 output items.
	if len(converted) != 13 {
		t.Fatalf("len = %d, want 13", len(converted))
	}
	last, ok := converted[12].(map[string]interface{})
	if !ok {
		t.Fatalf("item 12 = %T", converted[12])
	}
	if last["id"] != "msg_12" {
		t.Fatalf("id = %#v, want msg_12", last["id"])
	}
}
