package llm

// Catalog parity pins against the TS reference snapshot
// (pi/packages/ai/src/models.generated.ts, v0.74.0). Every shared group was
// field-diffed against the reference (story 40); these pins keep the synced
// groups from regressing and enforce the row invariants the consumers rely
// on (provider id == group key drives oauth ModifyModels and env-key
// resolution — the "github"/"github-copilot" mismatch this file guards was
// a live bug until story 40).

import (
	"sort"
	"testing"
)

func catalogGroup(t *testing.T, name string) map[string]*Model[Api] {
	t.Helper()
	grp, ok := Models[name]
	if !ok {
		t.Fatalf("catalog group %q missing", name)
	}
	return grp
}

// The catalog carries exactly the synced TS groups plus the two Go-side
// additions (subq, faux); the legacy invented "github" group must stay gone.
func TestCatalogGroupKeySet(t *testing.T) {
	want := []string{
		"amazon-bedrock", "anthropic", "azure-openai-responses", "faux",
		"github-copilot", "google", "google-vertex", "mistral", "openai",
		"openai-codex", "subq",
	}
	if len(Models) != len(want) {
		var got []string
		for g := range Models {
			got = append(got, g)
		}
		sort.Strings(got)
		t.Fatalf("catalog group count = %d, want %d; got %v", len(Models), len(want), got)
	}
	for _, g := range want {
		if _, ok := Models[g]; !ok {
			t.Fatalf("catalog group %q missing", g)
		}
	}
	if _, ok := Models["github"]; ok {
		t.Fatalf(`legacy "github" group present; TS provider id is "github-copilot"`)
	}
}

// Row counts per group, pinned to the TS snapshot.
func TestCatalogRowCounts(t *testing.T) {
	want := map[string]int{
		"amazon-bedrock":         93,
		"anthropic":              23,
		"azure-openai-responses": 42,
		"faux":                   1,
		"github-copilot":         26,
		"google":                 28,
		"google-vertex":          13,
		"mistral":                28,
		"openai":                 42,
		"openai-codex":           10,
		"subq":                   1,
	}
	for g, n := range want {
		if got := len(catalogGroup(t, g)); got != n {
			t.Fatalf("group %q row count = %d, want %d", g, got, n)
		}
	}
}

// Every row's Provider must equal its group key: oauth ModifyModels, env-key
// resolution and provider dispatch all key on the row Provider.
func TestCatalogProviderMatchesGroupKey(t *testing.T) {
	for g, grp := range Models {
		for id, m := range grp {
			if string(m.Provider) != g {
				t.Fatalf("%s.%s: Provider = %q, want group key %q", g, id, m.Provider, g)
			}
		}
	}
}

// The TS github-copilot group is mixed-API with per-row Copilot headers.
func TestCatalogGithubCopilotRows(t *testing.T) {
	grp := catalogGroup(t, "github-copilot")
	apiCounts := map[Api]int{}
	for id, m := range grp {
		apiCounts[m.Api]++
		if m.BaseURL != "https://api.individual.githubcopilot.com" {
			t.Fatalf("github-copilot.%s: BaseURL = %q", id, m.BaseURL)
		}
		want := map[string]string{
			"User-Agent":             "GitHubCopilotChat/0.35.0",
			"Editor-Version":         "vscode/1.107.0",
			"Editor-Plugin-Version":  "copilot-chat/0.35.0",
			"Copilot-Integration-Id": "vscode-chat",
		}
		if len(m.Headers) != len(want) {
			t.Fatalf("github-copilot.%s: Headers = %v, want the 4 Copilot headers", id, m.Headers)
		}
		for k, v := range want {
			if m.Headers[k] != v {
				t.Fatalf("github-copilot.%s: header %s = %q, want %q", id, k, m.Headers[k], v)
			}
		}
	}
	if apiCounts[Api("openai-responses")] != 12 ||
		apiCounts[Api("anthropic-messages")] != 7 ||
		apiCounts[Api("openai-completions")] != 7 {
		t.Fatalf("github-copilot api distribution = %v, want 12 responses / 7 anthropic-messages / 7 completions", apiCounts)
	}
}

