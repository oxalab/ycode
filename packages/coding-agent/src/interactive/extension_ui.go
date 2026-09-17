package interactive

// extension_ui.go builds the ExtensionUIContext the interactive TUI provides
// to extensions. This is the Go port of the reference's
// InteractiveMode.createExtensionUIContext() (interactive-mode.ts:1937-1992).
//
// Until this file, the pi-tui interactive mode never called BindExtensions,
// so every extension ran against NoOpUIContext — ctx.ui.notify was a no-op.
// Wiring this (orchestrator.go calls BindExtensions with this context) makes
// the full UI surface reachable from both Go-factory and JS extensions.
//
// Behavior mirrors the reference faithfully:
//   - notify(message, level) appends a THEMED LINE to the chat transcript
//     (not a toast, not a status bar). info=dim, warning="Warning: …" in
//     warning color, error="Error: …" in error color. This matches
//     interactive-mode.ts:2235-2243 → showError/showWarning/showStatus.
//   - status(message) writes to the footer status bar via FooterDataProvider.
//   - select/confirm/input swap a selector into editorContainer and block
//     until the user responds, mirroring showExtensionSelector/Input.

import (
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/coding-agent/src/core/tui/theme"
	"ycode/packages/tui"
	"ycode/packages/tui/components"
)

// createExtensionUIContext builds the ExtensionUIContext the orchestrator hands
// to session.BindExtensions. The closures capture the orchestrator so they can
// reach the chat tree, footer, and selector machinery.
//
// All callbacks MUST be safe to call from a goroutine (extension handlers run
// off the render-lock goroutine). Tree mutations go through engine.Mutate +
// RequestRender, the established async-safe pattern (see slash.go,
// add_provider.go).
func (o *Orchestrator) createExtensionUIContext() extensions.ExtensionUIContext {
	return extensions.ExtensionUIContext{
		// select swaps a SelectList into editorContainer and blocks until the
		// user picks or cancels. Mirrors showExtensionSelector
		// (interactive-mode.ts:1997-2035).
		Select: o.extensionSelect,

		// confirm is select with ["Yes", "No"] and a "title\nmessage" header
		// (reference: showExtensionConfirm at interactive-mode.ts:2052-2059).
		Confirm: func(title, message string, timeoutMs int) (bool, error) {
			idx, err := o.extensionSelect(title+"\n"+message, []string{"Yes", "No"}, 0, timeoutMs)
			if err != nil || idx < 0 {
				return false, err
			}
			return idx == 0, nil
		},

		// input uses the ExtensionInput widget swapped into editorContainer.
		// Mirrors showExtensionInput (interactive-mode.ts:2072-2110).
		Input: o.extensionInput,

		// editor uses the ExtensionEditor widget for multi-line input
		// (commit messages, descriptions, etc.). Mirrors showExtensionEditor.
		Editor: o.extensionEditor,

		// notify appends a themed line to the chat transcript. This is the
		// single most-used UI method — extensions call it from session_start,
		// command handlers, etc. to tell the user what they did.
		// Reference: interactive-mode.ts:2235-2243.
		Notify: o.extensionNotify,

		// status writes to the footer status bar (distinct from notify).
		// Reference: interactive-mode.ts:1654-1657 → footerDataProvider. The
		// key namespaces the slot in the reference (a Map); the Go footer
		// keeps a single slot for now, so the latest status wins.
		// Reference keyed-map behavior is a noted follow-up.
		Status: func(key, message string) {
			if o.footerData == nil {
				return
			}
			o.footerData.SetExtensionStatus(message)
			o.engine.RequestRender()
		},
	}
}

// extensionNotify appends a themed line to the chat transcript, dispatching by
// level. Mirrors the reference's showError/showWarning/showStatus exactly:
//   - "error"   → "Error: {message}"    in the error color
//   - "warning" → "Warning: {message}"  in the warning color
//   - else      → "{message}"           in the dim color
//
// Runs under engine.Mutate (extension handlers fire off the render-lock
// goroutine) + RequestRender to reflect the new line.
func (o *Orchestrator) extensionNotify(message, level string) {
	th := o.th
	if th == nil {
		th = theme.GetTheme()
	}
	var styled string
	switch level {
	case "error":
		styled = th.Fg("error", "Error: "+message)
	case "warning":
		styled = th.Fg("warning", "Warning: "+message)
	default:
		styled = th.Fg("dim", message)
	}
	// addNotice appends Spacer(1) + themed Text under the render lock.
	// Extension handlers may fire from a goroutine, so route through Mutate
	// for thread safety (addNotice is lock-assumed; Mutate acquires the lock).
	o.engine.Mutate(func() {
		o.addNotice(styled)
	})
	o.engine.RequestRender()
}

