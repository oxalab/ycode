package tui

// This file is a Go port of reference/tui/src/keybindings.ts — a registry that
// maps named actions to one-or-more KeyIDs, resolves user overrides, and detects
// conflicts. See packages/tui/mapping/08-keybindings.md.
//
// Note: the reference relies on JS object insertion order for conflict listing;
// Go maps are unordered, so we iterate sorted for deterministic output.

import "sort"

// Keybinding is a named action such as "tui.editor.cursorUp".
type Keybinding string

// KeybindingDefinition is the default keys + description for an action.
type KeybindingDefinition struct {
	DefaultKeys []KeyID
	Description string
}

// KeybindingDefinitions maps actions to their definitions.
type KeybindingDefinitions map[Keybinding]KeybindingDefinition

// KeybindingsConfig maps actions to a set of keys (user overrides).
type KeybindingsConfig map[Keybinding][]KeyID

// KeybindingConflict reports a key claimed by more than one user binding.
type KeybindingConflict struct {
	Key         KeyID
	Keybindings []Keybinding
}

// TUIKeybindings are the built-in default bindings.
var TUIKeybindings = KeybindingDefinitions{
	"tui.editor.cursorUp":          {DefaultKeys: []KeyID{"up"}, Description: "Move cursor up"},
	"tui.editor.cursorDown":        {DefaultKeys: []KeyID{"down"}, Description: "Move cursor down"},
	"tui.editor.cursorLeft":        {DefaultKeys: []KeyID{"left", "ctrl+b"}, Description: "Move cursor left"},
	"tui.editor.cursorRight":       {DefaultKeys: []KeyID{"right", "ctrl+f"}, Description: "Move cursor right"},
	"tui.editor.cursorWordLeft":    {DefaultKeys: []KeyID{"alt+left", "ctrl+left", "alt+b"}, Description: "Move cursor word left"},
	"tui.editor.cursorWordRight":   {DefaultKeys: []KeyID{"alt+right", "ctrl+right", "alt+f"}, Description: "Move cursor word right"},
	"tui.editor.cursorLineStart":   {DefaultKeys: []KeyID{"home", "ctrl+a"}, Description: "Move to line start"},
	"tui.editor.cursorLineEnd":     {DefaultKeys: []KeyID{"end", "ctrl+e"}, Description: "Move to line end"},
	"tui.editor.jumpForward":       {DefaultKeys: []KeyID{"ctrl+]"}, Description: "Jump forward to character"},
	"tui.editor.jumpBackward":      {DefaultKeys: []KeyID{"ctrl+alt+]"}, Description: "Jump backward to character"},
	"tui.editor.pageUp":            {DefaultKeys: []KeyID{"pageUp"}, Description: "Page up"},
	"tui.editor.pageDown":          {DefaultKeys: []KeyID{"pageDown"}, Description: "Page down"},
	"tui.editor.deleteCharBackward": {DefaultKeys: []KeyID{"backspace"}, Description: "Delete character backward"},
	"tui.editor.deleteCharForward":  {DefaultKeys: []KeyID{"delete", "ctrl+d"}, Description: "Delete character forward"},
	"tui.editor.deleteWordBackward": {DefaultKeys: []KeyID{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
	"tui.editor.deleteWordForward":  {DefaultKeys: []KeyID{"alt+d", "alt+delete"}, Description: "Delete word forward"},
	"tui.editor.deleteToLineStart":  {DefaultKeys: []KeyID{"ctrl+u"}, Description: "Delete to line start"},
	"tui.editor.deleteToLineEnd":    {DefaultKeys: []KeyID{"ctrl+k"}, Description: "Delete to line end"},
	"tui.editor.yank":              {DefaultKeys: []KeyID{"ctrl+y"}, Description: "Yank"},
	"tui.editor.yankPop":           {DefaultKeys: []KeyID{"alt+y"}, Description: "Yank pop"},
	"tui.editor.undo":              {DefaultKeys: []KeyID{"ctrl+-"}, Description: "Undo"},
	"tui.input.newLine":            {DefaultKeys: []KeyID{"shift+enter"}, Description: "Insert newline"},
	"tui.input.submit":             {DefaultKeys: []KeyID{"enter"}, Description: "Submit input"},
	"tui.input.tab":                {DefaultKeys: []KeyID{"tab"}, Description: "Tab / autocomplete"},
	"tui.input.copy":               {DefaultKeys: []KeyID{"ctrl+c"}, Description: "Copy selection"},
	"tui.select.up":                {DefaultKeys: []KeyID{"up"}, Description: "Move selection up"},
	"tui.select.down":              {DefaultKeys: []KeyID{"down"}, Description: "Move selection down"},
	"tui.select.pageUp":            {DefaultKeys: []KeyID{"pageUp"}, Description: "Selection page up"},
	"tui.select.pageDown":          {DefaultKeys: []KeyID{"pageDown"}, Description: "Selection page down"},
	"tui.select.confirm":           {DefaultKeys: []KeyID{"enter"}, Description: "Confirm selection"},
	"tui.select.cancel":            {DefaultKeys: []KeyID{"escape", "ctrl+c"}, Description: "Cancel selection"},
}

// KeybindingsManager resolves keybindings (defaults + user overrides) and reports
// conflicts among user bindings.
type KeybindingsManager struct {
	defs     KeybindingDefinitions
	user     KeybindingsConfig
	keysById map[Keybinding][]KeyID
	conflicts []KeybindingConflict
}

// NewKeybindingsManager builds a manager from definitions and optional user overrides.
func NewKeybindingsManager(defs KeybindingDefinitions, user KeybindingsConfig) *KeybindingsManager {
	if user == nil {
		user = KeybindingsConfig{}
	}
	m := &KeybindingsManager{defs: defs, user: user, keysById: map[Keybinding][]KeyID{}}
	m.rebuild()
	return m
}

func normalizeKeys(keys []KeyID) []KeyID {
	var out []KeyID
	seen := map[KeyID]bool{}
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func (m *KeybindingsManager) rebuild() {
	m.keysById = map[Keybinding][]KeyID{}
	m.conflicts = nil

	// Collect user claims, iterating user bindings in sorted action order so the
	// reported conflict keybinding lists are deterministic.
	claims := map[KeyID]map[Keybinding]bool{}
	claimOrder := map[KeyID][]Keybinding{}

	var ukbs []Keybinding
	for kb := range m.user {
		if _, ok := m.defs[kb]; ok {
			ukbs = append(ukbs, kb)
		}
	}
	sort.Slice(ukbs, func(i, j int) bool { return ukbs[i] < ukbs[j] })
	for _, kb := range ukbs {
		for _, key := range normalizeKeys(m.user[kb]) {
			if claims[key] == nil {
				claims[key] = map[Keybinding]bool{}
			}
			if !claims[key][kb] {
				claims[key][kb] = true
				claimOrder[key] = append(claimOrder[key], kb)
			}
		}
	}

	var conflictKeys []KeyID
	for key, kbs := range claims {
		if len(kbs) > 1 {
			conflictKeys = append(conflictKeys, key)
		}
	}
	sort.Slice(conflictKeys, func(i, j int) bool { return conflictKeys[i] < conflictKeys[j] })
	for _, key := range conflictKeys {
		m.conflicts = append(m.conflicts, KeybindingConflict{
			Key:         key,
			Keybindings: append([]Keybinding(nil), claimOrder[key]...),
		})
	}

	for kb, def := range m.defs {
		keys, ok := m.user[kb]
		if !ok {
			keys = def.DefaultKeys
		}
		m.keysById[kb] = normalizeKeys(keys)
	}
}

// Matches reports whether data matches any of the action's resolved keys.
func (m *KeybindingsManager) Matches(data string, kb Keybinding) bool {
	for _, k := range m.keysById[kb] {
		if MatchesKey(data, k) {
			return true
		}
	}
	return false
}

// GetKeys returns the resolved keys for an action (defensive copy).
func (m *KeybindingsManager) GetKeys(kb Keybinding) []KeyID {
	out := make([]KeyID, len(m.keysById[kb]))
	copy(out, m.keysById[kb])
	return out
}

// GetDefinition returns the definition for an action.
func (m *KeybindingsManager) GetDefinition(kb Keybinding) KeybindingDefinition { return m.defs[kb] }

// GetConflicts returns conflicts among user bindings (defensive copy).
func (m *KeybindingsManager) GetConflicts() []KeybindingConflict {
	out := make([]KeybindingConflict, len(m.conflicts))
	for i, c := range m.conflicts {
		out[i] = KeybindingConflict{
			Key:         c.Key,
			Keybindings: append([]Keybinding(nil), c.Keybindings...),
		}
	}
	return out
}

// SetUserBindings replaces the user overrides and rebuilds.
func (m *KeybindingsManager) SetUserBindings(cfg KeybindingsConfig) {
	m.user = cfg
	m.rebuild()
}

// GetUserBindings returns a defensive copy of the user overrides.
func (m *KeybindingsManager) GetUserBindings() KeybindingsConfig {
	out := KeybindingsConfig{}
	for k, v := range m.user {
		out[k] = append([]KeyID(nil), v...)
	}
	return out
}

// GetResolvedBindings returns the resolved keys for every defined action.
func (m *KeybindingsManager) GetResolvedBindings() KeybindingsConfig {
	out := KeybindingsConfig{}
	for kb := range m.defs {
		out[kb] = m.GetKeys(kb)
	}
	return out
}

// Global default manager.

var globalKeybindings *KeybindingsManager

// SetKeybindings installs the global default manager.
func SetKeybindings(m *KeybindingsManager) { globalKeybindings = m }

// GetKeybindings returns the global manager, lazily built from TUIKeybindings.
func GetKeybindings() *KeybindingsManager {
	if globalKeybindings == nil {
		globalKeybindings = NewKeybindingsManager(TUIKeybindings, nil)
	}
	return globalKeybindings
}
