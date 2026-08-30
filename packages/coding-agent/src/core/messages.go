package ycode

import (
	"fmt"

	"ycode/packages/llm/src"
)

const CompactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
const CompactionSummarySuffix = "\n</summary>"
const BranchSummaryPrefix = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
const BranchSummarySuffix = "</summary>"

// BashExecutionMessage represents a bash execution in the conversation
type BashExecutionMessage struct {
	Command 		string
	Output 			string
	ExitCode 		*int
	Cancelled 		bool
	Truncated 		bool
	FullOutputPath  string
	Timestamp 		int64
	ExcludeFromContext bool
}

func (m BashExecutionMessage) Role() string { return "bashExecution"}

func (m BashExecutionMessage) IsMessage() {}

// CustomMessage is an extension-injected message
type CustomMessage struct {
	CustomType 	string
	Content 	any  //string or []llm.ContentBlock
	Display 	bool
	Details 	any
	Timestamp   int64
}

func (m CustomMessage) Role() string { return "custom" }

func (m CustomMessage) IsMessage() {}

// BranchSummaryMessage summarizes a branch the conversation returned from
type BranchSummaryMessage struct {
	Summary 	string
	FromID 		string
	Timestamp 	int64
}

func (m BranchSummaryMessage) Role() string { return "branchSummary" }

func (m BranchSummaryMessage) IsMessage() {}

// CompactionSummaryMessage summarizes compacted conversation history
type CompactionSummaryMessage struct {
	Summary 	string
	TokensBefore int
	Timestamp 	int64
}

func (m CompactionSummaryMessage) Role() string { return "compactionSummary" }

func (m CompactionSummaryMessage) IsMessage() {}

func BashExecutionToText(msg BashExecutionMessage) string {
	text := fmt.Sprintf("Ran `%s`\n", msg.Command)
	if msg.Output != "" {
		text += "```\n" + msg.Output + "\n```"
	} else {
		text += "(no output)"
	}
	if msg.Cancelled {
		text += "\n\n(command cancelled)"
	} else if msg.ExitCode != nil && *msg.ExitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *msg.ExitCode)
	}
	if msg.Truncated && msg.FullOutputPath != "" {
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", msg.FullOutputPath)
	}
	return text
}

func CreateBranchSummaryMessage(summary string, fromID string, timestamp int64) BranchSummaryMessage {
	return BranchSummaryMessage{
		Summary: summary,
		FromID: fromID,
		Timestamp: timestamp,
	}
}

func CreateCompactionSummaryMessage(summary string, tokensBefore int, timestamp int64) CompactionSummaryMessage {
	return CompactionSummaryMessage{
		Summary: summary,
		TokensBefore: tokensBefore,
		Timestamp: timestamp,
	}
}

func CreateCustomMessage(customType string, content any, display bool, details any, timestamp int64) CustomMessage {
	return CustomMessage{
		CustomType: customType,
		Content: 	content,
		Display: 	display,
		Details: 	details,
		Timestamp:  timestamp,
	}
}

// ConvertToLlm tranforms CodingMessages (including custom types) to LLM-compatible messages
func ConvertToLlm(messages []any) []llm.Message {
	var result []llm.Message
	for _, m := range messages {
		switch msg := m.(type) {
		case BashExecutionMessage:
			if msg.ExcludeFromContext{
				continue
			}
			result = append(result, llm.UserMessage{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					llm.TextContent{Type: "text", Text: BashExecutionToText(msg)},
				},
				Timestamp: msg.Timestamp,
			})
		case CustomMessage:
			var content []llm.ContentBlock
			if s, ok := msg.Content.(string); ok {
				content = []llm.ContentBlock{llm.TextContent{Type: "text", Text: s}}
			} else if blocks, ok := msg.Content.([]llm.ContentBlock); ok {
				content = blocks
			}
			result = append(result, llm.UserMessage{
				Role: llm.RoleUser,
				Content: content,
				Timestamp: msg.Timestamp,
			})
		case BranchSummaryMessage:
			result = append(result, llm.UserMessage{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					llm.TextContent{Type: "text", Text: BranchSummaryPrefix + msg.Summary + BranchSummarySuffix },
				},
				Timestamp: msg.Timestamp,
			})
		case CompactionSummaryMessage:
			result = append(result, llm.UserMessage{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock {
					llm.TextContent{Type: "text", Text: CompactionSummaryPrefix + msg.Summary + CompactionSummarySuffix },
				},
				Timestamp: msg.Timestamp,
			})
		case llm.UserMessage:
			result = append(result, msg)
		case llm.AssistantMessage:
			result = append(result, msg)
		case llm.ToolResultMessage[any]:
			result = append(result, msg)
		}
	}
	return result
}