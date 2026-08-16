package components

import (
	"strconv"
	"strings"
	"time"

	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/timer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ExtensionInputCallbacks wires the input's submit/cancel results back to the
// caller via the result-protocol pattern (matching ExtensionEditor).
type ExtensionInputCallbacks struct {
	OnSubmit func(value string) tea.Cmd
	OnCancel func() tea.Cmd
}

// ExtensionInputOptions holds optional configuration. TimeoutMs, when > 0,
// starts an auto-cancel countdown whose remaining seconds are shown in the title.
type ExtensionInputOptions struct {
	TimeoutMs int
}

// ExtensionInput is a single-line text input shown when an extension requests
// short input (e.g. via the extension Input UI). Enter submits, Escape cancels.
// An optional countdown auto-cancels on expiry. Port of extension-input.ts.
//
// The countdown is driven by an embedded bubbles/timer (rather than the reusable
// CountdownTimer) so expiry can return a tea.Cmd through the result protocol —
// CountdownTimer's OnExpire is a void callback and cannot.
type ExtensionInput struct {
	textinput textinput.Model
	theme     *theme.Theme
	baseTitle string
	title     string
	width     int
	callbacks ExtensionInputCallbacks
	timer     *timer.Model // nil when no timeout
}

// NewExtensionInput creates an input with the given title and placeholder.
func NewExtensionInput(t *theme.Theme, title, placeholder string, cb ExtensionInputCallbacks, opts ExtensionInputOptions) ExtensionInput {
	in := textinput.New()
	in.Prompt = " > "
	in.Placeholder = placeholder
	in.CharLimit = 0

	ei := ExtensionInput{
		textinput: in,
		theme:     t,
		baseTitle: title,
		title:     title,
		width:     60,
		callbacks: cb,
	}
	if opts.TimeoutMs > 0 {
		tm := timer.New(time.Duration(opts.TimeoutMs) * time.Millisecond)
		ei.timer = &tm
		secs := (opts.TimeoutMs + 999) / 1000 // ceil
		ei.title = title + " (" + strconv.Itoa(secs) + "s)"
	}
	return ei
}

// SetWidth updates the input width (satisfies the overlayComponent interface).
func (e *ExtensionInput) SetWidth(w int) {
	e.width = w
	// Account for the prompt + borders so the editable field fits.
	width := w - 4
	if width < 1 {
		width = 1
	}
	e.textinput.Width = width
}

// Init satisfies tea.Model: focus the input and start the countdown if present.
func (e ExtensionInput) Init() tea.Cmd {
	if e.timer != nil {
		return tea.Batch(e.textinput.Focus(), e.timer.Init())
	}
	return e.textinput.Focus()
}

// Update satisfies tea.Model.
func (e ExtensionInput) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Countdown ticks: filter to this timer instance.
	if e.timer != nil {
		if tick, ok := msg.(timer.TickMsg); ok && (tick.ID == 0 || tick.ID == e.timer.ID()) {
			tm, cmd := e.timer.Update(msg)
			e.timer = &tm
			if e.timer.Timedout() {
				e.title = e.baseTitle + " (0s)"
				if e.callbacks.OnCancel != nil {
					return e, tea.Batch(cmd, e.callbacks.OnCancel())
				}
				return e, cmd
			}
			e.title = e.baseTitle + " (" + strconv.Itoa(int(e.timer.Timeout/time.Second)) + "s)"
			return e, cmd
		}
	}

	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.String() {
		case "enter":
			value := strings.TrimSpace(e.textinput.Value())
			if value == "" {
				return e, nil // ignore empty submit
			}
			if e.callbacks.OnSubmit != nil {
				return e, e.callbacks.OnSubmit(value)
			}
			return e, nil
		case "escape":
			if e.callbacks.OnCancel != nil {
				return e, e.callbacks.OnCancel()
			}
			return e, nil
		}
	}

	var cmd tea.Cmd
	e.textinput, cmd = e.textinput.Update(msg)
	return e, cmd
}

// View satisfies tea.Model.
func (e ExtensionInput) View() string {
	var b strings.Builder
	if e.title != "" && e.theme != nil {
		b.WriteString(e.theme.Fg("accent", e.theme.Bold(e.title)) + "\n")
	}
	rendered := e.textinput.View()
	if e.theme != nil {
		rendered = lipgloss.NewStyle().
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(e.theme.ToLipglossColor("border")).
			Render(rendered)
	}
	b.WriteString(rendered)
	if e.theme != nil {
		b.WriteString("\n" + e.theme.Dim("↵ submit  esc cancel"))
	}
	return b.String()
}
