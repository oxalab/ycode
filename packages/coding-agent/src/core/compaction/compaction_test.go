package compaction

// compaction_test.go pins FindCutPoint's cut selection (audit #31): the
// backward token walk must stop the moment the keep-recent budget is
// exceeded, even when the exceeding entry is not a valid cut point — the
// TS walk breaks unconditionally (compaction.ts:419).

import (
	"testing"

	llm "ycode/packages/llm/src"
)

func msgEntry(message llm.Message) *SessionEntry {
	return &SessionEntry{Type: EntryTypeMessage, Message: message}
}

func userMsg(text string) llm.Message {
	return llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: text}}}
}

func assistantMsg(text string) llm.Message {
	return llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: text}}}
}

// TestFindCutPoint_StopsAtNonCutPointBudgetOverflow is the #31 regression:
// budget exceeded at a trailing tool result (never a valid cut point) must
// keep the default cutPoints[0] — not walk back to the parent tool call.
func TestFindCutPoint_StopsAtNonCutPointBudgetOverflow(t *testing.T) {
	entries := []*SessionEntry{
		msgEntry(userMsg("question")),
		msgEntry(llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			llm.ToolCall{Type: "toolCall", ID: "tc_1", Name: "read", Arguments: map[string]any{"path": "/a.go"}},
		}}),
		msgEntry(llm.ToolResultMessage[any]{Role: llm.RoleTool, ToolCallID: "tc_1", Content: []llm.ContentBlock{
			llm.TextContent{Type: "text", Text: "file contents"},
		}}),
	}

	got := FindCutPoint(entries, 0, len(entries), 1)
	if got.FirstKeptEntryIndex != 0 {
		t.Errorf("FirstKeptEntryIndex = %d, want 0 (default cutPoints[0]; walking back to the assistant is the #31 bug)", got.FirstKeptEntryIndex)
	}
	if got.IsSplitTurn {
		t.Errorf("IsSplitTurn = true, want false (cut lands on the leading user message)")
	}
}

// TestFindCutPoint_AllWithinBudget: nothing exceeds the budget → keep from
// the first message (TS compaction.test.ts "keep everything within budget").
func TestFindCutPoint_AllWithinBudget(t *testing.T) {
	entries := []*SessionEntry{
		msgEntry(userMsg("question")),
		msgEntry(assistantMsg("answer")),
	}
	got := FindCutPoint(entries, 0, len(entries), 1_000_000)
	if got.FirstKeptEntryIndex != 0 {
		t.Errorf("FirstKeptEntryIndex = %d, want 0", got.FirstKeptEntryIndex)
	}
	if got.IsSplitTurn {
		t.Errorf("IsSplitTurn = true, want false")
	}
}

// TestFindCutPoint_SplitTurn: budget exceeded at the assistant (the trailing
// model-change entry is skipped by the walk, like TS skipping non-message
// entries) → cut at the assistant, IsSplitTurn=true, TurnStartIndex at the
// user that started the turn (TS compaction.test.ts "split turn when cutting
// at assistant message").
func TestFindCutPoint_SplitTurn(t *testing.T) {
	entries := []*SessionEntry{
		msgEntry(userMsg("question")),
		msgEntry(assistantMsg("answer")),
		{Type: EntryTypeModelChange, Provider: "p", ModelID: "m"},
	}
	got := FindCutPoint(entries, 0, len(entries), 1)
	if got.FirstKeptEntryIndex != 1 {
		t.Errorf("FirstKeptEntryIndex = %d, want 1 (budget exceeded at the assistant)", got.FirstKeptEntryIndex)
	}
	if !got.IsSplitTurn {
		t.Fatalf("IsSplitTurn = false, want true (cut at an assistant)")
	}
	if got.TurnStartIndex != 0 {
		t.Errorf("TurnStartIndex = %d, want 0 (the user that started the split turn)", got.TurnStartIndex)
	}
}
