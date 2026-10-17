package ycode

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tools"
	"ycode/packages/llm/src"
)

// ============================================================================
// Phase 11.6: Extension Wiring
//
// Migrated from reference/coding-agent/src/core/agent-session.ts lines 635-705, 2035-2393
//
// Four concerns:
//  1. emitExtensionEvent — translates agent events to extension events
//  2. bindExtensions — wires UI context, command actions, shutdown, error handler
//  3. bindExtensionCore — provides session API to extensions (tools, commands, model, etc.)
//  4. refreshToolRegistry — rebuilds the tool map from builtins + extension tools
// ============================================================================

// --- Extension Event Dispatch ---

// emitExtensionEvent translates core agent events into extension events.
// Mirrors TS: _emitExtensionEvent() lines 635-705
func (s *AgentSession) emitExtensionEvent(event sdk.AgentEvent) {
	if s.extensionRunner == nil {
		return
	}

	ctx := context.Background()

	switch e := event.(type) {
	case sdk.EventAgentStart:
		s.turnIndex = 0
		s.extensionRunner.Emit(ctx, map[string]any{
			"type": "agent_start",
		})

	case sdk.EventAgentEnd:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":     "agent_end",
			"messages": e.Messages,
		})

	case sdk.EventTurnStart:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":      "turn_start",
			"turnIndex": s.turnIndex,
		})

	case sdk.EventTurnEnd:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":      "turn_end",
			"turnIndex": s.turnIndex,
			"message":   e.Message,
		})
		s.turnIndex++

	case sdk.EventMessageStart:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":    "message_start",
			"message": e.Message,
		})

	case sdk.EventMessageUpdate:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":    "message_update",
			"message": e.Message,
		})

	case sdk.EventMessageEnd:
		extEvent := &extensions.MessageEndEvent{
			Type:    "message_end",
			Message: e.Message,
		}
		replacement, _ := s.extensionRunner.EmitMessageEnd(ctx, extEvent)
		if replacement != nil {
			s.replaceMessageInPlace(e.Message, replacement)
		}

	case sdk.EventToolExecutionStart:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":      "tool_execution_start",
			"toolCallId": e.ToolCallID,
			"toolName":  e.ToolName,
			"args":      e.Args,
		})

	case sdk.EventToolExecutionUpdate:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":        "tool_execution_update",
			"toolCallId":  e.ToolCallID,
			"toolName":    e.ToolName,
			"args":        e.Args,
			"partialResult": e.PartialResult,
		})

	case sdk.EventToolExecutionEnd:
		s.extensionRunner.Emit(ctx, map[string]any{
			"type":      "tool_execution_end",
			"toolCallId": e.ToolCallID,
			"toolName":  e.ToolName,
			"result":    e.Result,
			"isError":   e.IsError,
		})
	}
}

// replaceMessageInPlace replaces a message in the agent state.
func (s *AgentSession) replaceMessageInPlace(original, replacement any) {
	messages := s.Agent.Messages()
	for i, msg := range messages {
		if msg == original {
			messages[i] = replacement.(sdk.AgentMessage)
			s.Agent.SetMessages(messages)
			return
		}
	}
}

// --- Extension Binding ---

// BindExtensions wires UI context, command actions, shutdown handler, and error listener.
// Also emits session_start event and discovers extension resources.
// Mirrors TS: bindExtensions() lines 2035-2052
func (s *AgentSession) BindExtensions(bindings ExtensionBindings) error {
	s.extensionUIContext = bindings.UIContext
	s.extensionCommandContextActions = bindings.CommandContextActions
	if bindings.ShutdownHandler != nil {
		s.extensionShutdownHandler = bindings.ShutdownHandler
	}
	if bindings.OnError != nil {
		if s.extensionErrorUnsubscriber != nil {
			s.extensionErrorUnsubscriber()
		}
		s.extensionErrorListener = bindings.OnError
		s.extensionRunner.OnError(bindings.OnError)
	}

	s.applyExtensionBindings(s.extensionRunner)

	ctx := context.Background()
	s.extensionRunner.Emit(ctx, s.sessionStartEvent)

	// Discover extension resources (skills, prompts, themes)
	s.extendResourcesFromExtensions(
		func() bool {
			if s.sessionStartEvent.Reason == "reload" {
				return true
			}
			return false
		}(),
	)

	return nil
}

