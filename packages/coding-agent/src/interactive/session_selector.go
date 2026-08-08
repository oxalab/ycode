package interactive

// session_selector.go implements the sessionSelector component. It extends the
// simpleSelector pattern with two extra keybindings: app.session.delete
// (Ctrl+D) and app.session.rename (Ctrl+R), matching the reference
// session-selector.ts:562,568.
//
// List mode: shows the session list with a hint line below it.
// Rename mode: swaps in a single-line Input pre-filled with the current name;
// Enter saves, Escape cancels.
//
// The actual delete/rename work runs on a goroutine (file I/O) via the
// onDelete/onRename callbacks the orchestrator wires up — see session.go.

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// sessionItem is the orchestrator-facing shape for a session list row.
type sessionItem struct {
	path    string
	label   string
	desc    string
	current bool
}

// sessionSelector is the /switch session list with delete/rename actions.
type sessionSelector struct {
	*tui.Container
	th *theme.Theme

	list  *components.SelectList
	items []sessionItem

	// Rename mode state.
	mode         sessionSelectorMode
	renameTarget sessionItem
	renameInput  *components.Input

	// Callbacks. onDelete/onRename run the file operation; the caller is
	// responsible for refreshing the list (typically by calling refresh()
	// after the operation completes).
	onSwitch func(path string)
	onCancel func()
	onDelete func(path, label string)
	onRename func(path, currentName, newName string)

	focused bool
}

type sessionSelectorMode string

const (
	sessionModeList   sessionSelectorMode = "list"
	sessionModeRename sessionSelectorMode = "rename"
)

// newSessionSelector constructs an empty selector. Call refresh() to populate
// the list.
func newSessionSelector(th *theme.Theme) *sessionSelector {
	return &sessionSelector{
		Container: &tui.Container{},
		th:        th,
		mode:      sessionModeList,
	}
}

// refresh reloads items via load and rebuilds the list view.
func (s *sessionSelector) refresh(load func() []sessionItem) {
	s.items = load()
	s.rebuildList()
}

// rebuildList constructs the SelectList from s.items and re-renders list mode.
// Safe to call from any mode — it forces back to list mode.
func (s *sessionSelector) rebuildList() {
	items := make([]components.SelectItem, len(s.items))
	for i, it := range s.items {
		items[i] = components.SelectItem{
			Value:       it.path,
			Label:       it.label,
			Description: it.desc,
		}
	}
	s.list = components.NewSelectList(items, 10, buildSelectListTheme(s.th))
	s.list.OnSelect = func(item components.SelectItem) {
		if s.onSwitch != nil {
			s.onSwitch(item.Value)
		}
	}
	s.list.OnCancel = s.onCancel
	s.mode = sessionModeList
	s.renameInput = nil
	s.renderListMode()
}

// renderListMode rebuilds the container children for list mode.
func (s *sessionSelector) renderListMode() {
	s.Container.Clear()
	s.Container.AddChild(newDynamicBorder(fgStyle(s.th, "border")))
	s.Container.AddChild(s.list)
	if hint := s.buildHint(); hint != "" {
		s.Container.AddChild(components.NewText(hint, 1, 0, nil))
	}
	s.Container.AddChild(newDynamicBorder(fgStyle(s.th, "border")))
}

// buildHint returns the keybinding hint line shown beneath the list.
func (s *sessionSelector) buildHint() string {
	return keyHint(s.th, "app.session.delete", "delete") + "  " +
		keyHint(s.th, "app.session.rename", "rename")
}

// enterRenameMode swaps the list for a single-line Input pre-filled with the
// target's current name.
func (s *sessionSelector) enterRenameMode(target sessionItem) {
	s.renameTarget = target
	s.renameInput = components.NewInput()
	s.renameInput.SetValue(target.label)
	s.mode = sessionModeRename
	s.renderRenameMode()
}

// renderRenameMode rebuilds the container children for rename mode.
func (s *sessionSelector) renderRenameMode() {
	s.Container.Clear()
	s.Container.AddChild(newDynamicBorder(fgStyle(s.th, "border")))
	s.Container.AddChild(components.NewSpacer(1))
	s.Container.AddChild(components.NewText(
		s.th.Fg("accent", "Rename session:"), 1, 0, nil))
	s.Container.AddChild(components.NewSpacer(1))
	s.Container.AddChild(s.renameInput)
	s.Container.AddChild(components.NewSpacer(1))
	s.Container.AddChild(components.NewText(
		keyHint(s.th, "tui.select.confirm", "save")+"  "+
			keyHint(s.th, "tui.select.cancel", "cancel"), 1, 0, nil))
	s.Container.AddChild(components.NewSpacer(1))
	s.Container.AddChild(newDynamicBorder(fgStyle(s.th, "border")))
}

// exitRenameMode returns to list mode without committing.
func (s *sessionSelector) exitRenameMode() {
	s.mode = sessionModeList
	s.renameInput = nil
	s.renderListMode()
}

// HandleInput routes input by mode: rename mode owns its keys; list mode adds
// delete/rename interception before delegating to the SelectList.
func (s *sessionSelector) HandleInput(data string) {
	kb := tui.GetKeybindings()

	if s.mode == sessionModeRename {
		switch {
		case kb.Matches(data, "tui.select.cancel"):
			s.exitRenameMode()
			return
		case kb.Matches(data, "tui.select.confirm"), data == "\n", data == "\r":
			if s.renameInput != nil {
				newName := s.renameInput.GetValue()
				if s.onRename != nil {
					s.onRename(s.renameTarget.path, s.renameTarget.label, newName)
				}
			}
			s.exitRenameMode()
			return
		default:
			if s.renameInput != nil {
				s.renameInput.HandleInput(data)
			}
			return
		}
	}

	// List mode.
	if kb.Matches(data, "app.session.delete") {
		s.fireDelete()
		return
	}
	if kb.Matches(data, "app.session.rename") {
		s.enterRenameModeForSelected()
		return
	}
	s.list.HandleInput(data)
}

// fireDelete invokes onDelete for the currently-selected row.
func (s *sessionSelector) fireDelete() {
	target, ok := s.selectedItem()
	if !ok {
		return
	}
	if s.onDelete != nil {
		s.onDelete(target.path, target.label)
	}
}

// enterRenameModeForSelected enters rename mode for the currently-selected row.
func (s *sessionSelector) enterRenameModeForSelected() {
	target, ok := s.selectedItem()
	if !ok {
		return
	}
	s.enterRenameMode(target)
}

// selectedItem returns the sessionItem backing the list's current selection.
func (s *sessionSelector) selectedItem() (sessionItem, bool) {
	if s.list == nil {
		return sessionItem{}, false
	}
	item, ok := s.list.GetSelectedItem()
	if !ok {
		return sessionItem{}, false
	}
	for _, it := range s.items {
		if it.path == item.Value {
			return it, true
		}
	}
	return sessionItem{}, false
}

// Focused / SetFocused implement tui.Focusable. In rename mode, focus
// propagates to the Input so the IME cursor renders.
func (s *sessionSelector) Focused() bool { return s.focused }

func (s *sessionSelector) SetFocused(f bool) {
	s.focused = f
	if s.mode == sessionModeRename && s.renameInput != nil {
		s.renameInput.SetFocused(f)
	}
}
