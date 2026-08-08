// Package interactive is the pi-tui-based interactive TUI for the ycode
// coding-agent — the re-base of the agent UI onto the faithful pi-tui Go port
// (packages/tui + packages/tui/components), replacing the Bubble-Tea TUI at
// packages/coding-agent/src/core/tui.
//
// It mirrors the reference architecture (reference/coding-agent/src/modes/
// interactive): an imperative Orchestrator owns a *tui.TUI engine + a static
// container tree (header → chat → pendingMessages → status → widgetAbove →
// editorContainer → widgetBelow → footer) and bridges AgentSession events to
// component-tree mutations + RequestRender (no tea.Msg / program.Send).
//
// Status: P0 (foundation + launch seam + theme adapter). Gated behind
// YCODE_TUI=pi; the Bubble-Tea TUI stays the default until feature parity.
//
// See PLAN.md (the phased plan) and mapping/ (source-verified visual anatomy of
// every reference component). See README.md for session-resume context.
package interactive
