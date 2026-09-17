package extensions

import (
	"context"
	"sync"
	"fmt"
	"strings"
)

type ExtensionUIContext struct {
	// Dialog signatures mirror the reference's ExtensionUIContext
	// (extensions/types.ts): title is the first parameter of every dialog.
	// Select returns the INDEX of the chosen option (-1 if cancelled) and
	// defaultIdx pre-selects an entry — a ycode extension on top of the
	// reference's shape (the reference's select has no default and resolves
	// the option string).
	Select  func(title string, options []string, defaultIdx int, timeoutMs int) (int, error)
	Confirm func(title string, message string, timeoutMs int) (bool, error)
	Input   func(title string, placeholder string, timeoutMs int) (string, error)
	// Editor swaps a multi-line editor into the prompt area and blocks until
	// the user submits (returns the text) or cancels (returns ""). prefill
	// pre-populates the editor. Interactive mode shows the ExtensionEditor
	// widget with Ctrl+G external-editor support; RPC mode forwards the
	// request to the host; print mode returns "".
	Editor func(title string, prefill string) (string, error)
	// Notify surfaces a message to the user. level is "info" | "warning" |
	// "error" (matching the reference's ctx.ui.notify). Interactive mode
	// appends a themed line to the chat transcript (info=dim, warning=warning-
	// colored "Warning: …", error=error-colored "Error: …"); RPC mode forwards
	// it to the host.
	Notify func(message, level string)
	// Status writes to the footer status bar. key namespaces the slot (the
	// reference keys a Map so concurrent extensions don't clobber each other);
	// empty message clears. Interactive mode currently keeps a single slot.
	Status func(key, message string)
}

var NoOpUIContext = ExtensionUIContext{
	Select:  func(title string, options []string, defaultIdx int, timeoutMs int) (int, error) { return -1, nil },
	Confirm: func(title, message string, timeoutMs int) (bool, error) { return false, nil },
	Input:   func(title, placeholder string, timeoutMs int) (string, error) { return "", nil },
	Editor:  func(title, prefill string) (string, error) { return "", nil },
	Notify:  func(message, level string) {},
	Status:  func(key, message string) {},
}

type NewSessionHandler func(options map[string]any) (map[string]bool, error)
type ForkHandler func(entryID string, options map[string]any) (map[string]bool, error)
type NavigateTreeHandler func(targetID string, options map[string]any) (map[string]bool, error)
type SwitchSessionHandler func(sessionPath string, options map[string]any) (map[string]bool, error)
type ReloadHandler func() error
type ShutdownHandler func()

type BeforeAgentStartCombinedResult struct {
	Messages     []any
	SystemPrompt *string
}

type ResourcesDiscoverPaths struct {
	SkillPaths  []PathWithExtension
	PromptPaths []PathWithExtension
	ThemePaths  []PathWithExtension
}

type PathWithExtension struct {
	Path          string
	ExtensionPath string
}

type ExtensionRunner struct {
	mu 				sync.RWMutex
	extensions 		[]*Extension
	runtime 		*ExtensionRuntime
	uiContext 		ExtensionUIContext
	cwd 			string
	sessionManager 	any
	modelRegistry 	any
	errorListeners 	[]ExtensionErrorListener
	errorListenersMu sync.RWMutex
	hasUI 			bool

	getModel 		func() any
	isIdleFn		func() bool
	getSignalFn 	func() context.Context
	waitForIdleFn 	func() error
	abortFn 		func()
	hasPendingMessagesFn  func() bool
	getContextUsageFn func() *ContextUsage
	compactFn 		func(options *CompactOptions) error
	getSystemPromptFn func() string
	newSessionHandler NewSessionHandler
	forkHandler 	ForkHandler
	navigateTreeHandler NavigateTreeHandler
	switchSessionHandler SwitchSessionHandler
	reloadHandler 	ReloadHandler
	shutdownHandler ShutdownHandler
	shortcutDiagnostics []any
	commandDiagnostics []any
	staleMessage 	string
}

