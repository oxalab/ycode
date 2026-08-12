package ycode

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ============================================================================
// Keybinding types and defaults — mirrors TS keybindings.ts
// ============================================================================

// AppKeybinding defines a single keyboard shortcut.
type AppKeybinding struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// AppKeybindings defines all application keyboard shortcuts.
// Mirrors TS AppKeybindings interface (keybindings.ts:13-55).
type AppKeybindings struct {
	Submit             AppKeybinding `json:"submit"`
	Newline            AppKeybinding `json:"newline"`
	Abort              AppKeybinding `json:"abort"`
	Backspace          AppKeybinding `json:"backspace"`
	Delete             AppKeybinding `json:"delete"`
	Up                 AppKeybinding `json:"up"`
	Down               AppKeybinding `json:"down"`
	Left               AppKeybinding `json:"left"`
	Right              AppKeybinding `json:"right"`
	Home               AppKeybinding `json:"home"`
	End                AppKeybinding `json:"end"`
	Tab                AppKeybinding `json:"tab"`
	ShiftTab           AppKeybinding `json:"shiftTab"`
	Escape             AppKeybinding `json:"escape"`
	PageUp             AppKeybinding `json:"pageUp"`
	PageDown           AppKeybinding `json:"pageDown"`
	HistoryUp          AppKeybinding `json:"historyUp"`
	HistoryDown        AppKeybinding `json:"historyDown"`
	CycleModel         AppKeybinding `json:"cycleModel"`
	CycleModelReverse  AppKeybinding `json:"cycleModelReverse"`
	ToggleAutoCompact  AppKeybinding `json:"toggleAutoCompact"`
	Compact            AppKeybinding `json:"compact"`
	VimToggle          AppKeybinding `json:"vimToggle"`
	ScrollUp           AppKeybinding `json:"scrollUp"`
	ScrollDown         AppKeybinding `json:"scrollDown"`
	ScrollToTop        AppKeybinding `json:"scrollToTop"`
	ScrollToBottom     AppKeybinding `json:"scrollToBottom"`
	HalfPageUp         AppKeybinding `json:"halfPageUp"`
	HalfPageDown       AppKeybinding `json:"halfPageDown"`
	OpenDiff           AppKeybinding `json:"openDiff"`
	CopyLastMessage    AppKeybinding `json:"copyLastMessage"`
	Fork               AppKeybinding `json:"fork"`
	CostReport         AppKeybinding `json:"costReport"`
	ContextWindow      AppKeybinding `json:"contextWindow"`
	ToggleTool         AppKeybinding `json:"toggleTool"`
	CycleThinkingLevel AppKeybinding `json:"cycleThinkingLevel"`
	Mention            AppKeybinding `json:"mention"`
	AcceptDiff         AppKeybinding `json:"acceptDiff"`
	RejectDiff         AppKeybinding `json:"rejectDiff"`
	PasteImage         AppKeybinding `json:"pasteImage"`
	ClearInput         AppKeybinding `json:"clearInput"`
	Undo               AppKeybinding `json:"undo"`
	Redo               AppKeybinding `json:"redo"`
	WordBack           AppKeybinding `json:"wordBack"`
	WordForward        AppKeybinding `json:"wordForward"`
	DeleteWordBack     AppKeybinding `json:"deleteWordBack"`
	DeleteWordForward  AppKeybinding `json:"deleteWordForward"`
	DeleteToStart      AppKeybinding `json:"deleteToStart"`
	DeleteToEnd        AppKeybinding `json:"deleteToEnd"`
	MoveToStart        AppKeybinding `json:"moveToStart"`
	MoveToEnd          AppKeybinding `json:"moveToEnd"`
}

