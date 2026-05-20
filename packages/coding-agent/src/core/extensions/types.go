package extensions

import (
	"context"
	"encoding/json"
	llm "ycode/packages/llm/src"
)

// --- Event Types ---
type InputSource string 

const (
	InputSourceInteractive 	InputSource = "interactive"
	InputSourceRPC		 	InputSource = "rpc"
	InputSourceExtension 	InputSource = "extension"
)

type ModelSelectSource string

const (
	ModelSelectSourceSet 	ModelSelectSource = "set"
	ModelSelectSourceCycle 	ModelSelectSource = "cycle"
	ModelSelectSourceRestore 	ModelSelectSource = "restore"
)

type WidgetPlacement string 

const (
	WidgetPlacementAboveEditor 	WidgetPlacement = "aboveEditor"
	WidgetPlacementBelowEditor	WidgetPlacement = "belowEditor"
)

// --- Context Types ---
type ContextUsage struct {
	Tokens 		*int 	`json:"tokens"`
	ContextWindow int 	`json:"contextWindow"`
	Percent 	*int 	`json:"percent"`
}

type CompactOptions struct {
	CustomInstructions 	string
	OnComplete 			func(result any)
	OnError 			func(err error)
}

// --- Event Structures ---

type BaseEvent struct {
	Type 	string 	`json:"type"`
}

type ResourcesDiscoverEvent struct {
	Type 	string 	`json:"type"`
	Cwd 	string 	`json:"cwd"`
	Reason 	string 	`json:"reason"`
}

type ResourcesDiscoverResult struct {
	SkillPaths 	[]string 	`json:"skillPaths,omitempty"`
	PromptPaths []string 	`json:"promptPaths,omitempty"`
	ThemePaths 	[]string 	`json:"themePaths,omitempty"`
}

type SessionStartEvent struct {
	Type 		string 	`json:"type"`
	Reason 		string 	`json:"reason"`
	PreviousSessionFile string 	`json:"previousSessionFile,omitempty"`
}

