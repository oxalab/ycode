package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func azureTestModel(mut func(*src.Model[src.Api])) src.Model[src.Api] {
	m := src.Model[src.Api]{
		ID: "gpt-4o-mini", Name: "GPT-4o Mini", Api: src.Api("azure-openai-responses"),
		Provider: src.Provider("azure-openai-responses"), BaseURL: "",
		Reasoning: true, MaxTokens: 16384,
	}
	if mut != nil {
		mut(&m)
	}
	return m
}

// collectAzureStream drives StreamAzureOpenAIResponses and returns the final
// message plus the error message ("" on success).
func collectAzureStream(t *testing.T, m src.Model[src.Api], opts *AzureOpenAIResponsesOptions) (*src.AssistantMessage, string, error) {
	t.Helper()
	if opts == nil {
		opts = &AzureOpenAIResponsesOptions{}
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	var final *src.AssistantMessage
	errMsg := ""
	for ev := range StreamAzureOpenAIResponses(m, src.ChatContext{}, opts)() {
		if ev.Type == "error" {
			final = ev.Error
			if final != nil {
				errMsg = final.ErrorMessage
			}
		}
		if ev.Type == "done" {
			final = ev.Message
		}
	}
	return final, errMsg, nil
}

// --- URL construction ---

func TestAzureURLConstruction(t *testing.T) {
	var requestURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.String()
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	}))
	defer srv.Close()

	m := azureTestModel(nil)
	// A test host is not an Azure host, so pass the full v1 base (path
	// preservation is asserted in TestAzureNormalizeBaseURL).
	opts := &AzureOpenAIResponsesOptions{
		APIKey:       "k",
		AzureBaseURL: srv.URL + "/openai/v1",
	}
	if _, errMsg, _ := collectAzureStream(t, m, opts); errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if requestURL != "/openai/v1/responses?api-version=v1" {
		t.Fatalf("url = %q, want /openai/v1/responses?api-version=v1", requestURL)
	}

	// Resource name builds the default base; api-version override applies.
	t.Setenv("AZURE_OPENAI_API_VERSION", "2025-04-01-preview")
	var requestURL2 string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL2 = r.URL.String()
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	}))
	defer srv2.Close()
	opts2 := &AzureOpenAIResponsesOptions{APIKey: "k", AzureResourceName: "myres", AzureBaseURL: srv2.URL}
	if _, errMsg, _ := collectAzureStream(t, azureTestModel(nil), opts2); errMsg != "" {
		t.Fatalf("stream2: %s", errMsg)
	}
	if !strings.HasSuffix(requestURL2, "?api-version=2025-04-01-preview") {
		t.Fatalf("url2 = %q", requestURL2)
	}
}

func TestAzureNormalizeBaseURL(t *testing.T) {
	// Azure host with empty path → forced /openai/v1.
	got, err := normalizeAzureBaseUrl("https://r.openai.azure.com")
	if err != nil || got != "https://r.openai.azure.com/openai/v1" {
		t.Fatalf("got %q err %v", got, err)
	}
	// /openai path → forced /openai/v1, trailing slashes trimmed.
	got, err = normalizeAzureBaseUrl("https://r.openai.azure.com/openai///")
	if err != nil || got != "https://r.openai.azure.com/openai/v1" {
		t.Fatalf("got %q err %v", got, err)
	}
	// Deeper path preserved.
	got, err = normalizeAzureBaseUrl("https://r.openai.azure.com/openai/v1/")
	if err != nil || got != "https://r.openai.azure.com/openai/v1" {
		t.Fatalf("got %q err %v", got, err)
	}
	// cognitiveservices host also normalized.
	got, err = normalizeAzureBaseUrl("https://r.cognitiveservices.azure.com/")
	if err != nil || got != "https://r.cognitiveservices.azure.com/openai/v1" {
		t.Fatalf("got %q err %v", got, err)
	}
	// Invalid URL → reference wording.
	if _, err := normalizeAzureBaseUrl("::::not-a-url"); err == nil || !strings.Contains(err.Error(), "Invalid Azure OpenAI base URL: ::::not-a-url") {
		t.Fatalf("err = %v", err)
	}
}

