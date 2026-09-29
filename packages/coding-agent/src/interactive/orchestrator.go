package interactive

// orchestrator.go is the imperative core of the pi-tui interactive TUI. It owns
// the *tui.TUI engine and the static container tree, and (in later phases) bridges
// AgentSession events to component-tree mutations + RequestRender — the pi-tui
// equivalent of reference interactive-mode.ts, with no tea.Msg/program.Send.
//
// P0: engine + tree + lifecycle. P1: production editor (CustomEditor with app.*
// key interception) + autocomplete (slash/@file/path) + submit→Prompt and
// change→bash-border wiring. Subscribe/event-bridge (P3), slash/bash routing
// (P6), selectors (P5) come later.

import (
	"context"
	"strings"
	"sync"
	"time"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// Options configures the Orchestrator. It mirrors the args launchInteractive
// receives (cmd/ycode/runtime.go); only Runtime + Settings are used so far.
type Options struct {
	Runtime         *core.AgentSessionRuntime
	Settings        *core.SettingsManager
	InitialMessage  string
	InitialImages   []*llm.ImageContent
	InitialMessages []string
	ModelFallback   string
	Verbose         bool
}

// Orchestrator owns the pi-tui engine + static container tree.
type Orchestrator struct {
	runtime  *core.AgentSessionRuntime
	session  *core.AgentSession
	settings *core.SettingsManager
	th       *theme.Theme

	engine *tui.TUI
	kb     *tui.KeybindingsManager

	// Static tree (mapping/01-screen-layout.md).
	header          *tui.Container
	chat            *tui.Container
	pendingMessages *tui.Container
	status          *tui.Container
	widgetAbove     *tui.Container
	editorContainer *tui.Container
	widgetBelow     *tui.Container
	footer          *Footer                 // P4 status bar (pwd/stats/model)
	footerData      *core.FooterDataProvider // P4 git branch source
	footerCancel    context.CancelFunc      // cancels the git watch goroutine

	editor *CustomEditor

	loginDialog *LoginDialog // active login dialog, if any (cancelled on Stop)
	retryTimer  *CountdownTimer // active auto-retry countdown, if any (disposed on Stop)
	retryLoader *components.Loader // animated retry spinner in the status area, if any
	retryEscapeHandler func() // editor OnEscape saved while the retry handler is swapped in
	workingLoader *components.Loader // P4b working spinner in the status area, if any

	headerExpandable *ExpandableText // built by buildHeader; toggled by app.tools.expand
	headerExpanded   bool
	verbose          bool

	// CLI initial prompts (finding #33) — sent once the engine is running.
	initialMessage  string
	initialImages   []*llm.ImageContent
	initialMessages []string

	streamingComp *AssistantMessage // in-progress assistant message (P3a)
	unsubAgent    func()            // SDK agent event unsubscribe
	unsubSession  func()            // AgentSession event unsubscribe

	toolComps map[string]*ToolExecution // toolCallID → live tool component (P3b)
	bashComps map[string]*BashExecution // toolCallID → live bash component (P3b)

	// uiCtx is the ExtensionUIContext built by createExtensionUIContext, kept
	// so slash commands can call its methods (e.g. Confirm for /import).
	uiCtx extensions.ExtensionUIContext

	done     chan struct{}
	stopOnce sync.Once

	// lastCtrlC stamps the previous app.clear press for the 500ms double-press
	// exit window (reference interactive-mode.ts lastSigintTime). Only touched
	// under the engine render lock.
	lastCtrlC time.Time
}

// NewOrchestrator constructs the engine, theme, editor, and tree. It does not
// block — call Run() to start.
func NewOrchestrator(opts Options) *Orchestrator {
	// Semantic theme. GetTheme() panics if InitTheme wasn't called first
	// (mirrors core/tui/app.go:126-127). "" = auto-detect dark/light.
	theme.InitTheme("")
	th := theme.GetTheme()

	term := tui.NewProcessTerminal()
	engine := tui.NewTUI(term, tui.WithHardwareCursor(true))

	// Merged tui.* + app.* keybindings (global).
	kb := installAppKeybindings()

	base := components.NewEditor(engine, buildEditorTheme(th), components.EditorOptions{PaddingX: 1})
	editor := NewCustomEditor(base, kb)

	o := &Orchestrator{
		runtime:  opts.Runtime,
		session:  opts.Runtime.Session(),
		settings: opts.Settings,
		th:       th,
		engine:   engine,
		kb:       kb,
		editor:   editor,
		verbose:  opts.Verbose,
		initialMessage:  opts.InitialMessage,
		initialImages:   opts.InitialImages,
		initialMessages: opts.InitialMessages,
		toolComps: map[string]*ToolExecution{},
		bashComps: map[string]*BashExecution{},
		done:     make(chan struct{}),
	}

	// Autocomplete provider is wired after BindExtensions (below) so the
	// extension/template/skill command lists reflect what's actually loaded.
	// Wiring it here would capture an empty extension runner.

	o.setupEditorCallbacks()
	o.updateEditorBorder("") // initial border (current thinking level)

	// Footer (P4): git-branch provider + status bar that reads session state on
	// every render. Re-render when the polled branch changes.
	o.footerData = core.NewFooterDataProvider(opts.Runtime.Cwd())
	o.footer = NewFooter(o.session, o.footerData, th, true)
	o.footerData.OnChange(func(core.FooterData) { o.engine.RequestRender() })

	o.buildStaticTree()
	engine.SetFocus(editor)

	// Wire the extension UI context + error listener. Until this call, every
	// extension ran against NoOpUIContext — ctx.ui.notify was a no-op. This
	// makes the full UI surface (notify/status/select/confirm/input) reachable
	// from both Go-factory and JS extensions. Mirrors the reference's
	// InteractiveMode.bindCurrentSessionExtensions (interactive-mode.ts:1462).
	// Runs after the tree is built so session_start handlers can mutate it.
	o.uiCtx = o.createExtensionUIContext()
	_ = o.session.BindExtensions(core.ExtensionBindings{
		UIContext: o.uiCtx,
		OnError: func(err extensions.ExtensionError) {
			// Surface extension errors as a themed notice in the chat.
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Extension error ("+err.Event+"): "+err.Error))
			})
			o.engine.RequestRender()
		},
	})

	// Now that extensions/templates/skills are loaded, build the autocomplete
	// provider with the full command list. Builtins + prompt templates +
	// extension commands + skills, matching reference interactive-mode.ts:513.
	editor.SetAutocompleteProvider(buildAutocompleteProvider(opts.Runtime.Cwd(), o.session))

	return o
}

