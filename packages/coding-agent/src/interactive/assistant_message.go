package interactive

// assistant_message.go ports reference/coding-agent/src/modes/interactive/
// components/assistant-message.ts (verified — see mapping/11-messages.md).
// Renders an assistant message (ordered text + thinking blocks) plus an
// abort/error footer, wrapped in OSC 133 shell-integration markers when there
// are no tool calls. Streaming mutates the same instance via UpdateContent.

import (
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// OSC 133 shell-integration markers (reference assistant-message.ts:5-7).
const (
	osc133Start = "\x1b]133;A\x07"
	osc133End   = "\x1b]133;B\x07"
	osc133Final = "\x1b]133;C\x07"
)

// AssistantMessage renders one assistant message.
type AssistantMessage struct {
	*tui.Container
	contentContainer *tui.Container
	th               *theme.Theme
	mdTheme          components.MarkdownTheme
	hideThinking     bool
	hiddenLabel      string
	last             *llm.AssistantMessage
	hasToolCalls     bool
}

// NewAssistantMessage builds the component; if msg != nil it renders immediately.
func NewAssistantMessage(msg *llm.AssistantMessage, th *theme.Theme, mdTheme components.MarkdownTheme, hideThinking bool, hiddenLabel string) *AssistantMessage {
	if hiddenLabel == "" {
		hiddenLabel = "Thinking..."
	}
	c := &tui.Container{}
	cc := &tui.Container{}
	c.AddChild(cc)
	m := &AssistantMessage{Container: c, contentContainer: cc, th: th, mdTheme: mdTheme, hideThinking: hideThinking, hiddenLabel: hiddenLabel}
	if msg != nil {
		m.UpdateContent(msg)
	}
	return m
}

// UpdateContent clears and rebuilds the content from msg (reference :73-145).
func (m *AssistantMessage) UpdateContent(msg *llm.AssistantMessage) {
	m.last = msg
	m.contentContainer.Clear()

	// hasVisibleContent: any non-empty text/thinking block.
	hasVisible := false
	for _, b := range msg.Content {
		if (isText(b) && trimmed(textOf(b)) != "") || (isThinking(b) && trimmed(thinkingOf(b)) != "") {
			hasVisible = true
			break
		}
	}
	if hasVisible {
		m.contentContainer.AddChild(components.NewSpacer(1))
	}

	hasToolCalls := false
	for i, b := range msg.Content {
		switch {
		case isText(b):
			text := strings.TrimSpace(textOf(b))
			if text != "" {
				m.contentContainer.AddChild(components.NewMarkdown(text, 1, 0, m.mdTheme, nil))
			}
		case isThinking(b):
			thinking := strings.TrimSpace(thinkingOf(b))
			if thinking != "" {
				hasMore := visibleAfter(msg.Content, i)
				if m.hideThinking {
					m.contentContainer.AddChild(components.NewText(m.th.Italic(m.th.Fg("thinkingText", m.hiddenLabel)), 1, 0, nil))
					if hasMore {
						m.contentContainer.AddChild(components.NewSpacer(1))
					}
				} else {
					m.contentContainer.AddChild(components.NewMarkdown(thinking, 1, 0, m.mdTheme, &components.DefaultTextStyle{
						Color:  func(s string) string { return m.th.Fg("thinkingText", s) },
						Italic: true,
					}))
					if hasMore {
						m.contentContainer.AddChild(components.NewSpacer(1))
					}
				}
			}
		case isToolCall(b):
			hasToolCalls = true
		}
	}
	m.hasToolCalls = hasToolCalls

	// Abort/error footer — only when there are no tool calls (the tool components
	// surface errors themselves). (Reference :126-145.)
	if !hasToolCalls {
		switch msg.StopReason {
		case llm.StopReasonAborted:
			abortMsg := "Operation aborted"
			if msg.ErrorMessage != "" && msg.ErrorMessage != "Request was aborted" {
				abortMsg = msg.ErrorMessage
			}
			m.contentContainer.AddChild(components.NewSpacer(1))
			m.contentContainer.AddChild(components.NewText(m.th.Fg("error", abortMsg), 1, 0, nil))
		case llm.StopReasonError:
			errMsg := msg.ErrorMessage
			if errMsg == "" {
				errMsg = "Unknown error"
			}
			m.contentContainer.AddChild(components.NewSpacer(1))
			m.contentContainer.AddChild(components.NewText(m.th.Fg("error", "Error: "+errMsg), 1, 0, nil))
		}
	}
}

// Render wraps the content in OSC 133 markers unless there are tool calls or it
// is empty (reference :62-71).
func (m *AssistantMessage) Render(width int) []string {
	lines := m.Container.Render(width)
	if m.hasToolCalls || len(lines) == 0 {
		return lines
	}
	lines[0] = osc133Start + lines[0]
	lines[len(lines)-1] = osc133End + osc133Final + lines[len(lines)-1]
	return lines
}

// --- content-block helpers (handle both value and pointer blocks) ---

func isText(b llm.ContentBlock) bool {
	switch b.(type) {
	case llm.TextContent, *llm.TextContent:
		return true
	}
	return false
}

func isThinking(b llm.ContentBlock) bool {
	switch b.(type) {
	case llm.ThinkingContent, *llm.ThinkingContent:
		return true
	}
	return false
}

func isToolCall(b llm.ContentBlock) bool {
	switch b.(type) {
	case llm.ToolCall, *llm.ToolCall:
		return true
	}
	return false
}

func textOf(b llm.ContentBlock) string {
	switch v := b.(type) {
	case llm.TextContent:
		return v.Text
	case *llm.TextContent:
		return v.Text
	}
	return ""
}

func thinkingOf(b llm.ContentBlock) string {
	switch v := b.(type) {
	case llm.ThinkingContent:
		return v.Thinking
	case *llm.ThinkingContent:
		return v.Thinking
	}
	return ""
}

func trimmed(s string) string { return strings.TrimSpace(s) }

// visibleAfter reports whether a visible text/thinking block follows index i
// (reference :97-99 hasVisibleContentAfter).
func visibleAfter(content []llm.ContentBlock, i int) bool {
	for j := i + 1; j < len(content); j++ {
		b := content[j]
		if (isText(b) && trimmed(textOf(b)) != "") || (isThinking(b) && trimmed(thinkingOf(b)) != "") {
			return true
		}
	}
	return false
}
