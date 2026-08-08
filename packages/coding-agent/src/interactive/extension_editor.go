package interactive

// extension_editor.go ports reference components/extension-editor.ts (verified —
// see mapping/13-selectors.md): a multi-line editor widget extensions use for
// longer input, with Ctrl+G (app.editor.external) to edit in $VISUAL/$EDITOR.
// Editor-swap widget. Enter submits, Escape cancels.
//
// Wired to ExtensionUIContext.Editor via Orchestrator.extensionEditor
// (extension_ui.go). Go-side extensions can call ctx.UI.Editor(...); JS-side
// exposure (ui.editor as an awaitable Promise) is still pending — see
// js_context.go's buildJSUI.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// ExtensionEditor is a multi-line editor with external-editor support.
type ExtensionEditor struct {
	*tui.Container
	engine   *tui.TUI
	kb       *tui.KeybindingsManager
	editor   *components.Editor
	onSubmit func(string)
	onCancel func()
	focused  bool
}

// NewExtensionEditor builds the widget. prefill pre-populates the editor.
func NewExtensionEditor(engine *tui.TUI, kb *tui.KeybindingsManager, title, prefill string,
	th *theme.Theme, onSubmit func(string), onCancel func()) *ExtensionEditor {

	e := &ExtensionEditor{engine: engine, kb: kb, onSubmit: onSubmit, onCancel: onCancel}
	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText(th.Fg("accent", title), 1, 0, nil))
	c.AddChild(components.NewSpacer(1))

	e.editor = components.NewEditor(engine, buildEditorTheme(th), components.EditorOptions{PaddingX: 1})
	if prefill != "" {
		e.editor.SetText(prefill)
	}
	e.editor.OnSubmit = func(text string) { if onSubmit != nil { onSubmit(text) } }
	c.AddChild(e.editor)
	c.AddChild(components.NewSpacer(1))

	hint := keyHint(th, "tui.select.confirm", "submit") + "  " +
		keyHint(th, "tui.input.newLine", "newline") + "  " +
		keyHint(th, "tui.select.cancel", "cancel")
	if os.Getenv("VISUAL") != "" || os.Getenv("EDITOR") != "" {
		hint += "  " + keyHint(th, "app.editor.external", "external editor")
	}
	c.AddChild(components.NewText(hint, 1, 0, nil))
	c.AddChild(components.NewSpacer(1))
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	e.Container = c
	return e
}

// HandleInput: Escape cancels, Ctrl+G opens the external editor, else forwards
// to the editor (which handles typing + Enter-submit).
func (e *ExtensionEditor) HandleInput(data string) {
	kb := tui.GetKeybindings()
	if kb.Matches(data, "tui.select.cancel") {
		if e.onCancel != nil {
			e.onCancel()
		}
		return
	}
	if e.kb != nil && e.kb.Matches(data, "app.editor.external") {
		e.openExternalEditor()
		return
	}
	e.editor.HandleInput(data)
}

// Focused / SetFocused implement tui.Focusable — propagate to the editor.
func (e *ExtensionEditor) Focused() bool     { return e.focused }
func (e *ExtensionEditor) SetFocused(f bool) { e.focused = f; e.editor.SetFocused(f) }

// openExternalEditor suspends the TUI, edits the text in $VISUAL/$EDITOR, then
// resumes (extension-editor.ts:113-146). Best-effort: any error resumes the TUI.
func (e *ExtensionEditor) openExternalEditor() {
	editorCmd := os.Getenv("VISUAL")
	if editorCmd == "" {
		editorCmd = os.Getenv("EDITOR")
	}
	if editorCmd == "" {
		return
	}
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ycode-ext-editor-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, []byte(e.editor.GetText()), 0644); err != nil {
		return
	}
	e.engine.Stop()
	func() {
		defer func() {
			_ = os.Remove(tmpFile)
			e.engine.Start()
			e.engine.RequestRender(true) // full redraw (external editor used the screen)
		}()
		parts := strings.Fields(editorCmd)
		if len(parts) == 0 {
			return
		}
		cmd := exec.Command(parts[0], append(parts[1:], tmpFile)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err == nil {
			if content, err := os.ReadFile(tmpFile); err == nil {
				e.editor.SetText(strings.TrimSuffix(string(content), "\n"))
			}
		}
	}()
}
