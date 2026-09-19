// RPC protocol types for headless operation. Ports
// reference/coding-agent/src/modes/rpc/rpc-types.ts.
//
// Commands arrive as JSON lines on stdin; responses and events are emitted as
// JSON lines on stdout.
//
// ============================================================================
// TS discriminated unions → Go discriminator-tagged structs
// ============================================================================
//
// TS models RpcCommand / RpcResponse / RpcExtensionUIRequest as discriminated
// unions (on `type`/`command`/`method`). Go has no unions, so each becomes one
// struct carrying the discriminator plus every variant's fields as optional. A
// single json.Unmarshal populates it for any variant; the handler switches on the
// discriminator. This is the idiomatic Go translation and preserves the exact
// wire shape (field names are camelCase, matching the TS literals).
//
// Field types:
//   - Enum-like fields (ThinkingLevel, mode, source, method) are typed with the
//     real llm.ThinkingLevel where applicable, else plain string — their wire
//     form is a JSON string either way.
//   - Model is *llm.Model[llm.Api] (llm.Model has json tags matching the TS
//     Model<any> shape). Images is []*llm.ImageContent.
//   - Response Data and SlashCommand SourceInfo are `any` to avoid coupling this
//     protocol file to core's still-evolving types (SessionStats/BashResult/
//     CompactionResult/AgentMessage/SourceInfo); the handler (20.4) assigns the
//     concrete core values, which marshal to the correct wire objects.
//
// As a faithful Go enhancement over the TS inline string literals, the
// discriminator values are exported as named constants (Cmd*/Method*/Source*/
// Mode*) so 20.4's switches are typo-proof.

package modes

import (
	llm "ycode/packages/llm/src"
)

// ============================================================================
// Wire envelope type constants
// ============================================================================

const (
	ResponseType        = "response"            // RpcResponse.Type
	ExtUIRequestType   = "extension_ui_request"  // RpcExtensionUIRequest.Type
	ExtUIResponseType  = "extension_ui_response" // RpcExtensionUIResponse.Type
)

// ============================================================================
// RPC Commands (stdin) — TS RpcCommand union
// ============================================================================

// RpcCommandType values are the RpcCommand.Type discriminator literals.
const (
	CmdPrompt               = "prompt"
	CmdSteer                = "steer"
	CmdFollowUp             = "follow_up"
	CmdAbort                = "abort"
	CmdNewSession           = "new_session"
	CmdGetState             = "get_state"
	CmdSetModel             = "set_model"
	CmdCycleModel           = "cycle_model"
	CmdGetAvailableModels   = "get_available_models"
	CmdSetThinkingLevel     = "set_thinking_level"
	CmdCycleThinkingLevel   = "cycle_thinking_level"
	CmdSetSteeringMode      = "set_steering_mode"
	CmdSetFollowUpMode      = "set_follow_up_mode"
	CmdCompact              = "compact"
	CmdSetAutoCompaction    = "set_auto_compaction"
	CmdSetAutoRetry         = "set_auto_retry"
	CmdAbortRetry           = "abort_retry"
	CmdBash                 = "bash"
	CmdAbortBash            = "abort_bash"
	CmdGetSessionStats      = "get_session_stats"
	CmdExportHTML           = "export_html"
	CmdSwitchSession        = "switch_session"
	CmdFork                 = "fork"
	CmdClone                = "clone"
	CmdGetForkMessages      = "get_fork_messages"
	CmdGetLastAssistantText = "get_last_assistant_text"
	CmdSetSessionName       = "set_session_name"
	CmdGetMessages          = "get_messages"
	CmdGetCommands          = "get_commands"
)

// RpcStreamingBehavior values for the prompt command's optional streamingBehavior.
const (
	StreamingBehaviorSteer    = "steer"
	StreamingBehaviorFollowUp = "followUp"
)

// RpcCommand is a command read from the RPC stdin stream. Type is the
// discriminator; only the fields relevant to that Type are populated.
type RpcCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`

	// prompt / steer / follow_up
	Message           string                `json:"message,omitempty"`
	Images            []*llm.ImageContent   `json:"images,omitempty"`
	StreamingBehavior string                `json:"streamingBehavior,omitempty"` // steer | followUp (prompt only)

	// new_session
	ParentSession string `json:"parentSession,omitempty"`

	// set_model
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// set_thinking_level
	Level string `json:"level,omitempty"` // ThinkingLevel (wire string)

	// set_steering_mode / set_follow_up_mode
	Mode string `json:"mode,omitempty"` // all | one-at-a-time

	// compact
	CustomInstructions string `json:"customInstructions,omitempty"`

	// set_auto_compaction / set_auto_retry
	Enabled bool `json:"enabled,omitempty"`

	// bash
	Command string `json:"command,omitempty"`

	// switch_session
	SessionPath string `json:"sessionPath,omitempty"`

	// fork
	EntryID string `json:"entryId,omitempty"`

	// set_session_name
	Name string `json:"name,omitempty"`

	// export_html
	OutputPath string `json:"outputPath,omitempty"`
}

// ============================================================================
// RPC Slash Command — for the get_commands response
// ============================================================================

// RpcSlashCommandSource values.
const (
	SourceExtension = "extension"
	SourcePrompt    = "prompt"
	SourceSkill     = "skill"
)

// RpcSlashCommand describes a command available for invocation via prompt
// (returned by get_commands). SourceInfo is the owning resource's metadata
// (core.SourceInfo on the wire).
type RpcSlashCommand struct {
	Name        string `json:"name"`           // without leading slash
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`         // extension | prompt | skill
	SourceInfo  any    `json:"sourceInfo"`     // core.SourceInfo (wire object)
}