func TestAzureMissingBaseURL(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	os.Unsetenv("AZURE_OPENAI_RESOURCE_NAME")
	m := azureTestModel(func(m *src.Model[src.Api]) { m.BaseURL = "" })
	_, errMsg, _ := collectAzureStream(t, m, &AzureOpenAIResponsesOptions{APIKey: "k"})
	if !strings.Contains(errMsg, "Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestAzureMissingAPIKey(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	m := azureTestModel(nil)
	_, errMsg, _ := collectAzureStream(t, m, &AzureOpenAIResponsesOptions{AzureBaseURL: "https://r.openai.azure.com"})
	if !strings.Contains(errMsg, "Azure OpenAI API key is required. Set AZURE_OPENAI_API_KEY") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestAzureSimpleWrapperNoKey(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	m := azureTestModel(nil)
	var final *src.AssistantMessage
	var msg string
	for ev := range StreamSimpleAzureOpenAIResponses(m, src.ChatContext{}, &src.SimpleStreamOptions{})() {
		if ev.Type == "error" && ev.Error != nil {
			final = ev.Error
			msg = ev.Error.ErrorMessage
		}
	}
	if final == nil || !strings.Contains(msg, "No API key for provider: azure-openai-responses") {
		t.Fatalf("error = %+v msg = %q", final, msg)
	}
}

// --- Body ---

func TestAzureBodyPins(t *testing.T) {
	var bodyMap map[string]interface{}
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("api-key")
		buf, _ := io.ReadAll(r.Body)
		bodyMap = nil // fresh decode per request
		_ = json.Unmarshal(buf, &bodyMap)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{}}}\n\n"))
	}))
	defer srv.Close()

	m := azureTestModel(nil)
	opts := &AzureOpenAIResponsesOptions{
		APIKey: "key-1", AzureBaseURL: srv.URL, SessionID: "sess-1",
		AzureDeploymentName: "deploy-x", MaxTokens: intPtr(4096),
	}
	if _, errMsg, _ := collectAzureStream(t, m, opts); errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	if gotKey != "key-1" {
		t.Fatalf("api-key header = %q", gotKey)
	}
	if bodyMap["model"] != "deploy-x" {
		t.Fatalf("model = %v, want deployment name", bodyMap["model"])
	}
	if bodyMap["prompt_cache_key"] != "sess-1" {
		t.Fatalf("prompt_cache_key = %v", bodyMap["prompt_cache_key"])
	}
	if bodyMap["stream"] != true {
		t.Fatalf("stream = %v", bodyMap["stream"])
	}
	if _, has := bodyMap["store"]; has {
		t.Fatal("store must not be sent (TS azure never sets it)")
	}
	if bodyMap["max_output_tokens"] != float64(4096) {
		t.Fatalf("max_output_tokens = %#v", bodyMap["max_output_tokens"])
	}
	// Reasoning model, no effort options → default off level ("none").
	reasoning, _ := bodyMap["reasoning"].(map[string]interface{})
	if reasoning == nil || reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v, want {effort: none}", bodyMap["reasoning"])
	}
	if _, has := bodyMap["include"]; has {
		t.Fatal("include must be absent for the default off branch")
	}

	// Effort set → mapped effort + include + summary auto.
	opts2 := &AzureOpenAIResponsesOptions{APIKey: "k", AzureBaseURL: srv.URL, ReasoningEffort: strPtr("medium")}
	if _, errMsg, _ := collectAzureStream(t, m, opts2); errMsg != "" {
		t.Fatalf("stream2: %s", errMsg)
	}
	reasoning2, _ := bodyMap["reasoning"].(map[string]interface{})
	if reasoning2["effort"] != "medium" || reasoning2["summary"] != "auto" {
		t.Fatalf("reasoning2 = %#v", bodyMap["reasoning"])
	}
	include2, _ := bodyMap["include"].([]interface{})
	if len(include2) != 1 || include2[0] != "reasoning.encrypted_content" {
		t.Fatalf("include2 = %#v", bodyMap["include"])
	}

	// thinkingLevelMap with off:null → no reasoning in the default branch.
	m3 := azureTestModel(func(m *src.Model[src.Api]) {
		m.ThinkingLevelMap = map[string]*src.ThinkingLevel{"off": nil, "xhigh": src.ThinkingLevelPtr(src.XHigh)}
	})
	if _, errMsg, _ := collectAzureStream(t, m3, &AzureOpenAIResponsesOptions{APIKey: "k", AzureBaseURL: srv.URL}); errMsg != "" {
		t.Fatalf("stream3: %s", errMsg)
	}
	if _, has := bodyMap["reasoning"]; has {
		t.Fatalf("off:null must omit reasoning, got %#v", bodyMap["reasoning"])
	}
}

