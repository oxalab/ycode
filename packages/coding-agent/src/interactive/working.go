package interactive

// working.go implements the P4b "working" indicator + pending-message queue
// preview — the two status-region elements that were deferred after P4a.
//
// Working indicator (reference interactive-mode.ts:1681-1694 setWorkingVisible +
// components/bordered-loader.ts): an animated spinner shown in the status
// container while the assistant is streaming or the context is compacting. It is
// mutually exclusive in practice with the auto-retry status (retry.go), and both
// paths Clear()-then-add the status container.
//
// Pending queue (reference interactive-mode.ts:640 mount + :3593-3610
// updatePendingMessagesDisplay): dim single-line previews of steering/follow-up
// messages queued while the assistant is busy, rendered in the pendingMessages
// container (between chat and status). Driven by the "queue_update" event, whose
// Steering/FollowUp fields are already plain []string (agent_session_types.go:65).

import (
	"strings"

	"ycode/packages/tui/components"
)

// showStatusLoader shows (or updates) the animated working spinner in the status
// container with the given message.
//
// Uses the animated components.Loader. The Loader was previously unsafe under the
// render lock (NewLoader/SetMessage called RequestRender synchronously), which
// self-deadlocked inside Mutate and froze the TUI. That's now fixed: Start/SetMessage
// use renderText (no RequestRender); the ticker goroutine calls updateDisplay (which
// does request renders) from outside the lock. So creating/updating/stopping the
// Loader inside Mutate is safe.
func (o *Orchestrator) showStatusLoader(msg string) {
	o.engine.Mutate(func() {
		if o.workingLoader == nil {
			o.workingLoader = components.NewLoader(o.engine,
				fgStyle(o.th, "accent"), fgStyle(o.th, "muted"), msg, nil)
			o.status.Clear()
			o.status.AddChild(o.workingLoader)
			return
		}
		o.workingLoader.SetMessage(msg)
	})
}

// hideStatusLoader stops + removes the working spinner. Runs under Mutate.
func (o *Orchestrator) hideStatusLoader() {
	o.engine.Mutate(func() {
		if o.workingLoader == nil {
			return
		}
		o.workingLoader.Stop()
		o.workingLoader = nil
		o.status.Clear()
	})
}

// renderPending rebuilds the pendingMessages container from the latest
// steering/follow-up queues. Runs under Mutate from the event goroutine. Mirrors
// reference updatePendingMessagesDisplay's dim previews (the "edit queued" hint is
// omitted — there is no handler for it in the Go TUI).
func (o *Orchestrator) renderPending(steering, followUp []string) {
	o.engine.Mutate(func() {
		o.pendingMessages.Clear()
		for _, m := range steering {
			o.pendingMessages.AddChild(components.NewText(
				o.th.Fg("dim", "Steering: "+truncateOneLine(m)), 0, 0, nil))
		}
		for _, m := range followUp {
			o.pendingMessages.AddChild(components.NewText(
				o.th.Fg("dim", "Follow-up: "+truncateOneLine(m)), 0, 0, nil))
		}
	})
}

// truncateOneLine collapses a queued message to a single dim preview line.
func truncateOneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:77]) + "..."
	}
	return s
}
