package interactive

// retry.go wires the auto-retry countdown (P7). The session emits
// "auto_retry_start" (with RetryDelayMs/RetryAttempt/RetryMaxAttempt) and
// "auto_retry_end"; we show an animated "Retrying (X/Y) in Ns... (esc to
// cancel)" loader in the status container, ticking down via CountdownTimer,
// swap the editor's escape handler to abort the retry, and clear it all on
// end (reference interactive-mode.ts:2904-2957).
//
// Concurrency: the events arrive on the session event goroutine (not under the
// render lock) → Mutate is safe; the CountdownTimer ticks on its own goroutine
// and updates the Loader message via the mutex-guarded SetMessage +
// RequestRender. OnEscape is dispatched from the editor's input handling
// (under the render lock), so the abort runs on a goroutine.

import (
	"fmt"

	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
)

// handleRetryStart shows the retry loader + starts the countdown and swaps
// escape to abort the pending retry.
func (o *Orchestrator) handleRetryStart(event core.AgentSessionEvent) {
	attempt, max := event.RetryAttempt, event.RetryMaxAttempt
	loader := components.NewLoader(o.engine,
		fgStyle(o.th, "warning"), fgStyle(o.th, "muted"),
		o.retryStatusText(event.RetryDelayMs/1000, attempt, max), nil)
	o.engine.Mutate(func() {
		o.status.Clear()
		o.status.AddChild(loader)
		loader.Start() // safe under Mutate (Start requests no render)
	})
	o.retryLoader = loader

	// Escape aborts the retry (interactive-mode.ts:2906-2909). Dispatch off
	// the render lock — AbortRetry resolves the backoff sleep, whose abort
	// path emits auto_retry_end on the session goroutine.
	o.retryEscapeHandler = o.editor.OnEscape
	o.editor.OnEscape = func() { go o.session.AbortRetry() }

	if o.retryTimer != nil {
		o.retryTimer.Dispose()
	}
	o.retryTimer = NewCountdownTimer(event.RetryDelayMs, o.engine,
		func(remaining int) { loader.SetMessage(o.retryStatusText(remaining, attempt, max)) },
		nil, // expiry — the session emits auto_retry_end
	)
}

// stopRetryChrome tears down any active retry loader/countdown/escape swap.
// Shared by handleRetryEnd's cleanup, the agent_start handler (TS stops the
// retry loader on agent_start, interactive-mode.ts:2643-2650, since
// auto_retry_end may not have arrived), and Stop().
func (o *Orchestrator) stopRetryChrome() {
	if o.retryTimer != nil {
		o.retryTimer.Dispose()
		o.retryTimer = nil
	}
	if o.retryLoader != nil {
		o.retryLoader.Stop()
		o.retryLoader = nil
	}
	o.editor.OnEscape = o.retryEscapeHandler
	o.retryEscapeHandler = nil
}

// handleRetryEnd clears the retry status, restores the escape handler, stops
// the countdown + loader, and surfaces the final failure (TS shows
// "Retry failed after N attempts" only when the chain gave up — the
// user-aborted chain reports "Retry cancelled", which is its own notice).
func (o *Orchestrator) handleRetryEnd(event core.AgentSessionEvent) {
	o.stopRetryChrome()
	o.engine.Mutate(func() { o.status.Clear() })

	if event.RetrySuccess || event.RetryError == "Retry cancelled" {
		return
	}
	errMsg := event.RetryError
	if errMsg == "" {
		errMsg = "Unknown error"
	}
	o.engine.Mutate(func() {
		o.chat.AddChild(components.NewSpacer(1))
		o.chat.AddChild(components.NewText(o.th.Fg("error",
			fmt.Sprintf("Retry failed after %d attempt(s): %s", event.RetryAttempt, errMsg)), 0, 0, nil))
	})
}

// retryStatusText renders the warning-colored retry line in the reference's
// word order: attempt/max first, then the delay, then the cancel hint
// (interactive-mode.ts:2912-2914).
func (o *Orchestrator) retryStatusText(remainingSec, attempt, max int) string {
	if remainingSec < 0 {
		remainingSec = 0
	}
	return o.th.Fg("warning", fmt.Sprintf("Retrying (%d/%d) in %ds... (%s to cancel)",
		attempt, max, remainingSec, keyText(appInterrupt)))
}
