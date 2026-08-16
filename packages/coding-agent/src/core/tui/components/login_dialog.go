package components

import (
	"os/exec"
	"runtime"
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
)

// loginLineKind selects the styling for one content line.
type loginLineKind int

const (
	loginLinePlain loginLineKind = iota
	loginLineAccent
	loginLineDim
	loginLineWarning
	loginLineText
)

// loginLine is one rendered line of the dialog's content area.
type loginLine struct {
	kind loginLineKind
	text string
}

// LoginInputResult is the outcome of a ShowManualInput/ShowPrompt request:
// either a submitted Value or a Cancelled request.
type LoginInputResult struct {
	Value     string
	Cancelled bool
}

// loginDialogMsg carries an imperative call (from the OAuth goroutine) onto the
// tea main loop, where state can be mutated safely. The OAuth flow pushes these
// via LoginDialogCallbacks.Send (the App supplies program.Send).
type loginDialogMsg struct {
	kind         loginMsgKind
	url          string
	instructions string
	message      string // prompt / waiting / progress message
	placeholder  string
	infoLines    []string
	result       chan LoginInputResult // non-nil for input requests
}

type loginMsgKind int

const (
	loginMsgAuth loginMsgKind = iota
	loginMsgManualInput
	loginMsgPrompt
	loginMsgInfo
	loginMsgWaiting
	loginMsgProgress
	loginMsgCancel
)

// LoginDialogCallbacks connects the dialog to the App and the OAuth flow.
//
// OnComplete fires on cancel (success=false) and is returned as a tea.Cmd so
// the App can close the overlay. Send routes imperative state changes onto the
// tea loop (the App supplies program.Send — safe to call from goroutines).
type LoginDialogCallbacks struct {
	OnComplete func(success bool, message string) tea.Cmd
	Send       func(msg tea.Msg)
}

// LoginDialogComponent replaces the editor during an OAuth login flow. The
// OAuth flow drives it imperatively (ShowAuth/ShowPrompt/...); because that
// flow runs in a goroutine, every imperative method routes through Send and is
// applied on the main loop in Update.
//
// Port of reference/coding-agent/src/modes/interactive/components/login-dialog.ts
type LoginDialogComponent struct {
	theme *theme.Theme
	width int

	title string

	lines []loginLine

	input       textinput.Model
	inputActive bool

	// pendingResult is the channel for the active input request, resolved on
	// submit/cancel. nil when no request is pending.
	pendingResult chan LoginInputResult

	cb LoginDialogCallbacks
}

// NewLoginDialogComponent creates a new login dialog. If titleOverride is set
// it is used verbatim; otherwise the title is "Login to <providerName>".
func NewLoginDialogComponent(
	t *theme.Theme,
	providerName string,
	titleOverride string,
	callbacks LoginDialogCallbacks,
) *LoginDialogComponent {
	title := titleOverride
	if title == "" {
		title = "Login to " + providerName
	}

	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "..."
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 40

	return &LoginDialogComponent{
		theme:  t,
		title:  title,
		input:  ti,
		cb:     callbacks,
	}
}

// SetWidth updates the available width and resizes the input.
func (d *LoginDialogComponent) SetWidth(width int) {
	d.width = width
	if width > 2 {
		d.input.Width = width - 2
	}
}

// Init satisfies tea.Model.
func (d *LoginDialogComponent) Init() tea.Cmd { return textinput.Blink }

// --- Imperative API (called from the OAuth goroutine) ---

// ShowAuth displays the OAuth URL (clickable) plus optional instructions, and
// attempts to open it in the user's browser. Clears prior content.
func (d *LoginDialogComponent) ShowAuth(url, instructions string) {
	d.send(loginDialogMsg{kind: loginMsgAuth, url: url, instructions: instructions})
}

// ShowManualInput shows a prompt and waits for manual code/URL entry.
func (d *LoginDialogComponent) ShowManualInput(prompt string) <-chan LoginInputResult {
	ch := make(chan LoginInputResult, 1)
	d.send(loginDialogMsg{kind: loginMsgManualInput, message: prompt, result: ch})
	return ch
}

// ShowPrompt shows a message (plus optional placeholder hint) and waits for
// input. Appends to existing content (preserves a prior auth URL).
func (d *LoginDialogComponent) ShowPrompt(message, placeholder string) <-chan LoginInputResult {
	ch := make(chan LoginInputResult, 1)
	d.send(loginDialogMsg{kind: loginMsgPrompt, message: message, placeholder: placeholder, result: ch})
	return ch
}

// ShowInfo shows informational text. Clears prior content.
func (d *LoginDialogComponent) ShowInfo(lines []string) {
	d.send(loginDialogMsg{kind: loginMsgInfo, infoLines: lines})
}

// ShowWaiting shows a polling/waiting message. Appends to existing content.
func (d *LoginDialogComponent) ShowWaiting(message string) {
	d.send(loginDialogMsg{kind: loginMsgWaiting, message: message})
}

