//go:build !unix

package tui

import (
	"time"
)

// On non-Unix (Windows) there is no SIGWINCH equivalent exposed by x/term, and
// a ReadConsoleInput WINDOW_BUFFER_SIZE_EVENT watcher would consume input
// records racing the key-input reader on the same console handle. The
// reference gets a push 'resize' event from Node/libuv on every platform; the
// closest Go equivalent is polling the console size and firing the resize
// handler on change (the engine's widthChanged/heightChanged → fullRender
// logic picks it up from there). Node on Windows fires 'resize' only on
// column changes — a poll on width+height is a strict superset.
func (t *ProcessTerminal) setupResize() {
	if t.resizeHandler == nil {
		return
	}
	stop := make(chan struct{})
	t.resizeStop = stop

	lastW, lastH := t.Columns(), t.Rows()
	go func() {
		ticker := time.NewTicker(resizePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				w, h := t.Columns(), t.Rows()
				if w == lastW && h == lastH {
					continue
				}
				lastW, lastH = w, h
				t.resizeHandler()
			}
		}
	}()
}

func (t *ProcessTerminal) teardownResize() {
	if t.resizeStop != nil {
		close(t.resizeStop)
		t.resizeStop = nil
	}
}

// refreshDimensions is a no-op on non-Unix: the poll re-reads the live size
// every tick, so stale dimensions self-correct (Unix self-SIGWINCHes instead —
// the signal can be lost while the process is stopped).
func (t *ProcessTerminal) refreshDimensions() {}
