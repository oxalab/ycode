//go:build windows

package tui

import "golang.org/x/sys/windows"

// enableWindowsVTInput adds ENABLE_VIRTUAL_TERMINAL_INPUT (0x0200) to the console
// input handle so Windows sends VT sequences for modified keys (e.g. \x1b[Z for
// Shift+Tab). Run after raw mode (term.MakeRaw), which resets console flags.
func (t *ProcessTerminal) enableWindowsVTInput() {
	handle := windows.Handle(t.input.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) == nil {
		const enableVirtualTerminalInput = 0x0200
		_ = windows.SetConsoleMode(handle, mode|enableVirtualTerminalInput)
	}
}
