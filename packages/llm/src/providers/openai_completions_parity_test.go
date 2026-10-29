package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func parityModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "m-1", Name: "M1", Api: src.Api("openai-completions"),
		Provider: src.Provider("acme"), BaseURL: "https://example.invalid/v1",
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

// The helper must parse the ACCUMULATED string progressively: in-flight
// arguments grow instead of staying empty (reference parseStreamingJson).
func TestParseStreamingJsonProgressiveToolArgs(t *testing.T) {
	full := `{"command":"echo hi","timeout":30}`
	openQuote := len(`{"command":"`)
	text := "echo hi"
	var last map[string]interface{}
	for i := 2; i <= len(full); i++ {
		args := parseStreamingJson(full[:i])
		if i > openQuote {
			want := text
			if i <= openQuote+len(text) {
				want = full[openQuote:i] // value still streaming: exact prefix
			}
			cmd, _ := args["command"].(string)
			if cmd != want {
				t.Fatalf("cut %d: command = %#v, want %q", i, cmd, want)
			}
		}
		last = args
	}
	if last["command"] != "echo hi" || last["timeout"] != float64(30) {
		t.Fatalf("final args = %v", last)
	}
}

func TestParseOpenAICompletionsUsageCacheHitFallback(t *testing.T) {
	m := parityModel(nil)

	// DeepSeek-style: cache hits reported as prompt_cache_hit_tokens.
	u := parseOpenAICompletionsUsage(map[string]interface{}{
		"prompt_tokens": float64(100), "completion_tokens": float64(5),
		"prompt_cache_hit_tokens": float64(60),
	}, m)
	if u.CacheRead != 60 || u.Input != 40 || u.Output != 5 {
		t.Fatalf("cache-hit fallback: got %+v", u)
	}

	// A present-but-zero cached_tokens wins over the fallback (?? semantics).
	u = parseOpenAICompletionsUsage(map[string]interface{}{
		"prompt_tokens": float64(100), "completion_tokens": float64(5),
		"prompt_tokens_details":   map[string]interface{}{"cached_tokens": float64(0)},
		"prompt_cache_hit_tokens": float64(60),
	}, m)
	if u.CacheRead != 0 || u.Input != 100 {
		t.Fatalf("present zero cached_tokens must win: got %+v", u)
	}

	// details present without cached_tokens still falls back to the top-level
	// cache-hit count; cache reads subtract this request's cache writes
	// (reference normalization).
	u = parseOpenAICompletionsUsage(map[string]interface{}{
		"prompt_tokens": float64(100), "completion_tokens": float64(5),
		"prompt_tokens_details":   map[string]interface{}{"cache_write_tokens": float64(5)},
		"prompt_cache_hit_tokens": float64(60),
	}, m)
	if u.CacheRead != 55 || u.CacheWrite != 5 || u.Input != 40 {
		t.Fatalf("details-without-cached_tokens fallback: got %+v", u)
	}
}

func TestProcessOpenAICompletionsStreamChoiceUsageAndServedModel(t *testing.T) {
	m := parityModel(nil)
	sse := "data: {\"id\":\"c1\",\"model\":\"served-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\",\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":7,\"prompt_cache_hit_tokens\":60}}]}\n" +
		"data: [DONE]\n"
	ch := make(chan src.AssistantMessageEvent, 256)
	output := &src.AssistantMessage{Content: []src.ContentBlock{}, StopReason: src.StopReasonStop}
	if err := processOpenAICompletionsStream(io.NopCloser(strings.NewReader(sse)), ch, output, m, resolveOpenAICompat(m)); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if output.ResponseModel != "served-x" {
		t.Fatalf("responseModel = %q, want served-x", output.ResponseModel)
	}
	if output.Usage.CacheRead != 60 || output.Usage.Input != 40 || output.Usage.Output != 7 {
		t.Fatalf("choice.usage fallback: got %+v", output.Usage)
	}
}

