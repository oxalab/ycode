package oauth

import (
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
		refreshed, err := provider.RefreshTokens(nil, creds)
		if err != nil {
			return nil, "", fmt.Errorf("failed to refresh OAuth token for %s", providerID)
		}
		creds = refreshed
	}
	apiKey := provider.GetApiKey(creds)
	return &creds, apiKey, nil
}