// Run starts the engine and blocks until Stop (Ctrl+C / Ctrl+D on empty editor).
// engine.Start() is non-blocking (it spawns the stdin reader goroutine), so Run
// blocks on done.
func (o *Orchestrator) Run() {
	o.unsubAgent = o.subscribeAgent()
	o.unsubSession = o.subscribeSession()
	o.startFooterWatch()
	o.engine.Start()
	o.submitInitialPrompts()
	<-o.done
}

// startFooterWatch runs the git-HEAD poller (FooterDataProvider.Watch) on a
// goroutine; branch changes fire the OnChange → RequestRender registered in
// NewOrchestrator. Cancelled in Stop.
func (o *Orchestrator) startFooterWatch() {
	if o.footerData == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.footerCancel = cancel
	go o.footerData.Watch(ctx)
}

// Stop tears down the engine (restores the terminal) and unblocks Run.
func (o *Orchestrator) Stop() {
	o.stopOnce.Do(func() {
		if o.unsubAgent != nil {
			o.unsubAgent()
		}
		if o.unsubSession != nil {
			o.unsubSession()
		}
		if o.footerCancel != nil {
			o.footerCancel()
		}
		if o.loginDialog != nil {
			o.loginDialog.cancelLogin() // abort any in-flight OAuth flow
		}
		o.stopRetryChrome() // stop the auto-retry countdown/loader, restore escape
		if o.workingLoader != nil {
			o.workingLoader.Stop() // stop the animated working spinner
		}
		o.engine.Stop()
		close(o.done)
	})
}

// mdTheme returns the markdown theme derived from the semantic theme.
func (o *Orchestrator) mdTheme() components.MarkdownTheme { return buildMarkdownTheme(o.th) }

