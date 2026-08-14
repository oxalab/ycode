package ycode

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// ============================================================================
// Types — mirrors reference/coding-agent/src/core/sdk.ts lines 33-90
// ============================================================================

// CreateAgentSessionOptions holds all inputs for creating an AgentSession.
type CreateAgentSessionOptions struct {
	// Working directory for project-local discovery.
	Cwd string
	// Global config directory. Default: GetAgentDir().
	AgentDir string

	// Auth storage for credentials. Default: NewAuthStorage(agentDir/auth.json).
	AuthStorage *AuthStorage
	// Model registry. Default: NewModelRegistry(authStorage, agentDir/models.json).
	ModelRegistry *ModelRegistry

	// Model to use. Default: from settings, then first available.
	Model *llm.Model[llm.Api]
	// Thinking level. Default: from settings, then DefaultThinkingLevel (clamped to model).
	ThinkingLevel sdk.ThinkingLevel
	// Models available for cycling (Ctrl+P in interactive mode).
	ScopedModels []ScopedModelEntry

	// Optional default tool suppression mode.
	// "all": start with no tools enabled.
	// "builtin": disable default built-in tools but keep extension/custom tools.
	NoTools string
	// Optional allowlist of tool names. When provided, only listed tools are enabled.
	Tools []string
	// Custom tools to register in addition to built-in tools.
	CustomTools []extensions.ToolDefinition

	// Resource loader. When nil, DefaultResourceLoader is used.
	ResourceLoader ResourceLoader
	// Session manager. Default: NewSessionManager(cwd, GetDefaultSessionDir(cwd, agentDir)).
	SessionManager *SessionManager
	// Settings manager. Default: NewSettingsManagerFromDirs(cwd, agentDir).
	SettingsManager *SettingsManager
	// Session start event metadata for extension runtime startup.
	SessionStartEvent *extensions.SessionStartEvent
}

// CreateAgentSessionResult is returned by CreateAgentSession.
// Mirrors TS CreateAgentSessionResult (sdk.ts lines 83-90).
type CreateAgentSessionResult struct {
	Session              *AgentSession
	ExtensionsResult     *extensions.LoadExtensionsResult
	ModelFallbackMessage string
}

// ============================================================================
// getAttributionHeaders — mirrors sdk.ts lines 128-156
// ============================================================================

func getAttributionHeaders(model llm.Model[llm.Api], sm *SettingsManager) map[string]string {
	if !IsInstallTelemetryEnabled(sm) {
		return nil
	}

	provider := string(model.Provider)
	baseURL := model.BaseURL

	if provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai") {
		return map[string]string{
			"HTTP-Referer":       "https://pi.dev",
			"X-OpenRouter-Title": "pi",
			"X-OpenRouter-Categories": "cli-agent",
		}
	}

	if provider == "cloudflare-workers-ai" ||
		provider == "cloudflare-ai-gateway" ||
		strings.Contains(baseURL, "api.cloudflare.com") ||
		strings.Contains(baseURL, "gateway.ai.cloudflare.com") {
		return map[string]string{
			"User-Agent": "pi-coding-agent",
		}
	}

	return nil
}

// ============================================================================
// CreateAgentSession — mirrors sdk.ts lines 193-413
// ============================================================================