// applyExtensionBindings sets UI context and command context on the runner.
// Mirrors TS: _applyExtensionBindings() lines 2107-2115
func (s *AgentSession) applyExtensionBindings(runner *extensions.ExtensionRunner) {
	if runner == nil {
		return
	}
	runner.SetUIContext(&s.extensionUIContext)
	runner.BindCommandContext(&s.extensionCommandContextActions)
}

// extendResourcesFromExtensions asks extensions to discover resources and reloads.
func (s *AgentSession) extendResourcesFromExtensions(isReload bool) {
	if s.extensionRunner == nil || !s.extensionRunner.HasHandlers("resources_discover") {
		return
	}

	ctx := context.Background()
	reason := "startup"
	if isReload {
		reason = "reload"
	}

	result := s.extensionRunner.EmitResourcesDiscover(ctx, s.cwd, reason)
	if result == nil {
		return
	}

	if len(result.SkillPaths) == 0 && len(result.PromptPaths) == 0 && len(result.ThemePaths) == 0 {
		return
	}

	extensionPaths := ResourceExtensionPaths{
		SkillPaths:  s.buildExtensionResourcePaths(result.SkillPaths),
		PromptPaths: s.buildExtensionResourcePaths(result.PromptPaths),
		ThemePaths:  s.buildExtensionResourcePaths(result.ThemePaths),
	}
	s.resourceLoader.ExtendResources(extensionPaths)
	// Rebuild system prompt with new tools
	s.baseSystemPrompt = s.rebuildSystemPrompt(s.GetActiveToolNames())
	s.Agent.SetSystemPrompt(s.baseSystemPrompt)
}

// buildExtensionResourcePaths converts PathWithExtension entries to PathEntry.
func (s *AgentSession) buildExtensionResourcePaths(entries []extensions.PathWithExtension) []PathEntry {
	result := make([]PathEntry, 0, len(entries))
	for _, entry := range entries {
		source := s.getExtensionSourceLabel(entry.ExtensionPath)
		baseDir := ""
		if len(entry.ExtensionPath) > 0 && entry.ExtensionPath[0] != '<' {
			baseDir = filepath.Dir(entry.ExtensionPath)
		}
		result = append(result, PathEntry{
			Path: entry.Path,
			Metadata: PathMetadata{
				Source: source,
				Scope:  "temporary",
				Origin: "top-level",
				BaseDir: baseDir,
			},
		})
	}
	return result
}

