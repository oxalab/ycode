// packages/coding-agent/src/core/model_registry.go

package ycode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"ycode/packages/llm/src"
	"ycode/packages/llm/src/providers"
	"ycode/packages/llm/src/utils/oauth"
)

type ProviderOverride struct {
	BaseURL string
	Compat  llm.Compat
}

type ProviderRequestConfig struct {
	ApiKey     string
	Headers    map[string]string
	AuthHeader bool
}

type ResolvedRequestAuth struct {
	Ok      bool
	ApiKey  string
	Headers map[string]string
	Error   string
}

type ModelOverride struct {
	Name            string
	Reasoning       bool
	ThinkingLevelMap map[string]string
	Input           []string
	Cost            *llm.ModelCost
	ContextWindow   int
	MaxTokens       int
	Headers         map[string]string
	Compat          json.RawMessage
}

type ProviderConfigInput struct {
	Name         string
	BaseURL      string
	ApiKey       string
	Api          string
	StreamSimple func(model *llm.Model[llm.Api], context context.Context, options any) any
	Headers      map[string]string
	AuthHeader   bool
	OAuth        *OAuthProviderConfig
	Models       []ProviderModelDef
}

type OAuthProviderConfig struct {
	Name          string
	Login         func(ctx context.Context, callbacks oauth.OAuthLoginCallbacks) (*oauth.OAuthCredentials, error)
	RefreshTokens func(ctx context.Context, creds oauth.OAuthCredentials) (*oauth.OAuthCredentials, error)
	GetApiKey     func(creds oauth.OAuthCredentials) string
	ModifyModels  func(models []*llm.Model[llm.Api], creds AuthCredential) []*llm.Model[llm.Api]
}

type ProviderModelDef struct {
	ID              string
	Name            string
	Api             string
	BaseURL         string
	Reasoning       bool
	ThinkingLevelMap map[string]string
	Input           []string
	Cost            *llm.ModelCost
	ContextWindow   int
	MaxTokens       int
	Headers         map[string]string
	Compat          json.RawMessage
}

type customModelsResult struct {
	models         []*llm.Model[llm.Api]
	overrides      map[string]*ProviderOverride
	modelOverrides map[string]map[string]*modelOverrideDef
	err            string
}

func emptyCustomModelsResult(err string) *customModelsResult {
	return &customModelsResult{
		models:         []*llm.Model[llm.Api]{},
		overrides:      make(map[string]*ProviderOverride),
		modelOverrides: make(map[string]map[string]*modelOverrideDef),
		err:            err,
	}
}

type ModelRegistry struct {
	authStorage              *AuthStorage
	modelsJsonPath           string
	models                   []*llm.Model[llm.Api]
	providerRequestConfigs   map[string]*ProviderRequestConfig
	modelRequestHeaders      map[string]map[string]string
	registeredProviders      map[string]*ProviderConfigInput
	loadError                string
	mu                       sync.RWMutex
}

func NewModelRegistry(authStorage *AuthStorage, modelsJsonPath string) *ModelRegistry {
	if modelsJsonPath == "" {
		modelsJsonPath = GetModelsPath()
	}
	mr := &ModelRegistry{
		authStorage:            authStorage,
		modelsJsonPath:         modelsJsonPath,
		providerRequestConfigs: make(map[string]*ProviderRequestConfig),
		modelRequestHeaders:    make(map[string]map[string]string),
		registeredProviders:    make(map[string]*ProviderConfigInput),
	}
	mr.loadModels()
	return mr
}

func NewInMemoryModelRegistry(authStorage *AuthStorage) *ModelRegistry {
	mr := &ModelRegistry{
		authStorage:            authStorage,
		providerRequestConfigs: make(map[string]*ProviderRequestConfig),
		modelRequestHeaders:    make(map[string]map[string]string),
		registeredProviders:    make(map[string]*ProviderConfigInput),
	}
	mr.loadModels()
	return mr
}

