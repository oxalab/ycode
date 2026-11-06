package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func googleTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", Api: src.Api("google-generative-ai"),
		Provider: src.Provider("google"), BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		Reasoning: true, MaxTokens: 65536,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

// collectGoogleStream drives StreamGoogle against the model's BaseURL.
func collectGoogleStream(t *testing.T, m src.Model[src.Api], opts *GoogleOptions) (*src.AssistantMessage, []string, string) {
	t.Helper()
	if opts == nil {
		opts = &GoogleOptions{}
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	var types []string
	var final *src.AssistantMessage
	errMsg := ""
	for ev := range StreamGoogle(m, src.ChatContext{}, opts)() {
		types = append(types, ev.Type)
		if ev.Type == "error" && ev.Error != nil {
			final = ev.Error
			errMsg = ev.Error.ErrorMessage
		}
		if ev.Type == "done" {
			final = ev.Message
		}
	}
	return final, types, errMsg
}

// --- URL resolution ---

func TestGoogleURLResolution(t *testing.T) {
	if got := resolveGoogleBaseURL(""); got != "https://generativelanguage.googleapis.com/v1beta" {
		t.Fatalf("default base = %q", got)
	}
	if got := resolveGoogleBaseURL("https://x/v1beta/"); got != "https://x/v1beta" {
		t.Fatalf("trailing slash = %q", got)
	}
	if got := googleModelPath("gemini-2.5-pro"); got != "models/gemini-2.5-pro" {
		t.Fatalf("bare id = %q", got)
	}
	if got := googleModelPath("models/gemini-2.5-pro"); got != "models/gemini-2.5-pro" {
		t.Fatalf("prefixed id = %q", got)
	}
	if got := googleModelPath("tunedModels/my-tune"); got != "tunedModels/my-tune" {
		t.Fatalf("tuned id = %q", got)
	}
}

// --- Request shape ---

func TestGoogleRequestShape(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	var requestPath, gotKey string
	var wireBody map[string]interface{}
	sse := "data: {\"responseId\":\"r1\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hi\"}]}}],\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":5,\"totalTokenCount\":105}}\n\n" +
		"data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path + "?" + r.URL.RawQuery
		gotKey = r.Header.Get("x-goog-api-key")
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &wireBody)
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	m := googleTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	opts := &GoogleOptions{
		APIKey:      "g-key",
		Temperature: f64Ptr(0.7),
		MaxTokens:   intPtr(2048),
		ToolChoice:  strPtr("auto"),
	}
	final, types, errMsg := collectGoogleStream(t, m, opts)
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}

	if requestPath != "/models/gemini-2.5-flash:streamGenerateContent?alt=sse" {
		t.Fatalf("path = %q", requestPath)
	}
	if gotKey != "g-key" {
		t.Fatalf("x-goog-api-key = %q", gotKey)
	}

	// Wire body: no model key, generationConfig nesting, no toolConfig
	// without tools.
	if _, has := wireBody["model"]; has {
		t.Fatal("model key must be stripped from the wire body")
	}
	gc, _ := wireBody["generationConfig"].(map[string]interface{})
	if gc == nil || gc["temperature"] != 0.7 || gc["maxOutputTokens"] != float64(2048) {
		t.Fatalf("generationConfig = %#v", wireBody["generationConfig"])
	}
	if _, has := wireBody["toolConfig"]; has {
		t.Fatal("toolConfig must not be sent without tools")
	}

	// Stream results.
	if final == nil || final.StopReason != src.StopReasonStop {
		t.Fatalf("final = %+v", final)
	}
	if final.ResponseID != "r1" {
		t.Fatalf("responseId = %q", final.ResponseID)
	}
	if final.Usage.Input != 100 || final.Usage.Output != 5 || final.Usage.TotalTokens != 105 {
		t.Fatalf("usage = %+v", final.Usage)
	}
	if got, want := strings.Join(types, ","), "start,text_start,text_delta,text_end,done"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestGoogleStatus400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer srv.Close()
	m := googleTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	_, _, errMsg := collectGoogleStream(t, m, &GoogleOptions{APIKey: "bad"})
	if !strings.Contains(errMsg, "Google API error (400)") || !strings.Contains(errMsg, "API key not valid") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