// setupEditorCallbacks wires the editor's submit/change handlers.
//
// OnSubmit/OnChange run under the engine render lock (invoked from the base
// Editor's HandleInput), so they MUST NOT call RequestRender or block.
func (o *Orchestrator) setupEditorCallbacks() {
	o.editor.OnSubmit = o.handleSubmit
	o.editor.OnChange = o.handleChange
	// Ctrl+D on an empty editor exits (mirrors reference app.exit). Interrupt/
	// abort (OnEscape) + slash/bash routing land in P6.
	o.editor.OnCtrlD = func() { go o.Stop() }
	// app.clear (ctrl+c): first press clears the editor, second within 500ms
	// exits (reference interactive-mode.ts:3202-3211). Runs under the render
	// lock — SetText needs no explicit RequestRender (see above).
	o.editor.OnAction("app.clear", func() {
		if o.ctrlC() {
			go o.Stop()
		}
	})
	// app.tools.expand toggles the header's startup help (later also tool/thinking
	// blocks, P3). Runs under the render lock — SetExpanded just invalidates the
	// Text cache; the engine re-renders after HandleInput returns.
	o.editor.OnAction("app.tools.expand", o.toggleToolOutputExpansion)
	// Keybinding-driven cycling (P6): ctrl+p / shift+ctrl+p cycle models,
	// ctrl+l opens the model selector, shift+tab cycles thinking, ctrl+t toggles.
	o.editor.OnAction("app.model.cycleForward", o.cycleModelForward)
	o.editor.OnAction("app.model.cycleBackward", o.cycleModelBackward)
	o.editor.OnAction("app.model.select", o.openModelSelector)
	o.editor.OnAction("app.thinking.cycle", o.cycleThinking)
	o.editor.OnAction("app.thinking.toggle", o.toggleThinking)
}

// toggleToolOutputExpansion flips the shared expand flag (reference
// app.tools.expand / toolOutputExpanded) and applies it to BOTH the header and
// every tool/bash component currently in the chat (interactive-mode.ts:3420-3424).
func (o *Orchestrator) toggleToolOutputExpansion() {
	o.headerExpanded = !o.headerExpanded
	if o.headerExpandable != nil {
		o.headerExpandable.SetExpanded(o.headerExpanded)
	}
	for _, child := range o.chat.Children {
		switch c := child.(type) {
		case *ToolExecution:
			c.SetExpanded(o.headerExpanded)
		case *BashExecution:
			c.SetExpanded(o.headerExpanded)
		}
	}
}

// handleSubmit is the editor's submit handler. P1: records history and routes
// normal prompts to the session on a goroutine. Slash ("/…") and bash ("!…")
// routing is P6 — for now those are sent to the session as ordinary prompts
// (harmless; refined in P6).
func (o *Orchestrator) handleSubmit(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	// Bash mode: "!command" (or "!!command", excluded from context).
	if strings.HasPrefix(trimmed, "!") {
		o.handleBashSubmit(trimmed)
		return
	}
	// Slash commands are routed locally (selectors + session calls). Unknown
	// commands fall through to the session as an ordinary prompt. The base
	// Editor's submitValue has already cleared the text.
	if strings.HasPrefix(trimmed, "/") && o.routeCommand(trimmed) {
		return
	}
	// An LLM prompt requires a model. Bash + slash-commands still work flagless
	// (they don't need one); only a plain prompt is blocked with a hint.
	if o.session.GetModel().ID == "" {
		o.addNotice("No model configured. Use /login to add a provider, then /model to select one.")
		return
	}
	o.editor.AddToHistory(trimmed)
	// Render the user message. handleSubmit runs under the render lock, so this
	// is a direct mutation (no RequestRender — the engine re-renders after
	// HandleInput returns).
	o.chat.AddChild(components.NewSpacer(1))
	o.chat.AddChild(NewUserMessage(trimmed, o.th, o.mdTheme()))
	go func() { _ = o.session.Prompt(trimmed, nil) }()
}

// submitInitialPrompts sends the CLI initial message (+ images) and any
// remaining positional messages, in order (TS interactive-mode.ts:738-756).
// Sequential Prompts on one goroutine preserve ordering — Prompt settles the
// turn and any retry chain before returning (audit #14).
func (o *Orchestrator) submitInitialPrompts() {
	if o.initialMessage == "" && len(o.initialMessages) == 0 {
		return
	}
	go func() {
		if o.initialMessage != "" {
			o.sendInitialPrompt(o.initialMessage, o.initialImages)
		}
		for _, m := range o.initialMessages {
			o.sendInitialPrompt(m, nil)
		}
	}()
}

// sendInitialPrompt renders the user bubble and sends the prompt — the
// handleSubmit tail without editor history or slash/bash routing (TS calls
// session.prompt directly for the initial message). Runs on the initial-prompt
// goroutine, so Mutate is the sanctioned mutation path.
func (o *Orchestrator) sendInitialPrompt(text string, images []*llm.ImageContent) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if o.session.GetModel().ID == "" {
		o.engine.Mutate(func() {
			o.addNotice("No model configured. Use /login to add a provider, then /model to select one.")
		})
		return
	}
	o.engine.Mutate(func() {
		o.chat.AddChild(components.NewSpacer(1))
		o.chat.AddChild(NewUserMessage(trimmed, o.th, o.mdTheme()))
	})
	imgs := make([]any, len(images))
	for i, img := range images {
		imgs[i] = img
	}
	go func() { _ = o.session.Prompt(trimmed, &core.PromptOptions{Images: imgs}) }()
}