func NewExtensionRunner(
	extensions 	[]*Extension,
	runtime    	*ExtensionRuntime,
	cwd			string,
	sessionManager any,
	modelRegistry any,
) *ExtensionRunner {
	return &ExtensionRunner{
		extensions:     extensions,
		runtime:        runtime,
		uiContext:      NoOpUIContext,
		cwd:            cwd,
		sessionManager: sessionManager,
		modelRegistry:  modelRegistry,
		getModel:       func() any { return nil },
		isIdleFn:       func() bool { return true },
		getSignalFn:    func() context.Context { return nil },
		waitForIdleFn:  func() error { return nil },
		abortFn:        func() {},
		hasPendingMessagesFn: func() bool { return false },
		getContextUsageFn:    func() *ContextUsage { return nil },
		compactFn:            func(options *CompactOptions) error { return nil },
		getSystemPromptFn:    func() string { return "" },
		newSessionHandler:    func(options map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		forkHandler:          func(entryID string, options map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		navigateTreeHandler:  func(targetID string, options map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		switchSessionHandler: func(sessionPath string, options map[string]any) (map[string]bool, error) { return map[string]bool{"cancelled": false}, nil },
		reloadHandler:        func() error { return nil },
		shutdownHandler:      func() {},
	}
}

func (r *ExtensionRunner) BindCore(
	actions *ExtensionActions,
	contextActions *ExtensionContextActions,
	providerActions *struct {
		RegisterProvider 	func(name string, config *ProviderConfig)
		UnregisterProvider  func(name string)
	},
) {
	if actions != nil {
		r.runtime.Actions = actions
	}
	if contextActions != nil {
		r.runtime.ContextActions = contextActions

		r.getModel = func() any {
			if contextActions.Signal != nil {
				return nil
			}
			return nil
		}
		r.getSignalFn = func() context.Context { return contextActions.Signal }
		// Only override the constructor defaults when the caller actually provides
		// a callback. Without these guards, a contextActions with nil fields (e.g.
		// HasPendingMessages unset) clobbers the safe default with nil, and
		// CreateContext then panics calling it — which crashed SetModel →
		// SetThinkingLevel → Emit on any /model selection.
		if contextActions.IsIdle != nil {
			r.isIdleFn = contextActions.IsIdle
		}
		if contextActions.Abort != nil {
			r.abortFn = contextActions.Abort
		}
		if contextActions.HasPendingMessages != nil {
			r.hasPendingMessagesFn = contextActions.HasPendingMessages
		}
		if contextActions.Shutdown != nil {
			r.shutdownHandler = contextActions.Shutdown
		}
		if contextActions.GetContextUsage != nil {
			r.getContextUsageFn = contextActions.GetContextUsage
		}
		if contextActions.Compact != nil {
			r.compactFn = contextActions.Compact
		}
		if contextActions.GetSystemPrompt != nil {
			r.getSystemPromptFn = contextActions.GetSystemPrompt
		}
	}
	
	// Flush pending provider registrations
	for _, reg := range r.runtime.PendingProviderRegistrations {
		if providerActions != nil && providerActions.RegisterProvider != nil {
			providerActions.RegisterProvider(reg.Name, reg.Config)
		}
	}
	r.runtime.PendingProviderRegistrations = nil

	// Replace register/unregister with direct calls
	if providerActions != nil {
		r.runtime.RegisterProviderFn = func(name string, config *ProviderConfig, extensionPath string) {
			providerActions.RegisterProvider(name, config)
		}
		r.runtime.UnregisterProviderFn = func(name string) {
			providerActions.UnregisterProvider(name)
		}
	}
}

func (r *ExtensionRunner) BindCommandContext(actions *ExtensionCommandContextActions) {
	if actions != nil {
		r.waitForIdleFn = actions.WaitForIdle
		if actions.NewSession != nil {
			r.newSessionHandler = actions.NewSession
		}
		if actions.Fork != nil {
			r.forkHandler = actions.Fork
		}
		if actions.NavigateTree != nil {
			r.navigateTreeHandler = actions.NavigateTree
		}
		if actions.SwitchSession != nil {
			r.switchSessionHandler = actions.SwitchSession
		}
		if actions.Reload != nil {
			r.reloadHandler = actions.Reload
		}
	}
}

func (r *ExtensionRunner) SetUIContext(uiContext *ExtensionUIContext) {
	if uiContext != nil {
		r.uiContext = *uiContext
		r.hasUI = true
	} else {
		r.uiContext = NoOpUIContext
		r.hasUI = false
	}
}

func (r *ExtensionRunner) GetUIContext() ExtensionUIContext {
	return r.uiContext
}

func (r *ExtensionRunner) HasUI() bool {
	return r.hasUI
}

func (r *ExtensionRunner) GetExtensionPaths() []string {
	paths := make([]string, len(r.extensions))
	for i, ext := range r.extensions {
		paths[i] = ext.Path
	}
	return paths
}


func (r *ExtensionRunner) GetAllRegisteredTool() []*RegisteredTool {
	toolsByName := make(map[string]*RegisteredTool)
	for _, ext := range r.extensions {
		for name, tool := range ext.Tools {
			if _, exists := toolsByName[name]; !exists {
				toolsByName[name] = tool
			}
		}
	}
	result := make([]*RegisteredTool, 0, len(toolsByName))
	for _, tool := range toolsByName {
		result = append(result, tool)
	}
	return result
}

func (r *ExtensionRunner) GetFlags() map[string]*ExtensionFlag {
	allFlags := make(map[string]*ExtensionFlag)
	for _, ext := range r.extensions {
		for name, flag := range ext.Flags {
			if _, exists := allFlags[name]; !exists {
				allFlags[name] = flag
			}
		}
	}
	return allFlags
}
func (r *ExtensionRunner) SetFlagValue(name string, value any) {
	r.runtime.FlagValues[name] = value
}

func (r *ExtensionRunner) GetFlagValues() map[string]any {
	result := make(map[string]any, len(r.runtime.FlagValues))
	for k, v := range r.runtime.FlagValues {
		result[k] = v
	}
	return result
}

func (r *ExtensionRunner) GetShortcuts(resolvedKeybindings map[string]any) map[string]*ExtensionShortcut {
	r.shortcutDiagnostics = nil
	extensionShortcuts := make(map[string]*ExtensionShortcut)

	for _, ext := range r.extensions {
		for key, shortcut := range ext.Shortcuts {
			normalizedKey := strings.ToLower(key)
			if _, exists := extensionShortcuts[normalizedKey]; !exists {
				extensionShortcuts[normalizedKey] = shortcut
			}
		}
	}
	return extensionShortcuts
}

func (r *ExtensionRunner) GetShortcutDiagnostics() []any {
	return r.shortcutDiagnostics
}

func (r *ExtensionRunner) Invalidate(message string) {
	if r.staleMessage == "" {
		if message == "" {
			message = "This extension ctx is stale after session replacement or reload."
		}
		r.staleMessage = message
		r.runtime.Invalidate(message)
	}
}

func (r *ExtensionRunner) assertActive() {
	if r.staleMessage != "" {
		panic(r.staleMessage)
	}
}

func (r *ExtensionRunner) OnError(listener ExtensionErrorListener) {
	r.errorListenersMu.Lock()
	r.errorListeners = append(r.errorListeners, listener)
	r.errorListenersMu.Unlock()
}

func (r *ExtensionRunner) EmitError(err ExtensionError) {
	r.errorListenersMu.RLock()
	listeners := make([]ExtensionErrorListener, len(r.errorListeners))
	copy(listeners, r.errorListeners)
	r.errorListenersMu.RUnlock()

	for _, listener := range listeners {
		listener(err)
	}
}

func (r *ExtensionRunner) HasHandlers(eventType string) bool {
	for _, ext := range r.extensions {
		if handlers, ok := ext.Handlers[eventType]; ok && len(handlers) > 0 {
			return true
		}
	}
	return false
}

func (r *ExtensionRunner) GetMessageRenderer(customType string) MessageRenderer {
	for _, ext := range r.extensions {
		if renderer, ok := ext.MessageRenderers[customType]; ok {
			return renderer
		}
	}
	return nil
}

func (r *ExtensionRunner) GetRegisteredCommands() []*ResolvedCommand {
	r.commandDiagnostics = nil
	return r.resolveRegisteredCommands()
}

func (r *ExtensionRunner) GetCommandDiagnostics() []any {
	return r.commandDiagnostics
}

func (r *ExtensionRunner) GetCommand(name string) *ResolvedCommand {
	commands := r.resolveRegisteredCommands()
	for _, cmd := range commands {
		if cmd.InvocationName == name {
			return cmd
		}
	}
	return nil
}

func (r *ExtensionRunner) resolveRegisteredCommands() []*ResolvedCommand {
	var commands []*RegisteredCommand
	counts := make(map[string]int)

	for _, ext := range r.extensions {
		for _, cmd := range ext.Commands {
			commands = append(commands, cmd)
			counts[cmd.Name]++
		}
	}

	seen := make(map[string]int)
	takenNames := make(map[string]bool)
	result := make([]*ResolvedCommand, len(commands))

	for i, cmd := range commands {
		occurrence := seen[cmd.Name] + 1
		seen[cmd.Name] = occurrence

		invocationName := cmd.Name
		if counts[cmd.Name] > 1 {
			invocationName = fmt.Sprintf("%s:%d", cmd.Name, occurrence)
		}

		for takenNames[invocationName] {
			occurrence++
			invocationName = fmt.Sprintf("%s:%d", cmd.Name, occurrence)
		}
		takenNames[invocationName] = true

		result[i] = &ResolvedCommand{
			RegisteredCommand: *cmd,
			InvocationName:    invocationName,
		}
	}

	return result
}

func (r *ExtensionRunner) Shutdown() {
	r.shutdownHandler()
}

func (r *ExtensionRunner) CreateContext() map[string]any {
	r.assertActive()
	ctx := make(map[string]any)
	ctx["ui"] = r.uiContext
	ctx["hasUI"] = r.HasUI()
	ctx["cwd"] = r.cwd
	ctx["sessionManager"] = r.sessionManager
	ctx["modelRegistry"] = r.modelRegistry
	ctx["model"] = r.getModel()
	ctx["isIdle"] = r.isIdleFn()
	ctx["abort"] = r.abortFn
	ctx["hasPendingMessages"] = r.hasPendingMessagesFn()
	ctx["shutdown"] = r.shutdownHandler
	ctx["getContextUsage"] = r.getContextUsageFn
	ctx["compact"] = r.compactFn
	ctx["getSystemPrompt"] = r.getSystemPromptFn
	return ctx
}

func (r *ExtensionRunner) CreateCommandContext() map[string]any {
	ctx := r.CreateContext()
	ctx["waitForIdle"] = r.waitForIdleFn
	ctx["newSession"] = r.newSessionHandler
	ctx["fork"] = r.forkHandler
	ctx["navigateTree"] = r.navigateTreeHandler
	ctx["switchSession"] = r.switchSessionHandler
	ctx["reload"] = r.reloadHandler
	return ctx
}

func (r *ExtensionRunner) Emit(ctx context.Context, event any) (any, error) {
	extCtx := r.CreateContext()

	eventMap, ok := event.(map[string]any)
	if !ok {
		return nil, nil
	}
	eventType, _ := eventMap["type"].(string)

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers[eventType]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         eventType,
					Error:         err.Error(),
				})
				continue
			}

			if result != nil {
				resultMap, _ := result.(map[string]any)
				if cancelled, _ := resultMap["cancel"].(bool); cancelled {
					return result, nil
				}
			}
		}
	}

	return nil, nil
}

