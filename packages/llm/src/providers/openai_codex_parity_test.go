package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	src "ycode/packages/llm/src"
)

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }
func f64Ptr(v float64) *float64 { return &v }

// codexTestJWT fabricates a ChatGPT-shaped OAuth token whose payload carries
// the account-id claim (openai-codex-responses.ts JWT_CLAIM_PATH).
func codexTestJWT(t *testing.T, accountID string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": accountID},
	})
	if err != nil {
		t.Fatalf("jwt payload: %v", err)
	}
	return "hdr." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func codexTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "gpt-5.1", Name: "GPT-5.1", Api: src.Api("openai-codex-responses"),
		Provider: src.Provider("openai-codex"), BaseURL: "https://chatgpt.com/backend-api",
		Reasoning: true, MaxTokens: 128000,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

// startCodexServer runs an httptest server that answers every request via
// handler and returns the base URL to use as model.BaseURL.
func startCodexServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// collectCodexStream drives StreamOpenAICodexResponses and returns the final
// message plus all event types.
func collectCodexStream(t *testing.T, m src.Model[src.Api], opts *OpenAICodexResponsesOptions) (*src.AssistantMessage, []string, error) {
	t.Helper()
	if opts == nil {
		opts = &OpenAICodexResponsesOptions{}
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	ch := StreamOpenAICodexResponses(m, src.ChatContext{}, opts)()
	var types []string
	var final *src.AssistantMessage
	var streamErr error
	for ev := range ch {
		types = append(types, ev.Type)
		if ev.Type == "error" {
			final = ev.Error
			if final != nil {
				streamErr = fmt.Errorf("%s", final.ErrorMessage)
			}
		}
		if ev.Type == "done" {
			final = ev.Message
		}
	}
	return final, types, streamErr
}

// --- URL resolution ---

func TestCodexURLResolution(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://example.com", "https://example.com/codex/responses"},
		{"https://example.com/", "https://example.com/codex/responses"},
		{"https://example.com/codex", "https://example.com/codex/responses"},
		{"https://example.com/codex/", "https://example.com/codex/responses"},
		{"https://example.com/codex/responses", "https://example.com/codex/responses"},
	}
	for _, c := range cases {
		if got := resolveCodexURL(c.in); got != c.want {
			t.Fatalf("resolveCodexURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- Request shape ---

func TestCodexRequestShape(t *testing.T) {
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n"

	var bodyMap map[string]interface{}
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Errorf("path = %q, want /codex/responses", r.URL.Path)
		}
		buf, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(buf, &bodyMap); err != nil {
			t.Errorf("body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	})

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	opts := &OpenAICodexResponsesOptions{
		APIKey:    codexTestJWT(t, "acc_123"),
		MaxTokens: intPtr(777),
		SessionID: "sess-1",
	}
	final, types, err := collectCodexStream(t, m, opts)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	// Body pins (openai-codex-responses.ts buildRequestBody).
	if bodyMap["model"] != "gpt-5.1" {
		t.Fatalf("model = %v", bodyMap["model"])
	}
	if bodyMap["store"] != false || bodyMap["stream"] != true {
		t.Fatalf("store/stream = %v/%v", bodyMap["store"], bodyMap["stream"])
	}
	if bodyMap["instructions"] != "You are a helpful assistant." {
		t.Fatalf("instructions = %v", bodyMap["instructions"])
	}
	text, _ := bodyMap["text"].(map[string]interface{})
	if text == nil || text["verbosity"] != "low" {
		t.Fatalf("text = %#v", bodyMap["text"])
	}
	include, _ := bodyMap["include"].([]interface{})
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", bodyMap["include"])
	}
	if bodyMap["prompt_cache_key"] != "sess-1" {
		t.Fatalf("prompt_cache_key = %v", bodyMap["prompt_cache_key"])
	}
	if bodyMap["tool_choice"] != "auto" || bodyMap["parallel_tool_calls"] != true {
		t.Fatalf("tool_choice/parallel = %v/%v", bodyMap["tool_choice"], bodyMap["parallel_tool_calls"])
	}
	if _, has := bodyMap["max_output_tokens"]; has {
		t.Fatal("max_output_tokens must not be sent (TS codex never sets it)")
	}
	if _, has := bodyMap["reasoning"]; has {
		t.Fatal("reasoning must not be sent without an explicit effort")
	}

	// Terminal event mapping + usage.
	if final == nil || final.StopReason != src.StopReasonStop {
		t.Fatalf("final = %+v", final)
	}
	if final.Usage.Output != 5 || final.Usage.Input != 10 || final.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", final.Usage)
	}
	if final.ResponseID != "resp_1" {
		t.Fatalf("responseId = %q", final.ResponseID)
	}
	// start is pushed only after a successful response, then done.
	if got, want := strings.Join(types, ","), "start,done"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

// Direct params pins that need no server: custom system prompt and the
// strict:null tools shape (TS convertResponsesTools(tools, { strict: null })).
func TestCodexParamsDirect(t *testing.T) {
	m := codexTestModel(nil)
	tools := []src.Tool[any]{{Name: "read", Description: "d", Parameters: map[string]interface{}{"type": "object"}}}
	opts := &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a"), SessionID: "s"}
	params := buildOpenAICodexResponsesParams(m, src.ChatContext{SystemPrompt: "be brief", Tools: tools}, opts).(map[string]interface{})
	if params["instructions"] != "be brief" {
		t.Fatalf("instructions = %v", params["instructions"])
	}
	converted, _ := params["tools"].([]map[string]interface{})
	if len(converted) != 1 {
		t.Fatalf("tools = %#v", params["tools"])
	}
	if v, ok := converted[0]["strict"]; !ok || v != nil {
		t.Fatalf("strict = %#v, want explicit null", converted[0]["strict"])
	}

	// Reasoning only when an effort is requested; identity without a map.
	withEffort := buildOpenAICodexResponsesParams(m, src.ChatContext{}, &OpenAICodexResponsesOptions{
		APIKey: codexTestJWT(t, "a"), ReasoningEffort: strPtr("high"),
	}).(map[string]interface{})
	reasoning, _ := withEffort["reasoning"].(map[string]interface{})
	if reasoning == nil || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", withEffort["reasoning"])
	}

	// thinkingLevelMap remaps the requested effort.
	m2 := codexTestModel(func(m *src.Model[src.Api]) {
		m.ThinkingLevelMap = map[string]*src.ThinkingLevel{
			"minimal": src.ThinkingLevelPtr(src.Low),
			"off":     nil, // cannot disable thinking
		}
	})
	none := buildOpenAICodexResponsesParams(m2, src.ChatContext{}, &OpenAICodexResponsesOptions{
		APIKey: codexTestJWT(t, "a"), ReasoningEffort: strPtr("none"),
	}).(map[string]interface{})
	if _, has := none["reasoning"]; has {
		t.Fatalf("off:null must omit reasoning, got %#v", none["reasoning"])
	}
}

// --- Headers & auth ---

func TestCodexHeaders(t *testing.T) {
	var got http.Header
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	jwt := codexTestJWT(t, "acc_123")
	_, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: jwt, SessionID: "sess-9"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if h := got.Get("Authorization"); h != "Bearer "+jwt {
		t.Fatalf("Authorization = %q", h)
	}
	if h := got.Get("chatgpt-account-id"); h != "acc_123" {
		t.Fatalf("chatgpt-account-id = %q", h)
	}
	if h := got.Get("originator"); h != "pi" {
		t.Fatalf("originator = %q", h)
	}
	if ua := got.Get("User-Agent"); !strings.HasPrefix(ua, "pi (") {
		t.Fatalf("User-Agent = %q, want pi (...) prefix", ua)
	}
	if h := got.Get("OpenAI-Beta"); h != "responses=experimental" {
		t.Fatalf("OpenAI-Beta = %q", h)
	}
	if h := got.Get("Accept"); h != "text/event-stream" {
		t.Fatalf("accept = %q", h)
	}
	if h := got.Get("session_id"); h != "sess-9" {
		t.Fatalf("session_id = %q", h)
	}
	if h := got.Get("X-Client-Request-Id"); h != "sess-9" {
		t.Fatalf("x-client-request-id = %q", h)
	}
}

func TestCodexHeadersNoSession(t *testing.T) {
	var got http.Header
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	if _, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a")}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.Get("session_id") != "" || got.Get("X-Client-Request-Id") != "" {
		t.Fatalf("session headers set without SessionID: %v", got)
	}
}

func TestCodexBadToken(t *testing.T) {
	m := codexTestModel(nil)
	_, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: "not-a-jwt"})
	if err == nil || !strings.Contains(err.Error(), "Failed to extract accountId from token") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexNoAPIKey(t *testing.T) {
	m := codexTestModel(nil)
	_, _, err := collectCodexStream(t, m, nil)
	if err == nil || !strings.Contains(err.Error(), "No API key for provider: openai-codex") {
		t.Fatalf("err = %v", err)
	}
}

// --- Retry & errors ---

func TestCodexRetryThenSuccess(t *testing.T) {
	oldDelay := codexBaseDelayMs
	codexBaseDelayMs = 1
	t.Cleanup(func() { codexBaseDelayMs = oldDelay })

	var hits int32
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	final, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a")})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if final == nil || final.StopReason != src.StopReasonStop {
		t.Fatalf("final = %+v", final)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("hits = %d, want 3", atomic.LoadInt32(&hits))
	}
}

