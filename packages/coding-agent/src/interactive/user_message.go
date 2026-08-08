package interactive

// user_message.go ports reference/coding-agent/src/modes/interactive/components/
// user-message.ts (verified — see mapping/11-messages.md). A user message in a
// colored Box (userMessageBg) with userMessageText markdown, wrapped in OSC 133.

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// UserMessage renders one user message bubble.
type UserMessage struct {
	*tui.Container
}

// NewUserMessage builds the component: Container > Box(1,1,userMessageBg) >
// Markdown(0,0, userMessageText).
func NewUserMessage(text string, th *theme.Theme, mdTheme components.MarkdownTheme) *UserMessage {
	c := &tui.Container{}
	box := components.NewBox(1, 1, func(s string) string { return th.Bg("userMessageBg", s) })
	box.AddChild(components.NewMarkdown(text, 0, 0, mdTheme, &components.DefaultTextStyle{
		Color: func(s string) string { return th.Fg("userMessageText", s) },
	}))
	c.AddChild(box)
	return &UserMessage{Container: c}
}

// Render wraps the box in OSC 133 markers (reference user-message.ts:25-34).
func (u *UserMessage) Render(width int) []string {
	lines := u.Container.Render(width)
	if len(lines) == 0 {
		return lines
	}
	lines[0] = osc133Start + lines[0]
	lines[len(lines)-1] = osc133End + osc133Final + lines[len(lines)-1]
	return lines
}