func (r *ExtensionRunner) EmitMessageEnd(ctx context.Context, event *MessageEndEvent) (any, error) {
	extCtx := r.CreateContext()
	currentMessage := event.Message
	modified := false

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["message_end"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "message_end",
					Error:         err.Error(),
				})
				continue
			}

			if resultMap, ok := result.(map[string]any); ok {
				if msg, ok := resultMap["message"]; ok && msg != nil {
					currentMessage = msg
					modified = true
				}
			}
		}
	}

	if modified {
		return currentMessage, nil
	}
	return nil, nil
}

func (r *ExtensionRunner) EmitToolResult(ctx context.Context, event *ToolResultEvent) (*ToolResultEventResult, error) {
	extCtx := r.CreateContext()
	currentEvent := *event
	modified := false

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["tool_result"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			result, err := handler(&currentEvent, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "tool_result",
					Error:         err.Error(),
				})
				continue
			}

			if resultMap, ok := result.(map[string]any); ok {
				if content, ok := resultMap["content"]; ok {
					currentEvent.Result = content
					modified = true
				}
				if details, ok := resultMap["details"]; ok {
					currentEvent.Result = details
					modified = true
				}
				if isError, ok := resultMap["isError"]; ok {
					if b, ok := isError.(bool); ok {
						currentEvent.IsError = b
						modified = true
					}
				}
			}
		}
	}

	if !modified {
		return nil, nil
	}

	return &ToolResultEventResult{
		Content: []any{currentEvent.Result},
		Details: currentEvent.Result,
	}, nil
}