func (mr *ModelRegistry) Refresh() {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	mr.providerRequestConfigs = make(map[string]*ProviderRequestConfig)
	mr.modelRequestHeaders = make(map[string]map[string]string)
	mr.loadError = ""

	providers.ResetApiProviders()
	oauth.ResetOAuthProviders()

	mr.loadModels()

	for name, config := range mr.registeredProviders {
		mr.applyProviderConfig(name, config)
	}
}

func (mr *ModelRegistry) GetError() string {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	return mr.loadError
}

func (mr *ModelRegistry) loadModels() {
	var custom *customModelsResult
	if mr.modelsJsonPath != "" {
		custom = mr.loadCustomModels(mr.modelsJsonPath)
	} else {
		custom = emptyCustomModelsResult("")
	}

	if custom.err != "" {
		mr.loadError = custom.err
	}

	builtIn := mr.loadBuiltInModels(custom.overrides, custom.modelOverrides)
	combined := mr.mergeCustomModels(builtIn, custom.models)

	for _, oauthProvider := range mr.authStorage.GetOAuthProviders() {
		cred := mr.authStorage.Get(oauthProvider.ID())
		if cred != nil && cred.IsOAuth() {
			oauthCreds := oauth.OAuthCredentials{
				Refresh: cred.Refresh,
				Access:  cred.Access,
				Expires: cred.Expires,
				Extra:   cred.Extra,
			}
			// Convert []*Model to []Model for interface
			var valueSlice []llm.Model[llm.Api]
			for _, m := range combined {
				valueSlice = append(valueSlice, *m)
			}
			modified := oauthProvider.ModifyModels(valueSlice, oauthCreds)
			combined = make([]*llm.Model[llm.Api], len(modified))
			for i := range modified {
				combined[i] = &modified[i]
			}
		}
	}

	mr.models = combined
}

func (mr *ModelRegistry) loadBuiltInModels(
	overrides map[string]*ProviderOverride,
	modelOverrides map[string]map[string]*modelOverrideDef,
) []*llm.Model[llm.Api] {
	providers := llm.GetProviders()
	var result []*llm.Model[llm.Api]

	for _, provider := range providers {
		models := llm.GetModels(provider)
		providerOverride := overrides[provider]
		perModelOverrides := modelOverrides[provider]

		for _, m := range models {
			model := *m

			if providerOverride != nil {
				if providerOverride.BaseURL != "" {
					model.BaseURL = providerOverride.BaseURL
				}
			}

			if perModelOverrides != nil {
				if mo, ok := perModelOverrides[model.ID]; ok {
					model = *mr.applyModelOverride(&model, mo)
				}
			}

			result = append(result, &model)
		}
	}

	return result
}

func (mr *ModelRegistry) applyModelOverride(model *llm.Model[llm.Api], override *modelOverrideDef) *llm.Model[llm.Api] {
	result := *model

	if override.Name != "" {
		result.Name = override.Name
	}
	result.Reasoning = override.Reasoning
	if override.ContextWindow > 0 {
		result.ContextWindow = override.ContextWindow
	}
	if override.MaxTokens > 0 {
		result.MaxTokens = override.MaxTokens
	}
	if override.Cost != nil {
		c := *override.Cost
		if c.Input == 0 {
			c.Input = model.Cost.Input
		}
		if c.Output == 0 {
			c.Output = model.Cost.Output
		}
		if c.CacheRead == 0 {
			c.CacheRead = model.Cost.CacheRead
		}
		if c.CacheWrite == 0 {
			c.CacheWrite = model.Cost.CacheWrite
		}
		result.Cost = c
	}

	return &result
}

func (mr *ModelRegistry) mergeCustomModels(builtIn, custom []*llm.Model[llm.Api]) []*llm.Model[llm.Api] {
	merged := make([]*llm.Model[llm.Api], len(builtIn))
	copy(merged, builtIn)

	for _, customModel := range custom {
		found := false
		for i, m := range merged {
			if string(m.Provider) == string(customModel.Provider) && m.ID == customModel.ID {
				merged[i] = customModel
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, customModel)
		}
	}

	return merged
}

var jsonCommentRe = regexp.MustCompile(`"(?:\\.|[^"\\])*"|\/\/[^\n]*`)
var trailingCommaRe = regexp.MustCompile(`"(?:\\.|[^"\\])*"|,(\s*[}\]])`)