func TestCodexStatus401(t *testing.T) {
	var hits int32
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid token"}}`))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	_, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a")})
	if err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("err = %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("401 must not retry, hits = %d", atomic.LoadInt32(&hits))
	}
}

func TestCodexUsageLimitFriendlyMessage(t *testing.T) {
	oldDelay := codexBaseDelayMs
	codexBaseDelayMs = 1
	t.Cleanup(func() { codexBaseDelayMs = oldDelay })

	resetsAt := time.Now().Unix() + 1800 // seconds
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"code":"usage_limit_reached","plan_type":"Pro","resets_at":%d}}`, resetsAt)))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	_, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a")})
	if err == nil || !strings.Contains(err.Error(), "You have hit your ChatGPT usage limit (pro plan). Try again in ~30 min.") {
		t.Fatalf("err = %v", err)
	}
}

// --- SSE layer ---

func runCodexSSE(t *testing.T, sse string, opts *OpenAICodexResponsesOptions) (*src.AssistantMessage, error) {
	t.Helper()
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sse))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	if opts == nil {
		opts = &OpenAICodexResponsesOptions{}
	}
	opts.APIKey = codexTestJWT(t, "a")
	_, _, err := collectCodexStream(t, m, opts)
	return nil, err
}

func TestCodexSSEErrorEvent(t *testing.T) {
	if _, err := runCodexSSE(t, "data: {\"type\":\"error\",\"code\":\"x\",\"message\":\"boom\"}\n\n", nil); err == nil || !strings.Contains(err.Error(), "Codex error: boom") {
		t.Fatalf("err = %v", err)
	}
	// Message fallback: code alone.
	if _, err := runCodexSSE(t, "data: {\"type\":\"error\",\"code\":\"quota\"}\n\n", nil); err == nil || !strings.Contains(err.Error(), "Codex error: quota") {
		t.Fatalf("code-only err = %v", err)
	}
}

func TestCodexSSEResponseFailed(t *testing.T) {
	if _, err := runCodexSSE(t, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"nope\"}}}\n\n", nil); err == nil || err.Error() != "nope" {
		t.Fatalf("err = %v", err)
	}
	if _, err := runCodexSSE(t, "data: {\"type\":\"response.failed\"}\n\n", nil); err == nil || err.Error() != "Codex response failed" {
		t.Fatalf("fallback err = %v", err)
	}
}

func TestCodexSSEMalformedJSON(t *testing.T) {
	if _, err := runCodexSSE(t, "data: {not json}\n\n", nil); err == nil || !strings.Contains(err.Error(), "Invalid Codex SSE JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexSSEIncompleteIsLength(t *testing.T) {
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n"))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	final, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a")})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if final == nil || final.StopReason != src.StopReasonLength {
		t.Fatalf("final = %+v, want length", final)
	}
}

func TestCodexSSEUnknownStatusDropped(t *testing.T) {
	// normalizeCodexStatus: an unknown status is dropped; the shared stop
	// map then defaults to stop.
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"status\":\"brand_new\",\"usage\":{}}}\n\n"))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	final, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a")})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if final == nil || final.StopReason != src.StopReasonStop {
		t.Fatalf("final = %+v", final)
	}
}

// resolveCodexServiceTier: a "default" response tier must not override a
// requested flex/priority tier before pricing.
func TestCodexServiceTierRewrite(t *testing.T) {
	if got := resolveCodexServiceTier("default", "flex"); got != "flex" {
		t.Fatalf("default+flex = %q", got)
	}
	if got := resolveCodexServiceTier("priority", "flex"); got != "priority" {
		t.Fatalf("priority+flex = %q", got)
	}
	if got := resolveCodexServiceTier("", "flex"); got != "flex" {
		t.Fatalf("empty+flex = %q", got)
	}

	// End-to-end: flex request, "default" response → cost halved.
	srv := startCodexServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"service_tier\":\"default\",\"usage\":{\"input_tokens\":1000000,\"output_tokens\":0,\"total_tokens\":1000000}}}\n\n"))
	})
	m := codexTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Cost = src.ModelCost{Input: 2}
	})
	final, _, err := collectCodexStream(t, m, &OpenAICodexResponsesOptions{APIKey: codexTestJWT(t, "a"), ServiceTier: strPtr("flex")})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if final == nil || final.Usage.Cost.Total != 1 {
		t.Fatalf("cost = %+v, want 1 (flex ×0.5 of 2)", final.Usage.Cost)
	}
}

// --- Simple wrapper ---

func TestCodexSimpleWrapper(t *testing.T) {
	// xhigh on a model without xhigh support clamps down to high.
	m := codexTestModel(func(m *src.Model[src.Api]) { m.Reasoning = true })
	simple := &src.SimpleStreamOptions{}
	simple.Reasoning = src.XHigh
	opts := buildOpenAICodexResponsesSimpleOptions(m, simple)
	if opts.ReasoningEffort == nil || *opts.ReasoningEffort != "high" {
		t.Fatalf("xhigh effort = %v, want high", opts.ReasoningEffort)
	}

	// off → no reasoning at all.
	simple2 := &src.SimpleStreamOptions{}
	simple2.Reasoning = src.Off
	opts2 := buildOpenAICodexResponsesSimpleOptions(m, simple2)
	if opts2.ReasoningEffort != nil {
		t.Fatalf("off effort = %v, want nil", opts2.ReasoningEffort)
	}

	// thinkingLevelMap remaps at the params level (the clamp itself keeps a
	// supported level unchanged).
	m3 := codexTestModel(func(m *src.Model[src.Api]) {
		m.Reasoning = true
		m.ThinkingLevelMap = map[string]*src.ThinkingLevel{"minimal": src.ThinkingLevelPtr(src.Low)}
	})
	simple3 := &src.SimpleStreamOptions{}
	simple3.Reasoning = src.Minimal
	opts3 := buildOpenAICodexResponsesSimpleOptions(m3, simple3)
	if opts3.ReasoningEffort == nil || *opts3.ReasoningEffort != "minimal" {
		t.Fatalf("clamped minimal = %v, want minimal", opts3.ReasoningEffort)
	}
	bodyParams := buildOpenAICodexResponsesParams(m3, src.ChatContext{}, opts3).(map[string]interface{})
	reasoning3, _ := bodyParams["reasoning"].(map[string]interface{})
	if reasoning3 == nil || reasoning3["effort"] != "low" {
		t.Fatalf("body effort = %#v, want low (map remap)", bodyParams["reasoning"])
	}

	// Non-reasoning model clamps to off.
	m4 := codexTestModel(func(m *src.Model[src.Api]) { m.Reasoning = false })
	simple4 := &src.SimpleStreamOptions{}
	simple4.Reasoning = src.High
	if opts4 := buildOpenAICodexResponsesSimpleOptions(m4, simple4); opts4.ReasoningEffort != nil {
		t.Fatalf("non-reasoning effort = %v, want nil", opts4.ReasoningEffort)
	}
}

// --- Registry & catalog wiring ---

func TestCodexRegistered(t *testing.T) {
	if p := src.GetApiProvider("openai-codex-responses"); p == nil {
		t.Fatal("openai-codex-responses not registered")
	}
	if p := src.GetApiProvider("azure-openai-responses"); p == nil {
		t.Fatal("azure-openai-responses not registered")
	}

	codex := src.Models["openai-codex"]["gpt-5.1"]
	if codex == nil || codex.BaseURL != "https://chatgpt.com/backend-api" || !codex.Reasoning {
		t.Fatalf("catalog gpt-5.1 = %+v", codex)
	}
	// gpt-5.2 carries thinkingLevelMap {"xhigh":"xhigh","minimal":"low"} in TS.
	codex52 := src.Models["openai-codex"]["gpt-5.2"]
	if codex52 == nil {
		t.Fatal("catalog gpt-5.2 missing")
	}
	if mapped, has := codex52.ThinkingLevelMap["xhigh"]; !has || mapped == nil || *mapped != src.XHigh {
		t.Fatalf("gpt-5.2 thinkingLevelMap[xhigh] = %v/%v", mapped, has)
	}
	if mapped, has := codex52.ThinkingLevelMap["minimal"]; !has || mapped == nil || *mapped != src.Low {
		t.Fatalf("gpt-5.2 thinkingLevelMap[minimal] = %v/%v", mapped, has)
	}
	azure := src.Models["azure-openai-responses"]["gpt-4o-mini"]
	if azure == nil || azure.Provider != src.Provider("azure-openai-responses") || azure.Reasoning {
		t.Fatalf("catalog azure gpt-4o-mini = %+v", azure)
	}
	if len(src.Models["azure-openai-responses"]) < 40 {
		t.Fatalf("azure catalog group = %d entries, want 42 from TS", len(src.Models["azure-openai-responses"]))
	}
	if len(src.Models["openai-codex"]) != 10 {
		t.Fatalf("codex catalog group = %d entries, want 10 from TS", len(src.Models["openai-codex"]))
	}
}