func (r *ExtensionRunner) EmitToolCall(ctx context.Context, event *ToolCallEvent) (*ToolCallEventResult, error) {
	extCtx := r.CreateContext()
	var result *ToolCallEventResult

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["tool_call"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			handlerResult, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "tool_call",
					Error:         err.Error(),
				})
				continue
			}

			if resultMap, ok := handlerResult.(map[string]any); ok {
				if block, _ := resultMap["block"].(bool); block {
					reason, _ := resultMap["reason"].(string)
					return &ToolCallEventResult{Block: true, Reason: reason}, nil
				}
			}
		}
	}

	return result, nil
}

func (r *ExtensionRunner) EmitUserBash(ctx context.Context, event *UserBashEvent) (*UserBashEventResult, error) {
	extCtx := r.CreateContext()

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["user_bash"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "user_bash",
					Error:         err.Error(),
				})
				continue
			}

			if result != nil {
				if resultMap, ok := result.(*UserBashEventResult); ok {
					return resultMap, nil
				}
			}
		}
	}

	return nil, nil
}

func (r *ExtensionRunner) EmitContext(ctx context.Context, messages []any) ([]any, error) {
	extCtx := r.CreateContext()
	currentMessages := make([]any, len(messages))
	copy(currentMessages, messages)

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["context"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			event := &ContextEvent{Type: "context", Messages: currentMessages}
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "context",
					Error:         err.Error(),
				})
				continue
			}

			if resultMap, ok := result.(*ContextEventResult); ok && resultMap.Messages != nil {
				currentMessages = resultMap.Messages
			}
		}
	}

	return currentMessages, nil
}

