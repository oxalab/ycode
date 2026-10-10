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
	selector.onError = func(msg string) {
		// Fires under the render lock (selector HandleInput); addNotice is a
		// plain append and the engine re-renders after HandleInput returns.
		o.addNotice(o.th.Fg("error", msg))
	}
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

// rebuildTranscript re-renders the chat from the session's entries. Mirrors
// the reference renderSessionContext (interactive-mode.ts:3096-3164):
// assistant messages render plus one tool component per toolCall block
// (aborted/error turns get an immediate error result), following toolResult
// messages are matched onto those components by toolCallId, bash executions
// rebuild their command/output card, compaction entries render the summary
// card, and user messages feed the editor history. branchSummary and
// custom_message entries are skipped (no Go component; context restore does
// not depend on them).
func (o *Orchestrator) rebuildTranscript() {
	o.chat.Clear()
	pendingTools := make(map[string]*ToolExecution)
	for _, entry := range o.session.SessionManager.GetEntries() {
		switch entry.Type {
		case compaction.EntryTypeMessage:
			switch m := entry.Message.(type) {
			case llm.AssistantMessage:
				o.rebuildAssistantMessage(&m, pendingTools)
			case *llm.AssistantMessage:
				o.rebuildAssistantMessage(m, pendingTools)
			case llm.UserMessage:
				if text := userMessageText(m); text != "" {
					if len(o.chat.Children) > 0 {
						o.chat.AddChild(components.NewSpacer(1))
					}
					o.chat.AddChild(NewUserMessage(text, o.th, o.mdTheme()))
					o.editor.AddToHistory(text)
				}
			case llm.ToolResultMessage[any]:
				if tc, ok := pendingTools[m.ToolCallID]; ok {
					delete(pendingTools, m.ToolCallID)
					tc.UpdateResult(extractResultText(m.Content), m.IsError, false)
				}
			case core.BashExecutionMessage:
				bash := NewBashExecution(o.engine, m.Command, m.ExcludeFromContext, o.th)
				if m.Output != "" {
					bash.AppendOutput(m.Output)
				}
				isError := m.ExitCode != nil && *m.ExitCode != 0
				bash.SetComplete(m.ExitCode, m.Cancelled, isError)
				o.chat.AddChild(bash)
			}
		case compaction.EntryTypeCompaction:
			o.chat.AddChild(components.NewSpacer(1))
			o.chat.AddChild(NewCompactionSummary(&compaction.CompactionResult{
				Summary:      entry.Summary,
				TokensBefore: entry.TokensBefore,
			}, o.th, o.mdTheme()))
		}
	}
}

// rebuildAssistantMessage renders one persisted assistant message and opens a
// pending tool component for each of its toolCall blocks. Aborted/error turns
// get their error result immediately (pi interactive-mode.ts:3135-3148).
func (o *Orchestrator) rebuildAssistantMessage(m *llm.AssistantMessage, pendingTools map[string]*ToolExecution) {
	o.chat.AddChild(components.NewSpacer(1))
	o.chat.AddChild(NewAssistantMessage(m, o.th, o.mdTheme(), false, "Thinking..."))
	for _, block := range m.Content {
		tc, ok := block.(*llm.ToolCall)
		if !ok {
			if tcv, isValue := block.(llm.ToolCall); isValue {
				tc = &tcv
			} else {
				continue
			}
		}
		comp := NewToolExecution(tc.Name, tc.Arguments, o.th)
		comp.MarkStarted()
		if m.StopReason == llm.StopReasonAborted {
			comp.UpdateResult("Operation aborted", true, false)
		} else if m.StopReason == llm.StopReasonError && m.ErrorMessage != "" {
			comp.UpdateResult(m.ErrorMessage, true, false)
		} else {
			pendingTools[tc.ID] = comp
		}
		o.chat.AddChild(comp)
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
