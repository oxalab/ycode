package ycode

import (
	"fmt"
	"path/filepath"
	"strings"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// --- Types ---

type AgentSessionRuntimeDiagnosticType string

const (
	DiagnosticInfo 	AgentSessionRuntimeDiagnosticType = "string"
	DiagnosticWarning AgentSessionRuntimeDiagnosticType = "warning"
	DiagnosticError AgentSessionRuntimeDiagnosticType = "error"
)

type AgentSessionRuntimeDiagnostic struct {
	Type 	AgentSessionRuntimeDiagnosticType `json:"type"`
	Message string 							  `json:"message"`
}

/* CreateAgentSessionServiceOptions: Configures cwd-bound runtime services.
 CLI-provided resource paths should be resolved to absolute paths before
 reaching this function - later switches will not reinterpret them. */
type CreateAgentSessionServicesOptions struct {
	Cwd 		string
	AgentDir 	string
	AuthStorage *AuthStorage
	SettingsManager *SettingsManager
	ModelRegistry *ModelRegistry
	ExtensionFlagValues map[string]any
	ResourceLoaderOptions *DefaultResourceLoaderOptions
}

/* AgentSessionServices: Bundle of cwd-bound runtime services.
Infrastructure only - AgentSession itself is created separately. */
type AgentSessionServices struct {
	Cwd 			string
	AgentDir 		string
	AuthStorage 	*AuthStorage
	SettingsManager *SettingsManager
	ModelRegistry 	*ModelRegistry
	ResourceLoader 	ResourceLoader
	Diagnostics 	[]AgentSessionRuntimeDiagnostic
}

/* CreateAgentSessionFromServiceOptions: Input for creating an
 AgentSession from already-created services. */
type CreateAgentSessionFromServicesOptions struct {
	Services 		*AgentSessionServices
	SessionManager 	*SessionManager
	SessionStartEvent *extensions.SessionStartEvent
	Model 			any // *llm.Model[llm.Api]
	ThinkingLevel 	string
	ScopedModels 	[]ScopedModelEntry
	Tools 			[]string
	NoTools 		any // "all" | "builtin"
	CustomTools		[]extensions.ToolDefinition
}

// ScopedModelEntry: One entry in the scoped-models list.
type ScopedModelEntry struct {
	Model 			any
	ThinkingLevel 	string
}

// CreateAgentSessionResult is defined in sdk.go (Step 1).

// --- applyExtensionFlagValues ---
func applyExtensionFlagValues(
	resourceLoader ResourceLoader,
	extensionFlagValues map[string]any,
) []AgentSessionRuntimeDiagnostic {
	if len(extensionFlagValues) == 0 {
		return nil
	}
	var diagnostics []AgentSessionRuntimeDiagnostic
	extensionsResult := resourceLoader.GetExtensions()

	// Build registry of known flags from all loaded extensions
	registeredFlags := make(map[string]string) // name -> type ("boolean" or "string")
	for _, ext := range extensionsResult.Extensions {
		for name, flag := range ext.Flags {
			registeredFlags[name] = flag.Type
		}
	}

	// Apply each provided flag value
	var unknownFlags []string
	for name, value := range extensionFlagValues {
		flagType, ok := registeredFlags[name]
		if !ok {
			unknownFlags = append(unknownFlags, name)
			continue
		}
		if flagType == "boolean" {
			extensionsResult.Runtime.FlagValues[name] = true
		}
		if s, isString := value.(string); isString {
			extensionsResult.Runtime.FlagValues[name] = s
			continue
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
			Type: 	DiagnosticError,
			Message: fmt.Sprintf("Extension flag \"--%s\" requires a value", name),
		})
	}
	
	if len(unknownFlags) > 0 {
		suffix := "s"
		if len(unknownFlags) == 1 {
			suffix = ""
		}
		flagList := make([]string, len(unknownFlags))
		for i, name := range unknownFlags {
			flagList[i] = "--" + name
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    DiagnosticError,
			Message: fmt.Sprintf("Unknown option%s: %s", suffix, strings.Join(flagList, ", ")),
		})
	}
	return diagnostics
}

// ============================================================================
// CreateAgentSessionServices
// ============================================================================