func (r *ExtensionRunner) EmitBeforeProviderRequest(ctx context.Context, payload any) (any, error) {
	extCtx := r.CreateContext()
	currentPayload := payload

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["before_provider_request"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			event := &BeforeProviderRequestEvent{Type: "before_provider_request", Payload: currentPayload}
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "before_provider_request",
					Error:         err.Error(),
				})
				continue
			}

			if result != nil {
				currentPayload = result
			}
		}
	}

	return currentPayload, nil
}

func (r *ExtensionRunner) EmitBeforeAgentStart(
	ctx context.Context,
	prompt string,
	images []any,
	systemPrompt string,
	systemPromptOptions any,
) (*BeforeAgentStartCombinedResult, error) {
	extCtx := r.CreateContext()
	currentSystemPrompt := systemPrompt
	var messages []any
	systemPromptModified := false

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["before_agent_start"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			event := &BeforeAgentStartEvent{
				Type:               "before_agent_start",
				Prompt:             prompt,
				Images:             images,
				SystemPrompt:       currentSystemPrompt,
				SystemPromptOptions: systemPromptOptions,
			}
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "before_agent_start",
					Error:         err.Error(),
				})
				continue
			}

			if result != nil {
				if resultMap, ok := result.(*BeforeAgentStartEventResult); ok {
					if resultMap.Message != nil {
						messages = append(messages, resultMap.Message)
					}
					if resultMap.SystemPrompt != nil {
						currentSystemPrompt = *resultMap.SystemPrompt
						systemPromptModified = true
					}
				}
			}
		}
	}

	if len(messages) > 0 || systemPromptModified {
		return &BeforeAgentStartCombinedResult{
			Messages:     messages,
			SystemPrompt: &currentSystemPrompt,
		}, nil
	}

	return nil, nil
}

