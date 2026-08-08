package interactive

// countdown_timer.go ports reference components/countdown-timer.ts (verified —
// see mapping/13-selectors.md). A pure timer with no render output: it calls
// onTick immediately, then every second (calling onTick + RequestRender), and
// onExpire when it reaches 0. Used by the auto-retry status (P7) and the
// extension-input timeout. Dispose cancels it.

import (
	"sync"
	"time"

	"ycode/packages/tui"
)

// CountdownTimer ticks down from ceil(timeoutMs/1000) once per second.
type CountdownTimer struct {
	stop chan struct{}
	once sync.Once
}

// NewCountdownTimer starts the timer. onTick is called immediately with the
// initial remaining seconds, then each second; onExpire fires at 0. The engine
// (if non-nil) is re-rendered on each tick.
func NewCountdownTimer(timeoutMs int, engine *tui.TUI, onTick func(remainingSeconds int), onExpire func()) *CountdownTimer {
	t := &CountdownTimer{stop: make(chan struct{})}
	remaining := (timeoutMs + 999) / 1000 // ceil(timeoutMs / 1000)
	if remaining < 0 {
		remaining = 0
	}
	onTick(remaining)
	if remaining <= 0 {
		if onExpire != nil {
			onExpire()
		}
		return t // already expired; no ticking goroutine
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-t.stop:
				return
			case <-ticker.C:
				remaining--
				if onTick != nil {
					onTick(remaining)
				}
				if engine != nil {
					engine.RequestRender()
				}
				if remaining <= 0 {
					t.Dispose()
					if onExpire != nil {
						onExpire()
					}
					return
				}
			}
		}
	}()
	return t
}

// Dispose stops the timer (idempotent).
func (t *CountdownTimer) Dispose() {
	t.once.Do(func() { close(t.stop) })
}
