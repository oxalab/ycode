package ycode

// Agent-loop secondary 5b (docs/agent-loop-context-bug.md §5b): models.json
// `compat` blocks must reach llm.Model.Compat — provider-level as the base,
// model-level and modelOverride-level as per-field overlays — instead of being
// validated and silently discarded. Mirrors the reference registry tests.

import (
	"os"
	"path/filepath"
	"testing"

	llm "ycode/packages/llm/src"
)

func writeCompatModelsJson(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
}

func findCompatModel(mr *ModelRegistry, provider, id string) *llm.Model[llm.Api] {
	for _, m := range mr.GetAll() {
		if string(m.Provider) == provider && m.ID == id {
			return m
		}
	}
	return nil
}

func TestProviderCompatAppliesToCustomModels(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	writeCompatModelsJson(t, modelsPath, `{"providers":{"demo":{
		"baseUrl":"https://example.com/v1","apiKey":"DEMO_KEY","api":"openai-completions",
		"compat":{"supportsUsageInStreaming":false,"maxTokensField":"max_tokens"},
		"models":[{"id":"demo-model","reasoning":false,"input":["text"],
			"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},
			"contextWindow":1000,"maxTokens":100}]}}}`)

	mr := testRegistryAt(t, modelsPath)
	m := findCompatModel(mr, "demo", "demo-model")
	if m == nil {
		t.Fatal("custom model not loaded")
	}
	compat := m.Compat.OpenAICompletions
	if compat == nil {
		t.Fatal("model.Compat.OpenAICompletions is nil; provider-level compat was discarded")
	}
	if compat.SupportUsageInStreaming == nil || *compat.SupportUsageInStreaming {
		t.Errorf("supportsUsageInStreaming = %v, want false", compat.SupportUsageInStreaming)
	}
	if compat.MaxTokensField == nil || *compat.MaxTokensField != "max_tokens" {
		t.Errorf("maxTokensField = %v, want max_tokens", compat.MaxTokensField)
	}
}

func TestModelCompatOverridesProviderCompat(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	writeCompatModelsJson(t, modelsPath, `{"providers":{"demo":{
		"baseUrl":"https://example.com/v1","apiKey":"DEMO_KEY","api":"openai-completions",
		"compat":{"supportsUsageInStreaming":false,"maxTokensField":"max_tokens"},
		"models":[{"id":"demo-model","reasoning":false,"input":["text"],
			"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},
			"contextWindow":1000,"maxTokens":100,
			"compat":{"supportsUsageInStreaming":true,"maxTokensField":"max_completion_tokens"}}]}}}`)

	mr := testRegistryAt(t, modelsPath)
	m := findCompatModel(mr, "demo", "demo-model")
	if m == nil {
		t.Fatal("custom model not loaded")
	}
	compat := m.Compat.OpenAICompletions
	if compat == nil {
		t.Fatal("model.Compat.OpenAICompletions is nil")
	}
	if compat.SupportUsageInStreaming == nil || !*compat.SupportUsageInStreaming {
		t.Errorf("model-level supportsUsageInStreaming should win, got %v", compat.SupportUsageInStreaming)
	}
	if compat.MaxTokensField == nil || *compat.MaxTokensField != "max_completion_tokens" {
		t.Errorf("model-level maxTokensField should win, got %v", compat.MaxTokensField)
	}
}

func TestProviderCompatAppliesToBuiltInModels(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	// The Go generated registry has no openai-completions built-ins (unlike the
	// reference, whose registry test uses openrouter); its openai models run on
	// the openai-responses api, so exercise that compat branch instead.
	writeCompatModelsJson(t, modelsPath, `{"providers":{"openai":{
		"compat":{"sendSessionIdHeader":true}}}}`)

	mr := testRegistryAt(t, modelsPath)
	count := 0
	for _, m := range mr.GetAll() {
		if string(m.Provider) != "openai" {
			continue
		}
		count++
		compat := m.Compat.OpenAIResponses
		if compat == nil {
			t.Fatalf("model %s: provider-level compat did not reach built-in model", m.ID)
		}
		if compat.SendSessionIDHeader == nil || !*compat.SendSessionIDHeader {
			t.Errorf("model %s: sendSessionIdHeader = %v, want true", m.ID, compat.SendSessionIDHeader)
		}
	}
	if count == 0 {
		t.Fatal("no built-in openai models loaded")
	}
}

func TestModelOverrideCompatAppliesToBuiltInModel(t *testing.T) {
	builtIns := llm.GetModels("openai")
	if len(builtIns) == 0 {
		t.Skip("no built-in openai models")
	}
	target := builtIns[0].ID

	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	writeCompatModelsJson(t, modelsPath, `{"providers":{"openai":{
		"modelOverrides":{"`+target+`":{"compat":{"sendSessionIdHeader":true}}}}}}`)

	mr := testRegistryAt(t, modelsPath)
	m := findCompatModel(mr, "openai", target)
	if m == nil {
		t.Fatalf("overridden model %s not loaded", target)
	}
	compat := m.Compat.OpenAIResponses
	if compat == nil || compat.SendSessionIDHeader == nil || !*compat.SendSessionIDHeader {
		t.Errorf("modelOverride compat sendSessionIdHeader = %v, want true", compat)
	}
}
