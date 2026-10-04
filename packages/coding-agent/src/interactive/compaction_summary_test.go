package interactive

import (
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/tui"
)

// renderComponentText renders any single component at the given width and
// strips ANSI (works for Box, Text, Markdown children alike).
func renderComponentText(t *testing.T, comp tui.Component, width int) string {
	t.Helper()
	var b strings.Builder
	for _, line := range comp.Render(width) {
		b.WriteString(stripANSI(line))
	}
	return b.String()
}

// summaryCard returns the *CompactionSummary child of the chat container.
func summaryCard(t *testing.T, o *Orchestrator) *CompactionSummary {
	t.Helper()
	for _, child := range o.chat.Children {
		if c, ok := child.(*CompactionSummary); ok {
			return c
		}
	}
	t.Fatal("no CompactionSummary in chat container")
	return nil
}

// Audit #23: the compaction summary starts collapsed showing
// "Compacted from N tokens (ctrl+o to expand)", and ctrl+o
// (app.tools.expand) toggles it to the full markdown body and back.
func TestCompactionSummaryCollapsedByDefaultAndToggles(t *testing.T) {
	installAppKeybindings()
	ensureTheme()
	o := newTestOrchestratorEngine(t)

	o.handleCompactionEnd(core.AgentSessionEvent{
		EventType:       "compaction_end",
		CompactionResult: &compaction.CompactionResult{Summary: "the summary text", TokensBefore: 10, TokensAfter: 2},
	})

	// Creation-site parity: leading spacer, then the card, born collapsed
	// (the orchestrator's expand state starts false).
	if len(o.chat.Children) != 2 {
		t.Fatalf("expected spacer + summary card, got %d children", len(o.chat.Children))
	}
	card := summaryCard(t, o)
	collapsed := renderComponentText(t, card, 200)
	if !strings.Contains(collapsed, "Compacted from 10 tokens (") {
		t.Errorf("collapsed line missing, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "ctrl+o to expand)") {
		t.Errorf("expand hint missing, got %q", collapsed)
	}
	if strings.Contains(collapsed, "the summary text") {
		t.Errorf("summary body must not render while collapsed, got %q", collapsed)
	}

	// ctrl+o → expanded: markdown body appears, one-liner disappears.
	o.toggleToolOutputExpansion()
	expanded := renderComponentText(t, card, 200)
	if !strings.Contains(expanded, "the summary text") {
		t.Errorf("summary body missing after expand, got %q", expanded)
	}
	if !strings.Contains(expanded, "Compacted from 10 tokens") {
		t.Errorf("expanded markdown header missing, got %q", expanded)
	}
	if strings.Contains(expanded, "to expand)") {
		t.Errorf("expand hint should be gone when expanded, got %q", expanded)
	}

	// ctrl+o again → back to collapsed.
	o.toggleToolOutputExpansion()
	collapsedAgain := renderComponentText(t, card, 200)
	if !strings.Contains(collapsedAgain, "ctrl+o to expand)") {
		t.Errorf("collapsed hint missing after second toggle, got %q", collapsedAgain)
	}
	if strings.Contains(collapsedAgain, "the summary text") {
		t.Errorf("summary body should be hidden again, got %q", collapsedAgain)
	}
}

// A summary created while the global expand state is on must be born expanded
// (TS: component.setExpanded(this.toolOutputExpanded) at creation).
func TestCompactionSummaryInheritsExpandStateAtCreation(t *testing.T) {
	installAppKeybindings()
	ensureTheme()
	o := newTestOrchestratorEngine(t)
	o.toggleToolOutputExpansion() // headerExpanded → true

	o.handleCompactionEnd(core.AgentSessionEvent{
		EventType:       "compaction_end",
		CompactionResult: &compaction.CompactionResult{Summary: "born open", TokensBefore: 5, TokensAfter: 1},
	})

	card := summaryCard(t, o)
	text := renderComponentText(t, card, 200)
	if !strings.Contains(text, "born open") {
		t.Errorf("summary born while expanded should show its body, got %q", text)
	}
}