// handleChange recomputes the bash-mode indicator and editor border color on
// every edit (reference interactive-mode.ts onChange + updateEditorBorderColor).
func (o *Orchestrator) handleChange(text string) {
	o.updateEditorBorder(text)
}

// updateEditorBorderColor: bash mode (input starts with "!") → bashMode color;
// otherwise the current thinking-level color. Mirrors reference
// interactive-mode.ts:3372-3376 + theme.getThinkingBorderColor/getBashModeBorderColor.
func (o *Orchestrator) updateEditorBorder(text string) {
	if strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "!") {
		o.editor.BorderColor = o.th.GetBashModeBorderColor()
		return
	}
	o.editor.BorderColor = o.th.GetThinkingBorderColor(theme.ThinkingLevel(o.session.GetThinkingLevel()))
}

// buildAutocompleteProvider builds the editor's autocomplete source list from
// the session's current state: builtin slash commands + prompt templates +
// extension commands + skills, plus @file/path completion rooted at cwd.
// Mirrors reference interactive-mode.ts:513.
//
// The list is captured at construction time. If extensions/templates/skills
// are loaded dynamically after this call, the autocomplete won't reflect them
// until the orchestrator rebuilds the provider (e.g., via a future refresh
// hook).
func buildAutocompleteProvider(cwd string, session *core.AgentSession) tui.AutocompleteProvider {
	// Bound the slice by the sum of all source counts so we never reallocate.
	var builtinN, templateN, extN, skillN int
	if session != nil {
		templateN = len(session.GetPromptTemplates())
		extN = len(session.GetExtensionCommands())
		skillN = len(session.GetSkills())
	}
	builtinN = len(core.BuiltinSlashCommands)
	sources := make([]tui.CommandSource, 0, builtinN+templateN+extN+skillN)

	// 1. Builtin slash commands.
	for i := range core.BuiltinSlashCommands {
		c := core.BuiltinSlashCommands[i]
		sources = append(sources, tui.CommandSource{
			Slash: &tui.SlashCommand{
				Name:        c.Name,
				Description: c.Description,
			},
		})
	}

	if session == nil {
		return tui.NewCombinedAutocompleteProvider(sources, cwd, "")
	}

	// 2. Prompt templates (slash-invocable; carry ArgumentHint).
	for _, t := range session.GetPromptTemplates() {
		sources = append(sources, tui.CommandSource{
			Slash: &tui.SlashCommand{
				Name:         t.Name,
				Description:  t.Description,
				ArgumentHint: t.ArgumentHint,
			},
		})
	}

	// 3. Extension commands. ResolvedCommand embeds RegisteredCommand, which
	// carries an optional GetArgumentCompletions (func() []string). Adapt it
	// to the autocomplete provider's signature (func(prefix) ([]AutocompleteItem, bool)).
	for _, rc := range session.GetExtensionCommands() {
		slash := &tui.SlashCommand{
			Name:        rc.InvocationName,
			Description: rc.Description,
		}
		if rc.GetArgumentCompletions != nil {
			get := rc.GetArgumentCompletions
			slash.GetArgumentCompletions = func(prefix string) ([]tui.AutocompleteItem, bool) {
				all := get()
				items := make([]tui.AutocompleteItem, 0, len(all))
				for _, s := range all {
					if strings.HasPrefix(s, prefix) {
						items = append(items, tui.AutocompleteItem{Value: s, Label: s})
					}
				}
				return items, len(items) > 0
			}
		}
		sources = append(sources, tui.CommandSource{Slash: slash})
	}

	// 4. Skills. Slash-invocable as /skill-name; no argument completions today.
	for _, sk := range session.GetSkills() {
		sources = append(sources, tui.CommandSource{
			Slash: &tui.SlashCommand{
				Name:        sk.Name,
				Description: sk.Description,
			},
		})
	}

	return tui.NewCombinedAutocompleteProvider(sources, cwd, "")
}

// ctrlC implements app.clear (reference interactive-mode.ts:3202-3211): the
// first press clears the editor, a second press within 500ms exits. Returns
// true when the app should exit; the OnAction wrapper runs Stop on a goroutine
// (Stop stops the engine — calling it synchronously under the render lock
// would self-deadlock).
func (o *Orchestrator) ctrlC() bool {
	now := time.Now()
	if now.Sub(o.lastCtrlC) < 500*time.Millisecond {
		return true
	}
	o.editor.SetText("")
	o.lastCtrlC = now
	return false
}
