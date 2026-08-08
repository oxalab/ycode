package interactive

// slash.go is the full slash-command router (P6), covering the non-selector
// commands + bash mode + the shared session-rebind logic. Selector-opening
// commands (/login /model /theme /thinking /settings /switch) live in
// selector_open.go and are dispatched from here too.
//
// Concurrency: routeCommand + the quick handlers (name/help/unsupported/bash
// setup) run UNDER the engine render lock (from handleSubmit) → direct tree
// mutation only, no RequestRender (the engine re-renders after HandleInput).
// Blocking work (compact/new/bash/cycling) launches a goroutine, where
// RequestRender/Mutate are safe.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ycode/packages/tui/components"
	sdk "ycode/packages/agent-sdk/src"
	core "ycode/packages/coding-agent/src/core"
)

// routeCommand handles a "/command" line. Returns true if handled locally.
// Unknown commands fall through to the session as an ordinary prompt.
func (o *Orchestrator) routeCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	name := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))

	switch name {
	// --- Selectors (P5) ---
	case "/thinking", "/think", "/reasoning":
		o.openThinkingSelector()
		return true
	case "/theme":
		o.openThemeSelector()
		return true
	case "/model", "/models":
		o.openModelSelector()
		return true
	case "/settings", "/config":
		o.openSettingsSelector()
		return true
	case "/login":
		o.openLoginSelector()
		return true
	case "/logout":
		o.openLogoutSelector()
		return true
	case "/add-provider", "/add-provider-model", "/provider":
		o.openAddProviderForm()
		return true
	case "/switch", "/sessions", "/resume":
		o.openSessionSelector()
		return true
	case "/tree":
		o.openTreeSelector()
		return true
	case "/fork":
		o.openUserMessageSelector()
		return true
	case "/scoped-models":
		o.openScopedModelsSelector()
		return true
	case "/context":
		o.openContextPanel()
		return true

	// --- Session / orchestrator calls (P6 + residuals) ---
	case "/compact":
		o.cmdCompact(rest)
		return true
	case "/new":
		o.cmdNew()
		return true
	case "/name":
		o.cmdName(rest)
		return true
	case "/copy":
		o.cmdCopy()
		return true
	case "/session":
		o.cmdSession()
		return true
	case "/export":
		o.cmdExport(rest)
		return true
	case "/import":
		o.cmdImport(rest)
		return true
	case "/share":
		o.cmdShare()
		return true
	case "/changelog":
		o.cmdChangelog()
		return true
	case "/debug":
		o.cmdDebug()
		return true
	case "/clone":
		o.cmdClone()
		return true
	case "/reload":
		o.cmdReload()
		return true
	case "/hotkeys":
		o.cmdHotkeys()
		return true
	case "/quit", "/exit", "/q":
		go o.Stop()
		return true
	case "/help", "/commands", "/?":
		o.cmdHelp()
		return true

	// --- Genuinely blocked (no Go backend) — keep an honest notice ---
	// (previously /share and /changelog were here; now implemented in commands_extra.go)
	}
	return false
}

// cmdCompact manually compacts the context (runs the LLM → blocking → goroutine).
// The compaction_end event (if emitted) renders a CompactionSummary via the
// session event bridge.
func (o *Orchestrator) cmdCompact(instructions string) {
	go func() {
		_, _ = o.session.Compact(instructions)
		o.engine.RequestRender()
	}()
}

// cmdNew starts a fresh session and re-binds the event bridges + transcript.
func (o *Orchestrator) cmdNew() {
	go func() {
		result, err := o.runtime.NewSession(nil)
		if err != nil || result == nil || result.Cancelled {
			return
		}
		o.rebindSession()
	}()
}

// cmdName sets the session display name, or shows the current name when no arg
// is given (reference handleNameCommand). Footer re-renders next frame.
func (o *Orchestrator) cmdName(name string) {
	if name == "" {
		if current := o.session.SessionManager.GetSessionName(); current != "" {
			o.addNotice(o.th.Fg("dim", "Session name: "+current))
		} else {
			o.addNotice(o.th.Fg("warning", "Usage: /name <session name>"))
		}
		return
	}
	o.session.SetSessionName(name)
	o.addNotice(o.th.Fg("dim", "Session name set: "+name))
}

