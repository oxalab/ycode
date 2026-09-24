package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"ycode/packages/llm/src"
)

// ============================================================================
// Token Estimation
// ============================================================================

/*
EstimateTokens: Estimates token count for a message using chars/4 heuristic.
 - Handless standard llm message types. Custom types converted to UserMessages.
 by the Dep factory functions are handled as user messages.
*/

func EstimateTokens(message llm.Message) int {
	var chars int

	switch m := message.(type) {
	case llm.UserMessage:
		for _, block := range m.Content {
			if tc, ok := block.(llm.TextContent); ok {
				chars += len(tc.Text)
			}
		}
	case llm.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case llm.TextContent:
				chars += len(b.Text)
			case llm.ThinkingContent:
				chars += len(b.Thinking)
			case llm.ToolCall:
				argJSON, _ := json.Marshal(b.Arguments)
				chars += len(b.Name) + len(argJSON)
			}
		}
	case llm.ToolResultMessage[any]:
		for _, block := range m.Content {
			switch b := block.(type) {
			case llm.TextContent:
				chars += len(b.Text)
			case llm.ImageContent:
				chars += 4800 // ~1200 tokens per image
			}
		}
	}
	if chars == 0 {
		return 0
	}
	return (chars+3) / 4
}

// ============================================================================
// File Operation Extraction
// ============================================================================

/*
extractFileOperations: Collects file operations from messages and a previous
 compaction entry's details
*/
func extractFileOperations(
	messages 	[]llm.Message,
	entries 	[]*SessionEntry,
	prevCompactionIndex int,
) *FileOperations {
	fileOps := CreateFileOps()

	// Collect from previous compactions's details (if ycode-generated)
	if prevCompactionIndex >= 0 {
		prev := entries[prevCompactionIndex]
		if !prev.FromHook && prev.Details != nil {
			if details, ok := prev.Details.(*CompactionDetails); ok {
				for _, f := range details.ReadFiles {
					fileOps.Read[f] = true
				}
				for _, f := range details.ModifiedFiles {
					fileOps.Edited[f] = true
				}
			}
		}
	}

	// Extract from tool calls in messages
	for _, msg := range messages {
		ExtractFileOpsFromMessage(msg, fileOps)
	}
	return fileOps
}

// ============================================================================
// Message Extraction from Session Entries
// ============================================================================

/*
getMessageFromEntry: Extract an llm.Message from session entry.
 - Return nil for entries that don't contribute to LLM context.
*/
func getMessageFromEntry(entry *SessionEntry, deps *Deps) llm.Message {
	switch entry.Type {
	case EntryTypeMessage:
		// Skip tool results - context is in the assistant's tool call
		if _, ok := entry.Message.(llm.ToolResultMessage[any]); ok {
			return nil
		}
		return entry.Message
	
	case EntryTypeCustomMessage:
		return deps.CreateCustomMessage(
			entry.CustomType, entry.Content, entry.Display, entry.Details, entry.Timestamp,
		)
	case EntryTypeBranchSummary:
		return deps.CreateBranchSummaryMessage(entry.Summary, entry.FromID, entry.Timestamp)
	case EntryTypeCompaction:
		return deps.CreateCompactionSummaryMessage(entry.Summary, entry.TokensBefore, entry.Timestamp)
	default:
		return nil
	}
}

// getMessageFromEntryCompaction: This is getMessageFromEntry but skips the compaction entries.
func getMessageFromEntryForCompaction(entry *SessionEntry, deps *Deps) llm.Message {
	if entry.Type == EntryTypeCompaction {
		return nil
	}
	return getMessageFromEntry(entry, deps)
}

// ============================================================================
// Compaction Settings
// ============================================================================

// CompactionSettings: Controls when and how compaction triggers.
type CompactionSettings struct {
	Enabled 	bool  	`json:"enabled"`
	ReserveTokens int 	`json:"reserveTokens"`
	KeepRecentTokens int 	`json:"keepRecentTokens"`
}

// DefaultCompactionSettings: Provides sensible defaults.
var DefaultCompactionSettings = CompactionSettings{
	Enabled: 			true,
	ReserveTokens: 		16384,
	KeepRecentTokens: 	20000,
}

// ============================================================================
// Token Calculation Helpers
// ============================================================================


