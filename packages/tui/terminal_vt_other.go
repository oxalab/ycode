//go:build !windows

package tui

func (t *ProcessTerminal) enableWindowsVTInput() {}
