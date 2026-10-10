package ycode

// session_decode.go decodes persisted session entries into typed messages.
//
// SessionEntry.Message is the llm.Message interface, and encoding/json cannot
// populate a non-empty interface — before this file existed, every persisted
// message entry failed json.Unmarshal and was silently dropped by GetEntries
// and buildIndex, so /resume (and --continue) produced an empty transcript AND
// a lost LLM context (docs/session-resume-bug.md).
//
// This helper is the single decode path for both readers. It lives in package
// ycode (core) because BashExecutionMessage/CustomMessage/etc. are defined
// here and compaction cannot import its parent (core → compaction → llm).

import (
	"encoding/json"
	"fmt"

	"ycode/packages/coding-agent/src/core/compaction"
	llm "ycode/packages/llm/src"
)

// decodeSessionEntry decodes one raw JSONL line into a SessionEntry with a
// typed Message. Non-message entries behave exactly like a plain
// json.Unmarshal (they never touch the interface field). Message entries
// always fail the plain unmarshal (the interface field), so their Message is
// decoded manually, keyed on role (or key-shape for the tagless core message
// structs).
func decodeSessionEntry(raw json.RawMessage) (*compaction.SessionEntry, error) {
	entry := &compaction.SessionEntry{}
	if err := json.Unmarshal(raw, entry); err == nil {
		return entry, nil
	}
	// The plain unmarshal failed. encoding/json still populates every other
	// field before returning the UnmarshalTypeError for the interface Message
	// field — only EntryTypeMessage entries are expected to fail; anything
	// else is genuinely malformed.
	var probe struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if probe.Type != string(compaction.EntryTypeMessage) || len(probe.Message) == 0 {
		return nil, fmt.Errorf("session entry: cannot decode type %q", probe.Type)
	}
	msg, err := decodeSessionMessage(probe.Message)
	if err != nil {
		return nil, err
	}
	entry.Message = msg
	return entry, nil
}

// decodeSessionMessage decodes a persisted message object into its concrete
// llm.Message (or core message) type.
func decodeSessionMessage(raw json.RawMessage) (llm.Message, error) {
	var probe struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}

	// Tagged llm message shapes (role is serialized on disk).
	switch llm.Role(probe.Role) {
	case llm.RoleUser:
		var shadow struct {
			llm.UserMessage
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &shadow); err != nil {
			return nil, err
		}
		blocks, err := decodeContentBlocks(shadow.Content)
		if err != nil {
			return nil, err
		}
		shadow.UserMessage.Content = blocks
		return shadow.UserMessage, nil
	case llm.RoleAssistant:
		var shadow struct {
			llm.AssistantMessage
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &shadow); err != nil {
			return nil, err
		}
		blocks, err := decodeContentBlocks(shadow.Content)
		if err != nil {
			return nil, err
		}
		shadow.AssistantMessage.Content = blocks
		return shadow.AssistantMessage, nil
	case llm.RoleTool:
		var shadow struct {
			llm.ToolResultMessage[any]
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &shadow); err != nil {
			return nil, err
		}
		blocks, err := decodeContentBlocks(shadow.Content)
		if err != nil {
			return nil, err
		}
		shadow.ToolResultMessage.Content = blocks
		return shadow.ToolResultMessage, nil
	}

	// Roleless legacy shapes: the core message structs (BashExecutionMessage
	// etc.) marshal with Go field names and no role key (Role() is a method,
	// not a serialized field). Sniff by their distinguishing keys.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, err
	}
	switch {
	case hasKey(keys, "Command"):
		var m BashExecutionMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case hasKey(keys, "CustomType"):
		var m CustomMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case hasKey(keys, "FromID"):
		var m BranchSummaryMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case hasKey(keys, "TokensBefore"):
		var m CompactionSummaryMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	}
	return nil, fmt.Errorf("session message: unknown shape (role=%q)", probe.Role)
}

// decodeContentBlocks decodes a persisted content array keyed on each block's
// "type" discriminator. Unknown block types are skipped (forward-compatible
// with content written by a newer binary) rather than failing the entry.
func decodeContentBlocks(raw []json.RawMessage) ([]llm.ContentBlock, error) {
	blocks := make([]llm.ContentBlock, 0, len(raw))
	for _, r := range raw {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, err
		}
		switch probe.Type {
		case "text":
			var b llm.TextContent
			if err := json.Unmarshal(r, &b); err != nil {
				return nil, err
			}
			blocks = append(blocks, b)
		case "thinking":
			var b llm.ThinkingContent
			if err := json.Unmarshal(r, &b); err != nil {
				return nil, err
			}
			blocks = append(blocks, b)
		case "toolCall":
			var b llm.ToolCall
			if err := json.Unmarshal(r, &b); err != nil {
				return nil, err
			}
			// Providers append *llm.ToolCall (see extractToolCalls) — match.
			blocks = append(blocks, &b)
		case "image":
			var b llm.ImageContent
			if err := json.Unmarshal(r, &b); err != nil {
				return nil, err
			}
			blocks = append(blocks, b)
		}
	}
	return blocks, nil
}

func hasKey(keys map[string]json.RawMessage, key string) bool {
	_, ok := keys[key]
	return ok
}
