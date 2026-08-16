package tui

// Slash-command execution. Ports the command dispatch from the reference
// interactive mode (reference/coding-agent/src/modes/interactive/interactive-mode.ts
// setupEditorSubmitHandler, lines 2437-2616) into the Go submit path.
//
// The autocomplete POPUP was already implemented (components/editor.go); only
// EXECUTION was missing — handleSubmitSubmit previously echoed every "/…"
// submit as "(slash commands: Phase 17)" and did nothing. This file wires the
// builtin commands to their backing (existing selector openers + session/runtime
// methods). Commands needing new infrastructure (/share gh gist, /changelog,
// /hotkeys table, /export .jsonl, easter eggs) are deferred with a clear
// "not yet supported" message — the port's established pattern.
//
// Async model: blocking session ops run in a goroutine (matching the existing
// prompt path in app_update.go) and surface their result via sendToProgram(
// commandResultMsg{...}) (program.go). Selector-opening commands are synchronous
// and just return their tea.Cmd. The editor auto-Reset()s on submit, so commands
// do NOT clear input themselves.

import (
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// commandResultMsg surfaces the outcome of an async slash command (export,
// import, clone, new, copy, reload) back into the Update loop for rendering.
type commandResultMsg struct {
	ok      bool
	message string
}

// handleSlashCommand routes a "/cmd [args]" submit to its handler. It returns
// (model, cmd, handled); handled=false means the text was not a recognized
// builtin command and the caller reports "Unknown command" (and does NOT send
// it as a prompt — TS parity). Value receiver to match handleSubmitSubmit.
func (a App) handleSlashCommand(text string) (tea.Model, tea.Cmd, bool) {
	name, rest := parseSlashCommand(text)
	switch name {
	case "/quit":
		return a, tea.Quit, true

	// --- Overlays (synchronous: open and hand input to the selector) ---
	case "/settings":
		m, cmd := a.openSettingsSelector()
		return m, cmd, true
	case "/scoped-models":
		m, cmd := a.openScopedModelsSelector()
		return m, cmd, true
	case "/fork":
		m, cmd := a.openUserMessageSelector()
		return m, cmd, true
	case "/resume":
		m, cmd := a.openSessionSelector()
		return m, cmd, true
	case "/login":
		m, cmd := a.openOAuthSelector(components.OAuthModeLogin)
		return m, cmd, true
	case "/logout":
		m, cmd := a.openOAuthSelector(components.OAuthModeLogout)
		return m, cmd, true
	case "/model":
		return a.cmdModel(rest)
	case "/tree":
		return a.cmdTree()

	// --- Session operations (async) ---
	case "/compact":
		return a.cmdCompact(rest)
	case "/clone":
		return a.cmdClone()
	case "/new":
		return a.cmdNew()
	case "/export":
		return a.cmdExport(rest)
	case "/import":
		return a.cmdImport(rest)
	case "/name":
		return a.cmdName(rest)
	case "/session":
		return a.cmdSession()
	case "/copy":
		return a.cmdCopy()
	case "/reload":
		return a.cmdReload()

	// --- Deferred (need new infrastructure) ---
	case "/share", "/changelog", "/hotkeys":
		a.addStaticEntry(a.theme.Fg("muted", fmt.Sprintf("%s is not yet supported in this build", name)))
		return a, nil, true
	}
	return a, nil, false
}

// ============================================================================
// Per-command handlers
// ============================================================================

// cmdModel: /model [term]. An exact ID (or provider/id) match sets the model
// directly; otherwise the model selector opens (TS handleModelCommand).
func (a App) cmdModel(rest string) (tea.Model, tea.Cmd, bool) {
	term := strings.TrimSpace(rest)
	if term == "" {
		m, cmd := a.openModelSelector()
		return m, cmd, true
	}
	if a.session != nil {
		if reg := a.session.GetModelRegistry(); reg != nil {
			if models, err := reg.GetAvailable(); err == nil {
				if m := findExactModelMatch(term, models); m != nil {
					if err := a.session.SetModel(*m); err != nil {
						a.addStaticEntry(a.theme.Fg("error", "Failed to set model: "+err.Error()))
					} else {
						a.addStaticEntry(a.theme.Fg("muted", "Model: "+m.ID))
					}
					return a, nil, true
				}
			}
		}
	}
	// No exact match → open the selector.
	m, cmd := a.openModelSelector()
	return m, cmd, true
}

// cmdTree: /tree — open the session history tree selector. The selected entry
// reuses forkMessageResult (handleSelectorDone forks at that entry).
func (a App) cmdTree() (tea.Model, tea.Cmd, bool) {
	var roots []*core.SessionTreeNode
	var leafID string
	if sm := a.sessionManager; sm != nil {
		roots = sm.GetTree()
		leafID = sm.GetLeafId()
	}
	comp := components.NewTreeSelector(a.theme, roots, leafID, 10, components.TreeSelectorCallbacks{
		OnSelect: func(entryID string) tea.Cmd { return doneCmd(forkMessageResult{entryID: entryID}) },
		OnCancel: makeCancelCmd,
	})
	cmd := a.openOverlay(OverlayTreeSelector, comp)
	return a, cmd, true
}

// cmdCompact: /compact [instructions]. Requires ≥2 messages; the in-progress UI
// is driven by the existing compactionStart/End events, so this just fires it.
func (a App) cmdCompact(instr string) (tea.Model, tea.Cmd, bool) {
	if !hasEnoughMessagesToCompact(a.sessionManager) {
		a.addStaticEntry(a.theme.Fg("warning", "Nothing to compact (no messages yet)"))
		return a, nil, true
	}
	sess := a.session
	go func() { _, _ = sess.Compact(instr) }()
	return a, nil, true
}

// cmdClone: /clone — fork the session at the current leaf (TS handleCloneCommand).
func (a App) cmdClone() (tea.Model, tea.Cmd, bool) {
	if a.isStreaming {
		a.addStaticEntry(a.theme.Fg("warning", "Finish or abort the current response before cloning."))
		return a, nil, true
	}
	if a.sessionManager == nil {
		return a, nil, true
	}
	leafID := a.sessionManager.GetLeafId()
	if leafID == "" {
		a.addStaticEntry(a.theme.Fg("muted", "Nothing to clone yet"))
		return a, nil, true
	}
	rt := a.runtimeHost
	go func() {
		result, err := rt.Fork(leafID, &core.ForkOptions{Position: "at"})
		if err != nil {
			sendToProgram(commandResultMsg{ok: false, message: "Failed to clone: " + err.Error()})
			return
		}
		if result == nil || result.Cancelled {
			return
		}
		sendToProgram(commandResultMsg{ok: true, message: "Cloned to new session"})
	}()
	return a, nil, true
}

// cmdNew: /new — start a fresh session (TS handleClearCommand).
func (a App) cmdNew() (tea.Model, tea.Cmd, bool) {
	if a.isStreaming {
		a.addStaticEntry(a.theme.Fg("warning", "Finish or abort the current response before starting a new session."))
		return a, nil, true
	}
	rt := a.runtimeHost
	go func() {
		result, err := rt.NewSession(nil)
		if err != nil {
			sendToProgram(commandResultMsg{ok: false, message: "Failed to create session: " + err.Error()})
			return
		}
		if result == nil || result.Cancelled {
			return
		}
		sendToProgram(commandResultMsg{ok: true, message: "✓ New session started"})
	}()
	return a, nil, true
}

// cmdExport: /export [path]. HTML only (.jsonl export is not yet backed in Go).
func (a App) cmdExport(rest string) (tea.Model, tea.Cmd, bool) {
	path := parsePathArg(rest)
	if strings.HasSuffix(path, ".jsonl") {
		a.addStaticEntry(a.theme.Fg("muted", "(/export .jsonl is not yet supported — HTML only)"))
		return a, nil, true
	}
	sess := a.session
	go func() {
		out, err := sess.ExportToHtml(path)
		if err != nil {
			sendToProgram(commandResultMsg{ok: false, message: "Failed to export session: " + err.Error()})
			return
		}
		sendToProgram(commandResultMsg{ok: true, message: "Session exported to: " + out})
	}()
	return a, nil, true
}

// cmdImport: /import <path> — resume from a JSONL file (TS handleImportCommand).
func (a App) cmdImport(rest string) (tea.Model, tea.Cmd, bool) {
	path := parsePathArg(rest)
	if path == "" {
		a.addStaticEntry(a.theme.Fg("warning", "Usage: /import <path.jsonl>"))
		return a, nil, true
	}
	if a.isStreaming {
		a.addStaticEntry(a.theme.Fg("warning", "Finish or abort the current response before importing."))
		return a, nil, true
	}
	rt := a.runtimeHost
	go func() {
		result, err := rt.ImportFromJsonl(path, "")
		if err != nil {
			sendToProgram(commandResultMsg{ok: false, message: "Failed to import session: " + err.Error()})
			return
		}
		if result != nil && result.Cancelled {
			sendToProgram(commandResultMsg{ok: true, message: "Import cancelled"})
			return
		}
		sendToProgram(commandResultMsg{ok: true, message: "Session imported from: " + path})
	}()
	return a, nil, true
}

// cmdName: /name [name] — set or show the session display name.
func (a App) cmdName(rest string) (tea.Model, tea.Cmd, bool) {
	name := strings.TrimSpace(rest)
	if name == "" {
		if a.sessionManager != nil {
			if current := a.sessionManager.GetSessionName(); current != "" {
				a.addStaticEntry(a.theme.Fg("dim", "Session name: "+current))
			} else {
				a.addStaticEntry(a.theme.Fg("warning", "Usage: /name <name>"))
			}
		}
		return a, nil, true
	}
	if a.session != nil {
		a.session.SetSessionName(name)
		a.addStaticEntry(a.theme.Fg("dim", "Session name set: "+name))
	}
	return a, nil, true
}

// cmdSession: /session — render the session info + stats (TS handleSessionCommand).
func (a App) cmdSession() (tea.Model, tea.Cmd, bool) {
	if a.session == nil {
		return a, nil, true
	}
	name := ""
	if a.sessionManager != nil {
		name = a.sessionManager.GetSessionName()
	}
	a.addStaticEntry(formatSessionStats(a.session.GetSessionStats(), name, a.theme))
	return a, nil, true
}

// cmdCopy: /copy — copy the last assistant message text to the clipboard.
func (a App) cmdCopy() (tea.Model, tea.Cmd, bool) {
	if a.session == nil || a.session.Agent == nil {
		a.addStaticEntry(a.theme.Fg("error", "No agent messages to copy yet."))
		return a, nil, true
	}
	text := lastAssistantText(a.session.Agent.Messages())
	if text == "" {
		a.addStaticEntry(a.theme.Fg("error", "No agent messages to copy yet."))
		return a, nil, true
	}
	go func() {
		if err := clipboard.WriteAll(text); err != nil {
			sendToProgram(commandResultMsg{ok: false, message: "Failed to copy: " + err.Error()})
			return
		}
		sendToProgram(commandResultMsg{ok: true, message: "Copied last agent message to clipboard"})
	}()
	return a, nil, true
}

// cmdReload: /reload — reload settings/resources (closest Go equivalent to the
// TS runtimeHost.reloadResources; session.Reload also emits session_shutdown).
func (a App) cmdReload() (tea.Model, tea.Cmd, bool) {
	if a.isStreaming {
		a.addStaticEntry(a.theme.Fg("warning", "Wait for the current response to finish before reloading."))
		return a, nil, true
	}
	sess := a.session
	go func() {
		if err := sess.Reload(); err != nil {
			sendToProgram(commandResultMsg{ok: false, message: "Failed to reload: " + err.Error()})
			return
		}
		sendToProgram(commandResultMsg{ok: true, message: "Resources reloaded"})
	}()
	return a, nil, true
}

// ============================================================================
// Pure helpers (testable — no live session/runtime dependency)
// ============================================================================

// parseSlashCommand splits "/cmd rest..." into a lowercased name (with the
// leading "/") and the remainder (outer whitespace trimmed, internal spacing
// preserved). Returns ("", "") for a bare "/" or non-slash input.
func parseSlashCommand(text string) (name, rest string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") || text == "/" {
		return "", ""
	}
	idx := strings.IndexAny(text, " \t")
	if idx < 0 {
		return strings.ToLower(text), ""
	}
	return strings.ToLower(text[:idx]), strings.TrimLeft(text[idx:], " \t")
}