// cmdHelp lists the builtin commands in the chat (display).
func (o *Orchestrator) cmdHelp() {
	lines := []string{"Available commands:"}
	for _, c := range core.BuiltinSlashCommands {
		lines = append(lines, fmt.Sprintf("  /%s — %s", c.Name, c.Description))
	}
	o.addNotice(lines...)
}

// addNotice appends a muted notice block to the chat. Runs under the render lock.
func (o *Orchestrator) addNotice(lines ...string) {
	o.chat.AddChild(components.NewSpacer(1))
	for _, l := range lines {
		o.chat.AddChild(components.NewText(o.th.Fg("muted", l), 0, 0, nil))
	}
}

// rebindSession re-binds the orchestrator to the runtime's current session
// (after Switch/New): unsubscribe old event bridges, fetch the new session,
// re-subscribe, clear + rebuild the transcript, refresh footer/editor. Called
// from a goroutine (Switch/New do file I/O) → Mutate/RequestRender are safe.
func (o *Orchestrator) rebindSession() {
	if o.unsubAgent != nil {
		o.unsubAgent()
	}
	if o.unsubSession != nil {
		o.unsubSession()
	}
	o.session = o.runtime.Session()
	o.unsubAgent = o.subscribeAgent()
	o.unsubSession = o.subscribeSession()

	o.engine.Mutate(func() {
		o.rebuildTranscript()
		o.updateEditorBorder(o.editor.GetText())
		o.pendingMessages.Clear() // P4b: the new session emits its own queue_update
	})
	if o.footer != nil {
		o.footer.SetSession(o.session)
	}
	o.engine.RequestRender()
}

// handleBashSubmit runs a "!command" (or "!!command", excluded from context)
// directly via the session's bash executor, streaming output into a
// BashExecution component. Runs under the render lock for the setup; the exec
// itself runs on a goroutine.
func (o *Orchestrator) handleBashSubmit(text string) {
	excluded := strings.HasPrefix(text, "!!")
	cmd := text[1:]
	if excluded {
		cmd = cmd[1:]
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	if o.session.IsBashRunning() {
		o.addNotice("A bash command is already running. Press Esc to cancel it first.")
		return
	}
	o.editor.AddToHistory(text)
	be := NewBashExecution(o.engine, cmd, excluded, o.th)
	o.chat.AddChild(components.NewSpacer(1))
	o.chat.AddChild(be)

	go func() {
		result, err := o.session.ExecuteBash(cmd, func(chunk string) {
			o.engine.Mutate(func() { be.AppendOutput(chunk) })
		})
		cancelled := errors.Is(err, context.Canceled)
		isErr := err != nil && !cancelled
		var exitCode *int
		if result != nil {
			exitCode = result.ExitCode
			if exitCode != nil && *exitCode != 0 {
				isErr = true
			}
		}
		o.engine.Mutate(func() { be.SetComplete(exitCode, cancelled, isErr) })
		o.engine.RequestRender()
	}()
}

// --- Keybinding-driven cycling (ctrl+p / shift+tab / ctrl+t / ctrl+l) ---
// These run under the render lock (from CustomEditor action handlers); the
// session calls may init a client, so they run on goroutines.

func (o *Orchestrator) cycleModelForward() {
	go func() {
		_, _ = o.session.CycleModel("forward")
		o.engine.RequestRender()
	}()
}

func (o *Orchestrator) cycleModelBackward() {
	go func() {
		_, _ = o.session.CycleModel("backward")
		o.engine.RequestRender()
	}()
}

func (o *Orchestrator) cycleThinking() {
	go func() {
		o.session.CycleThinkingLevel()
		o.engine.Mutate(func() { o.updateEditorBorder(o.editor.GetText()) })
		o.engine.RequestRender()
	}()
}

// toggleThinking flips between off and the first reasoning level (ctrl+t).
func (o *Orchestrator) toggleThinking() {
	go func() {
		if o.session.GetThinkingLevel() == sdk.ThinkingOff {
			o.session.CycleThinkingLevel() // off → first available level
		} else {
			o.session.SetThinkingLevel(sdk.ThinkingOff)
		}
		o.engine.Mutate(func() { o.updateEditorBorder(o.editor.GetText()) })
		o.engine.RequestRender()
	}()
}