func CreateAgentSession(options CreateAgentSessionOptions) (*CreateAgentSessionResult, error) {
	// 1. Resolve defaults
	cwd := options.Cwd
	if cwd == "" && options.SessionManager != nil {
		cwd = options.SessionManager.GetCwd()
	}

	agentDir := options.AgentDir
	if agentDir == "" {
		agentDir = GetAgentDir()
	}

	// 2. Create or reuse services
	var authStorage *AuthStorage
	if options.AuthStorage != nil {
		authStorage = options.AuthStorage
	} else {
		authPath := filepath.Join(agentDir, "auth.json")
		authStorage = NewAuthStorage(authPath)
	}

	var modelRegistry *ModelRegistry
	if options.ModelRegistry != nil {
		modelRegistry = options.ModelRegistry
	} else {
		modelsPath := filepath.Join(agentDir, "models.json")
		modelRegistry = NewModelRegistry(authStorage, modelsPath)
	}

	var settingsManager *SettingsManager
	if options.SettingsManager != nil {
		settingsManager = options.SettingsManager
	} else {
		settingsManager = NewSettingsManagerFromDirs(cwd, agentDir)
	}

	var sessionManager *SessionManager
	if options.SessionManager != nil {
		sessionManager = options.SessionManager
	} else {
		sessionManager = NewSessionManager(cwd, GetDefaultSessionDir(cwd, agentDir))
	}

	// 3. Resource loader
	var resourceLoader ResourceLoader
	if options.ResourceLoader != nil {
		resourceLoader = options.ResourceLoader
	} else {
		rl := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SettingsManager: settingsManager,
		})
		if err := rl.Reload(); err != nil {
			return nil, fmt.Errorf("resource loader reload failed: %w", err)
		}
		resourceLoader = rl
	}

	// 4. Check if session has existing data to restore
	existingSession := sessionManager.BuildSessionContext()
	hasExistingSession := len(existingSession.Messages) > 0

	// Check for thinking_level_change entries in the branch
	hasThinkingEntry := false
	for _, entry := range sessionManager.GetBranch("") {
		if entry.Type == "thinking_level_change" {
			hasThinkingEntry = true
			break
		}
	}

	// 5. Resolve model
	var model *llm.Model[llm.Api]
	if options.Model != nil {
		model = options.Model
	}
	var modelFallbackMessage string

	// Try to restore model from existing session
	if model == nil && hasExistingSession && existingSession.Model != nil {
		restored := modelRegistry.Find(existingSession.Model.Provider, existingSession.Model.ModelID)
		if restored != nil && modelRegistry.HasConfiguredAuth(restored) {
			model = restored
		}
		if model == nil {
			modelFallbackMessage = fmt.Sprintf(
				"Could not restore model %s/%s",
				existingSession.Model.Provider, existingSession.Model.ModelID,
			)
		}
	}

	// Fall back to findInitialModel
	if model == nil {
		defaultProvider := settingsManager.GetDefaultProvider()
		defaultModelID := settingsManager.GetDefaultModel()
		defaultTL := DefaultThinkingLevel
		result, err := FindInitialModel(struct {
			CliProvider         string
			CliModel            string
			ScopedModels        []ScopedModel
			IsContinuing        bool
			DefaultProvider     string
			DefaultModelId      string
			DefaultThinkingLevel *llm.ThinkingLevel
			Registry            ModelRegistryResolver
		}{
			DefaultProvider:      defaultProvider,
			DefaultModelId:       defaultModelID,
			DefaultThinkingLevel: &defaultTL,
			IsContinuing:         hasExistingSession,
			Registry:             modelRegistry,
		})
		if err == nil && result != nil && result.Model != nil {
			model = result.Model
		}
		if model == nil {
			modelFallbackMessage = FormatNoModelIsAvailableMessage()
		} else if modelFallbackMessage != "" {
			modelFallbackMessage += fmt.Sprintf(". Using %s/%s", model.Provider, model.ID)
		}
	}

	// 6. Resolve thinking level
	var thinkingLevel llm.ThinkingLevel
	if options.ThinkingLevel != "" {
		thinkingLevel = llm.ThinkingLevel(options.ThinkingLevel)
	}

	if thinkingLevel == "" && hasExistingSession {
		if hasThinkingEntry {
			thinkingLevel = llm.ThinkingLevel(existingSession.ThinkingLevel)
		} else {
			tl := settingsManager.GetDefaultThinkingLevel()
			if tl != "" {
				thinkingLevel = llm.ThinkingLevel(tl)
			} else {
				thinkingLevel = llm.ThinkingLevel(DefaultThinkingLevel)
			}
		}
	}

	if thinkingLevel == "" {
		tl := settingsManager.GetDefaultThinkingLevel()
		if tl != "" {
			thinkingLevel = llm.ThinkingLevel(tl)
		} else {
			thinkingLevel = llm.ThinkingLevel(DefaultThinkingLevel)
		}
	}

	// Clamp to model capabilities
	if model == nil {
		thinkingLevel = llm.ThinkingLevel(sdk.ThinkingOff)
	}
	// Note: model-specific clamp happens when GetSupportedThinkingLevels is available.
	// For now ClampThinkingLevel with empty levels falls back to the requested level.

	// 7. Determine active tool names
	var defaultActiveToolNames = []string{"read", "bash", "edit", "write"}
	var initialActiveToolNames []string
	if options.Tools != nil {
		initialActiveToolNames = make([]string, len(options.Tools))
		copy(initialActiveToolNames, options.Tools)
	} else if options.NoTools != "" {
		initialActiveToolNames = []string{}
	} else {
		initialActiveToolNames = make([]string, len(defaultActiveToolNames))
		copy(initialActiveToolNames, defaultActiveToolNames)
	}

	var allowedToolNames []string
	if options.Tools != nil {
		allowedToolNames = options.Tools
	} else if options.NoTools == "all" {
		allowedToolNames = []string{}
	}

	// 8. Build convertToLlm wrapper with image blocking
	convertToLlmFn := func(messages []sdk.AgentMessage) ([]llm.Message, error) {
		// Convert to []any for our ConvertToLlm
		anyMsgs := make([]any, len(messages))
		for i, m := range messages {
			anyMsgs[i] = m
		}
		converted := ConvertToLlm(anyMsgs)

		// Check setting dynamically so mid-session changes take effect
		if !settingsManager.GetBlockImages() {
			return converted, nil
		}

		// Filter out ImageContent from all messages
		filtered := make([]llm.Message, len(converted))
		for i, msg := range converted {
			filtered[i] = filterImagesFromMessage(msg)
		}
		return filtered, nil
	}

	// 9. Extension runner ref (shared between Agent and AgentSession)
	extensionRunnerRef := &ExtensionRunnerRef{}

	// 10. Build the stream function
	streamFn := func(ctx context.Context, m llm.Model[llm.Api], chatCtx llm.ChatContext, opts *llm.SimpleStreamOptions) <-chan llm.AssistantMessageEvent {
		// Get API key and headers
		auth := modelRegistry.GetApiKeyAndHeaders(ctx, &m)
		if !auth.Ok {
			errMsg := fmt.Sprintf("auth error: %s", auth.Error)
			ch := make(chan llm.AssistantMessageEvent, 1)
			ch <- llm.AssistantMessageEvent{
				Type:  "error",
				Error: &llm.AssistantMessage{ErrorMessage: errMsg},
			}
			close(ch)
			return ch
		}

		providerRetry := settingsManager.GetProviderRetrySettings()

		// Build merged options
		streamOpts := llm.SimpleStreamOptions{}
		if opts != nil {
			streamOpts = *opts
		}

		// Set auth and retry on the embedded StreamOptions
		streamOpts.APIKey = auth.ApiKey
		if streamOpts.TimeoutMs == 0 && providerRetry.TimeoutMs != nil {
			streamOpts.TimeoutMs = *providerRetry.TimeoutMs
		}
		if streamOpts.MaxRetries == nil {
			streamOpts.MaxRetries = providerRetry.MaxRetries
		}
		if streamOpts.MaxRetryDelayMs == 0 && providerRetry.MaxRetryDelayMs != nil {
			streamOpts.MaxRetryDelayMs = *providerRetry.MaxRetryDelayMs
		}

		// Merge headers: attribution + auth + options
		headers := make(map[string]string)
		if attrHeaders := getAttributionHeaders(m, settingsManager); attrHeaders != nil {
			for k, v := range attrHeaders {
				headers[k] = v
			}
		}
		if auth.Headers != nil {
			for k, v := range auth.Headers {
				headers[k] = v
			}
		}
		if streamOpts.Headers != nil {
			for k, v := range streamOpts.Headers {
				headers[k] = v
			}
		}
		if len(headers) > 0 {
			streamOpts.Headers = headers
		}

		// Dispatch to the registered API provider
		provider := llm.GetApiProvider(m.Api)
		if provider == nil || provider.StreamSimple == nil {
			errMsg := fmt.Sprintf("no stream provider registered for api: %s", m.Api)
			ch := make(chan llm.AssistantMessageEvent, 1)
			ch <- llm.AssistantMessageEvent{
				Type:  "error",
				Error: &llm.AssistantMessage{ErrorMessage: errMsg},
			}
			close(ch)
			return ch
		}

		return provider.StreamSimple(ctx, m, chatCtx, &streamOpts)
	}

	// 11. Build onPayload hook — delegates to extension runner
	onPayloadHook := func(payload []byte) {
		runner := extensionRunnerRef.Current
		if runner == nil || !runner.HasHandlers("before_provider_request") {
			return
		}
		ctx := context.Background()
		runner.EmitBeforeProviderRequest(ctx, payload)
	}

	// 12. Build onResponse hook — delegates to extension runner
	onResponseHook := func(response *llm.StreamOptions) {
		runner := extensionRunnerRef.Current
		if runner == nil || !runner.HasHandlers("after_provider_response") {
			return
		}
		ctx := context.Background()
		runner.Emit(ctx, map[string]any{
			"type":    "after_provider_response",
			"headers": response.Headers,
		})
	}

	// 13. Build transformContext hook
	transformContextHook := func(messages []sdk.AgentMessage, signal context.Context) ([]sdk.AgentMessage, error) {
		runner := extensionRunnerRef.Current
		if runner == nil {
			return messages, nil
		}
		anyMsgs := make([]any, len(messages))
		for i, m := range messages {
			anyMsgs[i] = m
		}
		result, err := runner.EmitContext(signal, anyMsgs)
		if err != nil || result == nil {
			return messages, err
		}
		transformed := make([]sdk.AgentMessage, len(result))
		for i, m := range result {
			if am, ok := m.(sdk.AgentMessage); ok {
				transformed[i] = am
			}
		}
		return transformed, nil
	}

	// 14. Create the Agent
	var modelForAgent llm.Model[llm.Api]
	if model != nil {
		modelForAgent = *model
	}

	agent := sdk.NewAgent(sdk.AgentOptions{
		InititalState: &sdk.PartialAgentState{
			SystemPrompt:  "",
			Model:         &modelForAgent,
			ThinkingLevel: sdk.ThinkingLevel(thinkingLevel),
			Tools:         nil,
		},
		ConvertToLlm:     convertToLlmFn,
		TransformContext:  transformContextHook,
		StreamFn:         streamFn,
		OnPayload:        onPayloadHook,
		OnResponse:       onResponseHook,
		SteeringMode:     sdk.QueueMode(settingsManager.GetSteeringMode()),
		FollowUpMode:     sdk.QueueMode(settingsManager.GetFollowUpMode()),
		SessionID:        sessionManager.GetSessionID(),
		ThinkingBudgets:  convertThinkingBudgets(settingsManager.GetThinkingBudgets()),
		Transport:        llm.Transport(settingsManager.GetTransport()),
		MaxRetryDelayMs:  settingsManager.GetProviderRetryMaxDelayMs(),
	})

	// 15. Restore messages for existing sessions
	if hasExistingSession {
		agent.SetMessages(existingSession.Messages)
		if !hasThinkingEntry {
			sessionManager.AppendThinkingLevelChange(string(thinkingLevel))
		}
	} else {
		// Save initial model and thinking level for new sessions
		if model != nil {
			sessionManager.AppendModelChange(string(model.Provider), model.ID)
		}
		sessionManager.AppendThinkingLevelChange(string(thinkingLevel))
	}

	// 16. Create AgentSession
	session := NewAgentSession(AgentSessionConfig{
		Agent:                agent,
		SubscribeFunc:        agent.Subscribe,
		SessionManager:       sessionManager,
		SettingsManager:      settingsManager,
		Cwd:                  cwd,
		ScopedModels:         options.ScopedModels,
		ResourceLoader:       resourceLoader,
		CustomTools:          options.CustomTools,
		ModelRegistry:        modelRegistry,
		InitialActiveToolNames: initialActiveToolNames,
		AllowedToolNames:     allowedToolNames,
		ExtensionRunnerRef:   extensionRunnerRef,
		SessionStartEvent:    options.SessionStartEvent,
	})

	extensionsResult := resourceLoader.GetExtensions()

	return &CreateAgentSessionResult{
		Session:              session,
		ExtensionsResult:     extensionsResult,
		ModelFallbackMessage: modelFallbackMessage,
	}, nil
}

