package compaction

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"ycode/packages/llm/src"
)

// FileOperations: Tracks which files have been read written, or edited during a session
type FileOperations struct {
	Read 	map[string]bool
	Written map[string]bool
	Edited 	map[string]bool
}

// CreateFileOps: Creates a new FileOperations with empty sets
func CreateFileOps() *FileOperations {
	return &FileOperations{
		Read: make(map[string]bool),
		Written: make(map[string]bool),
		Edited: make(map[string]bool),
	}
}

// ExtractFileOpsFromMessage: Extracts file operations from tool calls in an assistant message.
func ExtractFileOpsFromMessage(msg llm.Message, fileOps *FileOperations) {
	assistant, ok := msg.(llm.AssistantMessage)
	if !ok {
		return
	}
	for _, block := range assistant.Content {
		tc, ok := block.(llm.ToolCall)
		if !ok {
			continue
		}
		pathVal, ok := tc.Arguments["path"]
		if !ok {
			continue
		}
		path, ok := pathVal.(string)
		if !ok || path == "" {
			continue
		}
		switch tc.Name {
		case "read":
			fileOps.Read[path] = true
		case "write":
			fileOps.Written[path] = true
		case "edit":
			fileOps.Edited[path] = true
		}
	}
}

// ComputeFileLists returns read-only files and modified files from operations
func ComputeFileLists(fileOps *FileOperations) (readFiles []string, modifiedFiles []string) {
	modified := make(map[string]bool)
	for f := range fileOps.Edited {
		modified[f] = true
	}
	for f := range fileOps.Written {
		if !modified[f] {
			readFiles = append(readFiles, f)
		}
	}
	for f := range modified {
		modifiedFiles= append(modifiedFiles, f)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return
}

// FormatFileOperations: Formats file operations as XML tags for summary.
func FormatFileOperations(readFiles []string, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, fmt.Sprintf("<read-files>\n%s\n</read-files>", strings.Join(readFiles, "\n")))
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, fmt.Sprintf("<modified-files>\n%s\n</modified-files>", strings.Join(modifiedFiles, "\n")))
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}



// --- Message Serialization ---

const toolResultMaxChars = 2000

/*
truncateForSummary: Truncates text to a maximum characted length for summarization.
 - Keeps the beginning and appends a truncation marker.
*/
func truncateForSummary(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	truncatedChars := len(text) - maxChars
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", text[:maxChars], truncatedChars)
}

/*
SerializeConversation: Serializes LLM messages to text for sumaarization.
 - This prevents the model from treating it as a conversation to continue.
 - Tool results are truncated to keep the summarization request within reasonable token budgets.
*/
func SerializeConversation(messages []llm.Message) string {
	var parts []string

	for _, msg := range messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			var texts []string
			for _, block := range m.Content {
				if tc, ok := block.(llm.TextContent); ok {
					texts = append(texts, tc.Text)
				}
			}
			content := strings.Join(texts, "")
			if content != "" {
				parts = append(parts, fmt.Sprintf("[User]: %s", content))
			}
		case llm.AssistantMessage:
			var textParts []string
			var thinkingParts []string
			var toolCalls []string

			for _, block := range m.Content {
				switch b := block.(type) {
				case llm.TextContent:
					textParts = append(textParts, b.Text)
				case llm.ThinkingContent:
					thinkingParts = append(thinkingParts, b.Thinking)
				case llm.ToolCall:
					var argParts []string
					for k, _ := range b.Arguments {
						jsonVal, _ := json.Marshal(b.Arguments[k])
						argParts = append(argParts, fmt.Sprintf("%s=%s", k, string(jsonVal)))
					}
					sort.Strings(argParts)
					toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", b.Name, strings.Join(argParts, ", ")))
				}
			}

			if len(thinkingParts) > 0 {
				parts = append(parts, fmt.Sprintf("[Assistant thinking]: %s", strings.Join(thinkingParts, "\n")))
			}
			if len(textParts) > 0 {
				parts = append(parts, fmt.Sprintf("[Assistant]: %s", strings.Join(textParts, "\n")))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, fmt.Sprintf("[Assistant tool calls]: %s", strings.Join(toolCalls, "; ")))
			}
		case llm.ToolResultMessage[any]:
			var texts []string
			for _, block := range m.Content {
				if tc, ok := block.(llm.TextContent); ok {
					texts = append(texts, tc.Text)
				}
			}
			content := strings.Join(texts, "")
			if content != "" {
				parts = append(parts, fmt.Sprintf("[Tool result]: %s", truncateForSummary(content, toolResultMaxChars)))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// ============================================================================
// Summarization System Prompt
// ============================================================================

// SummarizationSystemPrompt is the system prompt used for summarization tasks.
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI coding assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`



/*
KEYNOTES: 
 - No reflect import needed — the agent-sdk always stores tool results as 
 llm.ToolResultMessage[any] (confirmed in agent_loop.go:672), so we can 
 type-assert directly in the SerializeConversation switch

 - TS Set<string> → Go map[string]bool — idiomatic Go equivalent for 
 tracking unique strings

 - TS typeof msg.content === "string" check not needed — Go's type system 
 enforces Content []ContentBlock always; TS LLM messages can have string 
 content for simple text, but in Go this is structurally guaranteed to 
 be an array

 - Tool call argument sort order — the TS code uses Object.entries() which 
 preserves insertion order; Go maps iterate in random order so I sort the 
 keys for deterministic output. This is a minor deviation but actually an 
 improvement.

 - The llm.ToolCall type assertion uses value type (not pointer), matching 
 the pattern established in agent_loop.go:698
*/