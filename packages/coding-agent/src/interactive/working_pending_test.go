package interactive

import (
	"strings"
	"testing"

	"ycode/packages/tui"
)

// renderContainerLines renders a container at the given width and strips ANSI.
func renderContainerLines(c *tui.Container, width int) []string {
	var out []string
	for _, line := range c.Render(width) {
		out = append(out, stripANSI(line))
	}
	return out
}

// Audit #22: pending-queue previews mirror the reference — a leading spacer
// when anything is queued, and width-aware truncation (not a hard 80-char cut).
func TestRenderPendingSpacerAndPreviews(t *testing.T) {
	ensureTheme()
	o := newTestOrchestratorEngine(t)
	o.pendingMessages = &tui.Container{}

	o.renderPending([]string{"do the thing"}, []string{"later then"})
	if len(o.pendingMessages.Children) != 3 {
		t.Fatalf("expected spacer + 2 previews, got %d children", len(o.pendingMessages.Children))
	}
	lines := renderContainerLines(o.pendingMessages, 200)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Steering: do the thing") {
		t.Errorf("steering preview missing, got %q", joined)
	}
	if !strings.Contains(joined, "Follow-up: later then") {
		t.Errorf("follow-up preview missing, got %q", joined)
	}
	if lines[0] != "" {
		t.Errorf("first line should be the spacer's blank line, got %q", lines[0])
	}

	// Empty queues clear the container entirely (no spacer either).
	o.renderPending(nil, nil)
	if len(o.pendingMessages.Children) != 0 {
		t.Fatalf("expected empty container after clear, got %d children", len(o.pendingMessages.Children))
	}
}

func TestRenderPendingTruncatesToWidth(t *testing.T) {
	ensureTheme()
	o := newTestOrchestratorEngine(t)
	o.pendingMessages = &tui.Container{}

	long := strings.Repeat("x", 150)
	o.renderPending([]string{long}, nil)

	const width = 60
	lines := renderContainerLines(o.pendingMessages, width)
	var preview string
	for _, l := range lines {
		if strings.Contains(l, "Steering:") {
			preview = l
			break
		}
	}
	if preview == "" {
		t.Fatalf("no steering preview rendered, got %q", strings.Join(lines, "\n"))
	}
	// TruncatedText pads every line to exactly the container width.
	if len([]rune(preview)) != width {
		t.Errorf("preview line should be %d columns, got %d: %q", width, len([]rune(preview)), preview)
	}
	if !strings.Contains(preview, "...") {
		t.Errorf("long preview should end in ellipsis, got %q", preview)
	}
}

// Multi-line queued messages render their first line only (TruncatedText),
// matching the reference rather than collapsing newlines into one long line.
func TestRenderPendingMultilineFirstLineOnly(t *testing.T) {
	ensureTheme()
	o := newTestOrchestratorEngine(t)
	o.pendingMessages = &tui.Container{}

	o.renderPending([]string{"first line\nsecond line"}, nil)
	joined := strings.Join(renderContainerLines(o.pendingMessages, 200), "\n")
	if !strings.Contains(joined, "Steering: first line") {
		t.Errorf("first line missing, got %q", joined)
	}
	if strings.Contains(joined, "second line") {
		t.Errorf("second line should not render, got %q", joined)
	}
}
