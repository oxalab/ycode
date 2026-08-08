package interactive

// retry.go wires the auto-retry countdown (P7). The session emits
// "auto_retry_start" (with RetryDelayMs/RetryAttempt/RetryMaxAttempt) and
// "auto_retry_end"; we show a "Retrying in Ns... (attempt X/Y)" status line in
// the status container, ticking down via CountdownTimer, and clear it on end.
//
// Concurrency: the events arrive on the session event goroutine (not under the
// render lock) → Mutate is safe; the CountdownTimer ticks on its own goroutine
// and updates the Text via the mutex-guarded SetText + RequestRender.

import (
	"fmt"

	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
)

// handleRetryStart shows the retry status line + starts the countdown.
func (o *Orchestrator) handleRetryStart(event core.AgentSessionEvent) {
	attempt, max := event.RetryAttempt, event.RetryMaxAttempt
	txt := components.NewText(o.retryStatusText(event.RetryDelayMs/1000, attempt, max), 0, 0, nil)
	o.engine.Mutate(func() {
		o.status.Clear()
		o.status.AddChild(txt)
	})
	if o.retryTimer != nil {
		o.retryTimer.Dispose()
	}
	o.retryTimer = NewCountdownTimer(event.RetryDelayMs, o.engine,
		func(remaining int) { txt.SetText(o.retryStatusText(remaining, attempt, max)) },
		nil, // expiry — the session emits auto_retry_end
	)
}

// handleRetryEnd clears the retry status + stops the countdown.
func (o *Orchestrator) handleRetryEnd(_ core.AgentSessionEvent) {
	if o.retryTimer != nil {
		o.retryTimer.Dispose()
		o.retryTimer = nil
	}
	o.engine.Mutate(func() { o.status.Clear() })
}

// retryStatusText renders the warning-colored retry line.
func (o *Orchestrator) retryStatusText(remainingSec, attempt, max int) string {
	if remainingSec < 0 {
		remainingSec = 0
	}
	return o.th.Fg("warning", fmt.Sprintf("Retrying in %ds... (attempt %d/%d)", remainingSec, attempt, max))
}
