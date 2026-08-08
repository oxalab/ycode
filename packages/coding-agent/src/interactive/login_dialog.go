package interactive

// login_dialog.go ports reference components/login-dialog.ts (verified — see
// mapping/13-selectors.md). It is a stateful editor-swap dialog driven by
// AuthStorage.Login: the Login goroutine fires OnAuth/OnPrompt/OnProgress/
// OnManualCodeInput callbacks that rebuild the dialog's content, while user
// input arrives from HandleInput (under the engine render lock) and is handed
// to the blocking OnPrompt/OnManualCodeInput via a buffered channel.
//
// Concurrency model:
//   - show* methods are PURE content builders (no locking). The Login goroutine
//     wraps them in engine.Mutate (goroutine-safe). The constructor calls
//     showWaiting directly (it runs under the render lock from swapInSelector).
//   - OnPrompt/OnManualCodeInput block on promptCh (NOT holding the render lock).
//     HandleInput forwards typing to the Input; on submit the Input's OnSubmit
//     does a non-blocking send to promptCh (buffered, cap 1). Escape cancels.
//   - On Login completion, onComplete runs on the Login goroutine and wraps
//     closeSelector in Mutate (closeSelector's SetFocusFromInput is safe because
//     Mutate holds the render lock).

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	oauth "ycode/packages/llm/src/utils/oauth"
)

// LoginDialog is the OAuth/API-key login dialog (login-dialog.ts).
type LoginDialog struct {
	*tui.Container
	engine   *tui.TUI
	th       *theme.Theme
	provider string
	name     string

	content *tui.Container // dynamic content area (rebuilt by show*)
	input   *components.Input

	promptCh     chan string // user input → blocking OnPrompt/OnManualCodeInput
	promptActive bool        // a prompt is currently awaiting input
	cancelFn     context.CancelFunc
	onComplete   func(success bool, message string)

	focused bool
	done    bool
}

// NewLoginDialog builds the dialog. The caller runs Start (AuthStorage.Login)
// separately on a goroutine. onComplete fires exactly once when Login returns.
func NewLoginDialog(engine *tui.TUI, providerId, providerName string, th *theme.Theme,
	onComplete func(success bool, message string)) *LoginDialog {

	d := &LoginDialog{
		engine:     engine,
		th:         th,
		provider:   providerId,
		name:       providerName,
		promptCh:   make(chan string, 1),
		onComplete: onComplete,
	}

	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewText(th.Bold(th.Fg("accent", "Login to "+providerName)), 1, 0, nil))
	d.content = &tui.Container{}
	c.AddChild(d.content)
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	d.Container = c

	d.input = components.NewInput()
	d.input.OnSubmit = func(value string) {
		if d.promptActive {
			d.promptActive = false
			select {
			case d.promptCh <- value:
			default:
			}
		}
	}

	// Initial state (constructor runs under the render lock → direct mutation).
	d.showWaiting("Starting login...")
	return d
}