// Sampled rows pinned verbatim to the TS snapshot values.
func TestCatalogSampledRows(t *testing.T) {
	m := catalogGroup(t, "anthropic")["claude-3-5-haiku-20241022"]
	if m == nil {
		t.Fatal("anthropic.claude-3-5-haiku-20241022 missing")
	}
	if m.Name != "Claude Haiku 3.5" || m.Api != AnthropicMessages ||
		m.BaseURL != "https://api.anthropic.com" || m.Reasoning ||
		m.Cost != (ModelCost{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1}) ||
		m.ContextWindow != 200000 || m.MaxTokens != 8192 {
		t.Fatalf("anthropic row drifted: %+v", m)
	}

	m = catalogGroup(t, "openai")["gpt-4.1"]
	if m == nil {
		t.Fatal("openai.gpt-4.1 missing")
	}
	if m.Api != OpenAIResponses ||
		m.Cost != (ModelCost{Input: 2, Output: 8, CacheRead: 0.5, CacheWrite: 0}) ||
		m.ContextWindow != 1047576 || m.MaxTokens != 32768 {
		t.Fatalf("openai gpt-4.1 row drifted: %+v", m)
	}

	m = catalogGroup(t, "github-copilot")["claude-haiku-4.5"]
	if m == nil {
		t.Fatal("github-copilot.claude-haiku-4.5 missing")
	}
	if m.Api != AnthropicMessages || !m.Reasoning ||
		m.ContextWindow != 144000 || m.MaxTokens != 32000 ||
		m.Compat.AnthropicMessages == nil ||
		m.Compat.AnthropicMessages.SupportsEagerToolInputStreaming == nil ||
		*m.Compat.AnthropicMessages.SupportsEagerToolInputStreaming {
		t.Fatalf("github-copilot claude-haiku-4.5 row drifted: %+v", m)
	}

	m = catalogGroup(t, "github-copilot")["claude-opus-4.6"]
	if m == nil {
		t.Fatal("github-copilot.claude-opus-4.6 missing")
	}
	if v, ok := m.ThinkingLevelMap["xhigh"]; !ok || v == nil || *v != ThinkingLevel("max") {
		t.Fatalf("claude-opus-4.6 thinkingLevelMap = %v, want xhigh->max", m.ThinkingLevelMap)
	}
}

// Whole-catalog sanity: rows identify themselves and carry positive limits.
// BaseURL is not checked — the TS snapshot itself ships empty baseUrl rows
// (the azure group resolves its endpoint at runtime), and the story-40
// field diff covers it.
func TestCatalogRowSanity(t *testing.T) {
	for g, grp := range Models {
		for id, m := range grp {
			if m.ID != id {
				t.Fatalf("%s.%s: row ID = %q", g, id, m.ID)
			}
			if m.ContextWindow <= 0 || m.MaxTokens <= 0 {
				t.Fatalf("%s.%s: ContextWindow=%d MaxTokens=%d", g, id, m.ContextWindow, m.MaxTokens)
			}
			if len(m.Input) == 0 {
				t.Fatalf("%s.%s: empty input types", g, id)
			}
		}
	}
}

// Every catalog group except the oauth/AWS/test-fake ones must resolve env
// var names, so the add-provider flow can configure it.
func TestCatalogGroupsResolveEnvKeys(t *testing.T) {
	exempt := map[string]bool{"amazon-bedrock": true, "openai-codex": true, "faux": true}
	for g := range Models {
		if exempt[g] {
			continue
		}
		if len(GetApiKeyEnvVars(g)) == 0 {
			t.Fatalf("catalog group %q has no env var mapping (GetApiKeyEnvVars)", g)
		}
	}
	if got := GetApiKeyEnvVars("github-copilot"); len(got) != 3 {
		t.Fatalf("GetApiKeyEnvVars(github-copilot) = %v, want COPILOT_GITHUB_TOKEN/GH_TOKEN/GITHUB_TOKEN", got)
	}
}