// ShowProgress shows a progress message. Appends to existing content.
func (d *LoginDialogComponent) ShowProgress(message string) {
	d.send(loginDialogMsg{kind: loginMsgProgress, message: message})
}

// Cancel aborts the login flow.
func (d *LoginDialogComponent) Cancel() {
	d.send(loginDialogMsg{kind: loginMsgCancel})
}

func (d *LoginDialogComponent) send(msg loginDialogMsg) {
	if d.cb.Send != nil {
		d.cb.Send(msg)
	}
}

// --- tea.Model ---

// Update applies imperative messages and routes keyboard input.
func (d *LoginDialogComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case loginDialogMsg:
		return d.applyMsg(m)
	case tea.KeyMsg:
		switch m.String() {
		case "enter":
			return d, d.submitInput()
		case "escape", "ctrl+c":
			return d, d.doCancel()
		}
	}

	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return d, cmd
}

// View renders the title + content lines + (when active) the input field.
func (d LoginDialogComponent) View() string {
	var b strings.Builder
	b.WriteString(d.theme.Fg("accent", d.theme.Bold(d.title)) + "\n\n")

	for _, ln := range d.lines {
		b.WriteString(d.styleLine(ln) + "\n")
	}

	if d.inputActive {
		b.WriteString("\n" + d.input.View() + "\n")
		b.WriteString(d.theme.Fg("dim", "(esc to cancel, enter to submit)") + "\n")
	}
	return b.String()
}

// --- Internal ---

// applyMsg mutates state for an imperative message, on the main loop.
func (d *LoginDialogComponent) applyMsg(m loginDialogMsg) (tea.Model, tea.Cmd) {
	switch m.kind {
	case loginMsgAuth:
		d.lines = d.lines[:0]
		d.lines = append(d.lines,
			loginLine{kind: loginLineAccent, text: m.url},
			loginLine{kind: loginLineDim, text: clickHint()},
		)
		if m.instructions != "" {
			d.lines = append(d.lines, loginLine{kind: loginLineWarning, text: m.instructions})
		}
		openBrowser(m.url)
	case loginMsgManualInput:
		d.lines = append(d.lines,
			loginLine{kind: loginLineDim, text: m.message},
		)
		d.startInput(m.result)
	case loginMsgPrompt:
		d.lines = append(d.lines,
			loginLine{kind: loginLineText, text: m.message},
		)
		if m.placeholder != "" {
			d.lines = append(d.lines, loginLine{kind: loginLineDim, text: "e.g., " + m.placeholder})
		}
		d.input.SetValue("")
		d.startInput(m.result)
	case loginMsgInfo:
		d.lines = d.lines[:0]
		for _, l := range m.infoLines {
			d.lines = append(d.lines, loginLine{kind: loginLinePlain, text: l})
		}
		d.lines = append(d.lines, loginLine{kind: loginLineDim, text: "(esc to close)"})
	case loginMsgWaiting:
		d.lines = append(d.lines,
			loginLine{kind: loginLineDim, text: m.message},
			loginLine{kind: loginLineDim, text: "(esc to cancel)"},
		)
	case loginMsgProgress:
		d.lines = append(d.lines, loginLine{kind: loginLineDim, text: m.message})
	case loginMsgCancel:
		return d, d.doCancel()
	}
	return d, nil
}

// startInput activates the input field and records the pending result channel.
func (d *LoginDialogComponent) startInput(result chan LoginInputResult) {
	d.inputActive = true
	d.pendingResult = result
}

// submitInput resolves the active input request with the entered value.
func (d *LoginDialogComponent) submitInput() tea.Cmd {
	if d.pendingResult == nil {
		return nil
	}
	d.pendingResult <- LoginInputResult{Value: d.input.Value()}
	d.pendingResult = nil
	d.inputActive = false
	return nil
}

// doCancel rejects any pending request and notifies the App.
func (d *LoginDialogComponent) doCancel() tea.Cmd {
	if d.pendingResult != nil {
		d.pendingResult <- LoginInputResult{Cancelled: true}
		d.pendingResult = nil
	}
	d.inputActive = false
	if d.cb.OnComplete != nil {
		return d.cb.OnComplete(false, "Login cancelled")
	}
	return nil
}

// styleLine applies the line kind's theme styling.
func (d LoginDialogComponent) styleLine(ln loginLine) string {
	switch ln.kind {
	case loginLineAccent:
		return d.theme.Fg("accent", ln.text)
	case loginLineDim:
		return d.theme.Fg("dim", ln.text)
	case loginLineWarning:
		return d.theme.Fg("warning", ln.text)
	case loginLineText:
		return d.theme.Fg("text", ln.text)
	default:
		return ln.text
	}
}

// clickHint returns the platform-appropriate "open link" hint.
func clickHint() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+click to open"
	}
	return "Ctrl+click to open"
}

// openBrowser attempts to open url in the user's default browser. Errors are
// ignored (best-effort), matching the reference's fire-and-forget exec.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
