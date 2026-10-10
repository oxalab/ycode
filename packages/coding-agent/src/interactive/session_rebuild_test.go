package interactive

import (
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	llm "ycode/packages/llm/src"
	"ycode/packages/tui"
	"ycode/packages/tui/components"
)

// Regression tests for the /resume empty-transcript bug
// (docs/session-resume-bug.md): rebuildTranscript must render the decoded
// session entries — assistant + tool components with matched results, user
// bubbles with editor history, bash cards, compaction summaries.

func newRebuildTestOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	ensureTheme()
	dir := t.TempDir()
	o := newTestOrchestratorEngine(t)
	o.pendingMessages = &tui.Container{}
	o.session = &core.AgentSession{
		SessionManager: core.NewSessionManager(dir, dir),
	}
	return o
}

func chatChildSummary(o *Orchestrator) string {
	var b strings.Builder
	for _, child := range o.chat.Children {
		switch c := child.(type) {
		case *ToolExecution:
			b.WriteString("[tool:" + c.toolName + "]")
		case *BashExecution:
			b.WriteString("[bash]")
		case *CompactionSummary:
			b.WriteString("[compaction]")
		case *AssistantMessage:
			b.WriteString("[assistant]")
		case *UserMessage:
			b.WriteString("[user]")
		case *components.Text, *components.Spacer:
			b.WriteString("[spacer]")
		default:
			b.WriteString("[?]")
		}
	}
	return b.String()
}

// A scripted session: user asks, assistant calls a tool, tool answers, then a
// bash run and a compaction. After rebuildTranscript all of it renders.
func TestRebuildTranscriptRendersDecodedEntries(t *testing.T) {
	o := newRebuildTestOrchestrator(t)
	sm := o.session.SessionManager

	sm.AppendMessage(llm.UserMessage{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "read /a.go please"}},
	})
	sm.AppendMessage(llm.AssistantMessage{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			&llm.TextContent{Type: "text", Text: "On it."},
			&llm.ToolCall{Type: "toolCall", ID: "call-1", Name: "read", Arguments: map[string]any{"path": "/a.go"}},
		},
		StopReason: llm.StopReasonToolUse,
	})
	sm.AppendMessage(llm.ToolResultMessage[any]{
		Role:       llm.RoleTool,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "file body"}},
	})
	sm.AppendMessage(core.BashExecutionMessage{Command: "wc -l /a.go", Output: "42 /a.go"})
	sm.AppendCompaction("summary of the old stuff", "", 1234, nil, false)

	o.rebuildTranscript()

	summary := chatChildSummary(o)
	// spacer+assistant card, tool card, user bubble, bash card, spacer+compaction.
	if !strings.Contains(summary, "[tool:read]") {
		t.Errorf("tool component missing: %s", summary)
	}
	if !strings.Contains(summary, "[bash]") {
		t.Errorf("bash component missing: %s", summary)
	}
	if !strings.Contains(summary, "[compaction]") {
		t.Errorf("compaction card missing: %s", summary)
	}
	if strings.Contains(summary, "[?]") {
		t.Errorf("unrecognized children present: %s", summary)
	}

	// The rendered text must contain the user line, tool result and bash output.
	var text strings.Builder
	for _, line := range o.chat.Render(200) {
		text.WriteString(stripANSI(line))
	}
	joined := text.String()
	for _, want := range []string{"read /a.go please", "file body", "wc -l /a.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered transcript missing %q", want)
		}
	}

	// The compaction card renders collapsed (audit #23 behavior) — verify the
	// one-liner shows, and the summary appears when expanded.
	var compCard *CompactionSummary
	for _, child := range o.chat.Children {
		if c, ok := child.(*CompactionSummary); ok {
			compCard = c
		}
	}
	collapsed := renderComponentText(t, compCard, 200)
	if !strings.Contains(collapsed, "Compacted from 1234 tokens") {
		t.Errorf("collapsed compaction line missing: %q", collapsed)
	}
	compCard.SetExpanded(true)
	if expanded := renderComponentText(t, compCard, 200); !strings.Contains(expanded, "summary of the old stuff") {
		t.Errorf("compaction summary missing after expand: %q", expanded)
	}

	// User messages must feed the editor history (pi populateHistory).
	if len(o.editor.History()) == 0 {
		t.Error("editor history empty after rebuild — user messages not added")
	}
	if h := o.editor.History(); h[0] != "read /a.go please" {
		t.Errorf("editor history[0] = %q, want the resumed user prompt", h[0])
	}
}

// An assistant turn that ended aborted carries its error result inline
// instead of waiting for a toolResult that never comes.
func TestRebuildTranscriptAbortedToolCallGetsErrorResult(t *testing.T) {
	o := newRebuildTestOrchestrator(t)
	sm := o.session.SessionManager

	sm.AppendMessage(llm.AssistantMessage{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			&llm.ToolCall{Type: "toolCall", ID: "call-x", Name: "bash", Arguments: map[string]any{"command": "sleep 60"}},
		},
		StopReason: llm.StopReasonAborted,
	})

	o.rebuildTranscript()

	var found *ToolExecution
	for _, child := range o.chat.Children {
		if c, ok := child.(*ToolExecution); ok {
			found = c
		}
	}
	if found == nil {
		t.Fatal("tool component missing")
	}
	out := renderComponentText(t, found, 200)
	if !strings.Contains(out, "Operation aborted") {
		t.Errorf("aborted tool call should show error result, got %q", out)
	}
}