func stripJsonComments(input string) string {
	result := jsonCommentRe.ReplaceAllStringFunc(input, func(m string) string {
		if len(m) > 0 && m[0] == '"' {
			return m
		}
		return ""
	})
	result = trailingCommaRe.ReplaceAllStringFunc(result, func(m string) string {
		if len(m) > 0 && m[0] == '"' {
			return m
		}
		return ""
	})
	return result
}

func (mr *ModelRegistry) loadCustomModels(modelsJsonPath string) *customModelsResult {
	if _, err := os.Stat(modelsJsonPath); os.IsNotExist(err) {
		return emptyCustomModelsResult("")
	}

	data, err := os.ReadFile(modelsJsonPath)
	if err != nil {
		return emptyCustomModelsResult(fmt.Sprintf("Failed to read models.json: %s\n\nFile: %s", err.Error(), modelsJsonPath))
	}

	content := stripJsonComments(string(data))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return emptyCustomModelsResult(fmt.Sprintf("Failed to parse models.json: %s\n\nFile: %s", err.Error(), modelsJsonPath))
	}

	providersRaw, ok := raw["providers"]
	if !ok {
		return emptyCustomModelsResult("Invalid models.json: missing 'providers' key")
	}

	var config modelsConfig
	if err := json.Unmarshal(providersRaw, &config.Providers); err != nil {
		return emptyCustomModelsResult(fmt.Sprintf("Failed to parse providers: %s\n\nFile: %s", err.Error(), modelsJsonPath))
	}

	if err := mr.validateConfig(&config); err != nil {
		return emptyCustomModelsResult(fmt.Sprintf("%s\n\nFile: %s", err.Error(), modelsJsonPath))
	}

	overrides := make(map[string]*ProviderOverride)
	modelOverrides := make(map[string]map[string]*modelOverrideDef)

	for providerName, providerConfig := range config.Providers {
		if providerConfig.BaseURL != "" || providerConfig.Compat != nil {
			overrides[providerName] = &ProviderOverride{
				BaseURL: providerConfig.BaseURL,
			}
		}

		mr.storeProviderRequestConfig(providerName, providerConfig)

		if len(providerConfig.ModelOverrides) > 0 {
			moMap := make(map[string]*modelOverrideDef)
			for modelId, mo := range providerConfig.ModelOverrides {
				moMap[modelId] = mo
				mr.storeModelHeaders(providerName, modelId, mo.Headers)
			}
			modelOverrides[providerName] = moMap
		}
	}

	return &customModelsResult{
		models:         mr.parseModels(&config),
		overrides:      overrides,
		modelOverrides: modelOverrides,
	}
}

type providerConfig struct {
	Name           string                  `json:"name,omitempty"`
	BaseURL        string                  `json:"baseUrl,omitempty"`
	ApiKey         string                  `json:"apiKey,omitempty"`
	Api            string                  `json:"api,omitempty"`
	Headers        map[string]string       `json:"headers,omitempty"`
	Compat         json.RawMessage         `json:"compat,omitempty"`
	AuthHeader     bool                    `json:"authHeader,omitempty"`
	Models         []modelDefinition       `json:"models,omitempty"`
	ModelOverrides map[string]*modelOverrideDef `json:"modelOverrides,omitempty"`
}

