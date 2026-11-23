package interactive

// add_provider_test.go locks in slugify (the model-id/provider-key derivation for
// the "Add custom provider" flow). The full form + models.json write is exercised
// end-to-end on a real terminal; the SaveCustomProvider persistence is covered in
// the core package test.

import (
	"os"
	"path/filepath"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
	"ycode/packages/tui/components"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Provider":    "my-provider",
		"  Acme Corp  ":  "acme-corp",
		"GPT-4o mini":    "gpt-4o-mini",
		"OpenAI___Proxy": "openai-proxy",
		"---weird---":    "weird",
		"   ":            "custom",
		"":               "custom",
		"Café Résumé":    "caf-rsum", // non-ascii chars dropped (not separator-joined)
		"Multi   spaces": "multi-spaces",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// story-41: the /add-provider-model flow (addModelForm) and the shared
// all-enabled→clear persist rule for scoped models.

func addModelTestModels() []*llm.Model[llm.Api] {
	return []*llm.Model[llm.Api]{
		{ID: "glm-4.7", Provider: "zai", Api: llm.OpenAICompletions},
		{ID: "claude-haiku-4.5", Provider: "anthropic", Api: llm.AnthropicMessages},
		{ID: "gpt-4.1", Provider: "anthropic", Api: llm.AnthropicMessages},
	}
}

// providerOptionsFromModels dedups by provider in order and defaults a missing
// api to openai-completions.
func TestProviderOptionsFromModels(t *testing.T) {
	opts := providerOptionsFromModels(addModelTestModels())
	if len(opts) != 2 {
		t.Fatalf("opts = %d entries, want 2 (anthropic deduped)", len(opts))
	}
	if opts[0].key != "zai" || opts[0].api != llm.OpenAICompletions {
		t.Fatalf("opts[0] = %v, want zai/openai-completions", opts[0])
	}
	if opts[1].key != "anthropic" || opts[1].api != llm.AnthropicMessages {
		t.Fatalf("opts[1] = %v, want anthropic/anthropic-messages", opts[1])
	}

	// Missing api → openai-completions default.
	opts = providerOptionsFromModels([]*llm.Model[llm.Api]{{ID: "m", Provider: "p"}})
	if len(opts) != 1 || opts[0].api != llm.OpenAICompletions {
		t.Fatalf("default api = %v, want openai-completions", opts)
	}
}

// The add-model form: provider pick (with api default) → model id → api chooser
// → onFinish carries the collected values.
func TestAddModelFormFlow(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	providers := providerOptionsFromModels(addModelTestModels())

	var gotProvider string
	var gotAPI llm.Api
	var gotModel string
	var cancelled bool
	form := newAddModelForm(th, providers,
		func(provider string, api llm.Api, model string) { gotProvider, gotAPI, gotModel = provider, api, model },
		func() { cancelled = true })

	// Step 1: pick a provider — the API type defaults to its existing api.
	item, ok := form.pickList.GetSelectedItem()
	if !ok || item.Value != "zai" {
		t.Fatalf("picker preselect = %q, want zai", item.Value)
	}
	form.pickList.OnSelect(components.SelectItem{Value: "anthropic"})
	if form.apiType != llm.AnthropicMessages {
		t.Fatalf("apiType after provider pick = %q, want anthropic-messages (provider default)", form.apiType)
	}

	// Step 2: empty model id keeps us on the step; a real one advances.
	form.advance("  ")
	if form.step != amModelID {
		t.Fatalf("blank submit advanced to step %d, want %d (amModelID)", form.step, amModelID)
	}
	form.advance(" my-model ")
	if form.step != amAPIType {
		t.Fatalf("model id submit landed on step %d, want %d (amAPIType)", form.step, amAPIType)
	}

	// Step 3: the api chooser is preselected to the provider default.
	item, ok = form.apiList.GetSelectedItem()
	if !ok || item.Value != string(llm.AnthropicMessages) {
		t.Fatalf("api preselect = %q, want anthropic-messages", item.Value)
	}
	form.apiList.OnSelect(components.SelectItem{Value: string(llm.AnthropicMessages)})
	if gotProvider != "anthropic" || gotAPI != llm.AnthropicMessages || gotModel != "my-model" {
		t.Fatalf("onFinish = (%q, %q, %q), want (anthropic, anthropic-messages, my-model)", gotProvider, gotAPI, gotModel)
	}

	// Cancel wiring: the provider picker's OnCancel routes to onCancel.
	form.pickList.OnCancel()
	if !cancelled {
		t.Fatal("pickList.OnCancel did not fire onCancel")
	}
}

// scopedPersistPatterns: all-selected → nil (clear), subset → provider/id
// patterns (the --models CLI format).
func TestScopedPersistPatterns(t *testing.T) {
	ptrs := addModelTestModels()
	all := make([]llm.Model[llm.Api], len(ptrs))
	for i, p := range ptrs {
		all[i] = *p
	}
	if got := scopedPersistPatterns(all, len(all)); got != nil {
		t.Fatalf("all-selected = %v, want nil (clear)", got)
	}
	got := scopedPersistPatterns(all[:1], len(all))
	if len(got) != 1 || got[0] != "zai/glm-4.7" {
		t.Fatalf("subset = %v, want [zai/glm-4.7]", got)
	}
}

// persistAddModel appends via AddCustomModel (no apiKey required — that would
// make /add-provider-model unusable on built-in providers) and refreshes.
func TestPersistAddModel(t *testing.T) {
	ensureTheme()
	dir := t.TempDir()
	auth := core.NewAuthStorage(filepath.Join(dir, "auth.json"))
	registry := core.NewModelRegistry(auth, filepath.Join(dir, "models.json"))

	if err := persistAddModel(registry, "openai", llm.OpenAIResponses, "my-gpt"); err != nil {
		t.Fatalf("persistAddModel on built-in provider: %v", err)
	}

	// Refreshed registry exposes the model immediately.
	found := false
	for _, m := range registry.GetAll() {
		if string(m.Provider) == "openai" && m.ID == "my-gpt" {
			found = true
		}
	}
	if !found {
		t.Fatal("openai/my-gpt not available after persistAddModel + Refresh")
	}

	// Survives a re-read (models.json persisted).
	registry2 := core.NewModelRegistry(core.NewAuthStorage(filepath.Join(dir, "auth.json")), filepath.Join(dir, "models.json"))
	found = false
	for _, m := range registry2.GetAll() {
		if string(m.Provider) == "openai" && m.ID == "my-gpt" {
			found = true
		}
	}
	if !found {
		t.Fatal("openai/my-gpt missing after re-read")
	}

	// Custom provider: appending must keep the entry's baseUrl/apiKey (they are
	// mandatory for non-built-in providers — blanking them breaks the re-read).
	seed := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://api.acme.com/v1","apiKey":"sk-keep","api":"openai-completions","models":[{"id":"acme-one"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	registry3 := core.NewModelRegistry(core.NewAuthStorage(filepath.Join(dir, "auth.json")), filepath.Join(dir, "models.json"))
	if err := persistAddModel(registry3, "acme", llm.OpenAIResponses, "acme-two"); err != nil {
		t.Fatalf("persistAddModel on custom provider: %v", err)
	}
	registry4 := core.NewModelRegistry(core.NewAuthStorage(filepath.Join(dir, "auth.json")), filepath.Join(dir, "models.json"))
	models := registry4.GetAll()
	two, baseUrl := false, ""
	for _, m := range models {
		if string(m.Provider) == "acme" {
			if m.ID == "acme-two" {
				two = true
			}
			baseUrl = m.BaseURL
		}
	}
	if !two {
		t.Fatalf("acme/acme-two missing after append; models = %d", len(models))
	}
	if baseUrl != "https://api.acme.com/v1" {
		t.Fatalf("acme baseUrl = %q, want it preserved (apiKey/baseUrl untouched)", baseUrl)
	}
}