func TestAzureDeploymentNameMap(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-4o-mini=prod-gpt4o, o3-mini= prod-o3 ,bad,=x,a=")
	mapping := parseAzureDeploymentNameMap(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP"))
	if mapping["gpt-4o-mini"] != "prod-gpt4o" || mapping["o3-mini"] != "prod-o3" {
		t.Fatalf("mapping = %#v", mapping)
	}
	if len(mapping) != 2 {
		t.Fatalf("malformed entries must be skipped: %#v", mapping)
	}

	m := azureTestModel(nil)
	// Env map resolves the model id.
	if got := resolveAzureDeploymentName(m, nil); got != "prod-gpt4o" {
		t.Fatalf("env-mapped = %q", got)
	}
	// Options win over the env map.
	if got := resolveAzureDeploymentName(m, &AzureOpenAIResponsesOptions{AzureDeploymentName: "custom"}); got != "custom" {
		t.Fatalf("options = %q", got)
	}

	// Without the env map, the model id is the deployment.
	os.Unsetenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP")
	if got := resolveAzureDeploymentName(m, nil); got != "gpt-4o-mini" {
		t.Fatalf("fallback = %q", got)
	}
}

// --- Stream ---

func TestAzureSSEHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_a\",\"status\":\"in_progress\"}}\n\n" +
				"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"m1\",\"delta\":\"Hello\"}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"m1\",\"delta\":\" world\"}\n\n" +
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"m1\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_a\",\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":10,\"total_tokens\":110,\"input_tokens_details\":{\"cached_tokens\":20}}}}\n\n"))
	}))
	defer srv.Close()

	m := azureTestModel(func(m *src.Model[src.Api]) { m.Cost = src.ModelCost{Input: 1, Output: 1, CacheRead: 1} })
	final, errMsg, _ := collectAzureStream(t, m, &AzureOpenAIResponsesOptions{APIKey: "k", AzureBaseURL: srv.URL})
	if errMsg != "" {
		t.Fatalf("stream: %s", errMsg)
	}
	// cached tokens are subtracted from input (story-29 normalization).
	if final.Usage.Input != 80 || final.Usage.Output != 10 || final.Usage.CacheRead != 20 || final.Usage.TotalTokens != 110 {
		t.Fatalf("usage = %+v", final.Usage)
	}
	wantTotal := 80e-6 + 10e-6 + 20e-6
	if diff := final.Usage.Cost.Total - wantTotal; diff > 1e-15 || diff < -1e-15 {
		t.Fatalf("cost = %+v, want %v", final.Usage.Cost, wantTotal)
	}
	if final.ResponseID != "resp_a" || final.StopReason != src.StopReasonStop {
		t.Fatalf("final = %+v", final)
	}
}

func TestAzureStatus401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad api-key"}}`))
	}))
	defer srv.Close()
	m := azureTestModel(nil)
	_, errMsg, _ := collectAzureStream(t, m, &AzureOpenAIResponsesOptions{APIKey: "k", AzureBaseURL: srv.URL})
	if !strings.Contains(errMsg, "Azure OpenAI API error (401)") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

// --- ClampThinkingLevel (models.go) ---

func TestClampThinkingLevel(t *testing.T) {
	// Non-reasoning model: everything clamps to off.
	m := azureTestModel(func(m *src.Model[src.Api]) { m.Reasoning = false })
	if got := src.ClampThinkingLevel(&m, src.High); got != src.Off {
		t.Fatalf("non-reasoning high = %v", got)
	}

	// Reasoning model without map: xhigh → high, high stays.
	m2 := azureTestModel(nil)
	if got := src.ClampThinkingLevel(&m2, src.XHigh); got != src.High {
		t.Fatalf("xhigh = %v, want high", got)
	}
	if got := src.ClampThinkingLevel(&m2, src.Medium); got != src.Medium {
		t.Fatalf("medium = %v", got)
	}

	// Map with xhigh explicitly available keeps xhigh; null levels excluded.
	m3 := azureTestModel(func(m *src.Model[src.Api]) {
		m.ThinkingLevelMap = map[string]*src.ThinkingLevel{
			"xhigh": src.ThinkingLevelPtr(src.XHigh),
			"off":   nil,
		}
	})
	if got := src.ClampThinkingLevel(&m3, src.XHigh); got != src.XHigh {
		t.Fatalf("mapped xhigh = %v", got)
	}
	if got := src.ClampThinkingLevel(&m3, src.Off); got != src.Minimal {
		t.Fatalf("off on off:null model = %v, want minimal (walks up past excluded off)", got)
	}
	levels := src.GetSupportedThinkingLevels(&m3)
	for _, l := range levels {
		if l == src.Off {
			t.Fatalf("off must be excluded by off:null map: %v", levels)
		}
	}
}
