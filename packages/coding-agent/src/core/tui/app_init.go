package tui

import tea "github.com/charmbracelet/bubbletea"

// Init satisfies the tea.Model interface.
// Returns the initial set of commands to run on startup.
//
// Phase 15: starts the agent event bridge so streaming/tool/compaction
// events flow into the Update loop. The bridge is started via a command
// because Init cannot mutate the model directly (Elm Architecture); the
// program reference is set by SetProgram before Run(), which also sets
// the package-level globalProgram the bridge reads from.
func (a App) Init() tea.Cmd {
	session := a.session
	return tea.Batch(
		func() tea.Msg { return initCompleteMsg{} },
		// Start the event bridge in a command. Captures the session at Init
		// time; globalProgram is set before Run() so the bridge can send.
		func() tea.Msg {
			if session != nil {
				_ = startEventBridge(session)
			}
			return bridgeStartedMsg{}
		},
		// Start the spinner ticking so the working indicator animates.
		a.spinner.Tick,
	)
}

// bridgeStartedMsg signals that the event bridge has been started.
type bridgeStartedMsg struct{}
