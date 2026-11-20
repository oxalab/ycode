package oauth

import (
	"strings"
	"testing"
	"time"

	llm "ycode/packages/llm/src"
)

/*
Story 38: the registry used to be permanently empty (RegisterBuiltInProviders
had zero callers) and all three provider structs failed OAuthProviderInterface.
These pins lock the populated registry against the TS reference
(pi/packages/ai/src/utils/oauth/).
*/

func TestBuiltInProvidersPopulated(t *testing.T) {
	wantIDs := []string{"anthropic", "github-copilot", "openai-codex"}
	for _, id := range wantIDs {
		p, ok := GetOAuthProvider(id)
		if !ok {
			t.Fatalf("GetOAuthProvider(%q) not found — registry not populated", id)
		}
		if p.ID() != id {
			t.Fatalf("provider ID = %q, want %q", p.ID(), id)
		}
	}

	wantNames := map[string]string{
		"anthropic":      "Anthropic (Claude Pro/Max)",
		"github-copilot": "GitHub Copilot",
		"openai-codex":   "ChatGPT Plus/Pro (Codex Subscription)",
	}
	for _, p := range GetOAuthProviders() {
		if wantNames[p.ID()] != p.Name() {
			t.Fatalf("provider %q Name = %q, want %q (TS verbatim)", p.ID(), p.Name(), wantNames[p.ID()])
		}
	}
	if len(GetOAuthProviders()) != 3 {
		t.Fatalf("GetOAuthProviders() = %d providers, want 3", len(GetOAuthProviders()))
	}
}

func TestUsesCallbackServerMatrix(t *testing.T) {
	// TS: anthropic + codex set usesCallbackServer; copilot (device flow) leaves
	// it unset. The old Go copilot returned true.
	cases := map[string]bool{
		"anthropic":      true,
		"github-copilot": false,
		"openai-codex":   true,
	}
	for id, want := range cases {
		p, ok := GetOAuthProvider(id)
		if !ok {
			t.Fatalf("provider %q missing", id)
		}
		if got := p.UsesCallbackServer(); got != want {
			t.Fatalf("%s.UsesCallbackServer() = %v, want %v", id, got, want)
		}
	}
}

func TestCopilotClientIDMatchesTS(t *testing.T) {
	// TS decodes base64 "SXYxLmI1MDdhMDhjODdlY2ZlOTg=". The old Go value was
	// "IV1.b507a08c87ecfe98=" (capital IV1 + trailing '='), which every
	// device-flow request would carry as client_id.
	const want = "Iv1.b507a08c87ecfe98"
	if githubClientID != want {
		t.Fatalf("githubClientID = %q, want %q", githubClientID, want)
	}
}

func TestRegistryResetAndRestore(t *testing.T) {
	t.Cleanup(ResetOAuthProviders)

	ResetOAuthProviders()
	if len(GetOAuthProviders()) != 3 {
		t.Fatalf("after reset: %d providers, want 3 builtins reseeded", len(GetOAuthProviders()))
	}

	// RegisterOAuthProvider restores a builtin by id, deletes otherwise
	// (TS unregisterOAuthProvider semantics).
	RegisterOAuthProvider("anthropic")
	if _, ok := GetOAuthProvider("anthropic"); !ok {
		t.Fatal("builtin anthropic not restored")
	}
	RegisterOAuthProvider("not-a-provider")
	if _, ok := GetOAuthProvider("not-a-provider"); ok {
		t.Fatal("unknown id should be deleted, not registered")
	}
}

func copilotProxyToken() string {
	return "tid=abc;exp=1799999999;proxy-ep=proxy.individual.githubcopilot.com;x=1"
}

func TestGetGitHubCopilotBaseUrl(t *testing.T) {
	cases := []struct {
		name   string
		token  string
		domain string
		want   string
	}{
		{"token proxy-ep wins", copilotProxyToken(), "", "https://api.individual.githubcopilot.com"},
		{"token without claim falls back", "tid=abc;exp=123", "", "https://api.individual.githubcopilot.com"},
		{"empty token enterprise", "", "company.ghe.com", "https://copilot-api.company.ghe.com"},
		{"token beats enterprise", copilotProxyToken(), "company.ghe.com", "https://api.individual.githubcopilot.com"},
	}
	for _, tc := range cases {
		if got := getGitHubCopilotBaseUrl(tc.token, tc.domain); got != tc.want {
			t.Fatalf("%s: getGitHubCopilotBaseUrl(%q, %q) = %q, want %q", tc.name, tc.token, tc.domain, got, tc.want)
		}
	}
}

