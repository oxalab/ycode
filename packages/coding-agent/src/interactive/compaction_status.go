package interactive

// compaction_status.go wires the compaction status chrome (audit #19): a
// dedicated loader with a reason-specific label, escape-to-cancel, and
// end-state notices (reference interactive-mode.ts:2835-2902).
//
// Concurrency mirrors retry.go: compaction events arrive on the session event
// goroutine (not under the render lock) → Mutate is safe. OnEscape is
// dispatched from the editor's input handling (under the render lock), so the
// abort runs on a goroutine. AbortCompaction cancels the compaction context
// (agent_session_compaction.go:82), whose abort path emits compaction_end.

import (
	"fmt"

	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
)

// handleCompactionStart shows the reason-specific compaction loader and swaps
// escape to abort the compaction.
func (o *Orchestrator) handleCompactionStart(event core.AgentSessionEvent) {
	loader := components.NewLoader(o.engine,
		fgStyle(o.th, "accent"), fgStyle(o.th, "muted"),
		o.compactionStatusText(event.CompactionReason), nil)
	o.engine.Mutate(func() {
		o.status.Clear()
		o.status.AddChild(loader)
		loader.Start() // safe under Mutate (Start requests no render)
	})
	o.compactionLoader = loader

	// Escape cancels the compaction (interactive-mode.ts:2840-2843). The
	// editor stays active — submissions queue during compaction.
	o.compactionEscapeHandler = o.editor.OnEscape
	o.editor.OnEscape = func() { go o.session.AbortCompaction() }
}

// handleCompactionEnd clears the compaction chrome and surfaces the outcome:
// cancelled notice, error line, or the summary card (interactive-mode.ts:2861-2902).
func (o *Orchestrator) handleCompactionEnd(event core.AgentSessionEvent) {
	o.stopCompactionChrome()
	o.engine.Mutate(func() { o.status.Clear() })

	switch {
	case event.CompactionAborted:
		// Manual cancellation is an error-styled notice (TS showError); an
		// auto-compaction cancellation is a status line (TS showStatus).
		if event.CompactionReason == core.CompactionManual {
			o.engine.Mutate(func() { o.addNotice(o.th.Fg("error", "Compaction cancelled")) })
		} else {
			o.engine.Mutate(func() { o.addNotice(o.th.Fg("dim", "Auto-compaction cancelled")) })
		}
	case event.ComapctionError != "":
		msg := event.ComapctionError
		o.engine.Mutate(func() { o.addNotice(o.th.Fg("error", msg)) })
	case event.CompactionResult != nil:
		comp := NewCompactionSummary(event.CompactionResult, o.th, o.mdTheme())
		// Inherit the current global expand state (TS sets
		// component.setExpanded(this.toolOutputExpanded) at creation,
		// interactive-mode.ts:3026), after the leading spacer TS adds.
		comp.SetExpanded(o.headerExpanded)
		o.engine.Mutate(func() {
			o.chat.AddChild(components.NewSpacer(1))
			o.chat.AddChild(comp)
		})
	}
}

// stopCompactionChrome tears down any active compaction loader and restores
// the escape handler. Shared by handleCompactionEnd and Stop().
func (o *Orchestrator) stopCompactionChrome() {
	if o.compactionLoader != nil {
		o.compactionLoader.Stop()
		o.compactionLoader = nil
	}
	o.editor.OnEscape = o.compactionEscapeHandler
	o.compactionEscapeHandler = nil
}

// compactionStatusText renders the reason-specific label in the reference's
// exact wording (interactive-mode.ts:2845-2849).
func (o *Orchestrator) compactionStatusText(reason core.CompactionReason) string {
	cancelHint := fmt.Sprintf("(%s to cancel)", keyText(appInterrupt))
	switch reason {
	case core.CompactionManual:
		return "Compacting context... " + cancelHint
	case core.CompactionOverflow:
		return "Context overflow detected, Auto-compacting... " + cancelHint
	default:
		return "Auto-compacting... " + cancelHint
	}
}