type modelDefinition struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	Api             string            `json:"api,omitempty"`
	BaseURL         string            `json:"baseUrl,omitempty"`
	Reasoning       bool              `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"`
	Input           []string          `json:"input,omitempty"`
	Cost            *llm.ModelCost    `json:"cost,omitempty"`
	ContextWindow   int               `json:"contextWindow,omitempty"`
	MaxTokens       int               `json:"maxTokens,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Compat          json.RawMessage   `json:"compat,omitempty"`
}

type modelOverrideDef struct {
	Name            string            `json:"name,omitempty"`
	Reasoning       bool              `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"`
	Input           []string          `json:"input,omitempty"`
	Cost            *llm.ModelCost    `json:"cost,omitempty"`
	ContextWindow   int               `json:"contextWindow,omitempty"`
	MaxTokens       int               `json:"maxTokens,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Compat          json.RawMessage   `json:"compat,omitempty"`
}

type modelsConfig struct {
	Providers map[string]*providerConfig `json:"providers"`
}

func (mr *ModelRegistry) validateConfig(config *modelsConfig) error {
	builtInProviders := make(map[string]bool)
	for _, p := range llm.GetProviders() {
		builtInProviders[p] = true
	}

	for providerName, providerConfig := range config.Providers {
		isBuiltIn := builtInProviders[providerName]
		models := providerConfig.Models
		hasModelOverrides := len(providerConfig.ModelOverrides) > 0

		if len(models) == 0 {
			if providerConfig.BaseURL == "" && len(providerConfig.Headers) == 0 && providerConfig.Compat == nil && !hasModelOverrides {
				return fmt.Errorf("Provider %s: must specify \"baseUrl\", \"headers\", \"compat\", \"modelOverrides\", or \"models\"", providerName)
			}
		} else if !isBuiltIn {
			if providerConfig.BaseURL == "" {
				return fmt.Errorf("Provider %s: \"baseUrl\" is required when defining custom models", providerName)
			}
			if providerConfig.ApiKey == "" {
				return fmt.Errorf("Provider %s: \"apiKey\" is required when defining custom models", providerName)
			}
		}

		for _, modelDef := range models {
			if modelDef.ID == "" {
				return fmt.Errorf("Provider %s: model missing \"id\"", providerName)
			}
			if modelDef.ContextWindow < 0 {
				return fmt.Errorf("Provider %s, model %s: invalid contextWindow", providerName, modelDef.ID)
			}
			if modelDef.MaxTokens < 0 {
				return fmt.Errorf("Provider %s, model %s: invalid maxTokens", providerName, modelDef.ID)
			}
		}
	}

	return nil
}

func (mr *ModelRegistry) parseModels(config *modelsConfig) []*llm.Model[llm.Api] {
	var models []*llm.Model[llm.Api]
	builtInProviders := make(map[string]bool)
	for _, p := range llm.GetProviders() {
		builtInProviders[p] = true
	}

	builtInDefaultsCache := make(map[string]*struct {
		Api     string
		BaseURL string
	})

	getBuiltInDefaults := func(providerName string) *struct {
		Api     string
		BaseURL string
	} {
		if !builtInProviders[providerName] {
			return nil
		}
		if cached, ok := builtInDefaultsCache[providerName]; ok {
			return cached
		}
		builtIn := llm.GetModels(providerName)
		if len(builtIn) == 0 {
			return nil
		}
		defaults := &struct {
			Api     string
			BaseURL string
		}{Api: string(builtIn[0].Api), BaseURL: builtIn[0].BaseURL}
		builtInDefaultsCache[providerName] = defaults
		return defaults
	}

	for providerName, providerConfig := range config.Providers {
		if len(providerConfig.Models) == 0 {
			continue
		}

		builtInDefaults := getBuiltInDefaults(providerName)

		for _, modelDef := range providerConfig.Models {
			api := modelDef.Api
			if api == "" {
				api = providerConfig.Api
			}
			if api == "" && builtInDefaults != nil {
				api = builtInDefaults.Api
			}
			if api == "" {
				continue
			}

			baseURL := modelDef.BaseURL
			if baseURL == "" {
				baseURL = providerConfig.BaseURL
			}
			if baseURL == "" && builtInDefaults != nil {
				baseURL = builtInDefaults.BaseURL
			}
			if baseURL == "" {
				continue
			}

			mr.storeModelHeaders(providerName, modelDef.ID, modelDef.Headers)

			name := modelDef.Name
			if name == "" {
				name = modelDef.ID
			}

			input := modelDef.Input
			if len(input) == 0 {
				input = []string{"text"}
			}

			cost := llm.ModelCost{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}
			if modelDef.Cost != nil {
				cost = *modelDef.Cost
			}

			contextWindow := modelDef.ContextWindow
			if contextWindow == 0 {
				contextWindow = 128000
			}

			maxTokens := modelDef.MaxTokens
			if maxTokens == 0 {
				maxTokens = 16384
			}

			models = append(models, &llm.Model[llm.Api]{
				ID:            modelDef.ID,
				Name:          name,
				Api:           llm.Api(api),
				Provider:      llm.Provider(providerName),
				BaseURL:       baseURL,
				Reasoning:     modelDef.Reasoning,
				Input:         toInputTypes(input),
				Cost:          cost,
				ContextWindow: contextWindow,
				MaxTokens:     maxTokens,
			})
		}
	}

	return models
}

func toInputTypes(input []string) []llm.InputType {
	var result []llm.InputType
	for _, s := range input {
		result = append(result, llm.InputType(s))
	}
	return result
}

func (mr *ModelRegistry) GetAll() []*llm.Model[llm.Api] {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	return mr.models
}

// GetAuthStorage returns the auth storage backing this registry. Used by the
// TUI's OAuth selector to read credential/auth status per provider.
func (mr *ModelRegistry) GetAuthStorage() *AuthStorage {
	return mr.authStorage
}

func (mr *ModelRegistry) GetAvailable() ([]*llm.Model[llm.Api], error) {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	var available []*llm.Model[llm.Api]
	for _, m := range mr.models {
		if mr.HasConfiguredAuth(m) {
			available = append(available, m)
		}
	}
	return available, nil
}

func (mr *ModelRegistry) Find(provider, modelId string) *llm.Model[llm.Api] {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	for _, m := range mr.models {
		if string(m.Provider) == provider && m.ID == modelId {
			return m
		}
	}
	return nil
}

func (mr *ModelRegistry) HasConfiguredAuth(model *llm.Model[llm.Api]) bool {
	if mr.authStorage.HasAuth(string(model.Provider)) {
		return true
	}
	cfg, ok := mr.providerRequestConfigs[string(model.Provider)]
	return ok && cfg.ApiKey != ""
}

func (mr *ModelRegistry) getModelRequestKey(provider, modelId string) string {
	return provider + ":" + modelId
}

func (mr *ModelRegistry) storeProviderRequestConfig(providerName string, config *providerConfig) {
	if config.ApiKey == "" && len(config.Headers) == 0 && !config.AuthHeader {
		return
	}
	mr.providerRequestConfigs[providerName] = &ProviderRequestConfig{
		ApiKey:     config.ApiKey,
		Headers:    config.Headers,
		AuthHeader: config.AuthHeader,
	}
}

func (mr *ModelRegistry) storeModelHeaders(providerName, modelId string, headers map[string]string) {
	key := mr.getModelRequestKey(providerName, modelId)
	if len(headers) == 0 {
		delete(mr.modelRequestHeaders, key)
		return
	}
	mr.modelRequestHeaders[key] = headers
}

func (mr *ModelRegistry) GetApiKeyAndHeaders(ctx context.Context, model *llm.Model[llm.Api]) *ResolvedRequestAuth {
	providerConfig := mr.providerRequestConfigs[string(model.Provider)]

	falseVal := false
	apiKeyFromAuth, _ := mr.authStorage.GetApiKey(ctx, string(model.Provider), &falseVal)

	var apiKey string
	if apiKeyFromAuth != "" {
		apiKey = apiKeyFromAuth
	} else if providerConfig != nil && providerConfig.ApiKey != "" {
		resolved, err := ResolveConfigValueOrThrow(providerConfig.ApiKey, fmt.Sprintf("API key for provider %q", string(model.Provider)))
		if err != nil {
			return &ResolvedRequestAuth{Ok: false, Error: err.Error()}
		}
		apiKey = resolved
	}

	providerHeaders, _ := ResolveHeadersOrThrow(nil, fmt.Sprintf("provider %q", string(model.Provider)))
	if providerConfig != nil {
		providerHeaders, _ = ResolveHeadersOrThrow(providerConfig.Headers, fmt.Sprintf("provider %q", string(model.Provider)))
	}

	modelKey := mr.getModelRequestKey(string(model.Provider), model.ID)
	modelHeaders, _ := ResolveHeadersOrThrow(mr.modelRequestHeaders[modelKey], fmt.Sprintf("model %q/%q", string(model.Provider), model.ID))

	var headers map[string]string
	if len(model.Headers) > 0 || len(providerHeaders) > 0 || len(modelHeaders) > 0 {
		headers = make(map[string]string)
		for k, v := range model.Headers {
			headers[k] = v
		}
		for k, v := range providerHeaders {
			headers[k] = v
		}
		for k, v := range modelHeaders {
			headers[k] = v
		}
	}

	if providerConfig != nil && providerConfig.AuthHeader {
		if apiKey == "" {
			return &ResolvedRequestAuth{Ok: false, Error: fmt.Sprintf("No API key found for %q", string(model.Provider))}
		}
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["Authorization"] = "Bearer " + apiKey
	}

	return &ResolvedRequestAuth{Ok: true, ApiKey: apiKey, Headers: headers}
}

func (mr *ModelRegistry) GetProviderAuthStatus(provider string) AuthStatus {
	authStatus := mr.authStorage.GetAuthStatus(provider)
	if authStatus.Source != "" {
		return authStatus
	}

	providerApiKey := ""
	if cfg, ok := mr.providerRequestConfigs[provider]; ok {
		providerApiKey = cfg.ApiKey
	}
	if providerApiKey == "" {
		return authStatus
	}

	if strings.HasPrefix(providerApiKey, "!") {
		return AuthStatus{Configured: true, Source: "models_json_command"}
	}

	if os.Getenv(providerApiKey) != "" {
		return AuthStatus{Configured: true, Source: "environment", Label: providerApiKey}
	}

	return AuthStatus{Configured: true, Source: "models_json_key"}
}

func (mr *ModelRegistry) GetProviderDisplayName(provider string) string {
	if reg, ok := mr.registeredProviders[provider]; ok && reg.Name != "" {
		return reg.Name
	}

	for _, oauthProvider := range mr.authStorage.GetOAuthProviders() {
		if oauthProvider.ID() == provider {
			return oauthProvider.Name()
		}
	}

	if name, ok := BuiltInProviderDisplayNames[provider]; ok {
		return name
	}

	return provider
}

func (mr *ModelRegistry) GetApiKeyForProvider(ctx context.Context, provider string) (string, error) {
	falseVal := false
	apiKey, _ := mr.authStorage.GetApiKey(ctx, provider, &falseVal)
	if apiKey != "" {
		return apiKey, nil
	}

	providerApiKey := ""
	if cfg, ok := mr.providerRequestConfigs[provider]; ok {
		providerApiKey = cfg.ApiKey
	}
	if providerApiKey != "" {
		return ResolveConfigValueUncached(providerApiKey), nil
	}
	return "", nil
}

func (mr *ModelRegistry) IsUsingOAuth(model *llm.Model[llm.Api]) bool {
	cred := mr.authStorage.Get(string(model.Provider))
	return cred != nil && cred.IsOAuth()
}

func (mr *ModelRegistry) RegisterProvider(providerName string, config *ProviderConfigInput) error {
	if err := mr.validateProviderConfig(providerName, config); err != nil {
		return err
	}
	mr.applyProviderConfig(providerName, config)
	mr.upsertRegisteredProvider(providerName, config)
	return nil
}

func (mr *ModelRegistry) UnregisterProvider(providerName string) {
	if _, ok := mr.registeredProviders[providerName]; !ok {
		return
	}
	delete(mr.registeredProviders, providerName)
	mr.Refresh()
}

// SaveCustomProvider persists a custom provider (with one model) to models.json so
// it survives restarts — RegisterProvider, by contrast, is in-memory only. It reads
// the existing models.json, adds or overwrites the provider entry, and writes it
// back (0o600; parent dir 0o700). The loader requires baseUrl + apiKey for
// non-built-in providers that define models (validateConfig), so both are
// mandatory here. Call Refresh after so the new model becomes available.
func (mr *ModelRegistry) SaveCustomProvider(key, displayName, baseURL, apiKey, api, modelID string) error {
	path := mr.modelsJsonPath
	cfg := modelsConfig{Providers: map[string]*providerConfig{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg) // corrupt/empty file → start fresh
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]*providerConfig{}
	}
	cfg.Providers[key] = &providerConfig{
		Name:    displayName,
		BaseURL: baseURL,
		ApiKey:  apiKey,
		Api:     api,
		Models:  []modelDefinition{{ID: modelID}}, // Name defaults to ID in parseModels
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func (mr *ModelRegistry) upsertRegisteredProvider(providerName string, config *ProviderConfigInput) {
	existing, ok := mr.registeredProviders[providerName]
	if !ok {
		mr.registeredProviders[providerName] = config
		return
	}
	if config.Name != "" {
		existing.Name = config.Name
	}
	if config.BaseURL != "" {
		existing.BaseURL = config.BaseURL
	}
	if config.ApiKey != "" {
		existing.ApiKey = config.ApiKey
	}
	if config.Api != "" {
		existing.Api = config.Api
	}
	if config.Headers != nil {
		existing.Headers = config.Headers
	}
	if config.Models != nil {
		existing.Models = config.Models
	}
}

func (mr *ModelRegistry) validateProviderConfig(providerName string, config *ProviderConfigInput) error {
	if config.StreamSimple != nil && config.Api == "" {
		return fmt.Errorf("Provider %s: \"api\" is required when registering streamSimple", providerName)
	}

	if len(config.Models) == 0 {
		return nil
	}

	if config.BaseURL == "" {
		return fmt.Errorf("Provider %s: \"baseUrl\" is required when defining models", providerName)
	}
	if config.ApiKey == "" && config.OAuth == nil {
		return fmt.Errorf("Provider %s: \"apiKey\" or \"oauth\" is required when defining models", providerName)
	}

	for _, modelDef := range config.Models {
		api := modelDef.Api
		if api == "" {
			api = config.Api
		}
		if api == "" {
			return fmt.Errorf("Provider %s, model %s: no \"api\" specified", providerName, modelDef.ID)
		}
	}

	return nil
}

func (mr *ModelRegistry) applyProviderConfig(providerName string, config *ProviderConfigInput) {
	if config.OAuth != nil {
		oauth.RegisterOAuthProvider(providerName)
	}

	mr.storeProviderRequestConfig(providerName, &providerConfig{
		ApiKey:     config.ApiKey,
		Headers:    config.Headers,
		AuthHeader: config.AuthHeader,
	})

	if len(config.Models) > 0 {
		filtered := make([]*llm.Model[llm.Api], 0, len(mr.models))
		for _, m := range mr.models {
			if string(m.Provider) != providerName {
				filtered = append(filtered, m)
			}
		}
		mr.models = filtered

		for _, modelDef := range config.Models {
			api := modelDef.Api
			if api == "" {
				api = config.Api
			}

			mr.storeModelHeaders(providerName, modelDef.ID, modelDef.Headers)

			baseURL := modelDef.BaseURL
			if baseURL == "" {
				baseURL = config.BaseURL
			}

			mr.models = append(mr.models, &llm.Model[llm.Api]{
				ID:            modelDef.ID,
				Name:          modelDef.Name,
				Api:           llm.Api(api),
				Provider:      llm.Provider(providerName),
				BaseURL:       baseURL,
				Reasoning:     modelDef.Reasoning,
				Input:         toInputTypes(modelDef.Input),
				Cost:          derefCost(modelDef.Cost),
				ContextWindow: modelDef.ContextWindow,
				MaxTokens:     modelDef.MaxTokens,
			})
		}
	} else if config.BaseURL != "" || len(config.Headers) > 0 {
		for i, m := range mr.models {
			if string(m.Provider) == providerName {
				updated := *m
				if config.BaseURL != "" {
					updated.BaseURL = config.BaseURL
				}
				mr.models[i] = &updated
			}
		}
	}
}

func derefCost(c *llm.ModelCost) llm.ModelCost {
	if c == nil {
		return llm.ModelCost{}
	}
	return *c
}
