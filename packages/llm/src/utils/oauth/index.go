package oauth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

var builtInProviders []OAuthProviderInterface
var oauthProviderRegistry = make(map[string]OAuthProviderInterface)
var registryMu sync.RWMutex

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

func GetOAuthProvider(id string) (OAuthProviderInterface, bool){
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

func ResetOAuthProviders(){
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
type AnthropicProvider struct {}
func (p *AnthropicProvider) ID() string {return "anthropic"}
func (p *AnthropicProvider) Name() string { return "Anthropic"}
func (p *AnthropicProvider) Login(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	return LoginAnthropic(ctx, callbacks)
}
func (p *AnthropicProvider) RefreshTokens(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error) {
	return RefreshAnthropicToken(ctx, credentials.Refresh) 
}
func (p *AnthropicProvider) GetApiKey(credentials OAuthCredentials) string {
	return credentials.Access
}
func (p *AnthropicProvider) UseCallbackServer() bool {return true}

// GithubCopilotProvider 
type GithubCopilotProvider struct {}
func (p *GithubCopilotProvider) ID() string { return "github-copilot" }
func (p *GithubCopilotProvider) Name() string { return "GitHub Copilot" }
func (p *GithubCopilotProvider) Login(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	return LoginGitHubCopilot(ctx, callbacks)
}
func (p *GithubCopilotProvider) RefreshTokens(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error) {
	return RefreshGitHubCopilotToken(ctx, credentials)
}
func (p *GithubCopilotProvider) GetApiKey(credentials OAuthCredentials) string {
	return credentials.Access
}
func (p *GithubCopilotProvider) UseCallbackServer() bool { return true }

// OpenAICodexProvider
type OpenaiCodexProvider struct {}
func (p *OpenaiCodexProvider) ID() string { return "openai-codex" }
func (p *OpenaiCodexProvider) Name() string { return "ChatGPT Plus/Pro (Codex Subscription)"}
func (p *OpenaiCodexProvider) Login(ctx context.Context, callbacks OAuthLoginCallbacks) (*OAuthCredentials, error) {
	return LoginOpenAICodex(ctx, callbacks)
}
func (p *OpenaiCodexProvider) RefreshTokens(ctx context.Context, credentials OAuthCredentials) (*OAuthCredentials, error){
	return RefreshOpenAICodexToken(ctx, credentials)
}
func (p *OpenaiCodexProvider) GetApiKey(credentials OAuthCredentials) string {
	return credentials.Access
}
func (p *OpenaiCodexProvider) UseCallbackServer() bool {
	return true
}