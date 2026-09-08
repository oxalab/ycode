package compaction

import (
	"context"
	"ycode/packages/llm/src"
)

// ============================================================================
// Session Entry Types
// ============================================================================

/*
In the TS reference, SessionEntry is a discriminated union defined in
session-manager.ts. Here it lives in the compaction package to avoid circular imports
(session-manager.go imports compaction, so compaction cannot import the parent ycode package.)

When session_manager.go is created, it will use these types directly
*/

// SessionEntryType: Discriminates session entry variants.
type SessionEntryType string

const (
	EntryTypeMessage 		SessionEntryType = "message"
	EntryTypeThinkingLevelChange SessionEntryType = "thinking_level_change"
	EntryTypeModelChange 	SessionEntryType = "model_change"
	EntryTypeCompaction 	SessionEntryType = "compaction"
	EntryTypeBranchSummary  SessionEntryType = "branch_summary"
	EntryTypeCustom         SessionEntryType = "custom"
	EntryTypeCustomMessage  SessionEntryType = "custom_message"
	EntryTypeLabel          SessionEntryType = "label"
	EntryTypeSessionInfo    SessionEntryType = "session_info"
)

// SessionEntry: Represents a node in the session tree.
//  - Fields are populated based on Type; unused fields are zero values.

type SessionEntry struct {
	Type 	SessionEntryType `json:"type"`
	ID 		string 			`json:"id"`
	ParentID 	string 		`json:"parentId,omitempty"`
	Timestamp int64 		`json:"timestamp"`	

	// EntryTypeMessage
	Message 	llm.Message `json:"message,omitempty"`

	// EntryTypeBranchSummary
	FromID 		string 		`json:"fromId,omitempty"`
	Summary 	string 		`json:"summary,omitempty"`
	Details 	any 		`json:"details,omitempty"`
	FromHook 	bool 		`json:"fromHook,omitempty"`

	// EntryTypeCompaction
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`
	
	// EntryTypeCustomMessage
	CustomType 	string 		`json:"customType,omitempty"`
	Content 	any 		`json:"content,omitempty"`
	Display 	bool 		`json:"display,omitempty"`

	// EntryTypeModelChange
	Provider 	string 		`json:"provider,omitempty"`
	ModelID 	string 		`json:"modelId,omitempty"`

	// EntryTypeThinkingLevelChange
	ThinkingLevel 	string 	`json:"thinkingLevel,omitempty"`

	// EntryTypeLabel
	Label 		string 		`json:"label,omitempty"`

	// EntryTypeCustom
	Data 		any 		`json:"data,omitempty"`

	// EntryTypeSessionInfo
	Info 		map[string]any 	`json:"info,omitempty"`

	// EntryTypeLabel
	TargetID string `json:"targetId,omitempty"`

	// EntryTypeSessionInfo
	Name string `json:"name,omitempty"`

}

// GetMessage: Returns the llm.Message for message entries, nil otherwise
func (e *SessionEntry) GetMessage() llm.Message {
	if e.Type == EntryTypeMessage {
		return e.Message
	}
	return nil
}

/*
 --- Session Manager Interfaces ---
*/

// ReadonlySessionManager: Provides read-only access to session tree.
type ReadonlySessionManager interface {
	// GetBranch returns entries from root to the given leaf, inclusive.
	GetBranch(leafID string) []*SessionEntry
	// GetEntry returns a single entry by ID, or nil if not found.
	GetEntry(id string) *SessionEntry
}



// ============================================================================
// Branch Summarization Types
// ============================================================================

// BranchSummaryResult: outcome of generating a branch summary
type BranchSummaryResult struct {
	Summary 	string
	ReadFiles 	[]string
	ModifiedFiles []string
	Aborted 	bool
	Error 		string
}

// BranchSummaryDetails: Stores file tracking data in a BranchSummaryEntry.
type BranchSummaryDetails struct {
	ReadFiles 	[]string 	`json:"readFiles"`
	ModifiedFiles []string 	`json:"modifiedFiles"`
}


// BranchPreparation: Holds prepared messages and metadata for summarization.
type BranchSummarization struct {
	Messages 	[]llm.Message
	FileOps 	*FileOperations
	TotalTokens int
}

// BranchPreparation holds prepared messages and metadata for summarization.
type BranchPreparation struct {
	Messages    []llm.Message
	FileOps     *FileOperations
	TotalTokens int
}

// CollectEntriesResult: Holds the entries to summarize and the common ancestor.
type CollectEntriesResult struct {
	Entries 	[]*SessionEntry
	CommonAncestorID 	string
}

// GenerateBranchSummaryOptions: Configures branch summary generation
type GenerateBranchSummaryOptions struct {
	Ctx 	context.Context
	Model 	*llm.Model[llm.Api]
	APIKey 	string
	Headers map[string]string
	CustomInstructions string
	ReplaceInstructions bool
	ReserveTokens 	int
}

// ============================================================================
// Compaction Types
// ============================================================================

// CompactionDetails: Stores file tracking data in a CompactionEntry.
type CompactionDetails struct {
	ReadFiles 	[]string 	`json:"readFiles"`
	ModifiedFiles []string 	`json:"modifiedFiles"`
}

// CompactionResult: Holds the outcome of a compaction operation.
type CompactionResult struct {
	Summary 	string `json:"summary"`
	TokensBefore int `json:"tokensBefore"`
	TokensAfter  int `json:"tokensAfter"`
	FirstKeptEntry *SessionEntry `json:"firstKeptEntry"`
	ReadFiles 	[]string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
	Error 		string `json:"error,omitempty"`
}



// ============================================================================
// External Dependencies (to break circular imports)
// ===========================================================================

/*
The compaction package cannot impoty the parent ycode package because
session_manager.go will import compaction. Instead, the parent provides
these functions through the Deps struct when calling compact functions.
*/

// Deps: Holds functions provided by the parent package to avoid circular imports
type Deps struct {
	// ConvertToLLlm: converts a mix of message types to LLM-compatible messages.
	ComvertToLlm func(messages []any) []llm.Message
	// CreateBranchSummaryMessage creates a user message wrapping a branch summary.
	CreateBranchSummaryMessage func(summary, fromID string, timestamp int64) llm.Message

	// CreateCompactionSummaryMessage creates a user message wrapping a compaction summary.
	CreateCompactionSummaryMessage func(summary string, tokensBefore int, timestamp int64) llm.Message

	// CreateCustomMessage creates a user message for extension-injected content.
	CreateCustomMessage func(customType string, content any, display bool, details any, timestamp int64) llm.Message

	// CompleteSimple performs a non-streaming LLM completion.
	CompleteSimple func(ctx context.Context, model *llm.Model[llm.Api], systemPrompt string, messages []llm.Message, apiKey string, headers map[string]string, maxTokens int, thinkingLevel llm.ThinkingLevel) (*CompletionResult, error) 

}

// CompletionResult: Result of a non-streaming LLM completion.
type CompletionResult struct {
	Content 	[]llm.ContentBlock
	StopReason 	string
	ErrorMessage string
}



/*
KEYNOTES:
 1. SessionEntry as a flat struct — Go doesn't have union types. 
 Instead of 9 separate struct types with an interface, I use one struct 
 with a Type discriminator. Fields relevant only to certain types are 
 zero-valued for others. This is cleaner and avoids the need for 9 type 
 assertions.

 2. Deps struct for circular import break — session-manager.go (package ycode) 
 will import compaction, so compaction can't import ycode. The parent provides 
 its functions (ConvertToLlm, CreateBranchSummaryMessage, CompleteSimple, etc.) 
 through the Deps struct when calling into compaction.

 3. ReadonlySessionManager as interface — session-manager.go will implement this. 
 The compaction package only depends on the interface, not the concrete type.

 4. *llm.Model[llm.Api] — matches the generic model type from the llm package, 
 which has ContextWindow int (needed for token budget calculation).
*/