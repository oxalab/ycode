package main

// This file builds the agent-session runtime: the session-option resolution
// (model/thinking/scoped-models/tools), the createRuntime factory closure, the
// initial-message assembly, and the interactive-mode launch. It is the Go port
// of the runtime half of reference/coding-agent/src/main.ts (buildSessionOptions,
// the createRuntime closure, prepareInitialMessage, and the interactive-mode
// block).
//
// ============================================================================
// Deviations from the plan sketch + honest deferrals
// ============================================================================
//
// buildSessionOptions returns a typed sessionOptionsResult rather than the TS
// { options: CreateAgentSessionOptions; ... } bundle: Go has no union/optional,
// so Model is a *llm.Model[llm.Api] (nil = unset), ThinkingLevel a string (""
// = unset), NoTools a string ("" / "all" / "builtin"). The factory maps these
// onto CreateAgentSessionFromServicesOptions, taking care to avoid the
// typed-nil-in-interface trap: Model/NoTools are passed as untyped nil when
// unset.
//
// initTheme / theme watcher: not wired in this phase; the interactive App runs
// on the default theme (cf. 19.6/19.7).
//
// PI_STARTUP_BENCHMARK fast-path: needs the interactive App's init/stop split
// from Run; deferred.

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui"
	cli "ycode/packages/coding-agent/src/cli"
	llm "ycode/packages/llm/src"
)

// ============================================================================
// sessionOptionsResult — typed analogue of the TS buildSessionOptions return
// ============================================================================

// sessionOptionsResult is what buildSessionOptions produces. The zero value means
// "nothing chosen"; the factory maps the set fields onto
// CreateAgentSessionFromServicesOptions.
type sessionOptionsResult struct {
	Model                *llm.Model[llm.Api]
	ThinkingLevel        string                          // "" = unset
	ScopedModels         []core.ScopedModelEntry
	Tools                []string
	NoTools              string                          // "" | "all" | "builtin"
	CLIThinkingFromModel bool
	Diagnostics          []core.AgentSessionRuntimeDiagnostic
}

// buildSessionOptions resolves the model, thinking level, scoped models, and tool
// gating from parsed args + scoped models + registry + settings (TS
// buildSessionOptions). hasExistingSession suppresses the scoped-model fallback
// when resuming a session that already chose a model.
func buildSessionOptions(
	parsed cli.Args,
	scopedModels []core.ScopedModel,
	hasExistingSession bool,
	registry core.ModelRegistryResolver,
	settingsManager *core.SettingsManager,
) sessionOptionsResult {
	var result sessionOptionsResult

	// Model from CLI (--provider/--model or --model provider/id[:thinking]).
	if parsed.Model != "" {
		resolved := core.ResolveCliModel(parsed.Provider, parsed.Model, registry)
		if resolved.Warning != nil {
			result.Diagnostics = append(result.Diagnostics, core.AgentSessionRuntimeDiagnostic{
				Type: core.DiagnosticWarning, Message: *resolved.Warning,
			})
		}
		if resolved.Error != nil {
			result.Diagnostics = append(result.Diagnostics, core.AgentSessionRuntimeDiagnostic{
				Type: core.DiagnosticError, Message: *resolved.Error,
			})
		}
		if resolved.Model != nil {
			result.Model = resolved.Model
			// Allow "--model <pattern>:<thinking>" shorthand. Explicit --thinking
			// still wins (re-applied below).
			if string(parsed.Thinking) == "" && resolved.ThinkingLevel != nil {
				result.ThinkingLevel = string(*resolved.ThinkingLevel)
				result.CLIThinkingFromModel = true
			}
		}
	}

	// No CLI model + scoped models + fresh session: prefer the saved default when
	// it is in scope, else the first scoped model.
	if result.Model == nil && len(scopedModels) > 0 && !hasExistingSession {
		savedProvider := settingsManager.GetDefaultProvider()
		savedModelID := settingsManager.GetDefaultModel()
		var savedModel *llm.Model[llm.Api]
		if savedProvider != "" && savedModelID != "" {
			savedModel = registry.Find(savedProvider, savedModelID)
		}

		savedInScope := findScopedModel(scopedModels, savedModel)
		if savedInScope != nil {
			result.Model = savedInScope.Model
			if string(parsed.Thinking) == "" && savedInScope.ThinkingLevel != nil {
				result.ThinkingLevel = string(*savedInScope.ThinkingLevel)
			}
		} else {
			first := scopedModels[0]
			result.Model = first.Model
			if string(parsed.Thinking) == "" && first.ThinkingLevel != nil {
				result.ThinkingLevel = string(*first.ThinkingLevel)
			}
		}
	}

	// Explicit --thinking takes precedence over any model-derived thinking level.
	if string(parsed.Thinking) != "" {
		result.ThinkingLevel = string(parsed.Thinking)
	}

	// Scoped models for Ctrl+P cycling. A nil ThinkingLevel on an entry means
	// "inherit the current session thinking level" while cycling (left empty).
	if len(scopedModels) > 0 {
		result.ScopedModels = make([]core.ScopedModelEntry, 0, len(scopedModels))
		for _, sm := range scopedModels {
			entry := core.ScopedModelEntry{Model: sm.Model}
			if sm.ThinkingLevel != nil {
				entry.ThinkingLevel = string(*sm.ThinkingLevel)
			}
			result.ScopedModels = append(result.ScopedModels, entry)
		}
	}

	// Tool gating.
	if parsed.NoTools {
		result.NoTools = "all"
	} else if parsed.NoBuiltinTools {
		result.NoTools = "builtin"
	}
	if len(parsed.Tools) > 0 {
		result.Tools = append([]string(nil), parsed.Tools...)
	}

	return result
}

