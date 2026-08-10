package compaction

import (
	"context"
	"fmt"
	"time"
	"ycode/packages/llm/src"
)

// ============================================================================
// Entry Collection
// ============================================================================

/*
CollectEntriesForBranchSummary: Collects entries that should be summarized
 when navigating from oldLeafId to targetId.

 Walks from oldLeadId back to the common ancestory with targetId, collecting
 entries along the way. Does NOT stop at compaction boundaries. Those are included and
 their summaries become context.
*/
func CollectEntriesFromBranchSummary(
	session ReadonlySessionManager,
	oldLeadID string,
	targetID string,
) *CollectEntriesResult {
	// If no old position, nothing to summarize
	if oldLeadID == "" {
		return &CollectEntriesResult{}
	}

	// Find common ancestor (deepest node on both paths)
	oldBranch := session.GetBranch(oldLeadID)
	oldPathIDs := make(map[string]bool, len(oldBranch))
	for _, e := range oldBranch {
		oldPathIDs[e.ID] = true
	}

	targetPath := session.GetBranch(targetID)

	// targetPath is root-first, so iterate backwards to find deepest common ancestor
	var commonAncestorID string
	for i := len(targetPath) - 1; i >=0; i-- {
		if oldPathIDs[targetPath[i].ID] {
			commonAncestorID = targetPath[i].ID
			break
		}
	}

	// Collect entries from old leaf back to common ancestor
	var entries []*SessionEntry
	current := oldLeadID

	for current != "" && current != commonAncestorID {
		entry := session.GetEntry(current)
		if entry == nil {
			break
		}
		entries = append(entries, entry)
		current = entry.ParentID
	}
	// Reverse to get chronological order
	for i, j := 0, len(entries) - 1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	
	return &CollectEntriesResult{
		Entries: 	entries,
		CommonAncestorID: commonAncestorID,
	}
}


// ============================================================================
// Entry Preparation
// ============================================================================

/*
PrepareBranchEntries: Prepares entries for summarization with a tokek budget.

Walks entries from NEWEST to OLDEST, adding messages until the token budget
is hit. This ensures the most recent context is kept when the branch is long.

Also collects file operations from tool calls and existing branch_summary
entries' details (for cumulative tracking across nested summaries).
*/
func PrepareBranchEntries(
	entries []*SessionEntry,
	tokenBudget int,
	deps *Deps,
) *BranchPreparation {
	var messages []llm.Message
	fileOps := CreateFileOps()
	var totalTokens int

	/* First pass: collect file ops froom ALL entries (even beyond token budget).
	This captures cumulative file tracking from nested branch summaries.
	Only extract from ycode generated summaries (FromHook == false), not extension ones.
	*/
	for _, entry := range entries {
		if entry.Type == EntryTypeBranchSummary && !entry.FromHook && entry.Details != nil {
			if details, ok := entry.Details.(*BranchSummaryDetails); ok {
				for _, f := range details.ReadFiles {
					fileOps.Read[f] = true
				}
				for _, f := range details.ModifiedFiles {
					// Modified files fo into Edited for proper deduplication
					fileOps.Edited[f] = true
				}
			}
		}
	}

	// Second pass: Walk from newest to oldest, adding messages until token budget
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		message := getMessageFromEntry(entry, deps)
		if message == nil {
			continue
		}

		// Extract file ops from assistant messages (tool calls)
		ExtractFileOpsFromMessage(message, fileOps)

		tokens := EstimateTokens(message)

		// Check budget before adding
		if tokenBudget > 0 && totalTokens + tokens > tokenBudget {
			// If this is a summary entry, try to fit it anyways as its important context
			if entry.Type == EntryTypeCompaction || entry.Type == EntryTypeBranchSummary {
				if totalTokens < (tokenBudget*9)/10 {   // < 90% of the budget
					messages = prependMessage(messages, message)
					totalTokens += tokens
				}
			}
			// Stop - We've hit the budget
			break
		}
		messages = prependMessage(messages, message)
		totalTokens += tokens
	}
	return &BranchPreparation{
		Messages: messages,
		FileOps: fileOps,
		TotalTokens: totalTokens,
	}
}

