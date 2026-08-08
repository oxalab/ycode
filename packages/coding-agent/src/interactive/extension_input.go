package interactive

// extension_input.go ports reference components/extension-input.ts (verified —
// see mapping/13-selectors.md): a single-line input widget extensions use to
// ask the user for a value, with an optional timeout countdown in the title.
// It is an editor-swap widget (swapInSelector). Enter submits, Escape cancels.
//
// Wired to ExtensionUIContext.Input via Orchestrator.extensionInput
// (extension_ui.go). Go-side extensions can call ctx.UI.Input(...); JS-side
// exposure (ui.input as an awaitable Promise) is still pending — see
// js_context.go's buildJSUI.

import (
	"fmt"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// ExtensionInput is a single-line extension input with an optional timeout.
type ExtensionInput struct {
	*tui.Container
	input     *components.Input
	title     *components.Text
	countdown *CountdownTimer
	onSubmit  func(string)
	onCancel  func()
	focused   bool
}

// NewExtensionInput builds the widget. timeoutMs<=0 or engine==nil → no countdown.
func NewExtensionInput(engine *tui.TUI, title, placeholder string, timeoutMs int, th *theme.Theme,
	onSubmit func(string), onCancel func()) *ExtensionInput {

	e := &ExtensionInput{onSubmit: onSubmit, onCancel: onCancel}
	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewSpacer(1))
	e.title = components.NewText(th.Fg("accent", title), 1, 0, nil)
	c.AddChild(e.title)
	c.AddChild(components.NewSpacer(1))

	if timeoutMs > 0 && engine != nil {
		base := title
		e.countdown = NewCountdownTimer(timeoutMs, engine,
			func(s int) { e.title.SetText(th.Fg("accent", fmt.Sprintf("%s (%ds)", base, s))) },
			func() { if onCancel != nil { onCancel() } },
		)
	}
	_ = placeholder // placeholder hint could be added (the Input has no built-in placeholder)

	e.input = components.NewInput()
	c.AddChild(e.input)
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText(
		keyHint(th, "tui.select.confirm", "submit")+"  "+keyHint(th, "tui.select.cancel", "cancel"), 1, 0, nil))
	c.AddChild(components.NewSpacer(1))
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	e.Container = c
	return e
}

// HandleInput: Enter submits, Escape cancels, else forwards to the Input.
func (e *ExtensionInput) HandleInput(data string) {
	kb := tui.GetKeybindings()
	if kb.Matches(data, "tui.select.confirm") || data == "\n" {
		if e.onSubmit != nil {
			e.onSubmit(e.input.GetValue())
		}
	} else if kb.Matches(data, "tui.select.cancel") {
		if e.onCancel != nil {
			e.onCancel()
		}
	} else {
		e.input.HandleInput(data)
	}
}

// Focused / SetFocused implement tui.Focusable — propagate to the Input.
func (e *ExtensionInput) Focused() bool     { return e.focused }
func (e *ExtensionInput) SetFocused(f bool) { e.focused = f; e.input.SetFocused(f) }

// Dispose stops the countdown (call when the widget is removed).
func (e *ExtensionInput) Dispose() {
	if e.countdown != nil {
		e.countdown.Dispose()
	}
}
