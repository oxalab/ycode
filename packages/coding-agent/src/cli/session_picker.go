package cli

// This file runs the interactive session picker for --resume: a standalone
// bubbletea program wrapping the SessionSelectorComponent. It is the Go port of
// reference/coding-agent/src/cli/session-picker.ts.
//
// ============================================================================
// Deviations from the plan sketch + honest deferrals
// ============================================================================
//
// Plan sketch: SelectSession(sessionManager) (sessionID string, err error).
// The reference selectSession returns the selected session file PATH (or null),
// and main.ts consumes it as a path (SessionManager.open(selectedPath)). The
// faithful port therefore returns sessionPath, not an id — the sketch's
// "sessionID" name is a mislabel. Cancel maps to "" + nil error (the caller
// treats "" as "no selection", matching the TS null → "No session selected" path).
//
// The reference takes two async session loaders. The Go SessionManager derives
// both pools itself (core.ListSessions + core.ListAllSessions, mirroring the
// App's openSessionSelector), so SelectSession takes a *core.SessionManager. The
// Go list functions are synchronous, so there is no progress callback to thread.
//
// Theme: the reference inherits whatever theme initTheme set (the user's
// configured theme). Settings→theme wiring is not part of this phase, so the
// standalone picker uses the built-in dark theme via theme.NewThemeFromJSON. A
// later overload can accept the configured theme once settings are wired (19.8).
//
// Testability: SelectSession drives a real tea.Program (needs a TTY) and is not
// unit-tested here. The two pure seams — loadSessionPools (data gathering) and
// sessionPickerCallbacks (selection/cancel wiring) — are extracted and covered by
// session_picker_test.go, following the 19.x testing precedent (cf. list_models.go
// extracting renderModelTable). This is the first cli file to import bubbletea;
// unlike the stdout-only --list-models path, an interactive picker cannot avoid it.

import (
	tea "github.com/charmbracelet/bubbletea"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// SelectSession shows the interactive session picker and returns the path of the
// session the user chose, or "" if they cancelled (TS selectSession → path|null).
// It loads the current-folder sessions and all-folder sessions from sm, then runs
// a standalone bubbletea program over the SessionSelectorComponent.
func SelectSession(sm *core.SessionManager) (string, error) {
	t, err := defaultTheme()
	if err != nil {
		return "", err
	}

	current, all, currentPath := loadSessionPools(sm)

	var selected string
	model := newSessionPickerModel(t, current, all, currentPath, &selected)

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return "", err
	}
	return selected, nil
}

// loadSessionPools gathers the current-folder sessions, all-folder sessions, and
// the active session path from a SessionManager — the Go equivalent of the TS
// currentSessionsLoader / allSessionsLoader pair. A nil manager yields empty pools.
func loadSessionPools(sm *core.SessionManager) (current, all []*core.SessionInfo, currentPath string) {
	if sm == nil {
		return nil, nil, ""
	}
	current = core.ListSessions(sm.GetCwd(), sm.GetSessionDir())
	all = core.ListAllSessions()
	currentPath = sm.GetSessionFile()
	return current, all, currentPath
}

// sessionPickerCallbacks builds the select/cancel callbacks that capture the
// chosen path into selected and quit the program (the TS selector's
// onSelect/onCancel). OnSelect records the path; OnCancel clears it. A nil
// selected pointer is tolerated (defensive). In both cases tea.Quit terminates
// the standalone program.
func sessionPickerCallbacks(selected *string) components.SessionSelectorCallbacks {
	return components.SessionSelectorCallbacks{
		OnSelect: func(path string) tea.Cmd {
			if selected != nil {
				*selected = path
			}
			return tea.Quit
		},
		OnCancel: func() tea.Cmd {
			if selected != nil {
				*selected = ""
			}
			return tea.Quit
		},
	}
}

// newSessionPickerModel wraps the SessionSelectorComponent in a minimal tea.Model
// that forwards messages and resizes on WindowSizeMsg. The callbacks capture the
// selection into selected (shared with SelectSession).
func newSessionPickerModel(t *theme.Theme, current, all []*core.SessionInfo, currentSessionPath string, selected *string) sessionPickerModel {
	comp := components.NewSessionSelectorComponent(t, current, all, currentSessionPath, sessionPickerCallbacks(selected))
	comp.SetWidth(80) // sane default until the first WindowSizeMsg arrives
	return sessionPickerModel{comp: comp}
}

// sessionPickerModel is the standalone tea.Model driving the picker.
type sessionPickerModel struct {
	comp *components.SessionSelectorComponent
}

// Init satisfies tea.Model.
func (m sessionPickerModel) Init() tea.Cmd { return nil }

// Update resizes on WindowSizeMsg and forwards every other message to the
// embedded SessionSelectorComponent (which owns the key handling and emits the
// select/cancel cmds).
func (m sessionPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.comp.SetWidth(wsm.Width)
		return m, nil
	}
	model, cmd := m.comp.Update(msg)
	// The component returns itself (a *SessionSelectorComponent); keep the
	// up-to-date pointer so View reflects any mutations.
	if c, ok := model.(*components.SessionSelectorComponent); ok {
		m.comp = c
	}
	return m, cmd
}

// View delegates to the embedded component (header + search + session list).
func (m sessionPickerModel) View() string {
	return m.comp.View()
}

// defaultTheme returns the built-in dark theme for the standalone picker.
func defaultTheme() (*theme.Theme, error) {
	return theme.NewThemeFromJSON(theme.GetDefaultDarkTheme(), nil)
}
