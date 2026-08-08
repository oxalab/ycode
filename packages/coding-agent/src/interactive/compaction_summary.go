package interactive

// compaction_summary.go ports reference compaction-summary-message.ts (verified
// — see mapping/11-messages.md), P3c slice. A Box(customMessageBg) showing the
// "[compaction]" label + "Compacted from N tokens" + the summary. (The reference
// has collapsed/expanded states; P3c renders the expanded form — the
// expand/collapse toggle is a later refinement.)

import (
	"strconv"

	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// CompactionSummary renders a context-compaction summary in a labeled box.
type CompactionSummary struct {
	*components.Box
}

// NewCompactionSummary builds the component from a CompactionResult.
func NewCompactionSummary(result *compaction.CompactionResult, th *theme.Theme, mdTheme components.MarkdownTheme) *CompactionSummary {
	box := components.NewBox(1, 1, func(s string) string { return th.Bg("customMessageBg", s) })
	box.AddChild(components.NewText(th.Fg("customMessageLabel", "\x1b[1m[compaction]\x1b[22m"), 0, 0, nil))
	box.AddChild(components.NewSpacer(1))
	summary := result.Summary
	if summary == "" {
		summary = "(no summary)"
	}
	body := "**Compacted from " + strconv.Itoa(result.TokensBefore) + " tokens**\n\n" + summary
	box.AddChild(components.NewMarkdown(body, 0, 0, mdTheme, &components.DefaultTextStyle{
		Color: func(s string) string { return th.Fg("customMessageText", s) },
	}))
	return &CompactionSummary{Box: box}
}
