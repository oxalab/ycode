package ycode

// model_registry_save_test.go verifies SaveCustomProvider persists a custom
// provider to models.json so it survives a fresh ModelRegistry (i.e. it is NOT the
// in-memory RegisterProvider), preserves existing providers (read-modify-write),
// and the written model loads back with the right provider/id/api/baseURL.

import (
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