func (r *ExtensionRunner) EmitResourcesDiscover(ctx context.Context, cwd, reason string) *ResourcesDiscoverPaths {
	extCtx := r.CreateContext()
	result := &ResourcesDiscoverPaths{}

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["resources_discover"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			event := &ResourcesDiscoverEvent{Type: "resources_discover", Cwd: cwd, Reason: reason}
			handlerResult, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "resources_discover",
					Error:         err.Error(),
				})
				continue
			}

			if discoverResult, ok := handlerResult.(*ResourcesDiscoverResult); ok {
				for _, p := range discoverResult.SkillPaths {
					result.SkillPaths = append(result.SkillPaths, PathWithExtension{Path: p, ExtensionPath: ext.Path})
				}
				for _, p := range discoverResult.PromptPaths {
					result.PromptPaths = append(result.PromptPaths, PathWithExtension{Path: p, ExtensionPath: ext.Path})
				}
				for _, p := range discoverResult.ThemePaths {
					result.ThemePaths = append(result.ThemePaths, PathWithExtension{Path: p, ExtensionPath: ext.Path})
				}
			}
		}
	}

	return result
}

func (r *ExtensionRunner) EmitInput(ctx context.Context, text string, images []any, source InputSource) *InputEventResult {
	extCtx := r.CreateContext()
	currentText := text
	var currentImages []any
	if images != nil {
		currentImages = make([]any, len(images))
		copy(currentImages, images)
	}

	for _, ext := range r.extensions {
		handlers, ok := ext.Handlers["input"]
		if !ok || len(handlers) == 0 {
			continue
		}

		for _, handler := range handlers {
			event := &InputEvent{Type: "input", Text: currentText, Images: currentImages, Source: source}
			result, err := handler(event, extCtx)
			if err != nil {
				r.EmitError(ExtensionError{
					ExtensionPath: ext.Path,
					Event:         "input",
					Error:         err.Error(),
				})
				continue
			}

			if resultMap, ok := result.(*InputEventResult); ok {
				if resultMap.Action == "handled" {
					return resultMap
				}
				if resultMap.Action == "transform" {
					currentText = resultMap.Text
					if resultMap.Images != nil {
						currentImages = resultMap.Images
					}
				}
			}
		}
	}

	if currentText != text || !equalImages(currentImages, images) {
		return &InputEventResult{Action: "transform", Text: currentText, Images: currentImages}
	}
	return &InputEventResult{Action: "continue"}
}

func equalImages(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func EmitSessionShutdownEvent(runner *ExtensionRunner, event *SessionShutdownEvent) (bool, error) {
	if runner.HasHandlers("session_shutdown") {
		_, err := runner.Emit(context.TODO(), event)
		return err == nil, err
	}
	return false, nil
}