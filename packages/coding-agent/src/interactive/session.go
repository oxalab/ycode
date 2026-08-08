package interactive

// session.go implements the /switch session selector. It lists sessions
// (core.ListSessions), and on select switches the runtime's session
// (AgentSessionRuntime.SwitchSession rebinds the runtime, so Session() returns
// the new one), re-subscribes the agent/session event bridges, and rebuilds the
// chat transcript from the new session's entries.
//
// The selector also handles Ctrl+D (app.session.delete) and Ctrl+R
// (app.session.rename), matching the reference session-selector.ts:562,568.
// Delete tries `trash` then unlink (core.DeleteSessionFile); rename opens the
// session file and appends a session_info entry (core.RenameSession).

import (
	"fmt"
	"strings"
	"time"

	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	llm "ycode/packages/llm/src"
)

// openSessionSelector swaps in the session list (/switch).
func (o *Orchestrator) openSessionSelector() {
	o.swapInSelector(o.buildSessionSelector())
}

// buildSessionSelector constructs the session selector with delete/rename wired.
func (o *Orchestrator) buildSessionSelector() *sessionSelector {
	selector := newSessionSelector(o.th)
	selector.refresh(func() []sessionItem {
		return o.loadSessionItems()
	})
	selector.onSwitch = func(path string) { o.switchToSession(path) }
	selector.onCancel = o.closeSelector
	selector.onDelete = func(path, label string) {
		go func() {
			method, err := core.DeleteSessionFile(path)
			o.engine.Mutate(func() {
				if err != nil {
					o.addNotice(o.th.Fg("error", "Delete failed: "+err.Error()))
				} else {
					verb := "Deleted"
					if method == core.DeleteViaTrash {
						verb = "Trashed"
					}
					o.addNotice(o.th.Fg("dim", verb+" session: "+label))
					selector.refresh(func() []sessionItem { return o.loadSessionItems() })
				}
			})
			o.engine.RequestRender()
		}()
	}
	selector.onRename = func(path, currentName, newName string) {
		go func() {
			err := core.RenameSession(path, newName)
			o.engine.Mutate(func() {
				if err != nil {
					o.addNotice(o.th.Fg("error", "Rename failed: "+err.Error()))
				} else {
					o.addNotice(o.th.Fg("dim", "Renamed session → "+strings.TrimSpace(newName)))
					selector.refresh(func() []sessionItem { return o.loadSessionItems() })
				}
			})
			o.engine.RequestRender()
		}()
	}
	return selector
}

// loadSessionItems queries the session list and shapes it for the selector.
func (o *Orchestrator) loadSessionItems() []sessionItem {
	sessions := core.ListSessions(o.runtime.Cwd(), o.session.SessionManager.GetSessionDir())
	currentFile := o.session.SessionManager.GetSessionFile()
	items := make([]sessionItem, 0, len(sessions))
	for _, s := range sessions {
		label := s.Name
		if label == "" {
			label = truncateSessionLabel(s.FirstMessage)
		}
		desc := fmt.Sprintf("%d msgs · %s", s.MessageCount, relativeTime(s.Modified))
		if s.Path == currentFile {
			desc += " · (current)"
		}
		items = append(items, sessionItem{
			path:     s.Path,
			label:    label,
			desc:     desc,
			current:  s.Path == currentFile,
		})
	}
	return items
}

// switchToSession closes the selector, then switches on a goroutine (file I/O).
// On success it re-binds to the new session (shared rebindSession logic).
func (o *Orchestrator) switchToSession(path string) {
	o.closeSelector()
	go func() {
		result, err := o.runtime.SwitchSession(path, nil)
		if err != nil || result == nil || result.Cancelled {
			return // failed or a before-switch hook cancelled
		}
		o.rebindSession()
	}()
}

// rebuildTranscript re-renders the chat from the session's message entries.
func (o *Orchestrator) rebuildTranscript() {
	o.chat.Clear()
	for _, entry := range o.session.SessionManager.GetEntries() {
		if entry.Type != compaction.EntryTypeMessage {
			continue
		}
		switch m := entry.Message.(type) {
		case llm.AssistantMessage:
			o.chat.AddChild(components.NewSpacer(1))
			o.chat.AddChild(NewAssistantMessage(&m, o.th, o.mdTheme(), false, "Thinking..."))
		case *llm.AssistantMessage:
			o.chat.AddChild(components.NewSpacer(1))
			o.chat.AddChild(NewAssistantMessage(m, o.th, o.mdTheme(), false, "Thinking..."))
		case llm.UserMessage:
			if text := userMessageText(m); text != "" {
				o.chat.AddChild(components.NewSpacer(1))
				o.chat.AddChild(NewUserMessage(text, o.th, o.mdTheme()))
			}
		}
	}
}

// userMessageText joins the TextContent blocks of a user message.
func userMessageText(msg llm.UserMessage) string {
	var parts []string
	for _, b := range msg.Content {
		switch v := b.(type) {
		case llm.TextContent:
			parts = append(parts, v.Text)
		case *llm.TextContent:
			parts = append(parts, v.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// truncateSessionLabel shortens a first-message preview for the list label.
func truncateSessionLabel(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "(no messages)"
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:37]) + "..."
	}
	return s
}

// relativeTime renders a coarse "Xm ago" style duration.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