func (s *AgentSession) getExtensionSourceLabel(extensionPath string) string {
	if len(extensionPath) > 0 && extensionPath[0] == '<' {
		// Strip < > brackets
		clean := extensionPath
		if len(clean) > 0 && clean[0] == '<' {
			clean = clean[1:]
		}
		if len(clean) > 0 && clean[len(clean)-1] == '>' {
			clean = clean[:len(clean)-1]
		}
		return fmt.Sprintf("extension:%s", clean)
	}
	base := filepath.Base(extensionPath)
	name := base
	// Strip .ts/.js extension
	for _, ext := range []string{".ts", ".js"} {
		if len(name) > len(ext) && name[len(name)-len(ext):] == ext {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	return fmt.Sprintf("extension:%s", name)
}

// --- Extension Core Binding ---

// bindExtensionCore provides session API to extensions.
// Mirrors TS: _bindExtensionCore() lines 2131-2235
func (s *AgentSession) bindExtensionCore(runner *extensions.ExtensionRunner) {
	if runner == nil {
		return
	}

	actions := &extensions.ExtensionActions{
		SendMessage: func(msg any) error {
			s.SendCustomMessage("", msg, true, nil, false, "nextTurn")
			return nil
		},
		SendUserMessage: func(text string, images []any) error {
			return s.SendUserMessage(text, images)
		},
		NewSession: func(opts map[string]any) (map[string]bool, error) {
			if s.onNewSession == nil {
				return nil, fmt.Errorf("session replacement not available")
			}
			return s.onNewSession(opts)
		},
		Fork: func(entryID string, opts map[string]any) (map[string]bool, error) {
			if s.onFork == nil {
				return nil, fmt.Errorf("session replacement not available")
			}
			return s.onFork(entryID, opts)
		},
		NavigateTree: func(targetID string, opts map[string]any) (map[string]bool, error) {
			s.NavigateTree(targetID)
			return map[string]bool{"cancelled": false}, nil
		},
		SwitchSession: func(sessionPath string, opts map[string]any) (map[string]bool, error) {
			if s.onSwitchSession == nil {
				return nil, fmt.Errorf("session replacement not available")
			}
			return s.onSwitchSession(sessionPath, opts)
		},
		Reload: func() error {
			return s.Reload()
		},
		Shutdown: func() {
			if s.extensionShutdownHandler != nil {
				s.extensionShutdownHandler()
			}
		},
		SetModel: func(provider, modelID string) error {
			model := s.modelRegistry.Find(provider, modelID)
			if model == nil {
				return fmt.Errorf("model not found: %s/%s", provider, modelID)
			}
			return s.SetModel(*model)
		},
		SetThinkingLevel: func(level llm.ThinkingLevel) error {
			s.SetThinkingLevel(sdk.ThinkingLevel(level))
			return nil
		},
		RegisterProvider: func(name string, config *extensions.ProviderConfig) error {
			return s.modelRegistry.RegisterProvider(name, providerConfigToInput(config))
		},
		UnregisterProvider: func(name string) {
			s.modelRegistry.UnregisterProvider(name)
		},
	}

		contextActions := &extensions.ExtensionContextActions{
			IsIdle:   func() bool { return !s.Agent.IsStreaming() },
			Shutdown: func() {
				if s.extensionShutdownHandler != nil {
					s.extensionShutdownHandler()
				}
			},
		}

		runner.BindCore(actions, contextActions, &struct {
			RegisterProvider   func(name string, config *extensions.ProviderConfig)
			UnregisterProvider func(name string)
		}{})
	}

// --- Tool Registry ---

// refreshToolRegistry rebuilds the tool map from builtins + extension tools.
// Mirrors TS: _refreshToolRegistry() lines 2238-2327
func (s *AgentSession) refreshToolRegistry(activeToolNames []string, includeAllExtensionTools bool) {
	if s.extensionRunner == nil {
		return
	}

	previousRegistryNames := makeMapSetOfKeys(s.toolRegistry)
	previousActiveToolNames := s.GetActiveToolNames()
	isAllowedTool := func(name string) bool {
		if s.allowedToolNames == nil {
			return true
		}
		return s.allowedToolNames[name]
	}

	// Collect extension + SDK custom tools
	registeredTools := s.extensionRunner.GetAllRegisteredTool()
	var allCustomTools []ToolDefinitionEntry
	for _, tool := range registeredTools {
		if isAllowedTool(tool.Definition.Name) {
			allCustomTools = append(allCustomTools, ToolDefinitionEntry{
				Definition: *tool.Definition,
				SourceInfo: SourceInfo{Source: fmt.Sprintf("extension:%s", tool.Definition.Name)},
			})
		}
	}
	for _, def := range s.customTools {
		if isAllowedTool(def.Name) {
			allCustomTools = append(allCustomTools, ToolDefinitionEntry{
				Definition: def,
				SourceInfo: SourceInfo{Source: fmt.Sprintf("sdk:%s", def.Name)},
			})
		}
	}

	// Build definition registry from base tools
	definitionRegistry := make(map[string]ToolDefinitionEntry)
	for name, def := range s.baseToolDefinitions {
		if isAllowedTool(name) {
			definitionRegistry[name] = ToolDefinitionEntry{
				Definition: def,
				SourceInfo: SourceInfo{Source: fmt.Sprintf("builtin:%s", name)},
			}
		}
	}
	// Override/add extension tools
	for _, tool := range allCustomTools {
		definitionRegistry[tool.Definition.Name] = tool
	}
	s.toolDefinitions = definitionRegistry

	// Build prompt snippets and guidelines from definitions
	s.toolPromptSnippets = make(map[string]string)
	s.toolPromptGuidelines = make(map[string][]string)
	for _, entry := range definitionRegistry {
		if snippet := normalizePromptSnippet(entry.Definition.PromptSnippet); snippet != "" {
			s.toolPromptSnippets[entry.Definition.Name] = snippet
		}
		if guidelines := normalizePromptGuidelines(entry.Definition.PromptGuidelines); len(guidelines) > 0 {
			s.toolPromptGuidelines[entry.Definition.Name] = guidelines
		}
	}

	// Build tool registry (actual AgentTool instances).
	// Base tools come from the tools package. Extension/custom tools are wrapped
	// as AgentTool stubs that delegate to the extension runner.
	toolRegistry := make(map[string]any)
	ctxFactory := func() extensions.ExtensionContext {
		return extensions.ExtensionContext{Model: s.GetModel()}
	}
	for name, entry := range definitionRegistry {
		toolRegistry[name] = buildAgentToolFromDefinition(name, entry, ctxFactory)
	}
	s.toolRegistry = toolRegistry

	// Determine next active tools
	var nextActiveToolNames []string
	if activeToolNames != nil {
		nextActiveToolNames = append(nextActiveToolNames, activeToolNames...)
	} else {
		nextActiveToolNames = append(nextActiveToolNames, previousActiveToolNames...)
	}

	// Filter by allowed
	filtered := make([]string, 0, len(nextActiveToolNames))
	for _, name := range nextActiveToolNames {
		if isAllowedTool(name) {
			filtered = append(filtered, name)
		}
	}
	nextActiveToolNames = filtered

	if s.allowedToolNames != nil {
		for toolName := range s.toolRegistry {
			if s.allowedToolNames[toolName] {
				nextActiveToolNames = append(nextActiveToolNames, toolName)
			}
		}
	} else if includeAllExtensionTools {
		for _, tool := range allCustomTools {
			nextActiveToolNames = append(nextActiveToolNames, tool.Definition.Name)
		}
	} else if activeToolNames == nil {
		for toolName := range s.toolRegistry {
			if !previousRegistryNames[toolName] {
				nextActiveToolNames = append(nextActiveToolNames, toolName)
			}
		}
	}

	s.setActiveToolsByName(dedupeStrings(nextActiveToolNames))
}

// --- Build Runtime ---

// buildRuntime initializes the extension runner and tool registry.
// Mirrors TS: _buildRuntime() lines 2329-2381
func (s *AgentSession) buildRuntime(activeToolNames []string, flagValues map[string]any, includeAllExtensionTools bool) {
	// 1. Build base tool definitions from tools package
	// The read tool honors images.autoResize (TS agent-session.ts:2334-2345
	// re-reads the setting on every runtime rebuild).
	baseDefs := tools.CreateAllToolDefinitions(s.cwd, &tools.ToolsOptions{
		Read: &tools.ReadToolOptions{AutoResizeImages: s.SettingsManager.GetImageAutoResize()},
	})
	s.baseToolDefinitions = make(map[string]extensions.ToolDefinition, len(baseDefs))
	for name, def := range baseDefs {
		if def != nil {
			s.baseToolDefinitions[string(name)] = *def
		}
	}

	// 2. Get extension runtime from resource loader
	extResult := s.resourceLoader.GetExtensions()
	if extResult != nil && extResult.Runtime != nil && flagValues != nil {
		for name, value := range flagValues {
			extResult.Runtime.FlagValues[name] = value
		}
	}

	// 3. Create ExtensionRunner
	if extResult != nil {
		s.extensionRunner = extensions.NewExtensionRunner(
			extResult.Extensions,
			extResult.Runtime,
			s.cwd,
			s.SessionManager,
			s.modelRegistry,
		)
	}

	// 4. Set the runner ref so the Agent's callbacks can access it
	if s.extensionRunnerRef != nil {
		s.extensionRunnerRef.Current = s.extensionRunner
	}

	// 5. Bind core + UI
	s.bindExtensionCore(s.extensionRunner)
	s.applyExtensionBindings(s.extensionRunner)

	// 6. Determine default active tools and refresh registry
	defaultActiveTools := []string{"read", "bash", "edit", "write"}
	if activeToolNames == nil {
		activeToolNames = defaultActiveTools
	}
	s.refreshToolRegistry(activeToolNames, includeAllExtensionTools)
}

// --- Helpers ---

func makeMapSetOfKeys(m map[string]any) map[string]bool {
	result := make(map[string]bool, len(m))
	for k := range m {
		result[k] = true
	}
	return result
}

func normalizePromptSnippet(snippet string) string {
	if snippet == "" {
		return ""
	}
	return snippet
}

func normalizePromptGuidelines(guidelines []string) []string {
	var result []string
	for _, g := range guidelines {
		if g != "" {
			result = append(result, g)
		}
	}
	return result
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

/*
KEYNOTES:
 1. emitExtensionEvent switches on concrete event types from agent-sdk.
    TS uses string matching on event.type; Go uses type assertions.

 2. bindExtensionCore provides ~20 closure callbacks to the extension runner.
    Full wiring depends on methods from other phases (sendCustomMessage, setModel, etc.).
    Marked TODO for Phase 12 completion.

 3. refreshToolRegistry merges base tools + extension tools + SDK custom tools.
    The TS version calls wrapRegisteredTools() to create AgentTool instances.
    In Go, tool wrapping depends on agent-sdk's AgentTool type — stubbed for now.

 4. buildRuntime creates the ExtensionRunner. The TS version instantiates it with
    session-manager and model-registry. This depends on Phase 12 runtime setup.

 5. emitMessageEnd returns a replacement message — handled by the extension runner's
    EmitMessageEnd method which may transform the message.

 6. makeMapSetOfKeys replaces TS `new Set(this._toolRegistry.keys())`.
    dedupeStrings replaces TS `[...new Set(nextActiveToolNames)]`.
*/

func flattenGuidelines(m map[string][]string) []string {
	var result []string
	for _, gs := range m {
		result = append(result, gs...)
	}
	return result
}

// buildAgentToolFromDefinition creates an sdk.AgentTool[any] from a tool
// definition entry. For base/builtin tools this will be overridden by the
// actual tool from the tools package during setActiveToolsByName. For
// extension/custom tools this creates a delegating AgentTool. ctxFactory
// supplies the ExtensionContext (current model) to context-aware definitions
// at execute time; nil restores the context-free behavior.
func buildAgentToolFromDefinition(name string, entry ToolDefinitionEntry, ctxFactory func() extensions.ExtensionContext) sdk.AgentTool[any] {
	// Unmarshal Parameters from json.RawMessage to map[string]any
	var params map[string]any
	if len(entry.Definition.Parameters) > 0 {
		json.Unmarshal(entry.Definition.Parameters, &params)
	}

	return sdk.AgentTool[any]{
		Name:        name,
		Label:       entry.Definition.Label,
		Description: entry.Definition.Description,
		Parameters:  params,
		PrepareArguments: entry.Definition.PrepareArguments,
		Execute: func(toolCallID string, params any, signal context.Context, onUpdate sdk.AgentToolUpdateCallback[any]) (sdk.AgentToolResult[any], error) {
			// If the definition has its own Execute function, use it
			if entry.Definition.Execute != nil {
				args, _ := json.Marshal(params)
				var result any
				var err error
				if entry.Definition.ExecuteContext != nil && ctxFactory != nil {
					result, err = entry.Definition.ExecuteContext(signal, args, ctxFactory())
				} else {
					result, err = entry.Definition.Execute(signal, args)
				}
				if err != nil {
					return sdk.AgentToolResult[any]{
						Content: []llm.ContentBlock{
							llm.TextContent{Type: "text", Text: fmt.Sprintf("Tool %s error: %s", name, err.Error())},
						},
					}, err
				}
				content, _ := result.([]llm.ContentBlock)
				if content == nil {
					// Tools (bash/read/grep/etc.) return *ToolExecuteResult which wraps
					// the Content blocks — unwrap it instead of %v-dumping the struct.
					if ter, ok := result.(*tools.ToolExecuteResult); ok {
						content = ter.Content
					} else if ter, ok := result.(tools.ToolExecuteResult); ok {
						content = ter.Content
					}
				}
				if content == nil {
					content = []llm.ContentBlock{
						llm.TextContent{Type: "text", Text: fmt.Sprintf("%v", result)},
					}
				}
				return sdk.AgentToolResult[any]{Content: content}, nil
			}
			// Extension/custom tool execution is delegated to the extension runner
			// at a higher level. This stub satisfies the AgentTool interface.
			return sdk.AgentToolResult[any]{
				Content: []llm.ContentBlock{
					llm.TextContent{Type: "text", Text: fmt.Sprintf("Tool %s execution delegated to extension system", name)},
				},
			}, nil
		},
	}
}
