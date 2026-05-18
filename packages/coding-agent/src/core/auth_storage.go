package ycode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	llm "ycode/packages/llm/src"
	"ycode/packages/llm/src/utils/oauth"
)

type ApiKeyCredential struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

type OAuthCredential struct {
	Type    string         `json:"type"`
	Refresh string         `json:"refresh"`
	Access  string         `json:"access"`
	Expires int64          `json:"expires"`
	Extra   map[string]any `json:"extra,omitempty"`
}

type AuthCredential struct {
	Type    string         `json:"type"`
	Key     string         `json:"key,omitempty"`
	Refresh string         `json:"refresh,omitempty"`
	Access  string         `json:"access,omitempty"`
	Expires int64          `json:"expires,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

func (c AuthCredential) IsApiKey() bool {
	return c.Type == "api_key"
}

func (c AuthCredential) IsOAuth() bool {
	return c.Type == "oauth"
}

func (c AuthCredential) ToOAuth() OAuthCredential {
	return OAuthCredential{
		Type:    c.Type,
		Refresh: c.Refresh,
		Access:  c.Access,
		Expires: c.Expires,
		Extra:   c.Extra,
	}
}

type AuthStorageData = map[string]AuthCredential

type AuthStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Label      string `json:"label,omitempty"`
}

type LockResult struct {
	Result any
	Next   *string
}

type AuthStorageBackend interface {
	WithLock(fn func(current *string) LockResult) LockResult
	WithLockAsync(ctx context.Context, fn func(current *string) (LockResult, error)) (LockResult, error)
}

type FileAuthStorageBackend struct {
	authPath 	string
	mu 			sync.Mutex
}

func NewFileAuthStorageBackend(authPath string) *FileAuthStorageBackend {
	if authPath == "" {
		authPath = GetAuthPath()
	}
	return &FileAuthStorageBackend{authPath: authPath}
}

func (b *FileAuthStorageBackend) ensureParentDir() {
	dir := filepath.Dir(b.authPath)
	os.MkdirAll(dir, 0o700)
}

func (b *FileAuthStorageBackend) ensureFileExists() {
	if _, err := os.Stat(b.authPath); os.IsNotExist(err) {
		os.WriteFile(b.authPath, []byte("{}"), 0o600)
	}
}

func (b *FileAuthStorageBackend) WithLock(fn func(current *string) LockResult) LockResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ensureParentDir()
	b.ensureFileExists()

	var content *string
	if data, err := os.ReadFile(b.authPath); err == nil {
		s := string(data)
		content = &s
	}

	result := fn(content)
	if result.Next != nil {
		os.WriteFile(b.authPath, []byte(*result.Next), 0o600)
	}
	return result
}

func (b *FileAuthStorageBackend) WithLockAsync(ctx context.Context, fn func(current *string) (LockResult, error)) (LockResult, error) {
	done := make(chan struct{})
	var result LockResult 
	var err error

	go func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		b.ensureParentDir()
		b.ensureFileExists()

		var content *string
		if data, readErr := os.ReadFile(b.authPath); readErr == nil {
			s := string(data)
			content = &s
		}

		r, fnErr := fn(content)
		if fnErr != nil {
			err = fnErr
		} else {
			if r.Next != nil {
				os.WriteFile(b.authPath, []byte(*r.Next), 0o600)
			}
			result = r
		}
		close(done)
	}()
	select {
	case <-ctx.Done():
		return LockResult{}, ctx.Err()
	case <-done:
		return result, err
	}
}

type InMemoryAuthStorageBackend struct {
	mu 		sync.Mutex
	value 	*string
}

func (b *InMemoryAuthStorageBackend) WithLock(fn func(current *string) LockResult) LockResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := fn(b.value)
	if result.Next != nil {
		b.value = result.Next
	}
	return result
}

func (b *InMemoryAuthStorageBackend) WithLockAsync(ctx context.Context, fn func(content *string) (LockResult, error)) (LockResult, error) {
	done := make(chan struct{})
	var result LockResult
	var err error

	go func(){
		b.mu.Lock()
		defer b.mu.Unlock()
		r, fnErr := fn(b.value)
		if fnErr != nil {
			err = fnErr
		} else {
			if r.Next != nil {
				b.value = r.Next
			}
			result = r
		}
		close(done)
	}()

	select {
	case <-ctx.Done():
		return LockResult{}, ctx.Err()
	case <-done:
		return result, err
	}
}

type AuthStorage struct {
	storage 		AuthStorageBackend
	data 			AuthStorageData
	runtimeOverrides map[string]string
	fallbackResolver func(provider string) *string
	loadError 		error
	errors 			[]error
	mu 				sync.RWMutex
}

func NewAuthStorage(authPath string) *AuthStorage {
	storage := NewFileAuthStorageBackend(authPath)
	as := &AuthStorage{
		storage: 		storage,
		data: 			make(AuthStorageData),
		runtimeOverrides: make(map[string]string),
	}
	as.Reload()
	return as
}

func NewAuthStorageFromBackend(storage AuthStorageBackend) *AuthStorage {
	as := &AuthStorage{
		storage: 		storage,
		data: 			make(AuthStorageData),
		runtimeOverrides: make(map[string]string),
	}
	as.Reload()
	return as
}

func NewInMemoryAuthStorage(data AuthStorageData) *AuthStorage{
	backend := &InMemoryAuthStorageBackend{}
	if data == nil {
		data = make(AuthStorageData)
	}
	serialized, _ := json.MarshalIndent(data, "", "  ")
	s := string(serialized)
	backend.WithLock(func(current *string) LockResult {
		return LockResult{Next: &s}
	})
	return NewAuthStorageFromBackend(backend)
}

func (as *AuthStorage) SetRuntimeApiKey(provider, apiKey string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.runtimeOverrides[provider] = apiKey
}

func (as *AuthStorage) RemoveRuntimeApiKey(provider string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	delete(as.runtimeOverrides, provider)
}

func (as *AuthStorage) SetFallbackResolved(resolver func(provider string) *string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.fallbackResolver = resolver
}

func (as *AuthStorage) recordError(err error) {
	as.errors = append(as.errors, err)
}

func (as *AuthStorage) parseStorageData(content *string) AuthStorageData {
	if content == nil {
		return make(AuthStorageData)
	}
	var data AuthStorageData
	if err := json.Unmarshal([]byte(*content), &data); err != nil {
		return make(AuthStorageData)
	}
	return data
}

func (as *AuthStorage) Reload() {
	as.mu.Lock()
	defer as.mu.Unlock()

	result := as.storage.WithLock(func(current *string) LockResult{
		return LockResult{Result: current}
	}) 
	if content, ok := result.Result.(*string); ok {
		as.data = as.parseStorageData(content)
		as.loadError = nil
	}
}

func (as *AuthStorage) persistProviderChange(provider string, credential *AuthCredential) {
	if as.loadError != nil {
		return
	}

	as.storage.WithLock(func(current *string) LockResult{
		currentData := as.parseStorageData(current)
		if credential != nil {
			currentData[provider] = *credential
		} else {
			delete(currentData, provider)
		}
		serialized, _ := json.MarshalIndent(currentData, "", "  ")
		s := string(serialized)
		return LockResult{Next: &s}
	})
}

func (as *AuthStorage) Get(provider string) *AuthCredential {
	as.mu.Lock()
	defer as.mu.Unlock()
	if cred, ok := as.data[provider]; ok {
		return &cred
	}
	return nil
}

func (as *AuthStorage) Set(provider string, credential AuthCredential) {
	as.mu.Lock()
	as.data[provider] = credential
	as.mu.Unlock()
	as.persistProviderChange(provider, &credential)
}

func (as *AuthStorage) Remove(provider string) {
	as.mu.Lock()
	delete(as.data, provider)
	as.mu.Unlock()
	as.persistProviderChange(provider, nil)
}

func (as *AuthStorage) List() []string {
	as.mu.RLock()
	defer as.mu.RUnlock()
	keys := make([]string, 0, len(as.data))
	for k := range as.data {
		keys = append(keys, k)
	}
	return keys
}

func (as *AuthStorage) Has(provider string) bool {
	as.mu.RLock()
	defer as.mu.RUnlock()
	_, ok := as.data[provider]
	return ok
}

func (as *AuthStorage) HasAuth(provider string) bool {
	as.mu.RLock()
	defer as.mu.RUnlock()
	if _, ok := as.runtimeOverrides[provider]; ok {
		return true
	}
	if _, ok := as.data[provider]; ok {
		return true
	}
	if llm.GetEnvApiKey(provider) != "" {
		return true
	}
	if as.fallbackResolver != nil {
		if val := as.fallbackResolver(provider); val != nil {
			return true
		}
	}
	return false
}

func (as *AuthStorage) GetAuthStatus(provider string) AuthStatus {
	as.mu.RLock()
	defer as.mu.RUnlock()

	if _, ok := as.data[provider]; ok {
		return AuthStatus{Configured: true, Source: "stored"}
	}
	if _, ok := as.runtimeOverrides[provider]; ok {
		return AuthStatus{Configured: false, Source: "runtime", Label: "--api-key"}
	}
	envKeys := llm.FindEnvKeys(provider)
	if len(envKeys) > 0 && envKeys[0] != "" {
		return AuthStatus{Configured: false, Source: "environment", Label: envKeys[0]}
	}
	if as.fallbackResolver != nil {
		if val := as.fallbackResolver(provider); val != nil {
			return AuthStatus{Configured: false, Source: "fallback", Label:"custom provider config"}
		}
	}
	return AuthStatus{Configured: false}
}

func (as *AuthStorage) GetAll() AuthStorageData {
	as.mu.RLock()
	defer as.mu.RUnlock()
	result := make(AuthStorageData, len(as.data))
	for k, v := range as.data {
		result[k] = v
	}
	return result
}

func (as *AuthStorage) DrainErrors() []error {
	as.mu.Lock()
	defer as.mu.Unlock()
	drained := make([]error, len(as.errors))
	copy(drained, as.errors)
	as.errors = nil
	return drained
}

func (as *AuthStorage) Login(ctx context.Context, providerId string, callbacks oauth.OAuthLoginCallbacks) error {
	provider, ok := oauth.GetOAuthProvider(providerId)
	if !ok {
		return fmt.Errorf("unknown OAuth provider; %s", providerId)
	}
	creds, err := provider.Login(ctx, callbacks)
	if err != nil {
		return err
	}
	as.Set(providerId, AuthCredential{
		Type: 	"oauth",
		Refresh: creds.Refresh,
		Access: creds.Access,
		Expires: creds.Expires,
		Extra: creds.Extra,
	})
	return nil
}

func (as *AuthStorage) Logout(provider string) {
	as.Remove(provider)
}

func (as *AuthStorage) refreshOAuthTokenWithLock(ctx context.Context, providerId string) (*oauth.OAuthCredentials, string, error) {
	provider, ok := oauth.GetOAuthProvider(providerId)
	if !ok {
		return nil, "", nil
	}

	result, err := as.storage.WithLockAsync(ctx, func(current *string) (LockResult, error) {
		currentData := as.parseStorageData(current)
		as.mu.Lock()
		as.data = currentData
		as.loadError = nil
		as.mu.Unlock()

		cred, exists := currentData[providerId]
		if !exists || !cred.IsOAuth() {
			return LockResult{}, nil
		}

		if cred.Expires > 0 && (current != nil && *current != "") {
			oauthCred := cred.ToOAuth()
			if oauth_GetApiKey(oauthCred) != "" {
				return LockResult{Result: oauthCred}, nil
			}
		}

		oauthCreds := make(map[string]oauth.OAuthCredentials)
		for key, value := range currentData {
			if value.IsOAuth() {
				oauthCreds[key] = oauth.OAuthCredentials{
					Refresh: value.Refresh,
					Access: value.Refresh,
					Expires: value.Expires,
					Extra: value.Extra,
				}
			}
		}

		refreshed, _, refreshErr := oauth.GetOAuthApiKey(providerId, oauthCreds)
		if refreshErr != nil {
			return LockResult{}, refreshErr
		}
		if refreshed == nil {
			return LockResult{}, nil
		}
		merged := make(AuthStorageData)
		for k, v := range currentData {
			merged[k] = v
		}
		merged[providerId] = AuthCredential{
			Type: 		"oauth",
			Refresh: 	refreshed.Refresh,
			Access:		refreshed.Access,
			Expires: 	refreshed.Expires,
			Extra: 		refreshed.Extra,
		}
		as.mu.Lock()
		as.data = merged
		as.loadError = nil
		as.mu.Unlock()

		serialized, _ := json.MarshalIndent(merged, "", "  ")
		s := string(serialized)
		return LockResult{Result: refreshed, Next: &s}, nil
	})

	if err != nil {
		return nil, "", nil
	}
	if refreshed, ok := result.Result.(*oauth.OAuthCredentials); ok && refreshed != nil {
		apiKey := provider.GetApiKey(*refreshed)
		return refreshed, apiKey, nil
	}
	return nil, "", nil
}

func (as *AuthStorage) GetApiKey(ctx context.Context, providerId string, includeFallback *bool) (string, error) {
	as.mu.RLock()
	runtimeKey, hasRuntime := as.runtimeOverrides[providerId]
	as.mu.RUnlock()

	if hasRuntime {
		return runtimeKey, nil
	}

	as.mu.RLock()
	cred, exists := as.data[providerId]
	as.mu.RUnlock()

	if exists && cred.IsApiKey() {
		return ResolveConfigValue(cred.Key), nil
	}

	if exists && cred.IsOAuth() {
		provider, ok := oauth.GetOAuthProvider(providerId)
		if !ok {
			return "", nil
		}
		needsRefresh := cred.Expires > 0 && (cred.Expires == 0 || getCurrentTimeMillis() >= cred.Expires)
		if needsRefresh {
			refreshed, apiKey, err := as.refreshOAuthTokenWithLock(ctx, providerId)
			if err != nil {
				as.recordError(err)
				as.Reload()
				as.mu.RLock()
				updatedCred, updatedExists := as.data[providerId]
				as.mu.RUnlock()

				if updatedExists && updatedCred.IsOAuth() && getCurrentTimeMillis() < updatedCred.Expires {
					return provider.GetApiKey(oauth.OAuthCredentials{
						Access:		updatedCred.Access,
						Refresh: 	updatedCred.Refresh,
						Expires: 	updatedCred.Expires,
						Extra: 		updatedCred.Extra, 
					}), nil
				}
				return "", nil
			}
			if refreshed != nil {
				return apiKey, nil
			}
		} else {
			return provider.GetApiKey(oauth.OAuthCredentials{
				Access: 	cred.Access,
				Refresh: 	cred.Refresh,
				Expires: 	cred.Expires,
				Extra: 		cred.Extra,
			}), nil	
		}
	}
	envKey := llm.GetEnvApiKey(providerId)
	if envKey != "" {
		return envKey, nil
	}
	shouldIncludeFallback := true
	if includeFallback != nil {
		shouldIncludeFallback = *includeFallback
	}
	if shouldIncludeFallback && as.fallbackResolver != nil {
		if val := as.fallbackResolver(providerId); val != nil {
			return *val, nil
		}
	}
	return "", nil
}

func (as *AuthStorage) GetOAuthProviders() []oauth.OAuthProviderInterface {
	return oauth.GetOAuthProviders()
}

func getCurrentTimeMillis() int64 {
	return time.Now().UnixMilli()
}

func oauth_GetApiKey(cred OAuthCredential) string {
	return cred.Access
}