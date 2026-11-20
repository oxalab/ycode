package oauth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ycode/packages/llm/src"
)

var builtInProviders []OAuthProviderInterface
var oauthProviderRegistry = make(map[string]OAuthProviderInterface)
var registryMu sync.RWMutex

// The TS reference seeds the registry at module load via an array literal
// (pi .../oauth/index.ts:34-42 — importing the module implies a populated
// registry). Go's equivalent is package init, the same idiom
// providers/register_builtins.go uses for API providers.
func init() {
	RegisterBuiltInProviders([]OAuthProviderInterface{
		&AnthropicProvider{},
		&GithubCopilotProvider{},
		&OpenaiCodexProvider{},
	})
}

// Compile-time interface satisfaction (nothing asserted this before, which is
// why the mismatched provider structs went unnoticed).
var (
	_ OAuthProviderInterface = (*AnthropicProvider)(nil)
	_ OAuthProviderInterface = (*GithubCopilotProvider)(nil)
	_ OAuthProviderInterface = (*OpenaiCodexProvider)(nil)
)

// RegisterBuiltInProviders populates the registry with all built-in OAuth providers.
// Call this once during init after all providers are registered
func RegisterBuiltInProviders(providers []OAuthProviderInterface) {
	registryMu.Lock()
	defer registryMu.Unlock()
	builtInProviders = make([]OAuthProviderInterface, len(providers))
	copy(builtInProviders, providers)
	for _, p := range providers {
		oauthProviderRegistry[p.ID()] = p
	}
}

func GetOAuthProvider(id string) (OAuthProviderInterface, bool) {
	registryMu.Lock()
	defer registryMu.Unlock()
	p, ok := oauthProviderRegistry[id]
	return p, ok
}

func RegisterOAuthProvider(id string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, p := range builtInProviders {
		if p.ID() == id {
			oauthProviderRegistry[id] = p
			return
		}
	}
	delete(oauthProviderRegistry, id)
}

func ResetOAuthProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	clear(oauthProviderRegistry)
	for _, p := range builtInProviders {
		oauthProviderRegistry[p.ID()] = p
	}
}

func GetOAuthProviders() []OAuthProviderInterface {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]OAuthProviderInterface, 0, len(oauthProviderRegistry))
	for _, p := range oauthProviderRegistry {
		out = append(out, p)
	}
	return out
}

// GetOAuthApiKey: Returns the API key for a provider, refreshing if expired.
func GetOAuthApiKey(providerID string, credentials map[string]OAuthCredentials) (*OAuthCredentials, string, error) {
	provider, ok := GetOAuthProvider(providerID)
	if !ok {
		return nil, "", fmt.Errorf("unknown OAuth provider: %s", providerID)
	}
	creds, ok := credentials[providerID]
	if !ok {
		return nil, "", nil
	}
	if time.Now().UnixMilli() >= creds.Expires {
		refreshed, err := provider.RefreshTokens(context.TODO(), creds)
		if err != nil {
			return nil, "", fmt.Errorf("failed to refresh OAuth token for %s", providerID)
		}
		creds = refreshed
	}
	apiKey := provider.GetApiKey(creds)
	return &creds, apiKey, nil
}

// ---Provider Implementation---

// Anthropic Provider
type AnthropicProvider struct{}

func (p *AnthropicProvider) ID() string   { return "anthropic" }
func (p *AnthropicProvider) Name() string { return "Anthropic (Claude Pro/Max)" }
func (p *AnthropicProvider) Login(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return LoginAnthropic(ctx, callbacks)
}
func (p *AnthropicProvider) RefreshTokens(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	return RefreshAnthropicToken(ctx, credentials.Refresh)
}
func (p *AnthropicProvider) GetApiKey(credentials OAuthCredentials) string {
	return credentials.Access
}
func (p *AnthropicProvider) UsesCallbackServer() bool { return true }
func (p *AnthropicProvider) ModifyModels(models []llm.Model[llm.Api], credentials OAuthCredentials) []llm.Model[llm.Api] {
	return models
}

// GithubCopilotProvider
type GithubCopilotProvider struct{}

func (p *GithubCopilotProvider) ID() string   { return "github-copilot" }
func (p *GithubCopilotProvider) Name() string { return "GitHub Copilot" }
func (p *GithubCopilotProvider) Login(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return LoginGitHubCopilot(ctx, callbacks)
}
func (p *GithubCopilotProvider) RefreshTokens(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	return RefreshGitHubCopilotToken(ctx, credentials)
}
func (p *GithubCopilotProvider) GetApiKey(credentials OAuthCredentials) string {
	return credentials.Access
}

// TS leaves usesCallbackServer unset for the device flow — no local server.
func (p *GithubCopilotProvider) UsesCallbackServer() bool { return false }

// ModifyModels ports TS github-copilot.ts:390-395: rewrite the base URL on
// this provider's models from the token's proxy-ep claim (or the enterprise
// domain), so streaming hits the right regional/enterprise endpoint.
func (p *GithubCopilotProvider) ModifyModels(models []llm.Model[llm.Api], credentials OAuthCredentials) []llm.Model[llm.Api] {
	enterpriseURL, _ := credentials.Extra["enterprise_url"].(string)
	baseURL := getGitHubCopilotBaseUrl(credentials.Access, normalizeDomain(enterpriseURL))

	out := make([]llm.Model[llm.Api], len(models))
	copy(out, models)
	for i := range out {
		if string(out[i].Provider) == "github-copilot" {
			out[i].BaseURL = baseURL
		}
	}
	return out
}

// OpenAICodexProvider
type OpenaiCodexProvider struct{}

func (p *OpenaiCodexProvider) ID() string   { return "openai-codex" }
func (p *OpenaiCodexProvider) Name() string { return "ChatGPT Plus/Pro (Codex Subscription)" }
func (p *OpenaiCodexProvider) Login(ctx context.Context, callbacks OAuthLoginCallbacks) (OAuthCredentials, error) {
	return LoginOpenAICodex(ctx, callbacks)
}
func (p *OpenaiCodexProvider) RefreshTokens(ctx context.Context, credentials OAuthCredentials) (OAuthCredentials, error) {
	return RefreshOpenAICodexToken(ctx, credentials)
}
func (p *OpenaiCodexProvider) GetApiKey(credentials OAuthCredentials) string {
	return credentials.Access
}
func (p *OpenaiCodexProvider) UsesCallbackServer() bool {
	return true
}
func (p *OpenaiCodexProvider) ModifyModels(models []llm.Model[llm.Api], credentials OAuthCredentials) []llm.Model[llm.Api] {
	return models
}
