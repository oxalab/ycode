package tui

import tea "github.com/charmbracelet/bubbletea"

// globalProgram is the package-level reference to the running Bubble Tea
// program. Bubble Tea's Elm Architecture treats the model as immutable —
// the Program holds its own copy of the model, so a *tea.Program set on a
// model value before Run() (e.g., via SetProgram) does NOT propagate into
// the running program's copy. Async goroutines (like the event bridge) that
// need to inject messages must reach the *actual* program, hence this global.
//
// Set by SetProgram before Run(); read by the event bridge and any command
// that needs to send messages from outside the Update loop.
var globalProgram *tea.Program

// sendToProgram safely sends a message into the Bubble Tea loop.
// It is a no-op if no program is registered (e.g., during tests).
func sendToProgram(msg tea.Msg) {
	if globalProgram != nil {
		globalProgram.Send(msg)
	}
}
