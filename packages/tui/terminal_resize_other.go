//go:build !unix

package tui

// On non-Unix (Windows) there is no SIGWINCH equivalent exposed by x/term.
// Resize is detected by the engine polling Columns/Rows (see CAVEATS C8).

func (t *ProcessTerminal) setupResize()         {}
func (t *ProcessTerminal) teardownResize()      {}
func (t *ProcessTerminal) refreshDimensions()   {}