type SessionBeforeSwitchEvent struct {
	Type 		string 	`json:"type"`
	Reason 		string 	`json:"reason"`
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

type SessionBeforeForkEvent struct {
	Type 	string 	`json:"type"`
	EntryID string  `json:"entryId"`
	Position string  `json:"position"`		
}

type SessionShutdownEvent struct {
	Type 		string 	`json:"type"`
	Reason 		string 	`json:"reason"`
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

type SessionCompactEvent struct {
	Type 		string 		`json:"type"`
	CompactionEntry any 	`json:"compactionEntry"`
	FromExtension 	bool 	`json:"fromExtension"`
}

type ContextEvent struct {
	Type 	string 	`json:"type"`
	Messages []any 	`json:"messages"`
}

type ContextEventResult struct {
	Messages []any 	`json:"messages,omitempty"`
}

type BeforeProviderRequestEvent struct {
	Type 		string 		`json:"type"`
	Payload 	any 		`json:"payload"`
}

type AfterProviderResponseEvent struct {
	Type 	string 		`json:"type"`
	Status 	int 		`json:"status"`
	Headers map[string]string `json:"headers"`
}

type BeforeAgentStartEvent struct {
	Type 		string 	`json:"type"`
	Prompt 		string 	`json:"prompt"`
	Images 		[]any 	`json:"images"`
	SystemPrompt string `json:"systemPrompt"`
	SystemPromptOptions any `json:"systemPromptOptions"`
}

type BeforeAgentStartEventResult struct {
	Message 	*json.RawMessage 	`json:"message,omitempty"`
	SystemPrompt *string 			`json:"systemPrompt,omitempty"`
}

type AgentStartEvent struct {
	Type 	string 	`json:"type"`
}

type AgentEndEvent struct {
	Type 	string 	`json:"type"`
	Messages []any  `json:"messages"`
}

type TurnStartEvent struct {
	Type 	string 	`json:"type"`
	TurnIndex int 	`json:"turnIndex"`
	Timestamp int64 `json:"timestamp"`
}

type TurnEndEvent struct {
	Type 	string 	`json:"type"`
	TurnIndex int 	`json:"turnIndex"`
	Message  any 	`json:"message"`
}

type MessageStartEvent struct {
	Type 	string 	`json:"type"`
	Message any 	`json:"message"`
}

type MessageUpdateEvent struct {
	Type 	string 	`json:"type"`
	Messgae any 	`json:"message"`
	Event 	any 	`json:"assistantMessageEvent"`
}

type MessageEndEvent struct {
	Type 	string 	`json:"type"`
	Message any 	`json:"message"`
}

type MessageEndEventResult struct {
	Message 	any 	`json:"message,omitempty"`
}

type ToolExecutionStartEvent struct {
	Type 		string 		`json:"type"`
	ToolCallID	string 		`json:"toolCallId"`
	ToolName 	string 		`json:"toolName"`
	Args 		any 		`json:"args"`
}

type ToolExecutionUpdateEvent struct {
	Type 		string 		`json:"type"`
	ToolCallID	string 		`json:"toolCallId"`
	ToolName 	string 		`json:"toolName"`
	Args 		any 		`json:"args"`
	PartialResult any 		`json:"partialResult"`
}

type ToolExecutionEndEvent struct {
	Type      string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName  string `json:"toolName"`
	Result    any    `json:"result"`
	IsError   bool   `json:"isError"`
}

type ModelSelectEvent struct {
	Type 		string 	`json:"type"`
	Model 		*llm.Model[llm.Api] `json:"model"`
	PreviousModel *llm.Model[llm.Api] `json:"previousModel,omitempty"`
	Source 		ModelSelectSource `json:"source"`
}

type ThinkingLevelSelectEvent struct {
	Type 		string 	`json:"type"`
	Level 		llm.ThinkingLevel `json:"level"`
	PreviousLevel 	llm.ThinkingLevel `json:"previousLevel"`
}

type UserBashEvent struct {
	Type 		string 	`json:"type"`
	Command 	string 	`json:"command"`
	ExcludeFromContext bool `json:"excludeFromContext"`
	Cwd 	string 	`json:"cwd"`
}

type UserBashEventResult struct {
	Operations 	any 	`json:"operations,omitempty"`
	Result 		any 	`json:"result,omitempty"`
}

type InputEvent struct {
	Type 		string 	`json:"type"`
	Text 		string 	`json:"text"`
	Images 		[]any 	`json:"images,omitempty"`
	Source 		InputSource `json:"inputSource"`
}

type InputEventResult struct {
	Action 		string 	`json:"action"`
	Text 		string 	`json:"text,omitempty"`
	Images 		[]any 	`json:"images,omitempty"`
}

type ToolCallEvent struct {
	Type 		string 	`json:"type"`
	ToolCallID 	string 	`json:"toolCallId"`
	ToolName 	string 	`json:"toolName"`
	Args 		any 	`json:"args"`
}

type ToolCallEventResult struct {
	Block  bool   `json:"block,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ToolResultEvent struct {
	Type 		string 	`json:"type"`
	ToolCallID 	string 	`json:"toolCallId"`
	ToolName 	string 	`json:"toolName"`
	Args 		any 	`json:"args"`
	Result 		any 	`json:"result"`
	IsError 	bool 	`json:"isError"`
}

type ToolResultEventResult struct {
	Content []any `json:"content,omitempty"`
	Details any   `json:"details,omitempty"`
	IsError *bool `json:"isError,omitempty"`
}



// --- Tool Definition ---

type ToolExecutionMode string

const (
	ToolExecutionSequential 	ToolExecutionMode = "sequential"
	ToolExecutionParaller 		ToolExecutionMode = "parallel"
)

type ToolDefinition struct {
	Name 			string 		`json:"name"`
	Label 			string 		`json:"label"`
	Description  	string 		`json:"description"`
	PromptSnippet 	string 		`json:"promptSnippet,omitempty"`
	PromptGuidelines []string 	`json:"promptGuidelines,omitempty"`
	Parameters 		json.RawMessage `json:"parameters,omitempty"`
	RenderShell 	string 		`json:"renderShell,omitempty"`
	ExecutionMode 	ToolExecutionMode 	`json:"executionMode,omitempty"`
	Execute 		func(ctx context.Context, args json.RawMessage) (any, error)
	RenderCall  	func(args any) any
	RenderResult	func(args any, result any, options any) any
}

// --- Registered Types ---
type RegisteredTool struct {
	Definition 	*ToolDefinition
	SourceInfo 	any // SourceInfo from parent package
}

type ExtensionFlag struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Type          string `json:"type"`
	Default       any    `json:"default,omitempty"`
	ExtensionPath string `json:"extensionPath"`
}

type ExtensionShortcut struct {
	Shortcut      string
	Description   string
	Handler       func(ctx context.Context) error
	ExtensionPath string
}

type RegisteredCommand struct {
	Name                    string
	SourceInfo              any
	Description             string
	GetArgumentCompletions  func() []string
	Handler                 func(args string, ctx context.Context) error
}

type ResolvedCommand struct {
	RegisteredCommand
	InvocationName string
}


// --- Message Rendering ---

type MessageRenderOptions struct {
	Expanded bool `json:"expanded"`
}

type MessageRenderer func(message any, options MessageRenderOptions, theme any) any


// --- Extension Handler ---

type ExtensionHandler func(event any, ctx any) (any, error)

// --- Extension Struct ---
type Extension struct {
	Path 		string
	ResolvedPath 	string
	SourceInfo 	any
	Handlers 	map[string][]ExtensionHandler
	Tools 		map[string]*RegisteredTool
	MessageRenderers map[string]MessageRenderer
	Commands 	map[string]*RegisteredCommand
	Flags 		map[string]*ExtensionFlag
	Shortcuts 	map[string]*ExtensionShortcut
}

func NewExtension(path, resolvedPath string, sourceInfo any) *Extension {
	return &Extension{
		Path: 		path,
		ResolvedPath: resolvedPath,
		SourceInfo: sourceInfo,
		Handlers: 	make(map[string][]ExtensionHandler),
		Tools: 		make(map[string]*RegisteredTool),
		MessageRenderers: make(map[string]MessageRenderer),
		Commands: 	make(map[string]*RegisteredCommand),
		Flags: 		make(map[string]*ExtensionFlag),
		Shortcuts: 	make(map[string]*ExtensionShortcut),
	}
}

// --- Provider Registeration ---

type ProviderModelConfig struct {
	ID 			string 			`json:"id"`
	Name 		string 			`json:"name"`
	Api 		string 			`json:"api,omitempty"`
	BaseURL 	string 			`json:"baseUrl,omitempty"`
	Reasoning 	bool 			`json:"reasoning"`
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"`
	Input 		[]string 		`json:"input"`
	Cost 		*llm.ModelCost  `json:"cost,omitempty"`
	ContextWindow int 			`json:"contextWindow"`
	MaxTokens 	int 			`json:"maxTokens"`
	Headers 	map[string]string `json:"headers,omitempty"`
	Compat 		json.RawMessage `json:"compat,omitempty"`
}

type ProviderConfig struct {
	Name      string                 `json:"name,omitempty"`
	BaseURL   string                 `json:"baseUrl,omitempty"`
	ApiKey    string                 `json:"apiKey,omitempty"`
	Api       string                 `json:"api,omitempty"`
	Headers   map[string]string      `json:"headers,omitempty"`
	AuthHeader bool                  `json:"authHeader,omitempty"`
	Models    []*ProviderModelConfig `json:"models,omitempty"`
	OAuth     any                    `json:"oauth,omitempty"`
}

// --- Extension Factory ---
type ExtensionAPI interface {
	On(eventType string, handler ExtensionHandler)
	RegisterTool(definition *ToolDefinition, sourceInfo any)
	RegisterCommand(name string, handler func(args string, ctx context.Context) error, options map[string]any)
	RegisterShortcut(shortcut string, handler func(ctx context.Context) error, options map[string]any)
	RegisterFlag(flag *ExtensionFlag)
	RegisterMessageRenderer(customType string, rendered MessageRenderer)
	SendMessage(msg any) error
	SendUserMessage(text string, images []any) error
	RegisterProvider(name string, config *ProviderConfig)
	UnregisterProvider(name string)
}

type ExtensionFactory func(api ExtensionAPI) error


// --- Extension Runtime ---
type ExtensionRuntime struct {
	FlagValues 		map[string]any
	PendingProviderRegistrations []ProviderRegistration
	Invalidate 		func(message string)
	AssertActive 	func() error
	RegisterProviderFn 	func(name string, config *ProviderConfig, extensionPath string)
	UnregisterProviderFn func(name string)

	// Actions bound at runtime
	Actions 		*ExtensionActions
	ContextActions 	*ExtensionContextActions
	CommandActions 	*ExtensionCommandContextActions
}

type ProviderRegistration struct {
	Name 		string
	Config 		*ProviderConfig
	ExtensionPath string
}

type ExtensionActions struct {
	SendMessage 		func(msg any) error
	SendUserMessage 	func(text string, images []any) error
	NewSession 			func(opts map[string]any) (map[string]bool, error)
	Fork 				func(entryID string, opts map[string]any) (map[string]bool, error)
	NavigateTree 		func(targetID string, opts map[string]any) (map[string]bool, error)
	SwitchSession 		func(sessionPath string, opts map[string]any) (map[string]bool, error)
	Reload 				func() error
	Shutdown 			func()
	SetModel 			func(provider, modelID string) error
	SetThinkingLevel 	func(level llm.ThinkingLevel) error
	RegisterProvider 	func(name string, config *ProviderConfig) error
	UnregisterProvider 	func(name string)
}

type ExtensionContextActions struct {
	IsIdle 		func() bool
	Signal 		context.Context
	Abort 		func()
	HasPendingMessages func() bool
	Shutdown 	func()
	GetContextUsage func() *ContextUsage
	Compact 	func(opts *CompactOptions) error
	GetSystemPrompt func() string
}

type ExtensionCommandContextActions struct {
	WaitForIdle 	func() error
	NewSession 		func(opts map[string]any) (map[string]bool, error)
	Fork 			func(entryID string, opts map[string]any) (map[string]bool, error)
	NavigateTree	func(targetID string, opts map[string]any) (map[string]bool, error)
	SwitchSession 	func(sessionPath string, opts map[string]any) (map[string]bool, error)
	Reload 			func() error
}

// --- Load Result ---

type ExtensionError struct {
	ExtensionPath 	string 	`json:"extensionPath"`
	Event 			string 	`json:"event"`
	Error 			string  `json:"error"`
	Stack 			string 	`json:"stack,omitempty"`
}

type ExtensionErrorListener func(error ExtensionError)

type LoadExtensionsResult struct {
	Extensions 		[]*Extension
	Errors 			[]ExtensionLoadError
	Runtime 		*ExtensionRuntime
}

type ExtensionLoadError struct {
	Path 	string
	Error 	string
}