/* CreateAgentSessionServices: Creates cwd-bound runtime services.
 Returns services plus diagnostics. Does NOT create an AgentSession. */
func CreateAgentSessionServices(opts CreateAgentSessionServicesOptions) (*AgentSessionServices, error) {
	cwd := opts.Cwd
	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = GetAgentDir()
	}
	authStorage := opts.AuthStorage
	if authStorage == nil {
		authStorage = NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	}
	settingsManager := opts.SettingsManager
	if settingsManager == nil {
		settingsManager = NewSettingsManagerFromDirs(cwd, agentDir)
	}
	modelRegistry := opts.ModelRegistry
	if modelRegistry == nil {
		modelRegistry = NewModelRegistry(authStorage, filepath.Join(agentDir, "models.json"))
	}
	// Build resource loader options
	loaderOpts := DefaultResourceLoaderOptions{
		Cwd: 		cwd,
		AgentDir: 	agentDir,
		SettingsManager: settingsManager,
	}
	if opts.ResourceLoaderOptions != nil {
		// Overlay non-conflicting fields from caller
		loaderOpts.AdditionalExtensionPaths = opts.ResourceLoaderOptions.AdditionalExtensionPaths
		loaderOpts.AdditionalSkillPaths = opts.ResourceLoaderOptions.AdditionalSkillPaths
		loaderOpts.AdditionalPromptPaths = opts.ResourceLoaderOptions.AdditionalPromptPaths
		loaderOpts.AdditionalThemePaths = opts.ResourceLoaderOptions.AdditionalThemePaths
		loaderOpts.ExtensionFactories = opts.ResourceLoaderOptions.ExtensionFactories
		loaderOpts.NoExtensions = opts.ResourceLoaderOptions.NoExtensions
		loaderOpts.NoSkills = opts.ResourceLoaderOptions.NoSkills
		loaderOpts.NoPromptTemplates = opts.ResourceLoaderOptions.NoPromptTemplates
		loaderOpts.NoThemes = opts.ResourceLoaderOptions.NoThemes
		loaderOpts.NoContextFiles = opts.ResourceLoaderOptions.NoContextFiles
		loaderOpts.SystemPrompt = opts.ResourceLoaderOptions.SystemPrompt
		loaderOpts.AppendSystemPrompt = opts.ResourceLoaderOptions.AppendSystemPrompt
		loaderOpts.ExtensionsOverride = opts.ResourceLoaderOptions.ExtensionsOverride
		loaderOpts.SkillsOverride = opts.ResourceLoaderOptions.SkillsOverride
		loaderOpts.PromptsOverride = opts.ResourceLoaderOptions.PromptsOverride
		loaderOpts.ThemesOverride = opts.ResourceLoaderOptions.ThemesOverride
		loaderOpts.AgentsFilesOverride = opts.ResourceLoaderOptions.AgentsFilesOverride
		loaderOpts.SystemPromptOverride = opts.ResourceLoaderOptions.SystemPromptOverride
		loaderOpts.AppendSystemPromptOverride = opts.ResourceLoaderOptions.AppendSystemPromptOverride
		loaderOpts.PackageManager = opts.ResourceLoaderOptions.PackageManager
	}

	resourceLoader := NewDefaultResourceLoader(loaderOpts)
	if err := resourceLoader.Reload(); err != nil {
		return nil, fmt.Errorf("resource loader reload failed: %w", err)
	}
	var diagnostics []AgentSessionRuntimeDiagnostic
	extensionsResult := resourceLoader.GetExtensions()
	// Drain pending provider registrations from extensions into the model registry
	for _, reg := range extensionsResult.Runtime.PendingProviderRegistrations {
		if err := modelRegistry.RegisterProvider(reg.Name, providerConfigToInput(reg.Config)); err != nil {
			diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    DiagnosticError,
				Message: fmt.Sprintf("Extension \"%s\" error: %s", reg.ExtensionPath, err.Error()),
			})
		}
	}
	extensionsResult.Runtime.PendingProviderRegistrations = nil
	// Apply extension flag values
	diagnostics = append(diagnostics, applyExtensionFlagValues(resourceLoader, opts.ExtensionFlagValues)...)

	return &AgentSessionServices{
		Cwd: 		cwd,
		AgentDir: 	agentDir,
		AuthStorage: authStorage,
		SettingsManager: settingsManager,
		ModelRegistry: modelRegistry,
		ResourceLoader: resourceLoader,
		Diagnostics: diagnostics,
	}, nil
}

