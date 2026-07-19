package components

import (
	"context"

	"ycode/packages/tui"
)

// CancellableLoader extends Loader with Escape-to-cancel and a context for
// aborting async operations.
type CancellableLoader struct {
	*Loader
	ctx    context.Context
	cancel context.CancelFunc

	// OnAbort is called when the user presses Escape/Ctrl+C.
	OnAbort func()
}

// NewCancellableLoader returns a started CancellableLoader.
func NewCancellableLoader(t *tui.TUI, spinnerColor, messageColor tui.StyleFunc, message string) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &CancellableLoader{
		Loader: NewLoader(t, spinnerColor, messageColor, message, nil),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Context returns the context (cancelled on Escape).
func (c *CancellableLoader) Context() context.Context { return c.ctx }

// Aborted reports whether the loader was cancelled.
func (c *CancellableLoader) Aborted() bool {
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

// HandleInput cancels on Escape/Ctrl+C (tui.select.cancel).
func (c *CancellableLoader) HandleInput(data string) {
	if tui.GetKeybindings().Matches(data, "tui.select.cancel") {
		c.cancel()
		if c.OnAbort != nil {
			c.OnAbort()
		}
	}
}

// Dispose stops the animation.
func (c *CancellableLoader) Dispose() { c.Loader.Stop() }
