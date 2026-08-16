package tui

import (
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// View renders the entire TUI layout as a string.
//
// Phase 15 full layout (vertical stack):
//
//	┌─────────────────────────┐
//	│ Header (logo + version) │
//	├─────────────────────────┤
//	│                         │
//	│    Chat Viewport         │  ← messages, streaming, tool calls
//	│                         │
//	├─────────────────────────┤
//	│ Pending Messages        │  ← queued steer/followUp (when present)
//	├─────────────────────────┤
//	│ Status / Working        │  ← spinner + working message during streaming
//	├─────────────────────────┤
//	│ Editor (input area)     │
//	├─────────────────────────┤
//	│ Footer (pwd + stats)    │
//	└─────────────────────────┘
//
// When an overlay (selector) is active, it renders on top of this base layout.
// (Phase 16 wires real selectors; the overlay plumbing lives in app_selectors.go.)
func (a App) View() string {
	if a.width == 0 {
		// Window size not yet received
		return "ycode loading..."
	}

	base := a.renderBaseLayout()

	// Render an overlay on top if one is active (Phase 16 fills these in).
	if a.overlay != OverlayNone {
		overlay := a.renderOverlay()
		if overlay != "" {
			return a.composeOverlay(base, overlay)
		}
	}

	return base
}

// renderBaseLayout composes the standard vertical layout.
//
// Extension widget containers (Phase 18.4/18.5) slot in around the editor:
// widgets-above between the status line and the editor, widgets-below between
// the editor and the footer. Both render "" (0 height) until an extension feeds
// them — see app_extensions.go for the SDK prerequisite.
func (a App) renderBaseLayout() string {
	sections := []string{
		a.header.View(),
		a.renderChat(),
		a.renderPendingMessages(),
		a.renderStatus(),
		a.renderWidgetsAbove(),
		a.renderEditor(),
		a.renderWidgetsBelow(),
		a.footer.View(),
	}
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderChat renders the chat viewport area.
//
// Shows the real Chat component (messages + streaming). If the chat is empty
// and we're streaming, show the "Thinking..." indicator; if empty and idle,
// show a prompt hint.
func (a App) renderChat() string {
	if a.chat.MessageCount() == 0 {
		// Empty chat — show hint (the working indicator renders separately)
		return a.theme.Dim("  Type a message and press Enter to begin.")
	}

	return a.chat.View()
}

// renderPendingMessages renders queued steer/followUp messages between the
// chat and the status line. Phase 17 wires the real queue contents.
func (a App) renderPendingMessages() string {
	var lines []string
	for _, q := range a.compactionQueued {
		prefix := "Steering:"
		if q.mode == "followUp" {
			prefix = "Follow-up:"
		}
		lines = append(lines, a.theme.Dim("  "+prefix+" "+q.text))
	}
	if len(lines) == 0 {
		return ""
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderStatus renders the working indicator during streaming/retry/compaction.
// Uses the bubbles spinner animated frame + a themed message.
func (a App) renderStatus() string {
	if !a.workingVisible && !a.isCompacting && !a.isRetrying {
		return ""
	}

	// Choose the message and color based on state.
	message := a.workingMessage
	colorKey := "accent" // default spinner color for normal "working" state

	switch {
	case a.isCompacting:
		message = "Compacting context... (esc to cancel)"
		colorKey = "accent"
	case a.isRetrying:
		secs := a.retryCountdownMs / 1000
		if secs < 0 {
			secs = 0
		}
		message = "Retrying in " + itoa(secs) + "s..."
		colorKey = "warning"
	default:
		if message == "" {
			message = "Working..."
		}
	}

	spinnerFrame := a.theme.Fg(theme.ThemeColor(colorKey), a.spinner.View())
	label := a.theme.Fg("muted", " "+message)

	return "  " + spinnerFrame + label
}

// renderEditor renders the input editor.
func (a App) renderEditor() string {
	a.editor.SetWidth(a.width)
	return a.editor.View()
}

// renderOverlay renders the active selector overlay.
// Phase 16 implements real selectors; this is the composition hook.
func (a App) renderOverlay() string {
	// Phase 16: delegate to app_selectors.go once selectors exist.
	return renderSelectorOverlay(a)
}

// composeOverlay renders the overlay as a centered modal on top of the base
// layout. (The base is passed but intentionally dropped — selectors take over
// the screen, the standard TUI modal pattern. Escape returns to the base.)
func (a App) composeOverlay(base, overlay string) string {
	_ = base
	if a.height == 0 {
		return overlay
	}
	return lipgloss.NewStyle().Width(a.width).Height(a.height).Render(
		lipgloss.Place(a.width, a.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		),
	)
}

// itoa converts a non-negative int to its decimal string. (Avoids importing
// strconv for a single trivial call site.)
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