// Usage-only chunks (no candidates) must still update usage and cost.
func TestGoogleUsageWithoutCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			"data: {\"responseId\":\"r2\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\n" +
				"data: {\"usageMetadata\":{\"promptTokenCount\":1000000,\"candidatesTokenCount\":0,\"totalTokenCount\":1000000,\"cachedContentTokenCount\":10}}\n\n" +
				"data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer srv.Close()

	m := googleTestModel(func(m *src.Model[src.Api]) {
		m.BaseURL = srv.URL
		m.Cost = src.ModelCost{Input: 1, Output: 1, CacheRead: 1}
	})
	final, _, errMsg := collectGoogleStream(t, m, &GoogleOptions{APIKey: "k"})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if final.Usage.Input != 999990 || final.Usage.CacheRead != 10 {
		t.Fatalf("usage = %+v", final.Usage)
	}
	wantTotal := 999990e-6 + 10e-6
	if diff := final.Usage.Cost.Total - wantTotal; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("cost = %+v, want ~%v (CalculateCost wired)", final.Usage.Cost, wantTotal)
	}
}

// Thinking + tool-call SSE flow.
func TestGoogleSSEThinkingAndToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"pondering\"},{\"text\":\"answer\"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"read\",\"args\":{\"path\":\"a\"}},\"thoughtSignature\":\"c2ln\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":2,\"totalTokenCount\":3}}\n\n"))
	}))
	defer srv.Close()

	m := googleTestModel(func(m *src.Model[src.Api]) { m.BaseURL = srv.URL })
	final, types, errMsg := collectGoogleStream(t, m, &GoogleOptions{APIKey: "k"})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if final == nil || final.StopReason != src.StopReasonToolUse {
		t.Fatalf("stopReason = %v, want toolUse", final.StopReason)
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "thinking_start,thinking_delta,thinking_end") {
		t.Fatalf("thinking events missing: %s", joined)
	}
	if !strings.Contains(joined, "toolcall_start,toolcall_delta,toolcall_end") {
		t.Fatalf("toolcall events missing: %s", joined)
	}
	tc, ok := final.Content[2].(*src.ToolCall)
	if !ok {
		t.Fatalf("content[2] = %T", final.Content[2])
	}
	if tc.Name != "read" || tc.Arguments["path"] != "a" || tc.ThoughtSignature != "c2ln" {
		t.Fatalf("toolCall = %+v", tc)
	}
	// ID carries the timestamp segment: name_<millis>_<counter>.
	parts := strings.Split(tc.ID, "_")
	if len(parts) != 3 || parts[0] != "read" || len(parts[1]) != 13 {
		t.Fatalf("toolCall ID = %q, want read_<millis>_<n>", tc.ID)
	}
}

// --- Params ---

