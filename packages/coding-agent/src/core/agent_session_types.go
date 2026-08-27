package ycode

import (
	"context"
	"regexp"
	"strings"
	"ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// --- Skill Block Parsing ---

type ParsedSkillBlock struct {
	Name 	string
	Location string
	Content string
	UserMessage string
}

var skillBlockRegex = regexp.MustCompile(
	`(?s)^<skill name="([^"]+)" location="([^"]+)">\n(.*?)\n</skill>(?:\n\n(.*))?$`,
)

func ParseSkillBlock(text string) *ParsedSkillBlock {
	m := skillBlockRegex.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	userMsg := strings.TrimSpace(m[4])
	return &ParsedSkillBlock{
		Name:        m[1],
		Location:    m[2],
		Content:     m[3],
		UserMessage: userMsg,
	}
}

// ============================================================================
// AgentSessionEvent — session-specific events extending AgentEvent
// ============================================================================

// CompactionReason: Describes why compaction was triggered.

type CompactionReason string

const (
	CompactionManual CompactionReason = "manual"
	CompactionThreshold CompactionReason = "threshold"
	CompactionOverflow CompactionReason = "overflow"
)

/* AgentSessionEvent: Represents all events emitted by AgentSession.
Extension of the core AgentEvent from agent-sdk*/
type AgentSessionEvent struct {
	/*Discriminator - which event type this is.
	"queue_update", "compaction_start", "compaction_end",
	"session_info_changed", "thinking_level_changed",
	"auto_retry_start", "auto_retry_end",
	or any core AgentEvent type string.
	*/
	EventType string
	// Queue update fields
	Steering []string
	FollowUp []string
	// Compaction fields
	CompactionReason 	CompactionReason
	CompactionResult 	*compaction.CompactionResult
	CompactionAborted 	bool
	CompactionWillRetry bool
	ComapctionError 	string

	// Session info
	SessionName 	string

	// Thinking Level
	ThinkingLevel 	sdk.ThinkingLevel

	// Auto-retry fields
	RetryAttempt 	int
	RetryMaxAttempt int
	RetryDelayMs 	int
	RetryError 		string
	RetrySuccess	bool
}

type AgentSessionEventListener func(event AgentSessionEvent)

// ============================================================================
// Config types
// ============================================================================

type AgentSessionConfig struct {
	Agent                sdk.AgentState
	SubscribeFunc        func(listener func(event sdk.AgentEvent, ctx context.Context) error) func()
	SessionManager       *SessionManager
	SettingsManager      *SettingsManager
	Cwd                  string
	ScopedModels         []ScopedModelEntry
	ResourceLoader       ResourceLoader
	CustomTools          []extensions.ToolDefinition
	ModelRegistry        *ModelRegistry
	InitialActiveToolNames []string
	AllowedToolNames     []string
	ExtensionRunnerRef   *ExtensionRunnerRef
	SessionStartEvent    *extensions.SessionStartEvent

	// Runtime session-replacement callbacks — injected by AgentSessionRuntime
	OnNewSession    func(opts map[string]any) (map[string]bool, error)
	OnFork          func(entryID string, opts map[string]any) (map[string]bool, error)
	OnSwitchSession func(sessionPath string, opts map[string]any) (map[string]bool, error)
}

// ExtensionRunnerRef: mutable reference that the Agent uses to access the current ExtensionRunner
type ExtensionRunnerRef struct {
	Current 	*extensions.ExtensionRunner
}

type ExtensionBindings struct {
	UIContext 		extensions.ExtensionUIContext
	CommandContextActions extensions.ExtensionCommandContextActions
	ShutdownHandler extensions.ShutdownHandler
	OnError 		extensions.ExtensionErrorListener
}

type PromptOptions struct {
	// ExpandPromptTemplates controls skill-command/prompt-template expansion.
	// *bool so nil means "default true" (reference agent-session.ts:968
	// `options?.expandPromptTemplates ?? true`) while an explicit false — as
	// SendUserMessage passes for extension-originated text — is honored. A
	// plain bool could not distinguish unset from false, which made the
	// opt-out impossible and let literal "/foo" extension text run commands.
	ExpandPromptTemplates *bool
	Images 				  []any
	StreamingBehavior 	  string
	Source 				  extensions.InputSource
	PreflightResult 	  func(success bool)
}

type ModelCycleResult struct {
	Model 		llm.Model[llm.Api]
	ThinkingLevel sdk.ThinkingLevel
	IsScoped 	bool
}

type SessionStats struct {
	SessionFile 	string
	SessionID 		string
	UserMessages 	int
	AssistantMessages int
	ToolCalls 		int
	ToolResults 	int
	TotalMessages 	int
	Tokens 			TokenStats
	Cost 			float64
	ContextUsage 	extensions.ContextUsage
}

type TokenStats struct {
	Input 	int
	Output  int
	CacheRead int
	CacheWrite int
	Total int
}

type ToolDefinitionEntry struct {
	Definition 		extensions.ToolDefinition
	SourceInfo 		SourceInfo
}

// --- Constants ---

var ThinkingLevels = []sdk.ThinkingLevel{
	sdk.ThinkingOff,
	sdk.ThinkingMinimal,
	sdk.ThinkingLow,
	sdk.ThinkingMedium,
	sdk.ThinkingHigh,
}

/* KEYFLAGS:
1	AgentSessionEvent is a flat struct, not a sum type	TS uses discriminated unions. Go doesn't have those — using a flat struct with an EventType discriminator. Fields are zero-value when irrelevant. Alternative: interface-based, but that's more complex for listeners.
2	Model/ImageContent use any	TS generics (Model<any>, ImageContent) map to any in Go. Can be tightened to llm.Model[llm.Api] when fully wired.
3	CompactionResult is any	The compaction package types exist but the import would create a dependency cycle. Will be typed properly when chunks are assembled.
4	ExtensionRunnerRef is a named struct	TS uses { current?: ExtensionRunner } inline. Go needs a named struct for mutable reference semantics.
5	skillBlockRegex uses (?s) flag	Makes . match newlines, matching TS's [\s\S] pattern.
*/