// Start runs AuthStorage.Login on a goroutine, wiring the show* callbacks. The
// cancel context is stored so cancelLogin (Escape) / orchestrator Stop can abort
// an in-flight flow. Calls onComplete exactly once when Login returns.
func (d *LoginDialog) Start(auth *core.AuthStorage) {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancelFn = cancel
	callbacks := oauth.OAuthLoginCallbacks{
		Cancel: ctx,
		OnAuth: func(info oauth.OAuthAuthInfo) {
			d.engine.Mutate(func() { d.showAuth(info.URL, info.Instructions) })
		},
		OnPrompt: func(prompt oauth.OAuthPrompt) (string, error) {
			d.engine.Mutate(func() { d.showPrompt(prompt.Message, prompt.Placeholder) })
			d.promptActive = true
			select {
			case v := <-d.promptCh:
				return v, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		OnManualCodeInput: func() (string, error) {
			d.engine.Mutate(func() { d.showPrompt("Enter the code from your browser:", "") })
			d.promptActive = true
			select {
			case v := <-d.promptCh:
				return v, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		OnProgress: func(message string) {
			d.engine.Mutate(func() { d.showProgress(message) })
		},
	}
	go func() {
		err := auth.Login(ctx, d.provider, callbacks)
		d.finish(err == nil, errToString(err))
	}()
}

// cancelLogin aborts an in-flight login (Escape). The Login goroutine's blocking
// OnPrompt/OnManualCodeInput unblock via ctx.Done(); Login returns ctx.Err().
func (d *LoginDialog) cancelLogin() {
	if d.cancelFn != nil {
		d.cancelFn()
	}
}

// finish fires onComplete once and marks the dialog done.
func (d *LoginDialog) finish(success bool, message string) {
	if d.done {
		return
	}
	d.done = true
	if d.onComplete != nil {
		d.onComplete(success, message)
	}
}

// HandleInput: Escape cancels; everything else forwards to the Input (typing +
// submit). Runs under the render lock.
func (d *LoginDialog) HandleInput(data string) {
	if tui.GetKeybindings().Matches(data, "tui.select.cancel") {
		d.cancelLogin()
		return
	}
	d.input.HandleInput(data)
}

// Focused / SetFocused implement tui.Focusable — propagate to the Input.
func (d *LoginDialog) Focused() bool     { return d.focused }
func (d *LoginDialog) SetFocused(f bool) { d.focused = f; d.input.SetFocused(f) }

// --- show* content builders (pure; caller holds the render lock or wraps in Mutate) ---

func (d *LoginDialog) showAuth(url, instructions string) {
	d.content.Clear()
	d.content.AddChild(components.NewSpacer(1))
	d.content.AddChild(components.NewText(d.th.Fg("accent", osc8Link(url, url)), 1, 0, nil))
	clickHint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		clickHint = "Cmd+click to open"
	}
	d.content.AddChild(components.NewText(d.th.Fg("dim", osc8Link(url, clickHint)), 1, 0, nil))
	if instructions != "" {
		d.content.AddChild(components.NewSpacer(1))
		d.content.AddChild(components.NewText(d.th.Fg("warning", instructions), 1, 0, nil))
	}
	go openBrowser(url) // best-effort; never block the render thread
}

func (d *LoginDialog) showPrompt(message, placeholder string) {
	// Does NOT clear — preserves the auth URL above it (login-dialog.ts:128).
	d.content.AddChild(components.NewSpacer(1))
	d.content.AddChild(components.NewText(d.th.Fg("text", message), 1, 0, nil))
	if placeholder != "" {
		d.content.AddChild(components.NewText(d.th.Fg("dim", "e.g., "+placeholder), 1, 0, nil))
	}
	d.content.AddChild(d.input)
	hint := "(" + keyHint(d.th, "tui.select.cancel", "to cancel,") + " " +
		keyHint(d.th, "tui.select.confirm", "to submit") + ")"
	d.content.AddChild(components.NewText(hint, 1, 0, nil))
	d.input.SetValue("")
}

func (d *LoginDialog) showWaiting(message string) {
	d.content.Clear()
	d.content.AddChild(components.NewSpacer(1))
	d.content.AddChild(components.NewText(d.th.Fg("dim", message), 1, 0, nil))
	d.content.AddChild(components.NewText(
		"("+keyHint(d.th, "tui.select.cancel", "to cancel")+")", 1, 0, nil))
}

func (d *LoginDialog) showProgress(message string) {
	d.content.AddChild(components.NewText(d.th.Fg("dim", message), 1, 0, nil))
}

// osc8Link wraps text in an OSC 8 hyperlink terminal escape.
func osc8Link(url, text string) string {
	return "\x1b]8;;" + url + "\x07" + text + "\x1b]8;;\x07"
}

// openBrowser opens url in the OS default browser (best-effort).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

func errToString(err error) string {
	if err == nil {
		return ""
	}
	// context.Canceled surfaces as "Login cancelled" for a clean status.
	if strings.Contains(err.Error(), "context canceled") {
		return "Login cancelled"
	}
	return err.Error()
}
