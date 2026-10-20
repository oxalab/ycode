package interactive

// expandable_text.go ports reference interactive-mode.ts ExpandableText (lines
// 149-163): a Text whose content swaps between a collapsed and an expanded form
// via SetExpanded. Used by the header (and, later, the "What's New" changelog
// section). Implements the Expandable contract (SetExpanded).

import (
	"ycode/packages/tui/components"
)

// ExpandableText is a Text that toggles between collapsed/expanded content.
type ExpandableText struct {
	*components.Text
	collapsed func() string
	expanded  func() string
	expanded_ bool
}

// NewExpandableText builds an ExpandableText. The initial state follows
// isExpanded (reference getStartupExpansionState — P2 defaults to collapsed).
func NewExpandableText(collapsed, expanded func() string, isExpanded bool, paddingX, paddingY int) *ExpandableText {
	init := collapsed
	if isExpanded {
		init = expanded
	}
	t := components.NewText(init(), paddingX, paddingY, nil)
	return &ExpandableText{Text: t, collapsed: collapsed, expanded: expanded, expanded_: isExpanded}
}

// Render re-evaluates the active closure on every call before delegating to
// the embedded Text. The closures read live terminal dimensions (header
// centering), so the string baked at construction went stale after a terminal
// resize until the next ctrl+o toggle.
func (e *ExpandableText) Render(width int) []string {
	if e.expanded_ {
		e.Text.SetText(e.expanded())
	} else {
		e.Text.SetText(e.collapsed())
	}
	return e.Text.Render(width)
}

// SetExpanded swaps the text content (reference ExpandableText.setExpanded);
// Render materializes it.
func (e *ExpandableText) SetExpanded(expanded bool) {
	e.expanded_ = expanded
}

// IsExpanded reports the current expansion state.
func (e *ExpandableText) IsExpanded() bool { return e.expanded_ }