func TestGetBaseUrlFromTokenAnchoredRewrite(t *testing.T) {
	// TS rewrites only a leading "proxy." (replace(/^proxy\./, "api.")).
	if got := getBaseUrlFromToken("proxy-ep=proxy.individual.githubcopilot.com"); got != "https://api.individual.githubcopilot.com" {
		t.Fatalf("anchored rewrite = %q", got)
	}
	if got := getBaseUrlFromToken("proxy-ep=evil.proxy.foo"); got != "https://evil.proxy.foo" {
		t.Fatalf("non-leading proxy. must stay untouched, got %q", got)
	}
	if got := getBaseUrlFromToken("no-claim"); got != "" {
		t.Fatalf("missing claim should return empty, got %q", got)
	}
}

func copilotModelsFixture() []llm.Model[llm.Api] {
	return []llm.Model[llm.Api]{
		{ID: "gpt", Api: "openai-completions", Provider: llm.GithubCopilot, BaseURL: "https://original.example"},
		{ID: "claude", Api: "anthropic-messages", Provider: llm.Anthropic, BaseURL: "https://keep.me"},
	}
}

func TestCopilotModifyModelsRewritesBaseURL(t *testing.T) {
	p := &GithubCopilotProvider{}

	// Token with proxy-ep claim: copilot models rewritten, others untouched.
	out := p.ModifyModels(copilotModelsFixture(), OAuthCredentials{Access: copilotProxyToken()})
	if out[0].BaseURL != "https://api.individual.githubcopilot.com" {
		t.Fatalf("copilot model BaseURL = %q, want proxy-ep-derived URL", out[0].BaseURL)
	}
	if out[1].BaseURL != "https://keep.me" {
		t.Fatalf("non-copilot model BaseURL mutated: %q", out[1].BaseURL)
	}

	// Enterprise-only credentials: copilot-api.{domain}.
	out = p.ModifyModels(copilotModelsFixture(), OAuthCredentials{Extra: map[string]any{"enterprise_url": "https://company.ghe.com"}})
	if out[0].BaseURL != "https://copilot-api.company.ghe.com" {
		t.Fatalf("enterprise BaseURL = %q, want https://copilot-api.company.ghe.com", out[0].BaseURL)
	}

	// Empty credentials: default fallback URL.
	out = p.ModifyModels(copilotModelsFixture(), OAuthCredentials{})
	if out[0].BaseURL != "https://api.individual.githubcopilot.com" {
		t.Fatalf("fallback BaseURL = %q", out[0].BaseURL)
	}
}

func TestAnthropicCodexModifyModelsIdentity(t *testing.T) {
	// TS implements modifyModels only on github-copilot — the other two must
	// return the slice unchanged.
	models := copilotModelsFixture()
	for _, p := range []OAuthProviderInterface{&AnthropicProvider{}, &OpenaiCodexProvider{}} {
		out := p.ModifyModels(models, OAuthCredentials{Access: "x"})
		if len(out) != len(models) {
			t.Fatalf("%s ModifyModels length = %d, want %d", p.ID(), len(out), len(models))
		}
		for i := range out {
			if out[i].BaseURL != models[i].BaseURL {
				t.Fatalf("%s ModifyModels mutated model %d BaseURL: %q -> %q", p.ID(), i, models[i].BaseURL, out[i].BaseURL)
			}
		}
	}
}

func TestGetOAuthApiKey(t *testing.T) {
	t.Cleanup(ResetOAuthProviders)

	// Unknown provider → TS wording "Unknown OAuth provider: X" (lowercase here).
	_, _, err := GetOAuthApiKey("nope", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown OAuth provider: nope") {
		t.Fatalf("unknown-provider error = %v, want 'unknown OAuth provider: nope'", err)
	}

	// No credentials for a known provider → (nil, "", nil), TS returns null.
	creds, key, err := GetOAuthApiKey("anthropic", nil)
	if creds != nil || key != "" || err != nil {
		t.Fatalf("missing creds = (%v, %q, %v), want (nil, \"\", nil)", creds, key, err)
	}

	// Unexpired credentials return Access verbatim — no refresh path taken.
	future := time.Now().Add(time.Hour).UnixMilli()
	creds, key, err = GetOAuthApiKey("anthropic", map[string]OAuthCredentials{
		"anthropic": {Access: "sk-ant-oat-test", Refresh: "r", Expires: future},
	})
	if err != nil {
		t.Fatalf("unexpired lookup errored: %v", err)
	}
	if key != "sk-ant-oat-test" {
		t.Fatalf("key = %q, want the stored access token", key)
	}
	if creds == nil || creds.Access != "sk-ant-oat-test" {
		t.Fatalf("returned credentials = %+v, want unchanged", creds)
	}
}