// extensionSelect swaps a SelectList into editorContainer and blocks until the
// user picks (returns selected index) or cancels (returns -1). Mirrors the
// reference's showExtensionSelector, which uses a Promise that resolves when
// the list's onSelect/onCancel fires. title renders as an accent-bold header
// line above the options (extension-selector.ts:44); multi-line titles (e.g.
// confirm's "title\nmessage") render as multiple header lines.
//
// The blocking-via-channel pattern: build the selector, register onSelect/
// onCancel closures that resolve a Go channel, swap in, wait. The wait happens
// on the calling goroutine (extension handlers run off the render lock), so
// the engine keeps rendering on its own goroutine.
func (o *Orchestrator) extensionSelect(title string, options []string, defaultIdx, timeoutMs int) (int, error) {
	if len(options) == 0 {
		return -1, nil
	}
	items := make([]components.SelectItem, len(options))
	for i, opt := range options {
		items[i] = components.SelectItem{Value: opt, Label: opt}
	}
	result := make(chan int, 1) // 0 = cancelled, otherwise 1-based idx

	list := components.NewSelectList(items, len(items), buildSelectListTheme(o.th), components.SelectListLayoutOptions{})
	if defaultIdx > 0 && defaultIdx < len(items) {
		list.SetSelectedIndex(defaultIdx)
	}
	list.OnSelect = func(item components.SelectItem) {
		for i, si := range items {
			if si.Value == item.Value && si.Label == item.Label {
				result <- i + 1
				return
			}
		}
		result <- 0
	}
	list.OnCancel = func() { result <- 0 }

	// Titled shell (extension-selector.ts:42-46): border → spacer → title →
	// spacer → list → border.
	th := o.th
	if th == nil {
		th = theme.GetTheme()
	}
	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText(th.Bold(th.Fg("accent", title)), 1, 0, nil))
	c.AddChild(components.NewSpacer(1))
	c.AddChild(list)
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	sel := &simpleSelector{Container: c, list: list}

	// Swap in under the render lock.
	o.engine.Mutate(func() {
		o.editorContainer.Clear()
		o.editorContainer.AddChild(sel)
		o.engine.SetFocusFromInput(sel)
	})
	o.engine.RequestRender()

	// Block until the user responds.
	r := <-result
	o.engine.Mutate(func() { o.closeSelector() })
	o.engine.RequestRender()
	if r == 0 {
		return -1, nil
	}
	return r - 1, nil
}

// extensionInput swaps an input field into editorContainer and blocks until
// the user submits (returns the text) or cancels (returns ""). title is the
// widget's prompt line.
func (o *Orchestrator) extensionInput(title, placeholder string, timeoutMs int) (string, error) {
	result := make(chan string, 1)
	dlg := NewExtensionInput(o.engine, title, placeholder, timeoutMs, o.th,
		func(text string) { result <- text },
		func() { result <- "" },
	)

	o.engine.Mutate(func() {
		o.editorContainer.Clear()
		o.editorContainer.AddChild(dlg)
		o.engine.SetFocusFromInput(dlg)
	})
	o.engine.RequestRender()

	text := <-result
	o.engine.Mutate(func() { o.closeSelector() })
	o.engine.RequestRender()
	return text, nil
}

// extensionEditor swaps a multi-line editor into editorContainer and blocks
// until the user submits (returns the text) or cancels (returns ""). prefill
// pre-populates the editor. Mirrors extensionInput but uses the multi-line
// ExtensionEditor widget (with Ctrl+G external-editor support). title is the
// widget's prompt line.
//
// No timeout parameter: a multi-line compose flow shouldn't be interrupted
// mid-edit, and the widget doesn't render a countdown.
func (o *Orchestrator) extensionEditor(title, prefill string) (string, error) {
	result := make(chan string, 1)
	dlg := NewExtensionEditor(o.engine, o.kb, title, prefill, o.th,
		func(text string) { result <- text },
		func() { result <- "" },
	)

	o.engine.Mutate(func() {
		o.editorContainer.Clear()
		o.editorContainer.AddChild(dlg)
		o.engine.SetFocusFromInput(dlg)
	})
	o.engine.RequestRender()

	text := <-result
	o.engine.Mutate(func() { o.closeSelector() })
	o.engine.RequestRender()
	return text, nil
}