// findScopedModel returns the scoped model equal to target (TS
// scopedModels.find(modelsAreEqual)), or nil. Extracted so the saved-default
// lookup is testable in isolation.
func findScopedModel(scopedModels []core.ScopedModel, target *llm.Model[llm.Api]) *core.ScopedModel {
	if target == nil {
		return nil
	}
	for i := range scopedModels {
		if llm.ModelsAreEqual(scopedModels[i].Model, target) {
			return &scopedModels[i]
		}
	}
	return nil
}

// ============================================================================
// createRuntimeFactory — TS createRuntime closure
// ============================================================================

// createRuntimeFactory returns the cwd-bound runtime factory the SDK drives. It
// closes over the parsed args, the resolved resource paths, and the shared auth
// storage (shared so a CLI --api-key can be injected before the session is
// created). Mirrors the createRuntime closure in the TS main().
func createRuntimeFactory(
	parsed cli.Args,
	resolvedExtensionPaths, resolvedSkillPaths, resolvedPromptTemplatePaths, resolvedThemePaths []string,
	authStorage *core.AuthStorage,
) core.CreateAgentSessionRuntimeFactory {
	return func(opts core.CreateAgentSessionRuntimeFactoryOptions) (*core.CreateAgentSessionRuntimeResult, error) {
		services, err := core.CreateAgentSessionServices(core.CreateAgentSessionServicesOptions{
			Cwd:                 opts.Cwd,
			AgentDir:            opts.AgentDir,
			AuthStorage:         authStorage,
			ExtensionFlagValues: parsed.UnknownFlags,
			ResourceLoaderOptions: &core.DefaultResourceLoaderOptions{
				AdditionalExtensionPaths: resolvedExtensionPaths,
				AdditionalSkillPaths:     resolvedSkillPaths,
				AdditionalPromptPaths:    resolvedPromptTemplatePaths,
				AdditionalThemePaths:     resolvedThemePaths,
				NoExtensions:             parsed.NoExtensions,
				NoSkills:                 parsed.NoSkills,
				NoPromptTemplates:        parsed.NoPromptTemplates,
				NoThemes:                 parsed.NoThemes,
				NoContextFiles:           parsed.NoContextFiles,
				SystemPrompt:             parsed.SystemPrompt,
				AppendSystemPrompt:       parsed.AppendSystemPrompt,
			},
		})
		if err != nil {
			return nil, err
		}

		// Collect diagnostics: services + settings + per-extension load errors.
		diagnostics := append([]core.AgentSessionRuntimeDiagnostic(nil), services.Diagnostics...)
		diagnostics = append(diagnostics, collectSettingsDiagnostics(services.SettingsManager, "runtime creation")...)
		if exts := services.ResourceLoader.GetExtensions(); exts != nil {
			for _, le := range exts.Errors {
				diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    core.DiagnosticError,
					Message: fmt.Sprintf("Failed to load extension %q: %s", le.Path, le.Error),
				})
			}
		}

		// Scoped models: explicit --models, else settings-enabled models.
		var modelPatterns []string
		if len(parsed.Models) > 0 {
			modelPatterns = parsed.Models
		} else {
			modelPatterns = services.SettingsManager.GetEnabledModels()
		}
		var scopedModels []core.ScopedModel
		if len(modelPatterns) > 0 {
			scopedModels, err = core.ResolveModelScope(modelPatterns, services.ModelRegistry)
			if err != nil {
				return nil, err
			}
		}

		hasExistingSession := len(opts.SessionManager.BuildSessionContext().Messages) > 0
		sessOpts := buildSessionOptions(parsed, scopedModels, hasExistingSession,
			services.ModelRegistry, services.SettingsManager)
		diagnostics = append(diagnostics, sessOpts.Diagnostics...)

		// --api-key: inject into the shared auth storage once a model is known.
		if parsed.APIKey != "" {
			if sessOpts.Model == nil {
				diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    core.DiagnosticError,
					Message: "--api-key requires a model to be specified via --model, --provider/--model, or --models",
				})
			} else {
				authStorage.SetRuntimeApiKey(string(sessOpts.Model.Provider), parsed.APIKey)
			}
		}

		// Model/NoTools are passed as untyped nil when unset so the services
		// constructor's nil checks behave (avoids typed-nil-in-interface).
		var modelArg any
		if sessOpts.Model != nil {
			modelArg = sessOpts.Model
		}
		var noToolsArg any
		if sessOpts.NoTools != "" {
			noToolsArg = sessOpts.NoTools
		}

		created, err := core.CreateAgentSessionFromServices(core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    opts.SessionManager,
			SessionStartEvent: opts.SessionStartEvent,
			Model:             modelArg,
			ThinkingLevel:     sessOpts.ThinkingLevel,
			ScopedModels:      sessOpts.ScopedModels,
			Tools:             sessOpts.Tools,
			NoTools:           noToolsArg,
		})
		if err != nil {
			return nil, err
		}

		// When the CLI (or a model shorthand) requested a thinking level, re-apply
		// the session's resolved level so it is clamped/recorded (TS main).
		cliThinkingOverride := string(parsed.Thinking) != "" || sessOpts.CLIThinkingFromModel
		if cliThinkingOverride && created.Session.GetModel().ID != "" {
			created.Session.SetThinkingLevel(created.Session.GetThinkingLevel())
		}

		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *created,
			Services:                 services,
			Diagnostics:              diagnostics,
		}, nil
	}
}

