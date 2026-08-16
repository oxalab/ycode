package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Chat is a scrollable viewport that displays conversation messages.
//
// It wraps bubbles/viewport.Model and manages:
//   - A list of rendered message strings (each message is a pre-rendered block)
//   - Auto-scroll: new messages scroll to bottom, but user scrolling up disables
//     auto-scroll until they scroll back to the bottom or a new user message arrives.
//   - Height allocation: the parent tells Chat how much vertical space is available.
//
// Port of the chat container area from interactive-mode.ts.
type Chat struct {
	viewport viewport.Model

	// messages holds the rendered string for each message in the conversation.
	// These are joined with newlines to form the viewport content.
	messages []string

	// autoScroll tracks whether the viewport should snap to the bottom
	// when new content arrives. Disabled when the user scrolls up manually.
	autoScroll bool

	// Width and height allocated by the parent layout.
	width  int
	height int
}

// NewChat creates a new Chat component with the given dimensions.
func NewChat(width, height int) Chat {
	vp := viewport.New(width, height)
	vp.KeyMap = viewport.KeyMap{} // disable default keys; parent routes scroll keys
	return Chat{
		viewport:   vp,
		messages:   make([]string, 0),
		autoScroll: true,
		width:      width,
		height:     height,
	}
}

// SetSize updates the chat viewport dimensions.
func (c *Chat) SetSize(width, height int) {
	c.width = width
	c.height = height
	c.viewport.Width = width
	c.viewport.Height = height
}

// AddMessage appends a pre-rendered message to the chat and scrolls to bottom
// if auto-scroll is enabled.
func (c *Chat) AddMessage(rendered string) {
	c.messages = append(c.messages, rendered)
	c.rebuildContent()
}

// SetMessages replaces all messages and scrolls to bottom.
func (c *Chat) SetMessages(rendered []string) {
	c.messages = rendered
	c.autoScroll = true
	c.rebuildContent()
}

// AppendContent appends raw text to the last message (used for streaming deltas).
// The caller is responsible for having at least one message in the chat.
func (c *Chat) AppendContent(text string) {
	if len(c.messages) == 0 {
		return
	}
	c.messages[len(c.messages)-1] += text
	c.rebuildContent()
}

// UpdateLastMessage replaces the last message's rendered content.
func (c *Chat) UpdateLastMessage(rendered string) {
	if len(c.messages) == 0 {
		return
	}
	c.messages[len(c.messages)-1] = rendered
	c.rebuildContent()
}

// MessageCount returns the number of messages in the chat.
func (c *Chat) MessageCount() int {
	return len(c.messages)
}

// AutoScroll returns whether auto-scroll is currently enabled.
func (c *Chat) AutoScroll() bool {
	return c.autoScroll
}

// SetAutoScroll enables or disables auto-scroll.
func (c *Chat) SetAutoScroll(enabled bool) {
	c.autoScroll = enabled
}

// Init satisfies tea.Model (no initial commands needed).
func (c Chat) Init() tea.Cmd {
	return nil
}

// Update forwards messages to the viewport and tracks user scroll state.
func (c Chat) Update(msg tea.Msg) (Chat, tea.Cmd) {
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)

	// If the user scrolled up (viewport no longer at bottom), disable auto-scroll.
	// If they scrolled back to bottom, re-enable it.
	if !c.viewport.AtBottom() {
		c.autoScroll = false
	} else if c.viewport.AtBottom() && c.viewport.ScrollPercent() == 1.0 {
		c.autoScroll = true
	}

	return c, cmd
}

// View renders the chat viewport.
func (c Chat) View() string {
	if c.height <= 0 {
		return ""
	}
	return c.viewport.View()
}

// --- Internal ---

// rebuildContent joins all messages and sets the viewport content.
// If auto-scroll is enabled, it snaps to the bottom.
func (c *Chat) rebuildContent() {
	content := strings.Join(c.messages, "\n")
	c.viewport.SetContent(content)
	if c.autoScroll {
		c.viewport.GotoBottom()
	}
}