// DefaultKeybindings returns the default keyboard shortcuts.
// Mirrors TS KEYBINDINGS constant (keybindings.ts:63-202).
func DefaultKeybindings() AppKeybindings {
	return AppKeybindings{
		Submit:             AppKeybinding{Key: "enter", Description: "Submit the current message"},
		Newline:            AppKeybinding{Key: "shift+enter", Description: "Insert a newline"},
		Abort:              AppKeybinding{Key: "escape", Description: "Abort the current operation"},
		Backspace:          AppKeybinding{Key: "backspace", Description: "Delete previous character"},
		Delete:             AppKeybinding{Key: "delete", Description: "Delete next character"},
		Up:                 AppKeybinding{Key: "up", Description: "Move cursor up"},
		Down:               AppKeybinding{Key: "down", Description: "Move cursor down"},
		Left:               AppKeybinding{Key: "left", Description: "Move cursor left"},
		Right:              AppKeybinding{Key: "right", Description: "Move cursor right"},
		Home:               AppKeybinding{Key: "home", Description: "Move to start of line"},
		End:                AppKeybinding{Key: "end", Description: "Move to end of line"},
		Tab:                AppKeybinding{Key: "tab", Description: "Accept suggestion or indent"},
		ShiftTab:           AppKeybinding{Key: "shift+tab", Description: "Navigate to previous"},
		Escape:             AppKeybinding{Key: "escape", Description: "Cancel or dismiss"},
		PageUp:             AppKeybinding{Key: "pageup", Description: "Scroll up one page"},
		PageDown:           AppKeybinding{Key: "pagedown", Description: "Scroll down one page"},
		HistoryUp:          AppKeybinding{Key: "up", Description: "Previous history item"},
		HistoryDown:        AppKeybinding{Key: "down", Description: "Next history item"},
		CycleModel:         AppKeybinding{Key: "ctrl+p", Description: "Cycle through models"},
		CycleModelReverse:  AppKeybinding{Key: "ctrl+o", Description: "Cycle models in reverse"},
		ToggleAutoCompact:  AppKeybinding{Key: "ctrl+t", Description: "Toggle auto-compaction"},
		Compact:            AppKeybinding{Key: "ctrl+t", Description: "Manually compact context"},
		VimToggle:          AppKeybinding{Key: "ctrl+v", Description: "Toggle vim mode"},
		ScrollUp:           AppKeybinding{Key: "shift+up", Description: "Scroll conversation up"},
		ScrollDown:         AppKeybinding{Key: "shift+down", Description: "Scroll conversation down"},
		ScrollToTop:        AppKeybinding{Key: "shift+home", Description: "Scroll to top"},
		ScrollToBottom:     AppKeybinding{Key: "shift+end", Description: "Scroll to bottom"},
		HalfPageUp:         AppKeybinding{Key: "ctrl+up", Description: "Scroll half page up"},
		HalfPageDown:       AppKeybinding{Key: "ctrl+down", Description: "Scroll half page down"},
		OpenDiff:           AppKeybinding{Key: "ctrl+d", Description: "Open diff view"},
		CopyLastMessage:    AppKeybinding{Key: "ctrl+shift+c", Description: "Copy last message"},
		Fork:               AppKeybinding{Key: "ctrl+f", Description: "Fork at selected message"},
		CostReport:         AppKeybinding{Key: "ctrl+l", Description: "Show cost report"},
		ContextWindow:      AppKeybinding{Key: "ctrl+w", Description: "Show context window"},
		ToggleTool:         AppKeybinding{Key: "ctrl+e", Description: "Toggle tool on/off"},
		CycleThinkingLevel: AppKeybinding{Key: "ctrl+r", Description: "Cycle thinking level"},
		Mention:            AppKeybinding{Key: "@", Description: "Mention/autocomplete"},
		AcceptDiff:         AppKeybinding{Key: "ctrl+y", Description: "Accept current diff"},
		RejectDiff:         AppKeybinding{Key: "ctrl+n", Description: "Reject current diff"},
		PasteImage:         AppKeybinding{Key: "ctrl+v", Description: "Paste image from clipboard"},
		ClearInput:         AppKeybinding{Key: "ctrl+u", Description: "Clear input"},
		Undo:               AppKeybinding{Key: "ctrl+z", Description: "Undo"},
		Redo:               AppKeybinding{Key: "ctrl+shift+z", Description: "Redo"},
		WordBack:           AppKeybinding{Key: "alt+b", Description: "Move word back"},
		WordForward:        AppKeybinding{Key: "alt+f", Description: "Move word forward"},
		DeleteWordBack:     AppKeybinding{Key: "ctrl+w", Description: "Delete word back"},
		DeleteWordForward:  AppKeybinding{Key: "alt+d", Description: "Delete word forward"},
		DeleteToStart:      AppKeybinding{Key: "ctrl+u", Description: "Delete to start of line"},
		DeleteToEnd:        AppKeybinding{Key: "ctrl+k", Description: "Delete to end of line"},
		MoveToStart:        AppKeybinding{Key: "ctrl+a", Description: "Move to start of line"},
		MoveToEnd:          AppKeybinding{Key: "ctrl+e", Description: "Move to end of line"},
	}
}

// ============================================================================
// Keybinding name migrations — mirrors TS KEYBINDING_NAME_MIGRATIONS
// ============================================================================

// KeybindingNameMigrations maps old keybinding names to new ones.
// Used when loading config from older versions.
var KeybindingNameMigrations = map[string]string{
	"submitPrompt":          "submit",
	"insertNewline":         "newline",
	"abort":                 "abort",
	"cycleModels":           "cycleModel",
	"cycleModelsReverse":    "cycleModelReverse",
	"toggleAutoCompaction":  "toggleAutoCompact",
	"compact":               "compact",
	"toggleVim":             "vimToggle",
	"scrollUp":              "scrollUp",
	"scrollDown":            "scrollDown",
	"scrollTop":             "scrollToTop",
	"scrollBottom":          "scrollToBottom",
	"halfPageUp":            "halfPageUp",
	"halfPageDown":          "halfPageDown",
	"openDiff":              "openDiff",
	"copyLastResponse":      "copyLastMessage",
	"fork":                  "fork",
	"showCost":              "costReport",
	"showContextWindow":     "contextWindow",
	"toggleTool":            "toggleTool",
	"cycleThinkingLevel":    "cycleThinkingLevel",
	"acceptDiff":            "acceptDiff",
	"rejectDiff":            "rejectDiff",
	"pasteImage":            "pasteImage",
	"clearInput":            "clearInput",
	"undo":                  "undo",
	"redo":                  "redo",
	"wordBack":              "wordBack",
	"wordForward":           "wordForward",
	"deleteWordBack":        "deleteWordBack",
	"deleteWordForward":     "deleteWordForward",
	"deleteToStart":         "deleteToStart",
	"deleteToEnd":           "deleteToEnd",
	"moveToStart":           "moveToStart",
	"moveToEnd":             "moveToEnd",
}