// findExactModelMatch returns the available model whose ID (or "provider/id")
// equals term, or nil. The autocomplete inserts "provider/id" as the value, so
// both forms are accepted.
func findExactModelMatch(term string, models []*llm.Model[llm.Api]) *llm.Model[llm.Api] {
	if term == "" {
		return nil
	}
	for _, m := range models {
		if m == nil {
			continue
		}
		if m.ID == term || string(m.Provider)+"/"+m.ID == term {
			return m
		}
	}
	return nil
}

// countMessages returns the number of message-type entries in a session tree.
func countMessages(entries []*compaction.SessionEntry) int {
	n := 0
	for _, e := range entries {
		if e != nil && e.Type == compaction.EntryTypeMessage {
			n++
		}
	}
	return n
}

// hasEnoughMessagesToCompact reports whether the session has ≥2 messages to
// compact (TS handleCompactCommand gate).
func hasEnoughMessagesToCompact(sm *core.SessionManager) bool {
	if sm == nil {
		return false
	}
	return countMessages(sm.GetEntries()) >= 2
}

// parsePathArg extracts the first path token from a command's remainder,
// stripping one matching pair of surrounding quotes (single or double). Mirrors
// the first-token behavior of the TS getPathCommandArgument.
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

// formatSessionStats renders a SessionStats snapshot as a styled multi-line
// chat entry (TS handleSessionCommand). Cache/cost blocks are omitted when zero.
func formatSessionStats(stats core.SessionStats, name string, t *theme.Theme) string {
	var b strings.Builder
	b.WriteString(t.Bold("Session Info") + "\n\n")
	if name != "" {
		b.WriteString(t.Fg("dim", "Name:") + " " + name + "\n")
	}
	file := stats.SessionFile
	if file == "" {
		file = "In-memory"
	}
	b.WriteString(t.Fg("dim", "File:") + " " + file + "\n")
	b.WriteString(t.Fg("dim", "ID:") + " " + stats.SessionID + "\n\n")

	b.WriteString(t.Bold("Messages") + "\n")
	b.WriteString(t.Fg("dim", "User:") + " " + itoa(stats.UserMessages) + "\n")
	b.WriteString(t.Fg("dim", "Assistant:") + " " + itoa(stats.AssistantMessages) + "\n")
	b.WriteString(t.Fg("dim", "Tool Calls:") + " " + itoa(stats.ToolCalls) + "\n")
	b.WriteString(t.Fg("dim", "Tool Results:") + " " + itoa(stats.ToolResults) + "\n")
	b.WriteString(t.Fg("dim", "Total:") + " " + itoa(stats.TotalMessages) + "\n\n")

	b.WriteString(t.Bold("Tokens") + "\n")
	b.WriteString(t.Fg("dim", "Input:") + " " + itoa(stats.Tokens.Input) + "\n")
	b.WriteString(t.Fg("dim", "Output:") + " " + itoa(stats.Tokens.Output) + "\n")
	if stats.Tokens.CacheRead > 0 || stats.Tokens.CacheWrite > 0 {
		b.WriteString(t.Fg("dim", "Cache Read:") + " " + itoa(stats.Tokens.CacheRead) + "\n")
		b.WriteString(t.Fg("dim", "Cache Write:") + " " + itoa(stats.Tokens.CacheWrite) + "\n")
	}
	if stats.Cost > 0 {
		b.WriteString(t.Fg("dim", "Cost:") + " " + fmt.Sprintf("$%.4f", stats.Cost) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// lastAssistantText returns the concatenated text of the most recent assistant
// message (scanning backward), or "" if there is none. Used by /copy.
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