func TestGoogleParamsDirect(t *testing.T) {
	m := googleTestModel(nil)

	// systemInstruction wrapped + sanitized; tools → parametersJsonSchema.
	tools := []src.Tool[any]{{Name: "read", Description: "d", Parameters: map[string]interface{}{"type": "object"}}}
	params := buildGoogleParams(m, src.ChatContext{SystemPrompt: "be <brief>", Tools: tools}, &GoogleOptions{
		ToolChoice: strPtr("auto"),
	}).(map[string]interface{})
	si, _ := params["systemInstruction"].(map[string]interface{})
	siParts, _ := si["parts"].([]map[string]interface{})
	if len(siParts) != 1 || siParts[0]["text"] == nil {
		t.Fatalf("systemInstruction = %#v", params["systemInstruction"])
	}
	toolList, _ := params["tools"].([]map[string]interface{})
	if len(toolList) != 1 {
		t.Fatalf("tools = %#v", params["tools"])
	}
	decls, _ := toolList[0]["functionDeclarations"].([]map[string]interface{})
	if len(decls) != 1 || decls[0]["parametersJsonSchema"] == nil {
		t.Fatalf("functionDeclarations = %#v", toolList[0])
	}
	tc, _ := params["toolConfig"].(map[string]interface{})
	if tc == nil {
		t.Fatal("toolConfig missing with tools+choice")
	}

	// Disabled thinking per model class.
	pro := googleTestModel(func(m *src.Model[src.Api]) { m.ID = "gemini-3-pro-preview" })
	got := getDisabledGoogleThinkingConfig(pro)
	if got["thinkingLevel"] != "LOW" {
		t.Fatalf("pro disabled = %#v", got)
	}
	flash := googleTestModel(func(m *src.Model[src.Api]) { m.ID = "gemini-3-flash-preview" })
	if got := getDisabledGoogleThinkingConfig(flash); got["thinkingLevel"] != "MINIMAL" {
		t.Fatalf("flash disabled = %#v", got)
	}
	gemma := googleTestModel(func(m *src.Model[src.Api]) { m.ID = "gemma-4-27b-it" })
	if got := getDisabledGoogleThinkingConfig(gemma); got["thinkingLevel"] != "MINIMAL" {
		t.Fatalf("gemma4 disabled = %#v", got)
	}
	twoX := googleTestModel(nil)
	if got := getDisabledGoogleThinkingConfig(twoX); got["thinkingBudget"] != 0 {
		t.Fatalf("2.x disabled = %#v", got)
	}

	// Enabled thinking: level XOR budget.
	params2 := buildGoogleParams(m, src.ChatContext{}, &GoogleOptions{
		Thinking: &GoogleThinkingConfig{Enabled: true, Level: strPtr("HIGH")},
	}).(map[string]interface{})
	gc := params2["generationConfig"].(map[string]interface{})
	thinking := gc["thinkingConfig"].(map[string]interface{})
	if thinking["thinkingLevel"] != "HIGH" || thinking["includeThoughts"] != true {
		t.Fatalf("thinking = %#v", thinking)
	}
	if _, has := thinking["thinkingBudget"]; has {
		t.Fatal("level and budget are mutually exclusive")
	}
}

// --- Simple wrapper ---

// captureSimpleGoogleBody drives StreamSimpleGoogle and returns the wire
// body's generationConfig.thinkingConfig.
func captureSimpleGoogleBody(t *testing.T, m src.Model[src.Api], o *src.SimpleStreamOptions) (map[string]interface{}, string) {
	t.Helper()
	var thinkingConfig map[string]interface{}
	var errMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(buf, &body)
		if gc, ok := body["generationConfig"].(map[string]interface{}); ok {
			thinkingConfig, _ = gc["thinkingConfig"].(map[string]interface{})
		}
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer srv.Close()

	mutated := m
	mutated.BaseURL = srv.URL
	for ev := range StreamSimpleGoogle(mutated, src.ChatContext{}, o)() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	return thinkingConfig, errMsg
}

func simpleOpts(t *testing.T, level src.ThinkingLevel) *src.SimpleStreamOptions {
	t.Helper()
	o := &src.SimpleStreamOptions{}
	o.Reasoning = level
	o.APIKey = "test-key"
	return o
}

