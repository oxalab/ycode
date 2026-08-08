package interactive

// keybindings.go builds the KeybindingsManager shared by the editor, selectors,
// and the header's keybinding hints.
//
// It merges the pi-tui tui.* defaults with EVERY app.* action (keys taken
// verbatim from reference/coding-agent/src/core/keybindings.ts KEYBINDINGS — the
// authoritative source) and installs the result globally, mirroring reference
// interactive-mode's setKeybindings. This makes the header's hint text resolve
// real keys and lets CustomEditor intercept app.* keys. (The actions themselves
// are wired incrementally P1–P6; user overrides from keybindings.json land in P6.)

import (
	"runtime"

	"ycode/packages/tui"
)

// App action IDs handled by CustomEditor / referenced by header hints.
const (
	appInterrupt  = "app.interrupt"
	appExit       = "app.exit"
	appPasteImage = "app.clipboard.pasteImage"
)

// installAppKeybindings builds the merged tui.* + app.* manager and installs it
// globally. Returns the manager for the CustomEditor to hold.
func installAppKeybindings() *tui.KeybindingsManager {
	defs := tui.KeybindingDefinitions{}
	for k, v := range tui.TUIKeybindings {
		defs[k] = v
	}

	// add registers an action with one or more default keys (verbatim from
	// reference keybindings.ts).
	add := func(action string, keys ...string) {
		ks := make([]tui.KeyID, 0, len(keys))
		for _, s := range keys {
			ks = append(ks, tui.KeyID(s))
		}
		defs[tui.Keybinding(action)] = tui.KeybindingDefinition{DefaultKeys: ks}
	}

	add(appInterrupt, "escape")
	add("app.clear", "ctrl+c")
	add(appExit, "ctrl+d")
	if runtime.GOOS != "windows" {
		add("app.suspend", "ctrl+z")
	} else {
		add("app.suspend") // disabled on Windows (reference: [])
	}
	add("app.thinking.cycle", "shift+tab")
	add("app.model.cycleForward", "ctrl+p")
	add("app.model.cycleBackward", "shift+ctrl+p")
	add("app.model.select", "ctrl+l")
	add("app.tools.expand", "ctrl+o")
	add("app.thinking.toggle", "ctrl+t")
	add("app.session.toggleNamedFilter", "ctrl+n")
	add("app.editor.external", "ctrl+g")
	add("app.message.followUp", "alt+enter")
	add("app.message.dequeue", "alt+up")
	if runtime.GOOS != "windows" {
		add(appPasteImage, "ctrl+v")
	} else {
		add(appPasteImage, "alt+v")
	}
	add("app.session.new")  // no default key
	add("app.session.tree") //
	add("app.session.fork") //
	add("app.session.resume") //
	add("app.tree.foldOrUp", "ctrl+left", "alt+left")
	add("app.tree.unfoldOrDown", "ctrl+right", "alt+right")
	add("app.tree.editLabel", "shift+l")
	add("app.tree.toggleLabelTimestamp", "shift+t")
	add("app.session.togglePath", "ctrl+p")
	add("app.session.toggleSort", "ctrl+s")
	add("app.session.rename", "ctrl+r")
	add("app.session.delete", "ctrl+d")
	add("app.session.deleteNoninvasive", "ctrl+backspace")
	add("app.models.save", "ctrl+s")
	add("app.models.enableAll", "ctrl+a")
	add("app.models.clearAll", "ctrl+x")
	add("app.models.toggleProvider", "ctrl+p")
	add("app.models.reorderUp", "alt+up")
	add("app.models.reorderDown", "alt+down")
	add("app.tree.filter.default", "ctrl+d")
	add("app.tree.filter.noTools", "ctrl+t")
	add("app.tree.filter.userOnly", "ctrl+u")
	add("app.tree.filter.labeledOnly", "ctrl+l")
	add("app.tree.filter.all", "ctrl+a")
	add("app.tree.filter.cycleForward", "ctrl+o")
	add("app.tree.filter.cycleBackward", "shift+ctrl+o")

	m := tui.NewKeybindingsManager(defs, nil)
	tui.SetKeybindings(m)
	return m
}

// defaultKeybindings returns the merged global manager.
func defaultKeybindings() *tui.KeybindingsManager {
	return tui.GetKeybindings()
}