// CalculateContextTokens: Computes total context tokens from usage data.
func CalculateContextTokens(usage llm.Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func getAssistantUsage(msg llm.Message) *llm.Usage {
	assistant, ok := msg.(llm.AssistantMessage)
	if !ok {
		return nil
	}
	if assistant.StopReason == llm.StopReasonAborted || assistant.StopReason == llm.StopReasonError {
		return nil
	}
	if assistant.Usage.TotalTokens == 0 && assistant.Usage.Input == 0 && assistant.Usage.Output == 0 {
		return nil
	}
	return &assistant.Usage
}

type assistantUsageInfo struct {
	Usage 	llm.Usage
	Index 	int
}

// GetLastAssistantUsage: Finds the last non-aborted assistant message usage from entries.
func GetLastAssistantUsage(entries []*SessionEntry) *llm.Usage {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == EntryTypeMessage {
			if usage := getAssistantUsage(entries[i].Message); usage != nil {
				return usage
			}
		}
	}
	return nil
}

// ContextUsageEstimate: Holds the result of context token estimation.
type ContextUsageEstimate struct {
	Tokens 		int
	UsageTokens int
	TrailingTokens int
	LastUsageIndex int
}

func getLastAssistantUsageInfo(messages []llm.Message) *assistantUsageInfo {
	for i := len(messages) - 1; i >= 0; i-- {
		if usage := getAssistantUsage(messages[i]); usage != nil {
			return &assistantUsageInfo{Usage: *usage, Index: i}
		}
	}
	return nil
}

/*
EstimateContextTokens: Estimates context tokens from messages, using the lastt 
 assistant usage when available. If there are messages after the last usage,
 their tokens are estimated with EstimateTokens.
*/

func EstimateContextTokens(messages []llm.Message) ContextUsageEstimate {
	usageInfo := getLastAssistantUsageInfo(messages)

	if usageInfo == nil {
		var estimated int
		for _, msg := range messages {
			estimated += EstimateTokens(msg)
		}
		return ContextUsageEstimate{
			Tokens: 	estimated,
			UsageTokens: 0,
			TrailingTokens: estimated,
			LastUsageIndex: -1,
		}
	}

	usageTokens := CalculateContextTokens(usageInfo.Usage)
	var trailingTokens int
	for i := usageInfo.Index + 1; i < len(messages); i++ {
		trailingTokens += EstimateTokens(messages[i])
	}

	return ContextUsageEstimate{
		Tokens: 	usageTokens + trailingTokens,
		UsageTokens: usageTokens,
		TrailingTokens: trailingTokens,
		LastUsageIndex: usageInfo.Index,
	}
}


// ShouldCompact: Checks if compaction should trigger based on context usage.
func ShouldCompact(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// ============================================================================
// Cut Point Detection
// ============================================================================

/*
findValidCutPoints: Returns indices of entries that are valid cut boundaries.
 - User and assistant messages are valid. Tool results are not (they must follow
 their tool call). Branch summaries and custom messages are user-role, so valid.
*/
func findValidCutPoints(entries []*SessionEntry, startIndex, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		switch entry.Type{
		case EntryTypeMessage:
			switch entry.Message.(type) {
			case llm.UserMessage, llm.AssistantMessage:
				cutPoints = append(cutPoints, i)
			}
		case EntryTypeBranchSummary, EntryTypeCustomMessage:
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

/*
FindTurnStartIndex: Walks backwards from entryIndex to find the user message
 that starts the turn. Returns -1 if no turn start is found.
*/
func FindTurnStartIndex(entries []*SessionEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		entry := entries[i]
		if entry.Type == EntryTypeBranchSummary || entry.Type == EntryTypeCustomMessage {
			return i
		}
		if entry.Type == EntryTypeMessage {
			if _, ok := entry.Message.(llm.UserMessage); ok {
				return i
			}
		}
	}
	return -1
}

// CutPointResult: Describes where to cut the session history.
type CutPointResult struct {
	FirstKeptEntryIndex 	int
	TurnStartIndex 			int
	IsSplitTurn 			bool
}

/*
FindCutPoint: Finds the cut point in session entries that keeps approximately
 keepRecentTokens. Walks backwards from newest, accumulating estimated sizes.
*/
func FindCutPoint(entries []*SessionEntry, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)

	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1, IsSplitTurn: false}
	}

	// Walk backwarss accumulating token estimates
	var accumulatedTokens int
	cutIndex := cutPoints[0]  // Default: keep from first message

	for i := endIndex - 1; i >= startIndex; i -- {
		entry := entries[i]
		if entry.Type != EntryTypeMessage {
			continue
		}
		messageTokens := EstimateTokens(entry.Message)
		accumulatedTokens += messageTokens

		if accumulatedTokens >= keepRecentTokens {
			// Find closest valid cut point at or after this entry
			for _, c := range cutPoints {
				if c >= i {
					cutIndex = c
					goto done
				}
			}
			// No valid cut point at/after i (budget exceeded at a tool
			// result, etc.): stop and keep the default cutPoints[0] — the
			// TS walk breaks unconditionally here (compaction.ts:419).
			break
		}
	}