// ============================================================================
// RPC State — returned by get_state
// ============================================================================

// RpcQueueMode values for steering/follow-up modes.
const (
	ModeAll        = "all"
	ModeOneAtATime = "one-at-a-time"
)

// RpcSessionState is the session snapshot returned by get_state.
type RpcSessionState struct {
	Model                 *llm.Model[llm.Api] `json:"model,omitempty"`
	ThinkingLevel         llm.ThinkingLevel   `json:"thinkingLevel"`
	IsStreaming           bool                `json:"isStreaming"`
	IsCompacting          bool                `json:"isCompacting"`
	SteeringMode          string              `json:"steeringMode"`           // all | one-at-a-time
	FollowUpMode          string              `json:"followUpMode"`           // all | one-at-a-time
	SessionFile           string              `json:"sessionFile,omitempty"`
	SessionID             string              `json:"sessionId"`
	SessionName           string              `json:"sessionName,omitempty"`
	AutoCompactionEnabled bool                `json:"autoCompactionEnabled"`
	MessageCount          int                 `json:"messageCount"`
	PendingMessageCount   int                 `json:"pendingMessageCount"`
}

// ============================================================================
// RPC Responses (stdout) — TS RpcResponse union
// ============================================================================

// RpcResponse is emitted on stdout. Command is the discriminator; Success=false
// carries an Error; Success=true carries command-specific Data (or none).
type RpcResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"` // always ResponseType
	Command string `json:"command"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`        // success=false
	Data    any    `json:"data,omitempty"`         // success=true, command-specific
}

// NewSuccessResponse builds a success response envelope. The nil handling
// mirrors the TS success() helper (rpc-mode.ts:57-66), which distinguishes
// undefined from null: untyped nil omits the data key (TS undefined); a typed
// nil pointer survives omitempty and marshals as "data":null (TS null).
func NewSuccessResponse(id, command string, data any) RpcResponse {
	return RpcResponse{ID: id, Type: ResponseType, Command: command, Success: true, Data: data}
}

// NewErrorResponse builds the error response envelope (any command can fail).
func NewErrorResponse(id, command, message string) RpcResponse {
	return RpcResponse{ID: id, Type: ResponseType, Command: command, Success: false, Error: message}
}

// ============================================================================
// Extension UI requests (stdout) — TS RpcExtensionUIRequest union
// ============================================================================

// RpcExtensionUIMethod values.
const (
	MethodSelect        = "select"
	MethodConfirm       = "confirm"
	MethodInput         = "input"
	MethodEditor        = "editor"
	MethodNotify        = "notify"
	MethodSetStatus     = "setStatus"
	MethodSetWidget     = "setWidget"
	MethodSetTitle      = "setTitle"
	MethodSetEditorText = "set_editor_text"
)

// RpcNotifyType values for the notify method.
const (
	NotifyInfo    = "info"
	NotifyWarning = "warning"
	NotifyError   = "error"
)

// RpcWidgetPlacement values for the setWidget method.
const (
	WidgetPlacementAboveEditor = "aboveEditor"
	WidgetPlacementBelowEditor = "belowEditor"
)

// RpcExtensionUIRequest is emitted on stdout when an extension needs user input.
// Method is the discriminator; only the relevant fields are populated.
type RpcExtensionUIRequest struct {
	Type   string `json:"type"` // always ExtUIRequestType
	ID     string `json:"id"`
	Method string `json:"method"`

	// select / confirm / input / editor / notify / setTitle
	Title       string   `json:"title,omitempty"`
	Options     []string `json:"options,omitempty"` // select
	// DefaultIdx pre-selects a select entry. ycode extension on top of the
	// reference's wire shape (the reference has no defaultIndex); hosts that
	// don't know the field ignore it.
	DefaultIdx  int      `json:"defaultIdx,omitempty"`  // select
	Timeout     int      `json:"timeout,omitempty"`     // select/confirm/input (ms)
	Message     string   `json:"message,omitempty"`     // confirm / notify
	NotifyType  string   `json:"notifyType,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"` // input
	Prefill     string   `json:"prefill,omitempty"`     // editor

	// setStatus
	StatusKey  string `json:"statusKey,omitempty"`
	StatusText string `json:"statusText,omitempty"` // string | undefined

	// setWidget
	WidgetKey       string   `json:"widgetKey,omitempty"`
	WidgetLines     []string `json:"widgetLines,omitempty"`     // string[] | undefined
	WidgetPlacement string   `json:"widgetPlacement,omitempty"` // aboveEditor | belowEditor

	// set_editor_text
	Text string `json:"text,omitempty"`
}

// ============================================================================
// Extension UI responses (stdin) — TS RpcExtensionUIResponse union
// ============================================================================

// RpcExtensionUIResponse is the client's reply to an RpcExtensionUIRequest.
//
// Confirmed is *bool (not bool) so a confirm response of `false` is
// distinguishable from "field absent": the TS union carries `confirmed: boolean`
// where false is meaningful, and a plain bool + omitempty would drop it.
type RpcExtensionUIResponse struct {
	Type      string `json:"type"` // always ExtUIResponseType
	ID        string `json:"id"`
	Value     string `json:"value,omitempty"`     // input / select / editor result
	Confirmed *bool  `json:"confirmed,omitempty"` // confirm result (false is meaningful)
	Cancelled bool   `json:"cancelled,omitempty"` // TS sends cancelled:true only
}
