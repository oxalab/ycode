package interactive

// custom_editor.go ports reference/coding-agent/src/modes/interactive/components/
// custom-editor.ts (verified — see mapping/13-selectors.md). It embeds the pi-tui
// Editor and intercepts app-level keybindings (extension shortcuts, paste-image,
// interrupt/escape, exit) plus a registered action map, then delegates the rest
// to the base Editor — which owns all the editing fidelity (undo, kill-ring,
// history, word-nav, char-jump, paste markers, autocomplete, Kitty keys).

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
)

// CustomEditor wraps the pi-tui Editor with app keybinding interception.
type CustomEditor struct {
	*components.Editor
	kb *tui.KeybindingsManager

	actionHandlers map[tui.Keybinding]func()

	// Dynamic handlers (mirror reference onEscape/onCtrlD/onPasteImage/onExtensionShortcut).
	OnEscape            func()
	OnCtrlD             func()
	OnPasteImage        func()
	OnExtensionShortcut func(data string) bool
}

// NewCustomEditor wraps a base pi-tui Editor.
func NewCustomEditor(ed *components.Editor, kb *tui.KeybindingsManager) *CustomEditor {
	return &CustomEditor{Editor: ed, kb: kb, actionHandlers: map[tui.Keybinding]func(){}}
}

// OnAction registers a handler for an app action (e.g. "app.model.cycleForward").
func (c *CustomEditor) OnAction(action tui.Keybinding, handler func()) {
	c.actionHandlers[action] = handler
}

// HandleInput intercepts app keybindings before delegating to the base Editor
// (reference custom-editor.ts:30-79). Runs under the engine render lock, so
// handlers must not call RequestRender or block.
func (c *CustomEditor) HandleInput(data string) {
	// 1. Extension-registered shortcuts take precedence.
	if c.OnExtensionShortcut != nil && c.OnExtensionShortcut(data) {
		return
	}

	// 2. Paste image.
	if c.kb.Matches(data, appPasteImage) {
		if c.OnPasteImage != nil {
			c.OnPasteImage()
		}
		return
	}

	// 3. Interrupt/escape — only when autocomplete is NOT open (otherwise let the
	// base Editor cancel the dropdown).
	if c.kb.Matches(data, appInterrupt) {
		if !c.IsShowingAutocomplete() {
			h := c.OnEscape
			if h == nil {
				h = c.actionHandlers[appInterrupt]
			}
			if h != nil {
				h()
				return
			}
		}
		c.Editor.HandleInput(data)
		return
	}

	// 4. Exit (Ctrl+D) — only when the editor is empty; otherwise fall through so
	// the base Editor treats Ctrl+D as delete-char-forward.
	if c.kb.Matches(data, appExit) {
		if len(c.GetText()) == 0 {
			h := c.OnCtrlD
			if h == nil {
				h = c.actionHandlers[appExit]
			}
			if h != nil {
				h()
				return
			}
		}
		// fall through (editor non-empty)
	}

	// 5. Other registered app actions. (Map order is non-deterministic; P1
	// registers none, so this is a no-op until P6 adds model/thinking/etc. cycling.)
	for action, h := range c.actionHandlers {
		if action == appInterrupt || action == appExit {
			continue
		}
		if c.kb.Matches(data, action) {
			h()
			return
		}
	}

	// 6. Delegate to the base Editor (editing + autocomplete + tui.* keys).
	c.Editor.HandleInput(data)
}