func TestConvertMessagesImageOnlyToolResultPlaceholder(t *testing.T) {
	m := parityModel(func(m *src.Model[src.Api]) { m.Input = []src.InputType{src.InputImage} })
	ctx := src.ChatContext{Messages: []src.Message{
		&src.ToolResultMessage[any]{
			Role: src.RoleTool, ToolCallID: "t1", ToolName: "shot",
			Content: []src.ContentBlock{&src.ImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"}},
		},
	}}
	params := convertOpenAICompletionsMessages(m, ctx, resolveOpenAICompat(m))
	if len(params) == 0 {
		t.Fatal("no params produced")
	}
	if params[0]["role"] != "tool" {
		t.Fatalf("role = %v, want tool", params[0]["role"])
	}
	if params[0]["content"] != "(see attached image)" {
		t.Fatalf("content = %#v, want placeholder", params[0]["content"])
	}
}

// lastRole must advance for user/assistant messages (reference sets it at the
// loop bottom), or the requiresAssistantAfterToolResult bridge fires in the
// wrong places.
func TestConvertMessagesAssistantBridgeRegression(t *testing.T) {
	m := parityModel(func(m *src.Model[src.Api]) {
		m.Compat.OpenAICompletions = &src.OpenAICompletionsCompat{RequireAssistantAfterToolResult: compatBool(true)}
	})
	user := func(txt string) src.Message {
		return &src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{&src.TextContent{Type: "text", Text: txt}}}
	}
	toolResult := &src.ToolResultMessage[any]{
		Role: src.RoleTool, ToolCallID: "t1", ToolName: "bash",
		Content: []src.ContentBlock{&src.TextContent{Type: "text", Text: "out"}},
	}
	assistant := &src.AssistantMessage{
		Role:    src.RoleAssistant,
		Content: []src.ContentBlock{&src.TextContent{Type: "text", Text: "done"}},
	}
	compat := resolveOpenAICompat(m)

	roles := func(params []map[string]interface{}) []string {
		out := make([]string, 0, len(params))
		for _, p := range params {
			r, _ := p["role"].(string)
			out = append(out, r)
		}
		return out
	}

	// toolResult, assistant, user: the assistant between them means the user
	// must NOT get a bridge (lastRole advanced past toolResult).
	params := convertOpenAICompletionsMessages(m, src.ChatContext{Messages: []src.Message{toolResult, assistant, user("go on")}}, compat)
	if got, want := roles(params), []string{"tool", "assistant", "user"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", got, want)
	}

	// toolResult, user, user: exactly ONE bridge, before the first user.
	params = convertOpenAICompletionsMessages(m, src.ChatContext{Messages: []src.Message{toolResult, user("a"), user("b")}}, compat)
	got := roles(params)
	if want := "tool,assistant,user,user"; strings.Join(got, ",") != want {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	bridge := params[1]["content"]
	if bridge != "I have processed the tool results." {
		t.Fatalf("bridge content = %#v", bridge)
	}
}

func TestDetectOpenAICompatProviderMatrix(t *testing.T) {
	// together
	c := detectOpenAICompat(parityModel(func(m *src.Model[src.Api]) { m.Provider = src.Provider("together") }))
	if c.ThinkingFormat != "together" || c.SupportsStore || c.SupportsDeveloperRole ||
		c.MaxTokensField != "max_tokens" || c.SupportsStrictMode || c.SupportsReasoningEffort || c.SupportsLongCacheRetention {
		t.Fatalf("together: %+v", c)
	}

	// moonshot via URL
	c = detectOpenAICompat(parityModel(func(m *src.Model[src.Api]) { m.BaseURL = "https://api.moonshot.cn/v1" }))
	if c.MaxTokensField != "max_tokens" || c.SupportsStrictMode || c.SupportsReasoningEffort || !c.SupportsLongCacheRetention {
		t.Fatalf("moonshot: %+v", c)
	}

	// cloudflare ai-gateway via URL
	c = detectOpenAICompat(parityModel(func(m *src.Model[src.Api]) { m.BaseURL = "https://gateway.ai.cloudflare.com/v1/acc/x" }))
	if c.MaxTokensField != "max_tokens" || c.SupportsStrictMode || c.SupportsReasoningEffort || c.SupportsLongCacheRetention {
		t.Fatalf("cf-gateway: %+v", c)
	}

	// anthropic-over-openrouter cache control is PROVIDER-keyed in the
	// reference, not URL-keyed.
	c = detectOpenAICompat(parityModel(func(m *src.Model[src.Api]) {
		m.Provider = src.Provider("openrouter")
		m.ID = "anthropic/claude-x"
	}))
	if c.CacheControlFormat != "anthropic" || c.ThinkingFormat != "openrouter" {
		t.Fatalf("openrouter provider: %+v", c)
	}
	c = detectOpenAICompat(parityModel(func(m *src.Model[src.Api]) {
		m.BaseURL = "https://openrouter.ai/api/v1"
		m.ID = "anthropic/claude-x"
	}))
	if c.CacheControlFormat != "" || c.ThinkingFormat != "openrouter" {
		t.Fatalf("openrouter url-only: %+v", c)
	}
}

func TestMissingAPIKeyFailsFast(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("NO-SUCH-PROVIDER-ZZZ_API_KEY", "")
	m := parityModel(func(m *src.Model[src.Api]) { m.Provider = src.Provider("no-such-provider-zzz") })
	_, err := doOpenAICompletionsRequest(&http.Client{}, m, src.ChatContext{Messages: []src.Message{}},
		map[string]interface{}{"stream": true}, nil, resolveOpenAICompat(m))
	if err == nil || !strings.Contains(err.Error(), "OpenAI API key is required") {
		t.Fatalf("err = %v, want missing-key error", err)
	}
}

func TestPICacheRetentionEnvLong(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	m := parityModel(func(m *src.Model[src.Api]) { m.BaseURL = "https://api.openai.com/v1" })
	params := compatTestParams(t, m, &OpenAICompletionsOptions{SessionID: "sess-1"})
	if params["prompt_cache_retention"] != "24h" {
		t.Fatalf("prompt_cache_retention = %v, want 24h", params["prompt_cache_retention"])
	}
	if params["prompt_cache_key"] != "sess-1" {
		t.Fatalf("prompt_cache_key = %v, want sess-1", params["prompt_cache_key"])
	}
}