// ============================================================================
// CreateAgentSessionFromServices
// ============================================================================

/* CreateAgentSessionFromServices: Creates an AgentSession from previously created
 services. Mirrors TS createAgentSessionFromServices (agent-session-services.ts:179-198). */
func CreateAgentSessionFromServices(opts CreateAgentSessionFromServicesOptions) (*CreateAgentSessionResult, error) {
	var model *llm.Model[llm.Api]
	if opts.Model != nil {
		if m, ok := opts.Model.(*llm.Model[llm.Api]); ok {
			model = m
		}
	}

	var scopedModels []ScopedModelEntry
	if opts.ScopedModels != nil {
		scopedModels = opts.ScopedModels
	}

	var noTools string
	if opts.NoTools != nil {
		if s, ok := opts.NoTools.(string); ok {
			noTools = s
		}
	}

	var tools []string
	if opts.Tools != nil {
		tools = opts.Tools
	}

	var customTools []extensions.ToolDefinition
	if opts.CustomTools != nil {
		customTools = opts.CustomTools
	}

	var thinkingLevel sdk.ThinkingLevel
	if opts.ThinkingLevel != "" {
		thinkingLevel = sdk.ThinkingLevel(opts.ThinkingLevel)
	}

	return CreateAgentSession(CreateAgentSessionOptions{
		Cwd:               opts.Services.Cwd,
		AgentDir:          opts.Services.AgentDir,
		AuthStorage:       opts.Services.AuthStorage,
		ModelRegistry:     opts.Services.ModelRegistry,
		ResourceLoader:    opts.Services.ResourceLoader,
		SessionManager:    opts.SessionManager,
		Model:             model,
		ThinkingLevel:     thinkingLevel,
		ScopedModels:      scopedModels,
		Tools:             tools,
		NoTools:           noTools,
		CustomTools:       customTools,
		SessionStartEvent: opts.SessionStartEvent,
	})
}

// ============================================================================
// Helpers
// ============================================================================


/* providerConfigToInput: Converts an extensions's ProviderConfig to the
 ProviderConfigInput expected by ModelRegistry.RegisterProvider. */
func providerConfigToInput(config *extensions.ProviderConfig) *ProviderConfigInput {
	if config == nil {
		return nil
	}
	out := &ProviderConfigInput{
		Name:      config.Name,
		BaseURL:   config.BaseURL,
		ApiKey:    config.ApiKey,
		Api:       config.Api,
		Headers:   config.Headers,
		AuthHeader: config.AuthHeader,
	}

	// Convert models
	for _, m := range config.Models {
		if m == nil {
			continue
		}
		out.Models = append(out.Models, ProviderModelDef{
			ID:               m.ID,
			Name:             m.Name,
			Api:              m.Api,
			BaseURL:          m.BaseURL,
			Reasoning:        m.Reasoning,
			ThinkingLevelMap: m.ThinkingLevelMap,
			Input:            m.Input,
			Cost:             m.Cost,
			ContextWindow:    m.ContextWindow,
		})
	}

	// OAuth: extension provides `any` (often a config map). ModelRegistry
	// expects *OAuthProviderConfig. Until extensions can produce a fully
	// formed OAuthProviderConfig, this conversion is best-effort and may
	// be nil for providers registered via extensions.
	if oauth, ok := config.OAuth.(*OAuthProviderConfig); ok {
		out.OAuth = oauth
	}
	return out
}

/* KEYFLAGS:
1   Model/ThinkingLevel use any/string	TS uses Model<any> from @earendil-works/pi-ai and ThinkingLevel enum. Until your llm package types are wired in, these are placeholders.
2	CreateAgentSessionFromServices is a stub	It depends on createAgentSession from sdk.ts (Phase 13). Marked TODO — full implementation comes with sdk migration.
3	providerConfigToInput is a stub	Need to check field-by-field compatibility between extensions.ProviderConfig and ProviderConfigInput. Let me verify — both should have the same shape since they're both model registration payloads:

*/