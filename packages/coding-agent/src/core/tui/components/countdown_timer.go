package components

import (
	"time"

	"github.com/charmbracelet/bubbles/timer"
	tea "github.com/charmbracelet/bubbletea"
)

// CountdownTimerCallbacks fires as the timer counts down. OnTick is called with
// the remaining seconds (0 on the final tick); OnExpire fires once when the
// timer reaches zero. Either may be nil.
type CountdownTimerCallbacks struct {
	OnTick   func(remainingSeconds int)
	OnExpire func()
}

// CountdownTimer is a reusable, callback-driven countdown used by dialog
// components (e.g. an auto-cancelling selector). It wraps bubbles/timer.
//
// Unlike the TS reference (which self-runs via setInterval), this is a tea.Model:
// the owning component forwards messages to it and issues its Init() command so
// ticks flow through the program's event loop. The onTick/onExpire callbacks
// fire as ticks are processed in Update.
//
// Port of reference/coding-agent/src/modes/interactive/components/countdown-timer.ts
type CountdownTimer struct {
	timer    timer.Model
	onTick   func(int)
	onExpire func()
	expired  bool
}

// NewCountdownTimer creates a timer that expires after timeoutSeconds. The
// initial remaining value is reported via OnTick immediately (mirroring the TS
// constructor), matching ceil(timeoutMs/1000) semantics.
func NewCountdownTimer(timeoutSeconds int, callbacks CountdownTimerCallbacks) *CountdownTimer {
	timeout := time.Duration(timeoutSeconds) * time.Second
	c := &CountdownTimer{
		timer:    timer.New(timeout), // 1-second interval
		onTick:   callbacks.OnTick,
		onExpire: callbacks.OnExpire,
	}
	if c.onTick != nil && timeoutSeconds > 0 {
		c.onTick(timeoutSeconds)
	}
	return c
}

// Init starts the timer (the owning component should return this from its Init).
func (c *CountdownTimer) Init() tea.Cmd { return c.timer.Init() }

// Update advances the timer on its own TickMsgs and fires the callbacks.
func (c *CountdownTimer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	tick, ok := msg.(timer.TickMsg)
	if !ok {
		return c, nil
	}
	// Ignore ticks belonging to other timer instances.
	if tick.ID != 0 && tick.ID != c.timer.ID() {
		return c, nil
	}

	var cmd tea.Cmd
	c.timer, cmd = c.timer.Update(msg)

	if c.timer.Timedout() {
		if !c.expired {
			c.expired = true
			if c.onTick != nil {
				c.onTick(0)
			}
			if c.onExpire != nil {
				c.onExpire()
			}
		}
		// Expired: stop scheduling further ticks.
		return c, nil
	}

	if c.onTick != nil {
		c.onTick(int(c.timer.Timeout / time.Second))
	}
	return c, cmd
}

// View is empty — this is a non-visual helper; the owning component renders the
// remaining time itself (driven by OnTick).
func (c CountdownTimer) View() string { return "" }

// RemainingSeconds returns the current remaining seconds.
func (c CountdownTimer) RemainingSeconds() int {
	return int(c.timer.Timeout / time.Second)
}

// Stop pauses the timer (mirrors the TS dispose).
func (c *CountdownTimer) Stop() tea.Cmd { return c.timer.Stop() }