// ============================================================================
// prepareInitialMessage — TS prepareInitialMessage
// ============================================================================

// prepareInitialMessage assembles the initial prompt from stdin + @file text +
// the first positional message (TS prepareInitialMessage). It mutates parsed:
// BuildInitialMessage shifts the first positional message off parsed.Messages
// (it becomes the initial message), leaving the rest as follow-up turns.
//
// autoResizeImages defaults to true in the caller because SettingsManager has no
// GetImageAutoResize accessor yet (deferred); cwd threads the working directory
// for relative @file resolution (ProcessFileArguments takes it explicitly).
func prepareInitialMessage(parsed *cli.Args, cwd string, autoResizeImages bool, stdinContent string) (cli.InitialMessageResult, error) {
	if len(parsed.FileArgs) == 0 {
		return cli.BuildInitialMessage(cli.InitialMessageInput{
			Parsed:       parsed,
			StdinContent: stdinContent,
		}), nil
	}
	text, images, err := cli.ProcessFileArguments(parsed.FileArgs, cwd,
		&cli.ProcessFileOptions{AutoResizeImages: autoResizeImages})
	if err != nil {
		return cli.InitialMessageResult{}, err
	}
	return cli.BuildInitialMessage(cli.InitialMessageInput{
		Parsed:       parsed,
		FileText:     text,
		FileImages:   images,
		StdinContent: stdinContent,
	}), nil
}

// ============================================================================
// Interactive launch — TS interactive-mode block
// ============================================================================

// launchInteractive starts the interactive TUI App over the runtime (TS
// interactive-mode block). The scoped-models banner is printed first when there
// are scoped models and startup is not quiet.
func launchInteractive(
	parsed cli.Args,
	runtime *core.AgentSessionRuntime,
	initial cli.InitialMessageResult,
	modelFallbackMessage string,
	migratedProviders []string,
	settingsManager *core.SettingsManager,
) {
	scopedModels := runtime.Session().GetScopedModels()
	if len(scopedModels) > 0 && (parsed.Verbose || !settingsManager.GetQuietStartup()) {
		printScopedModelsBanner(scopedModels)
	}

	// The pi-tui-based interactive TUI (packages/coding-agent/src/interactive)
	// is the DEFAULT. YCODE_TUI=bt opts back into the legacy Bubble-Tea TUI,
	// which stays available until it is deleted in a separate change after a
	// soak (P8).
	if os.Getenv("YCODE_TUI") != "bt" {
		launchInteractivePi(parsed, runtime, initial, modelFallbackMessage, migratedProviders, settingsManager)
		return
	}

	// AppOptions.InitialImages is []any; widen the initial-message images
	// ([]*llm.ImageContent) via the shared helper.
	initialImages := toAnyImages(initial.Images)

	app := tui.NewApp(runtime, tui.AppOptions{
		MigratedProviders:    migratedProviders,
		ModelFallbackMessage: modelFallbackMessage,
		InitialMessage:       initial.InitialMessage,
		InitialImages:        initialImages,
		InitialMessages:      parsed.Messages,
		Verbose:              parsed.Verbose,
	})

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	app.SetProgram(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, ansiRed+"Error: "+err.Error()+ansiReset)
		os.Exit(1)
	}
}

// printScopedModelsBanner prints the "Model scope: ..." dim line (TS
// interactive-mode scoped-models banner). ScopedModelEntry.Model is any; it
// holds a *llm.Model[llm.Api].
func printScopedModelsBanner(scopedModels []core.ScopedModelEntry) {
	parts := make([]string, 0, len(scopedModels))
	for _, sm := range scopedModels {
		id := ""
		if m, ok := sm.Model.(*llm.Model[llm.Api]); ok && m != nil {
			id = m.ID
		}
		if sm.ThinkingLevel != "" {
			id += ":" + sm.ThinkingLevel
		}
		parts = append(parts, id)
	}
	fmt.Println(ansiDim + "Model scope: " + strings.Join(parts, ", ") + " " +
		ansiGray + "(Ctrl+P to cycle)" + ansiReset)
}
