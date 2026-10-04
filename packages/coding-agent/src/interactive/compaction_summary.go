package interactive

// compaction_summary.go ports reference compaction-summary-message.ts. A
// Box(customMessageBg) showing the "[compaction]" label plus, depending on
// state: collapsed (default) a single "Compacted from N tokens (ctrl+o to
// expand)" line; expanded the "Compacted from N tokens" markdown header + the
// summary body. ctrl+o (app.tools.expand) toggles via Orchestrator's
// toggleToolOutputExpansion, which reaches every chat child implementing
// SetExpanded(bool).

import (
	"strconv"

	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// CompactionSummary renders a context-compaction summary in a labeled box.
type CompactionSummary struct {
	*components.Box

	result  *compaction.CompactionResult
	th      *theme.Theme
	mdTheme components.MarkdownTheme

	expanded bool
}

// NewCompactionSummary builds the component from a CompactionResult. Starts
// collapsed like the reference; callers apply the current global expand state
// via SetExpanded.
func NewCompactionSummary(result *compaction.CompactionResult, th *theme.Theme, mdTheme components.MarkdownTheme) *CompactionSummary {
	box := components.NewBox(1, 1, func(s string) string { return th.Bg("customMessageBg", s) })
	c := &CompactionSummary{Box: box, result: result, th: th, mdTheme: mdTheme}
	c.updateDisplay()
	return c
}

// SetExpanded switches between the collapsed one-liner and the full markdown
// body (reference setExpanded/updateDisplay).
func (c *CompactionSummary) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// updateDisplay rebuilds the box children for the current state.
func (c *CompactionSummary) updateDisplay() {
	c.Clear()
	c.AddChild(components.NewText(c.th.Fg("customMessageLabel", "\x1b[1m[compaction]\x1b[22m"), 0, 0, nil))
	c.AddChild(components.NewSpacer(1))

	if c.expanded {
		summary := c.result.Summary
		if summary == "" {
			summary = "(no summary)"
		}
		body := "**Compacted from " + strconv.Itoa(c.result.TokensBefore) + " tokens**\n\n" + summary
		c.AddChild(components.NewMarkdown(body, 0, 0, c.mdTheme, &components.DefaultTextStyle{
			Color: func(s string) string { return c.th.Fg("customMessageText", s) },
		}))
	} else {
		c.AddChild(components.NewText(
			c.th.Fg("customMessageText", "Compacted from "+strconv.Itoa(c.result.TokensBefore)+" tokens (")+
				c.th.Fg("dim", keyText("app.tools.expand"))+
				c.th.Fg("customMessageText", " to expand)"), 0, 0, nil))
	}
}

// String returns a debug representation.
func (c *CompactionSummary) String() string {
	return "CompactionSummary{expanded: " + strconv.FormatBool(c.expanded) + "}"
}
