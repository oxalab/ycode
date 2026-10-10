package ycode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ycode/packages/coding-agent/src/core/compaction"
	llm "ycode/packages/llm/src"
)

// Regression tests for the session-resume deserialization bug
// (docs/session-resume-bug.md): SessionEntry.Message is the llm.Message
// interface, so every persisted message entry used to fail json.Unmarshal and
// was silently dropped — empty transcript AND lost LLM context on /resume.

// round-trip through the production write path: AppendMessage → file →
// OpenSessionManager → GetEntries/BuildSessionContext restore everything.
func TestSessionMessageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir, dir)

	exit := 0
	assistant := llm.AssistantMessage{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			&llm.TextContent{Type: "text", Text: "thinking about it"},
			&llm.ThinkingContent{Type: "thinking", Thinking: "hmm"},
			&llm.ToolCall{Type: "toolCall", ID: "call-9", Name: "read", Arguments: map[string]any{"path": "/a.go"}},
		},
		StopReason: llm.StopReasonToolUse,
	}
	user := llm.UserMessage{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "please read /a.go"}},
	}
	toolResult := llm.ToolResultMessage[any]{
		Role:       llm.RoleTool,
		ToolCallID: "call-9",
		ToolName:   "read",
		Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "file contents"}},
		IsError:    false,
	}
	bash := BashExecutionMessage{Command: "ls -la", Output: "total 0", ExitCode: &exit}

	sm.AppendMessage(assistant)
	sm.AppendMessage(user)
	sm.AppendMessage(toolResult)
	sm.AppendMessage(bash)

	// Reload from disk exactly like a /resume does.
	reloaded := OpenSessionManager(sm.GetSessionFile(), dir, "")

	entries := reloaded.GetEntries()
	if len(entries) != 4 {
		t.Fatalf("expected 4 decoded message entries, got %d", len(entries))
	}

	if m, ok := entries[0].Message.(llm.AssistantMessage); !ok {
		t.Fatalf("entry 0 = %T, want llm.AssistantMessage", entries[0].Message)
	} else if len(m.Content) != 3 {
		t.Fatalf("assistant content = %d blocks, want 3", len(m.Content))
	} else if tc, ok := m.Content[2].(*llm.ToolCall); !ok || tc.ID != "call-9" || tc.Name != "read" {
		t.Fatalf("assistant toolCall block wrong: %#v", m.Content[2])
	} else if th, ok := m.Content[1].(llm.ThinkingContent); !ok || th.Thinking != "hmm" {
		t.Fatalf("assistant thinking block wrong: %#v", m.Content[1])
	}

	if m, ok := entries[1].Message.(llm.UserMessage); !ok {
		t.Fatalf("entry 1 = %T, want llm.UserMessage", entries[1].Message)
	} else if txt := textOf(m.Content); txt != "please read /a.go" {
		t.Fatalf("user text = %q", txt)
	}

	if m, ok := entries[2].Message.(llm.ToolResultMessage[any]); !ok {
		t.Fatalf("entry 2 = %T, want llm.ToolResultMessage[any]", entries[2].Message)
	} else if m.ToolCallID != "call-9" {
		t.Fatalf("tool result id = %q, want call-9", m.ToolCallID)
	}

	if _, ok := entries[3].Message.(BashExecutionMessage); !ok {
		t.Fatalf("entry 3 = %T, want BashExecutionMessage", entries[3].Message)
	}

	// The LLM context restore path must see the messages too.
	ctxMsgs := reloaded.BuildSessionContext()
	if len(ctxMsgs.Messages) != 4 {
		t.Fatalf("BuildSessionContext returned %d messages, want 4", len(ctxMsgs.Messages))
	}
}

func textOf(blocks []llm.ContentBlock) string {
	for _, b := range blocks {
		if tc, ok := b.(*llm.TextContent); ok {
			return tc.Text
		}
		if tc, ok := b.(llm.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// Legacy lines written before any decoder existed (and the tagless core
// message shapes) must decode too.
func TestDecodeLegacySessionLines(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"."}`,
		`{"type":"message","id":"m1","timestamp":1,"message":{"role":"user","content":[{"type":"text","text":"legacy user"}]}}`,
		`{"type":"message","id":"m2","timestamp":2,"message":{"Command":"echo hi","Output":"hi","ExitCode":0,"Cancelled":false,"Truncated":false,"FullOutputPath":"","Timestamp":2,"ExcludeFromContext":true}}`,
		`{"type":"model_change","id":"m3","timestamp":3,"provider":"x","modelID":"y"}`,
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "legacy.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := OpenSessionManager(file, dir, "")
	// Note: the session header line is filtered; the 3 remaining entries decode.
	entries := sm.GetEntries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 decoded entries, got %d", len(entries))
	}
	if m, ok := entries[0].Message.(llm.UserMessage); !ok {
		t.Fatalf("entry 0 = %T, want llm.UserMessage", entries[0].Message)
	} else if txt := textOf(m.Content); txt != "legacy user" {
		t.Fatalf("user text = %q", txt)
	}
	bash, ok := entries[1].Message.(BashExecutionMessage)
	if !ok {
		t.Fatalf("entry 1 = %T, want BashExecutionMessage", entries[1].Message)
	}
	if bash.Command != "echo hi" || !bash.ExcludeFromContext {
		t.Fatalf("bash message wrong: %+v", bash)
	}
	if entries[2].Type != compaction.EntryTypeModelChange {
		t.Fatalf("entry 2 type = %s", entries[2].Type)
	}
}

func TestDecodeContentBlocks(t *testing.T) {
	raw := []json.RawMessage{
		json.RawMessage(`{"type":"text","text":"plain"}`),
		json.RawMessage(`{"type":"thinking","thinking":"deep thought"}`),
		json.RawMessage(`{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"ls"}}`),
		json.RawMessage(`{"type":"image","data":"Zm9v","mimeType":"image/png"}`),
		json.RawMessage(`{"type":"something-future"}`),
	}
	blocks, err := decodeContentBlocks(raw)
	if err != nil {
		t.Fatalf("decodeContentBlocks: %v", err)
	}
	if len(blocks) != 4 {
		// Unknown block types are skipped (forward compatibility).
		t.Fatalf("expected 4 blocks (unknown type skipped), got %d", len(blocks))
	}
	if _, ok := blocks[0].(llm.TextContent); !ok {
		t.Errorf("block 0 = %T, want TextContent", blocks[0])
	}
	if _, ok := blocks[1].(llm.ThinkingContent); !ok {
		t.Errorf("block 1 = %T, want ThinkingContent", blocks[1])
	}
	tc, ok := blocks[2].(*llm.ToolCall)
	if !ok || tc.Name != "bash" {
		t.Errorf("block 2 = %#v, want *ToolCall named bash", blocks[2])
	}
	if _, ok := blocks[3].(llm.ImageContent); !ok {
		t.Errorf("block 3 = %T, want ImageContent", blocks[3])
	}
}