// prependMessage inserts at front (simulating unshift) without quadratic cost.
func prependMessage(slice []llm.Message, msg llm.Message) []llm.Message{
	slice = append(slice, nil)
	copy(slice[1:], slice)
	slice[0] = msg
	return slice
}

// ============================================================================
// Summary Generation
// ============================================================================

const branchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

const branchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// GenerateBranchSummary: Generates a summary of abandoned branch entries.
func GenerateBranchSummary(
	ctx context.Context,
	deps *Deps,
	entries []*SessionEntry,
	options *GenerateBranchSummaryOptions,
) *BranchSummaryResult {
	model := options.Model
	apiKey := options.APIKey
	headers := options.Headers
	customInstructions := options.CustomInstructions
	replaceInstructions := options.ReplaceInstructions
	reserveTokens := options.ReserveTokens
	if reserveTokens == 0 {
		reserveTokens = 16384
	}

	// Token budget = context window minus reserved space + response
	contextWindow := model.ContextWindow
	if contextWindow == 0 {
		contextWindow = 128000
	}
	tokenBudget := contextWindow - reserveTokens
	preparation := PrepareBranchEntries(entries, tokenBudget, deps)
	if len(preparation.Messages) == 0 {
		return &BranchSummaryResult{Summary: "No content to summarize."}
	}
	
	/* Transform to LLM-compatible messages, then serialize to text.
	Serialization prevents the model from treating it as a conversation to continue. */
	llmMessages := deps.ComvertToLlm(messagesToAny(preparation.Messages))
	conversationText := SerializeConversation(llmMessages)

	// Build Prompt
	var instructions string
	if replaceInstructions && customInstructions != "" {
		instructions = customInstructions
	} else if customInstructions != "" {
		instructions = fmt.Sprintf("%s\n\nAdditional focus: %s", branchSummaryPrompt, customInstructions)
	} else {
		instructions = branchSummaryPrompt
	}
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, instructions)

	summarizationMessages := []llm.Message{
		llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{
				llm.TextContent{Type: "text", Text: promptText},
			},
			Timestamp: time.Now().UnixMilli(),
		},
	}

	// Call LLM for summarization
	result, err := deps.CompleteSimple(
		ctx, model, SummarizationSystemPrompt, summarizationMessages,
		apiKey, headers, 2048, "",
	)
	if err != nil {
		return &BranchSummaryResult{Error: err.Error()}
	}

	// Check if aborted or errored
	if result.StopReason == string(llm.StopReasonAborted) {
		return &BranchSummaryResult{Aborted: true}
	}
	if result.StopReason == string(llm.StopReasonError) {
		errMsg := "Summarization failed"
		if result.ErrorMessage != "" {
			errMsg = result.ErrorMessage
		}
		return &BranchSummaryResult{Error: errMsg}
	}

	summary := extractTextFromContent(result.Content)

	// Prepend preamble to provide context about the branch summary
	summary = branchSummaryPreamble + summary

	// Compure file lists and append to summary
	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if summary == "" {
		summary = "No summary generated"
	}
	return &BranchSummaryResult{
		Summary: summary,
		ReadFiles: readFiles,
		ModifiedFiles: modifiedFiles,
	}
}



/* KEYNOTES:
 1. Reuses getMessageFromEntry from compaction.go — the TS has a duplicate local version in 
 branch-summarization.ts, but since both are in the same Go package, we use the one from 
 compaction.go directly. It handles all entry types including compaction entries.

 2. Reuses messagesToAny and extractTextFromContent from compaction.go — same package, 
 no duplication needed.

 3. prependMessage helper — Go doesn't have Array.unshift(). The helper grows the slice 
 and shifts elements, achieving O(n) prepend instead of rebuilding each time.

 4. (tokenBudget*9)/10 replaces tokenBudget * 0.9 — integer math, no float conversion needed.

 5. GenerateBranchSummary returns *BranchSummaryResult — not (result, error). This matches the 
 TS pattern where errors are returned as part of the result struct (with Error and Aborted fields), 
 since branch summarization failure is non-fatal and should be handled gracefully by the caller.

 6. Empty string "" passed as thinkingLevel to `CompleteSimple** — branch summarization doesn't use reasoning.
*/