// ============================================================================
// Helpers
// ============================================================================

// filterImagesFromMessage replaces ImageContent blocks with a text placeholder.
// Mirrors TS convertToLlmWithBlockImages filter logic (sdk.ts lines 288-315).
func filterImagesFromMessage(msg llm.Message) llm.Message {
	// Only filter user messages and tool results
	switch m := msg.(type) {
	case llm.UserMessage:
		m.Content = filterImageBlocks(m.Content)
		return m
	case llm.ToolResultMessage[any]:
		m.Content = filterImageBlocks(m.Content)
		return m
	default:
		return msg
	}
}

// filterImageBlocks replaces ImageContent with text placeholder, deduping consecutive placeholders.
func filterImageBlocks(blocks []llm.ContentBlock) []llm.ContentBlock {
	hasImages := false
	for _, c := range blocks {
		if isImageBlock(c) {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return blocks
	}

	filtered := make([]llm.ContentBlock, 0, len(blocks))
	prevWasDisabled := false
	for _, c := range blocks {
		if isImageBlock(c) {
			if prevWasDisabled {
				continue // dedupe consecutive placeholders
			}
			filtered = append(filtered, llm.TextContent{Type: "text", Text: "Image reading is disabled."})
			prevWasDisabled = true
		} else {
			filtered = append(filtered, c)
			prevWasDisabled = false
		}
	}
	return filtered
}

// isImageBlock checks if a content block is an image type.
func isImageBlock(c llm.ContentBlock) bool {
	switch c.(type) {
	case llm.ImageContent:
		return true
	default:
		return false
	}
}

// convertThinkingBudgets converts settings ThinkingBudgetsSettings to llm.ThinkingBudgets.
func convertThinkingBudgets(tb *ThinkingBudgetsSettings) *llm.ThinkingBudgets {
	if tb == nil {
		return nil
	}
	return &llm.ThinkingBudgets{
		High:   tb.High,
		Medium: tb.Medium,
		Low:    tb.Low,
	}
}