func TestGoogleSimpleWrapper(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")

	// No key -> immediate error event.
	errMsg := ""
	for ev := range StreamSimpleGoogle(googleTestModel(nil), src.ChatContext{}, &src.SimpleStreamOptions{})() {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.ErrorMessage
		}
	}
	if !strings.Contains(errMsg, "No API key for provider: google") {
		t.Fatalf("errMsg = %q", errMsg)
	}

	// No reasoning -> thinking disabled.
	tc, err := captureSimpleGoogleBody(t, googleTestModel(nil), simpleOpts(t, ""))
	if err != "" {
		t.Fatalf("stream: %s", err)
	}
	// enabled:false maps to the disabled config; for a 2.x model that is
	// {thinkingBudget: 0} with no includeThoughts.
	budget, _ := tc["thinkingBudget"].(float64)
	if tc == nil || budget != 0 {
		t.Fatalf("no-reasoning thinkingConfig = %#v", tc)
	}
	if _, has := tc["includeThoughts"]; has {
		t.Fatalf("disabled thinking must not include thoughts: %#v", tc)
	}

	// off clamps to high effort; the default test model is 2.5-flash (24576).
	tc, err = captureSimpleGoogleBody(t, googleTestModel(nil), simpleOpts(t, src.Off))
	if err != "" {
		t.Fatalf("stream: %s", err)
	}
	if tc == nil || tc["thinkingBudget"] != float64(24576) {
		t.Fatalf("off->high budget = %#v, want 24576 (flash table)", tc)
	}

	// Level-based: gemini-3-pro minimal -> LOW.
	tc, err = captureSimpleGoogleBody(t, googleTestModel(func(m *src.Model[src.Api]) { m.ID = "gemini-3-pro-preview" }), simpleOpts(t, src.Minimal))
	if err != "" {
		t.Fatalf("stream: %s", err)
	}
	if tc == nil || tc["thinkingLevel"] != "LOW" {
		t.Fatalf("pro minimal = %#v, want LOW", tc)
	}

	// Gemma 4 medium -> HIGH.
	tc, err = captureSimpleGoogleBody(t, googleTestModel(func(m *src.Model[src.Api]) { m.ID = "gemma-4-27b-it" }), simpleOpts(t, src.Medium))
	if err != "" {
		t.Fatalf("stream: %s", err)
	}
	if tc == nil || tc["thinkingLevel"] != "HIGH" {
		t.Fatalf("gemma medium = %#v, want HIGH", tc)
	}

	// flash-lite uses the google 512 table.
	tc, err = captureSimpleGoogleBody(t, googleTestModel(func(m *src.Model[src.Api]) { m.ID = "gemini-2.5-flash-lite" }), simpleOpts(t, src.Minimal))
	if err != "" {
		t.Fatalf("stream: %s", err)
	}
	if tc == nil || tc["thinkingBudget"] != float64(512) {
		t.Fatalf("flash-lite minimal = %#v, want 512 (google table)", tc)
	}

	// Custom budgets override.
	custom := simpleOpts(t, src.High)
	custom.ThinkingBudgets = &src.ThinkingBudgets{High: intPtr(999)}
	tc, err = captureSimpleGoogleBody(t, googleTestModel(nil), custom)
	if err != "" {
		t.Fatalf("stream: %s", err)
	}
	if tc == nil || tc["thinkingBudget"] != float64(999) {
		t.Fatalf("custom budget = %#v, want 999", tc)
	}

	// Direct helper pins (kept cheap): default -1 for unknown models.
	if got := getGoogleBudget("gemini-1.5-pro", "high", nil); got != -1 {
		t.Fatalf("unknown model budget = %d, want -1", got)
	}
	if got := getGoogleThinkingLevel("medium", "gemini-2.5-pro"); got != "MEDIUM" {
		t.Fatalf("2.5 medium level = %q", got)
	}
}

// --- Registry & catalog wiring ---

func TestGoogleRegistered(t *testing.T) {
	if p := src.GetApiProvider("google-generative-ai"); p == nil {
		t.Fatal("google-generative-ai not registered")
	}
	if p := src.GetApiProvider("google-vertex"); p == nil {
		t.Fatal("google-vertex not registered")
	}

	if len(src.Models["google"]) != 28 {
		t.Fatalf("google catalog = %d models, want 28", len(src.Models["google"]))
	}
	g := src.Models["google"]["gemini-2.5-pro"]
	if g == nil || g.Api != src.Api("google-generative-ai") || g.Provider != src.Provider("google") {
		t.Fatalf("catalog gemini-2.5-pro = %+v", g)
	}
	if !g.Reasoning || g.MaxTokens != 65536 {
		t.Fatalf("gemini-2.5-pro reasoning/maxTokens = %v/%d", g.Reasoning, g.MaxTokens)
	}

	v := src.Models["google-vertex"]
	if len(v) != 13 {
		t.Fatalf("vertex catalog = %d models, want 13", len(v))
	}
	any := v["gemini-3-pro-preview"]
	if any == nil || any.Api != src.Api("google-vertex") || any.BaseURL != "https://{location}-aiplatform.googleapis.com" {
		t.Fatalf("vertex gemini-3-pro-preview = %+v", any)
	}
	if mapped, has := any.ThinkingLevelMap["low"]; !has || mapped == nil || *mapped != src.ThinkingLevel("LOW") {
		t.Fatalf("vertex thinkingLevelMap[low] = %v/%v, want LOW", mapped, has)
	}
}
