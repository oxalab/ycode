package ycode

// model_registry_save_test.go verifies SaveCustomProvider persists a custom
// provider to models.json so it survives a fresh ModelRegistry (i.e. it is NOT the
// in-memory RegisterProvider), preserves existing providers (read-modify-write),
// and the written model loads back with the right provider/id/api/baseURL.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	llm "ycode/packages/llm/src"
)

// testRegistryAt builds a ModelRegistry whose models.json + auth.json live under
// the same temp dir, so two registries built against the same modelsPath share the
// file (used to prove persistence across a re-read).
func testRegistryAt(t *testing.T, modelsPath string) *ModelRegistry {
	t.Helper()
	auth := NewAuthStorage(filepath.Join(filepath.Dir(modelsPath), "auth.json"))
	return NewModelRegistry(auth, modelsPath)
}

func TestSaveCustomProviderPersistsAndLoads(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	mr := testRegistryAt(t, modelsPath)

	if err := mr.SaveCustomProvider("acme", "Acme", "https://api.acme.com/v1",
		"sk-test", string(llm.OpenAICompletions), "acme"); err != nil {
		t.Fatalf("SaveCustomProvider: %v", err)
	}

	// A FRESH registry reading the same models.json must see the provider — proving
	// persistence (not just the in-memory registeredProviders map).
	mr2 := testRegistryAt(t, modelsPath)
	found := false
	for _, m := range mr2.GetAll() {
		if string(m.Provider) == "acme" && m.ID == "acme" {
			found = true
			if m.Api != llm.OpenAICompletions {
				t.Errorf("api = %q, want %q", m.Api, llm.OpenAICompletions)
			}
			if m.BaseURL != "https://api.acme.com/v1" {
				t.Errorf("baseURL = %q, want https://api.acme.com/v1", m.BaseURL)
			}
		}
	}
	if !found {
		t.Errorf("custom provider model not loaded back after re-read; got %d models", len(mr2.GetAll()))
	}
}

func TestSaveCustomProviderPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")

	// Seed models.json with an existing provider.
	seed := `{"providers":{"existing":{"baseUrl":"https://api.existing.com/v1","apiKey":"k1","api":"openai-completions","models":[{"id":"existing-model"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	mr := testRegistryAt(t, modelsPath)

	// Add a new custom provider — must not clobber the existing one.
	if err := mr.SaveCustomProvider("acme", "Acme", "https://api.acme.com/v1",
		"sk-test", string(llm.OpenAICompletions), "acme"); err != nil {
		t.Fatalf("SaveCustomProvider: %v", err)
	}

	mr2 := testRegistryAt(t, modelsPath) // re-read from disk
	have := map[string]bool{}
	for _, m := range mr2.GetAll() {
		have[string(m.Provider)+"/"+m.ID] = true
	}
	if !have["acme/acme"] {
		t.Error("new provider acme/acme missing after save")
	}
	if !have["existing/existing-model"] {
		t.Error("pre-existing provider existing/existing-model was clobbered")
	}
}

// Re-running the add-provider flow with the same name must EXTEND the provider
// (append the model, update fields), not replace the entry and silently drop
// its earlier models (the story-41 data-loss bug).
func TestSaveCustomProviderAppendsModelToExisting(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")

	mr := testRegistryAt(t, modelsPath)
	if err := mr.SaveCustomProvider("acme", "Acme", "https://api.acme.com/v1",
		"sk-1", string(llm.OpenAICompletions), "acme-one"); err != nil {
		t.Fatalf("first SaveCustomProvider: %v", err)
	}
	if err := mr.SaveCustomProvider("acme", "Acme II", "https://api2.acme.com/v1",
		"sk-2", string(llm.OpenAIResponses), "acme-two"); err != nil {
		t.Fatalf("second SaveCustomProvider: %v", err)
	}

	mr2 := testRegistryAt(t, modelsPath)
	ids := map[string]bool{}
	var baseURL, api string
	for _, m := range mr2.GetAll() {
		if string(m.Provider) != "acme" {
			continue
		}
		ids[m.ID] = true
		baseURL, api = m.BaseURL, string(m.Api)
	}
	if !ids["acme-one"] || !ids["acme-two"] {
		t.Fatalf("models after re-add = %v, want acme-one AND acme-two (no silent drop)", ids)
	}
	if baseURL != "https://api2.acme.com/v1" || api != string(llm.OpenAIResponses) {
		t.Fatalf("fields not updated on re-add: baseURL=%q api=%q", baseURL, api)
	}
	if len(ids) != 2 {
		t.Fatalf("provider has %d models, want exactly 2", len(ids))
	}
}

// Re-adding the same model id must not duplicate the entry. Counted in the RAW
// models.json — the loaded view dedups via mergeCustomModels and would mask an
// on-disk duplicate.
func TestSaveCustomProviderSameModelNoDuplicate(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")

	mr := testRegistryAt(t, modelsPath)
	for i := 0; i < 2; i++ {
		if err := mr.SaveCustomProvider("acme", "Acme", "https://api.acme.com/v1",
			"sk-1", string(llm.OpenAICompletions), "acme-one"); err != nil {
			t.Fatalf("SaveCustomProvider #%d: %v", i+1, err)
		}
	}

	data, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Providers map[string]struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range cfg.Providers["acme"].Models {
		if m.ID == "acme-one" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("acme-one appears %d times in models.json, want 1 (dedup by id)", n)
	}
}

// /add-provider-model on a custom provider appends to its entry without
// touching name/baseUrl/apiKey.
func TestAddCustomModelOnCustomProvider(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	seed := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://api.acme.com/v1","apiKey":"sk-keep","api":"openai-completions","models":[{"id":"acme-one"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	mr := testRegistryAt(t, modelsPath)
	if err := mr.AddCustomModel("acme", string(llm.OpenAIResponses), "acme-two"); err != nil {
		t.Fatalf("AddCustomModel: %v", err)
	}

	mr2 := testRegistryAt(t, modelsPath)
	ids := map[string]bool{}
	var baseURL string
	for _, m := range mr2.GetAll() {
		if string(m.Provider) != "acme" {
			continue
		}
		ids[m.ID] = true
		baseURL = m.BaseURL
	}
	if !ids["acme-one"] || !ids["acme-two"] || len(ids) != 2 {
		t.Fatalf("models = %v, want acme-one + acme-two", ids)
	}
	if baseURL != "https://api.acme.com/v1" {
		t.Fatalf("baseUrl changed: %q", baseURL)
	}
}

// /add-provider-model on a built-in provider writes a bare models.json entry
// (validateConfig only requires baseUrl/apiKey for non-built-ins) and the
// loader merges it additively with the built-in group — no clobbering.
func TestAddCustomModelOnBuiltInProvider(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")

	mr := testRegistryAt(t, modelsPath)
	if err := mr.AddCustomModel("openai", string(llm.OpenAIResponses), "my-gpt"); err != nil {
		t.Fatalf("AddCustomModel: %v", err)
	}

	// The bare entry must survive validation + re-read.
	mr2 := testRegistryAt(t, modelsPath)
	builtin, custom := 0, false
	for _, m := range mr2.GetAll() {
		if string(m.Provider) != "openai" {
			continue
		}
		if m.ID == "my-gpt" {
			custom = true
		} else {
			builtin++
		}
	}
	if !custom {
		t.Fatal("openai/my-gpt missing after save + re-read")
	}
	if builtin == 0 {
		t.Fatal("built-in openai models clobbered by the bare models.json entry")
	}
}
