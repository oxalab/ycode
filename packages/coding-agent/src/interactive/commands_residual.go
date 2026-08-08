package interactive

// commands_residual.go ports the post-P8 residual slash commands that are fully
// backed by the Go core (reference handlers cited per command). These run from
// routeCommand (slash.go) UNDER the engine render lock, so the quick/sync ones
// mutate the chat directly; blocking work (export/reload/clone/copy) launches a
// goroutine where Mutate/RequestRender are safe.
//
// Sources of truth (reference/coding-agent/src/modes/interactive/interactive-mode.ts):
//   - /copy    handleCopyCommand    (:2474) — last assistant text → clipboard
//   - /session handleSessionCommand (:2484) — name/file/id + message + token stats
//   - /export  handleExportCommand  (:2459) — .html → ExportToHtml (.jsonl unsupported in Go)
//   - /clone   handleCloneCommand   (:2504) — fork at the current leaf
//   - /reload  handleReloadCommand  (:2535) — reload settings/resources
//   - /hotkeys handleHotkeysCommand (:2494) — render active keybindings
// The Go core entry points are the same ones the BT TUI calls (app_commands.go).

import (
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// cmdCopy copies the last assistant message text to the clipboard (reference
// handleCopyCommand). "No agent messages to copy yet." when there is none.
// Clipboard I/O runs on a goroutine (clipboard.WriteAll can shell out).
func (o *Orchestrator) cmdCopy() {
	text := lastAssistantText(o.session.Agent.Messages())
	if text == "" {
		o.addNotice(o.th.Fg("error", "No agent messages to copy yet."))
		return
	}
	go func() {
		if err := clipboard.WriteAll(text); err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to copy: "+err.Error()))
			})
			return
		}
		o.engine.Mutate(func() {
			o.addNotice(o.th.Fg("dim", "Copied last agent message to clipboard"))
		})
		o.engine.RequestRender()
	}()
}

// cmdSession renders the session info + stats in the chat (reference
// handleSessionCommand).
func (o *Orchestrator) cmdSession() {
	name := o.session.SessionManager.GetSessionName()
	stats := o.session.GetSessionStats()
	o.addNotice(formatSessionStats(stats, name, o.th))
}

// cmdExport writes the session to HTML (reference handleExportCommand). JSONL
// export has no Go backend → honest notice. Bare /export lets ExportToHtml pick
// the path (mirrors the BT behavior at app_commands.go:215-231).
func (o *Orchestrator) cmdExport(rest string) {
	path := parsePathArg(rest)
	if strings.HasSuffix(path, ".jsonl") {
		o.addNotice(o.th.Fg("muted", "(/export .jsonl is not yet supported — HTML only)"))
		return
	}
	go func() {
		out, err := o.session.ExportToHtml(path)
		if err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to export session: "+err.Error()))
			})
			o.engine.RequestRender()
			return
		}
		o.engine.Mutate(func() {
			o.addNotice(o.th.Fg("dim", "Session exported to: "+out))
		})
		o.engine.RequestRender()
	}()
}

// cmdClone forks the session at the current leaf (reference handleCloneCommand
// = fork with Position:"at"). Blocked while streaming (matches reference).
func (o *Orchestrator) cmdClone() {
	if o.session.IsStreaming() {
		o.addNotice(o.th.Fg("warning", "Finish or abort the current response before cloning."))
		return
	}
	leafID := o.session.SessionManager.GetLeafId()
	if leafID == "" {
		o.addNotice(o.th.Fg("muted", "Nothing to clone yet"))
		return
	}
	go func() {
		result, err := o.runtime.Fork(leafID, &core.ForkOptions{Position: "at"})
		if err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to clone: "+err.Error()))
			})
			o.engine.RequestRender()
			return
		}
		if result == nil || result.Cancelled {
			return
		}
		o.engine.Mutate(func() { o.addNotice(o.th.Fg("dim", "Cloned to new session")) })
		o.engine.RequestRender()
	}()
}

// cmdReload reloads settings/extensions/skills/prompts/themes (reference
// handleReloadCommand → session.Reload). Blocked while streaming.
func (o *Orchestrator) cmdReload() {
	if o.session.IsStreaming() {
		o.addNotice(o.th.Fg("warning", "Wait for the current response to finish before reloading."))
		return
	}
	go func() {
		if err := o.session.Reload(); err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to reload: "+err.Error()))
			})
			o.engine.RequestRender()
			return
		}
		o.engine.Mutate(func() { o.addNotice(o.th.Fg("dim", "Resources reloaded")) })
		o.engine.RequestRender()
	}()
}

// cmdHotkeys renders the active keybindings in the chat (reference
// handleHotkeysCommand). core.DefaultKeybindings() mirrors reference
// keybindings.ts KEYBINDINGS verbatim (Key + Description for every action); the
// BT TUI renders the same data. Each action is shown as "key — description".
func (o *Orchestrator) cmdHotkeys() {
	o.addNotice(formatHotkeys(core.DefaultKeybindings(), o.th))
}

// parsePathArg extracts the first path token from a command's remainder, stripping
// one matching pair of surrounding quotes. Mirrors BT parsePathArg (the TS
// getPathCommandArgument's first-token behavior).
func parsePathArg(rest string) string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	if (rest[0] == '"' || rest[0] == '\'') && len(rest) >= 2 {
		q := rest[0]
		if end := strings.IndexByte(rest[1:], q); end >= 0 {
			return rest[1 : 1+end]
		}
	}
	if idx := strings.IndexAny(rest, " \t"); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