done:
	// Scan backwards from cutIndex to include non-message entries (settings, etc.)
	for cutIndex > startIndex {
		prevEntry := entries[cutIndex-1]
		if prevEntry.Type == EntryTypeCompaction || prevEntry.Type == EntryTypeMessage {
			break
		}
		cutIndex--
	}

	// Determing if this is a split turn
	cutEntry := entries[cutIndex]
	_, isUserMessage := cutEntry.Message.(llm.UserMessage)
	isUserMessage = isUserMessage || cutEntry.Type == EntryTypeBranchSummary || cutEntry.Type == EntryTypeCustomMessage

	var turnStartIndex int
	if isUserMessage {
		turnStartIndex = -1
	} else {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex: 	 turnStartIndex,
		IsSplitTurn: 		 !isUserMessage && turnStartIndex != -1,
	}
}

// ============================================================================
// Summarization Prompts
// ============================================================================

const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const turnPrefixSummarizationPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`

// ============================================================================
// Summarization Functions
// ============================================================================


// messagesToAny: Converts []llm.Message to []any for ConvertToLlm.
func messagesToAny(messages []llm.Message) []any {
	anys := make([]any, len(messages))
	for i, m := range messages {
		anys[i] = m
	}
	return anys
}

func extractTextFromContent(blocks []llm.ContentBlock) string {
	var texts []string
	for _, block := range blocks {
		if tc, ok := block.(llm.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "\n")
}

/*
GenerateSummary: Generates a summary of the conversation using the LLM.
 If previousSummary is provided, uses the update prompt to merge.
*/
func GenerateSummary(
	ctx context.Context,
	deps *Deps,
	currentMessages []llm.Message,
	model *llm.Model[llm.Api],
	reserveTokens int,
	apiKey 	string,
	headers map[string]string,
	customInstructions string,
	previousSummary	string,
	thinkingLevel llm.ThinkingLevel,
) (string, error) {
	maxTokens := (reserveTokens * 4) / 5   // 80% of the reserve
	if model.MaxTokens > 0 && maxTokens > model.MaxTokens {
		maxTokens = model.MaxTokens
	}

	// Use update prompt if we have a previous summary, otherwise initial prompt
	basePrompt := summarizationPrompt
	if previousSummary != "" {
		basePrompt = updateSummarizationPrompt
	}
	if customInstructions != "" {
		basePrompt = fmt.Sprintf("%s\n\nAdditional focus: %s", basePrompt, customInstructions)
	}

	// Serialize conversation to text so model doesn't try to continue it
	llmMessages := deps.ComvertToLlm(messagesToAny(currentMessages))
	conversationText := SerializeConversation(llmMessages)

	// Build the prompt with conversation wrapped in tags
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n", conversationText)
	if previousSummary != "" {
		promptText += fmt.Sprintf("<previous-summary>\n%s\n</previous-summary>\n\n", previousSummary)
	}
	promptText += basePrompt

	summarizationMessages := []llm.Message{
		llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{
				llm.TextContent{Type: "text", Text: promptText},
			},
			Timestamp: time.Now().UnixMilli(),
		},
	}
	result, err := deps.CompleteSimple(
		ctx, model, SummarizationSystemPrompt, summarizationMessages,
		apiKey, headers, maxTokens, thinkingLevel,
	)
	if err != nil {
		return "", err
	}
	if result.StopReason == string(llm.StopReasonError) {
		return "", fmt.Errorf("summarization failed: %s", result.ErrorMessage)
	}
	return extractTextFromContent(result.Content), nil
}

func generateTurnPrefixSummary(
	ctx context.Context,
	deps *Deps,
	messages []llm.Message,
	model *llm.Model[llm.Api],
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	thinkingLevel llm.ThinkingLevel,
) (string, error) {
	maxTokens := reserveTokens / 2  // Smaller budget for turn prefix
	if model.MaxTokens > 0 && maxTokens > model.MaxTokens {
		maxTokens = model.MaxTokens
	}

	llmMessages := deps.ComvertToLlm(messagesToAny(messages))
	conversationText := SerializeConversation(llmMessages)
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, turnPrefixSummarizationPrompt)

	summarizationMessages := []llm.Message{
		llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{
				llm.TextContent{Type: "text", Text: promptText},
			},
			Timestamp: time.Now().UnixMilli(),
		},
	}
	result, err := deps.CompleteSimple(
		ctx, model, SummarizationSystemPrompt, summarizationMessages,
		apiKey, headers, maxTokens, thinkingLevel,
	)
	if err != nil {
		return "", err
	}
	if result.StopReason == string(llm.StopReasonError) {
		return "", fmt.Errorf("turn prefix summarization failed: %s", result.ErrorMessage)
	}
	return extractTextFromContent(result.Content), nil
}


// ============================================================================
// Compaction Preparation
// ============================================================================


//  CompactionPreparation: Holds pre-calculated data for a compaction operation
type CompactionPreparation struct {
	FirstKeptEntryID 	string
	MessagesToSummarize []llm.Message
	TurnPrefixMessages  []llm.Message
	IsSplitTurn 		bool
	TokensBefore 		int
	PreviousSummary 	string
	FileOps 			*FileOperations
	Settings 			CompactionSettings
}

/*
PrepareCompaction: Analyzes session entries and prepares data for compaction.
 - sessionManager should be pre-built by the called using buildSessionContext.
 - Returns nil if compaction is not possible (e.g. last entry is already a compaction)
*/
func PrepareCompaction(
	pathEntries []*SessionEntry,
	sessionMessages []llm.Message,
	settings 	CompactionSettings,
	deps 		*Deps,
) *CompactionPreparation {
	if len(pathEntries) > 0 && pathEntries[len(pathEntries)-1].Type == EntryTypeCompaction {
		return nil
	}

	// Find previous compaction boundary
	prevCompactionIndex := -1
	for i := len(pathEntries) - 1; i >= 0; i-- {
		if pathEntries[i].Type == EntryTypeCompaction {
			prevCompactionIndex = i
			break
		}
	}
	var previousSummary string
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		previousSummary = pathEntries[prevCompactionIndex].Summary
		firstKeptEntryIndex := -1
		for i, entry := range pathEntries {
			if entry.ID == pathEntries[prevCompactionIndex].FirstKeptEntryID {
				firstKeptEntryIndex = i
				break
			}
		}
		if firstKeptEntryIndex >= 0 {
			boundaryStart = firstKeptEntryIndex
		} else {
			boundaryStart = prevCompactionIndex + 1
		}
	}
	boundaryEnd := len(pathEntries)
	tokensBefore := EstimateContextTokens(sessionMessages).Tokens
	cutPoint := FindCutPoint(pathEntries, boundaryStart, boundaryEnd, settings.KeepRecentTokens)

	firstKeptEntry := pathEntries[cutPoint.FirstKeptEntryIndex]
	if firstKeptEntry.ID == "" {
		return nil  // Session needs migration
	}
	firstKeptEntryID := firstKeptEntry.ID
	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}

	// Messages to summarize (will be discarded after summary)
	var messagesToSummarize []llm.Message
	for i := boundaryStart; i < historyEnd; i++ {
		msg := getMessageFromEntryForCompaction(pathEntries[i], deps)
		if msg != nil {
			messagesToSummarize = append(messagesToSummarize, msg)
		}
	}

	// Messages for turn prefix summary (if splitting a turn)
	var turnPrefixMessages []llm.Message
	if cutPoint.IsSplitTurn {
		for i := cutPoint.TurnStartIndex; i < cutPoint.FirstKeptEntryIndex; i++ {
			msg := getMessageFromEntryForCompaction(pathEntries[i], deps)
			if msg != nil {
				turnPrefixMessages = append(turnPrefixMessages, msg)
			}
		}
	}

	// Extract file operations from messages and previous compaction
	fileOps := extractFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex)

	// Also extract file ops from turn prefix if splitting
	if cutPoint.IsSplitTurn {
		for _, msg := range turnPrefixMessages {
			ExtractFileOpsFromMessage(msg, fileOps)
		}
	}

	return &CompactionPreparation{
		FirstKeptEntryID:    firstKeptEntryID,
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}
}

// ============================================================================
// Main Compaction Function
// ============================================================================


/*
Compact: Generates summaries for compaction using prepared data.
 - Returns a CompactionResult - the caller (SessionManager) adds ID/ParentID when saving.
*/
func Compact(
	ctx context.Context,
	deps *Deps,
	preparation *CompactionPreparation,
	model *llm.Model[llm.Api],
	apiKey string,
	headers map[string]string,
	customInstructions string,
	thinkingLevel llm.ThinkingLevel,
) (*CompactionResult, error) {
	firstKeptEntryID := preparation.FirstKeptEntryID
	messagesToSummarize := preparation.MessagesToSummarize
	turnPrefixMessages := preparation.TurnPrefixMessages
	isSplitTurn := preparation.IsSplitTurn
	tokensBefore := preparation.TokensBefore
	previousSummary := preparation.PreviousSummary
	fileOps := preparation.FileOps
	settings := preparation.Settings

	var summary string
	var err error

	if isSplitTurn && len(turnPrefixMessages) > 0 {
		// Generate both summaries in parallel
		var historyResult, turnPrefixResult string
		var historyErr, turnPrefixErr error

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			if len(messagesToSummarize) > 0 {
				historyResult, historyErr = GenerateSummary(
					ctx, deps, messagesToSummarize, model,
					settings.ReserveTokens, apiKey, headers,
					customInstructions, previousSummary, thinkingLevel,
				)
			} else {
				historyResult = "No prior history."
			}
		}()

		go func() {
			defer wg.Done()
			turnPrefixResult, turnPrefixErr = generateTurnPrefixSummary(
				ctx, deps, turnPrefixMessages, model,
				settings.ReserveTokens, apiKey, headers, thinkingLevel,
			)
		}()

		wg.Wait()
		if historyErr != nil {
			return nil, historyErr
		}
		if turnPrefixErr != nil {
			return nil, turnPrefixErr
		}
		summary = fmt.Sprintf("%s\n\n---\n\n**Turn Context (split turn):**\n\n%s", historyResult, turnPrefixResult)
	} else {
		summary, err = GenerateSummary(
			ctx, deps, messagesToSummarize, model,
			settings.ReserveTokens, apiKey, headers,
			customInstructions, previousSummary, thinkingLevel,
		)
		if err != nil {
			return nil, err
		}
	}
	// Compute file lists and append to summary
	readFiles, modifiedFiles := ComputeFileLists(fileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if firstKeptEntryID == "" {
		return nil, fmt.Errorf("first kept entry has no UUID — session may need migration")
	}

	return &CompactionResult{
		Summary:      summary,
		TokensBefore: tokensBefore,
		ReadFiles:    readFiles,
		ModifiedFiles: modifiedFiles,
	}, nil
	
}


/*
FOOTNOTES:
 - EstimateTokens handles only standard llm.Message types (UserMessage, AssistantMessage, ToolResultMessage). 
 Custom types (BranchSummaryMessage, etc.) are converted to UserMessage by the Deps factory 
 functions before reaching this code. The token estimation uses the wrapped text which is 
 slightly conservative (over-estimates due to prefix/suffix) — acceptable

 - Circular dependency break — PrepareCompaction accepts pre-built sessionMessages []llm.Message 
 instead of calling buildSessionContext internally. The caller (session-manager.go) builds the context 
 and passes it in.

 - findCutPoint uses goto to break out of the nested loop — this is a valid Go idiom for this pattern. 
 The TS code uses a break inside a labeled loop; goto is the Go equivalent.

 - Parallel summarization in Compact uses sync.WaitGroup with two goroutines, matching the TS Promise.all pattern.

 - (chars + 3) / 4 replaces Math.ceil(chars / 4) — equivalent for non-negative integers, no math import needed.

 - llm.ThinkingLevel values are "minimal"/"low"/"medium"/"high"/"xhigh" — no "off" value in Go. 
 The CompleteSimple implementation should check if thinkingLevel is empty string (not set) to skip 
 reasoning. The caller passes empty string when reasoning is off.
*/