// ============================================================================
// MigrateKeybindingsConfig — mirrors TS migrateKeybindingsConfig
// ============================================================================

// MigrateKeybindingsConfig renames old keybinding names to their current equivalents.
func MigrateKeybindingsConfig(config map[string]string) map[string]string {
	result := make(map[string]string, len(config))
	for name, key := range config {
		if newName, ok := KeybindingNameMigrations[name]; ok {
			result[newName] = key
		} else {
			result[name] = key
		}
	}
	return result
}

// ============================================================================
// KeybindingsManager — mirrors TS KeybindingsManager
// ============================================================================

// KeybindingsManager loads, migrates, and provides keybindings.
type KeybindingsManager struct {
	configDir  string
	keybindings AppKeybindings
}

// NewKeybindingsManager creates a keybindings manager for the given config directory.
func NewKeybindingsManager(configDir string) *KeybindingsManager {
	return &KeybindingsManager{
		configDir:   configDir,
		keybindings: DefaultKeybindings(),
	}
}

// Keybindings returns the current keybindings.
func (m *KeybindingsManager) Keybindings() AppKeybindings {
	return m.keybindings
}

// Load reads keybindings from the config file, migrating if necessary.
func (m *KeybindingsManager) Load() error {
	configPath := filepath.Join(m.configDir, "keybindings.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // use defaults
		}
		return err
	}

	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Migrate old names
	migrated := MigrateKeybindingsConfig(raw)

	// Apply to defaults
	defaults := DefaultKeybindings()
	applyKeybindingOverrides(&defaults, migrated)
	m.keybindings = defaults
	return nil
}

// applyKeybindingOverrides applies user overrides to the default keybindings.
func applyKeybindingOverrides(kb *AppKeybindings, overrides map[string]string) {
	// Use reflection-like approach with a simple switch
	for name, key := range overrides {
		switch name {
		case "submit":
			kb.Submit.Key = key
		case "newline":
			kb.Newline.Key = key
		case "abort":
			kb.Abort.Key = key
		case "cycleModel":
			kb.CycleModel.Key = key
		case "cycleModelReverse":
			kb.CycleModelReverse.Key = key
		case "toggleAutoCompact":
			kb.ToggleAutoCompact.Key = key
		case "compact":
			kb.Compact.Key = key
		case "vimToggle":
			kb.VimToggle.Key = key
		case "scrollUp":
			kb.ScrollUp.Key = key
		case "scrollDown":
			kb.ScrollDown.Key = key
		case "scrollToTop":
			kb.ScrollToTop.Key = key
		case "scrollToBottom":
			kb.ScrollToBottom.Key = key
		case "halfPageUp":
			kb.HalfPageUp.Key = key
		case "halfPageDown":
			kb.HalfPageDown.Key = key
		case "openDiff":
			kb.OpenDiff.Key = key
		case "copyLastMessage":
			kb.CopyLastMessage.Key = key
		case "fork":
			kb.Fork.Key = key
		case "costReport":
			kb.CostReport.Key = key
		case "contextWindow":
			kb.ContextWindow.Key = key
		case "toggleTool":
			kb.ToggleTool.Key = key
		case "cycleThinkingLevel":
			kb.CycleThinkingLevel.Key = key
		case "acceptDiff":
			kb.AcceptDiff.Key = key
		case "rejectDiff":
			kb.RejectDiff.Key = key
		case "pasteImage":
			kb.PasteImage.Key = key
		case "clearInput":
			kb.ClearInput.Key = key
		case "undo":
			kb.Undo.Key = key
		case "redo":
			kb.Redo.Key = key
		case "wordBack":
			kb.WordBack.Key = key
		case "wordForward":
			kb.WordForward.Key = key
		case "deleteWordBack":
			kb.DeleteWordBack.Key = key
		case "deleteWordForward":
			kb.DeleteWordForward.Key = key
		case "deleteToStart":
			kb.DeleteToStart.Key = key
		case "deleteToEnd":
			kb.DeleteToEnd.Key = key
		case "moveToStart":
			kb.MoveToStart.Key = key
		case "moveToEnd":
			kb.MoveToEnd.Key = key
		}
	}
}