// lastAssistantText returns the concatenated text of the most recent assistant
// message (scanning backward), or "" if there is none. Port of BT lastAssistantText
// (app_commands.go:446) — used by /copy.
func lastAssistantText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		am, ok := messages[i].(llm.AssistantMessage)
		if !ok {
			continue
		}
		var sb strings.Builder
		for _, block := range am.Content {
			switch c := block.(type) {
			case *llm.TextContent:
				if c != nil {
					sb.WriteString(c.Text)
				}
			case llm.TextContent:
				sb.WriteString(c.Text)
			}
		}
		return sb.String()
	}
	return ""
}

// formatSessionStats renders a SessionStats snapshot as a styled multi-line chat
// entry (reference handleSessionCommand). Port of BT formatSessionStats
// (app_commands.go:411); cache/cost blocks are omitted when zero.
func formatSessionStats(stats core.SessionStats, name string, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.Bold("Session Info") + "\n\n")
	if name != "" {
		b.WriteString(th.Fg("dim", "Name:") + " " + name + "\n")
	}
	file := stats.SessionFile
	if file == "" {
		file = "In-memory"
	}
	b.WriteString(th.Fg("dim", "File:") + " " + file + "\n")
	b.WriteString(th.Fg("dim", "ID:") + " " + stats.SessionID + "\n\n")

	b.WriteString(th.Bold("Messages") + "\n")
	b.WriteString(th.Fg("dim", "User:") + " " + strconvItoa(stats.UserMessages) + "\n")
	b.WriteString(th.Fg("dim", "Assistant:") + " " + strconvItoa(stats.AssistantMessages) + "\n")
	b.WriteString(th.Fg("dim", "Tool Calls:") + " " + strconvItoa(stats.ToolCalls) + "\n")
	b.WriteString(th.Fg("dim", "Tool Results:") + " " + strconvItoa(stats.ToolResults) + "\n")
	b.WriteString(th.Fg("dim", "Total:") + " " + strconvItoa(stats.TotalMessages) + "\n\n")

	b.WriteString(th.Bold("Tokens") + "\n")
	b.WriteString(th.Fg("dim", "Input:") + " " + strconvItoa(stats.Tokens.Input) + "\n")
	b.WriteString(th.Fg("dim", "Output:") + " " + strconvItoa(stats.Tokens.Output) + "\n")
	if stats.Tokens.CacheRead > 0 || stats.Tokens.CacheWrite > 0 {
		b.WriteString(th.Fg("dim", "Cache Read:") + " " + strconvItoa(stats.Tokens.CacheRead) + "\n")
		b.WriteString(th.Fg("dim", "Cache Write:") + " " + strconvItoa(stats.Tokens.CacheWrite) + "\n")
	}
	if stats.Cost > 0 {
		b.WriteString(th.Fg("dim", "Cost:") + " " + fmt.Sprintf("$%.4f", stats.Cost) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatHotkeys renders the keybindings as "key — description" lines (reference
// handleHotkeysCommand). Empty keys (actions with no default binding) are skipped.
func formatHotkeys(kb core.AppKeybindings, th *theme.Theme) string {
	rows := []struct{ key, desc string }{
		{kb.Submit.Key, kb.Submit.Description},
		{kb.Newline.Key, kb.Newline.Description},
		{kb.Abort.Key, kb.Abort.Description},
		{kb.HistoryUp.Key, kb.HistoryUp.Description},
		{kb.HistoryDown.Key, kb.HistoryDown.Description},
		{kb.CycleModel.Key, kb.CycleModel.Description},
		{kb.CycleModelReverse.Key, kb.CycleModelReverse.Description},
		{kb.CycleThinkingLevel.Key, kb.CycleThinkingLevel.Description},
		{kb.ToggleAutoCompact.Key, kb.ToggleAutoCompact.Description},
		{kb.Compact.Key, kb.Compact.Description},
		{kb.CopyLastMessage.Key, kb.CopyLastMessage.Description},
		{kb.Fork.Key, kb.Fork.Description},
		{kb.CostReport.Key, kb.CostReport.Description},
		{kb.ContextWindow.Key, kb.ContextWindow.Description},
		{kb.ScrollUp.Key, kb.ScrollUp.Description},
		{kb.ScrollDown.Key, kb.ScrollDown.Description},
		{kb.Mention.Key, kb.Mention.Description},
		{kb.PasteImage.Key, kb.PasteImage.Description},
		{kb.Undo.Key, kb.Undo.Description},
		{kb.Redo.Key, kb.Redo.Description},
		{kb.DeleteToStart.Key, kb.DeleteToStart.Description},
		{kb.DeleteToEnd.Key, kb.DeleteToEnd.Description},
		{kb.MoveToStart.Key, kb.MoveToStart.Description},
		{kb.MoveToEnd.Key, kb.MoveToEnd.Description},
		{kb.WordBack.Key, kb.WordBack.Description},
		{kb.WordForward.Key, kb.WordForward.Description},
	}
	var b strings.Builder
	b.WriteString(th.Bold("Keybindings") + "\n")
	for _, r := range rows {
		if r.key == "" {
			continue
		}
		b.WriteString(th.Fg("dim", r.key+" — "+r.desc) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// strconvItoa is strconv.Itoa via a thin wrapper to keep the theme helper params
// generic (the interactive package already imports strconv elsewhere).
func strconvItoa(n int) string { return fmt.Sprintf("%d", n) }
