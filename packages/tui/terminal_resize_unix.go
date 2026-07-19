//go:build unix

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// setupResize listens for SIGWINCH and forwards to the resize handler.
func (t *ProcessTerminal) setupResize() {
	if t.resizeHandler == nil {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	t.resizeCh = ch
	go func() {
		for range ch {
			t.resizeHandler()
		}
	}()
}

func (t *ProcessTerminal) teardownResize() {
	if t.resizeCh != nil {
		signal.Stop(t.resizeCh)
		t.resizeCh = nil
	}
}

// refreshDimensions sends SIGWINCH to re-read dimensions after a suspend/resume
// (the signal can be lost while the process is stopped).
func (t *ProcessTerminal) refreshDimensions() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(syscall.SIGWINCH)
	}
}
