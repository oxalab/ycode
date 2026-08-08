package interactive

// tree.go builds the static container tree that mirrors the reference screen
// layout (reference/coding-agent/src/modes/interactive/interactive-mode.ts
// ~lines 365-381, 576, 639-646). See mapping/01-screen-layout.md.
//
// Root (the *tui.TUI, a Container) children, top → bottom:
//   header → chat → pendingMessages → status → widgetAbove → editorContainer
//          → widgetBelow → footer
//
// P0: the static tree. header (P2) and footer (P4) are real components;
// chat/pending/status/widgets are empty containers populated by the event
// bridge (chat in P3, status/widgets in P7).

import (
	"ycode/packages/tui"
)

func (o *Orchestrator) buildStaticTree() {
	o.header = o.buildHeader()
	o.chat = &tui.Container{}
	o.pendingMessages = &tui.Container{}
	o.status = &tui.Container{}
	o.widgetAbove = &tui.Container{}
	o.editorContainer = &tui.Container{}
	o.widgetBelow = &tui.Container{}

	// Footer (P4) is built in NewOrchestrator (needs the FooterDataProvider);
	// chat/pending/status/widgets are empty containers populated by the event
	// bridge in P3/P7.

	// editorContainer holds exactly one child — the editor (or a swapped-in
	// selector/dialog, P5). Focus anchor of the tree.
	o.editorContainer.AddChild(o.editor)

	// Root children, in screen order.
	o.engine.AddChild(o.header)
	o.engine.AddChild(o.chat)
	o.engine.AddChild(o.pendingMessages)
	o.engine.AddChild(o.status)
	o.engine.AddChild(o.widgetAbove)
	o.engine.AddChild(o.editorContainer)
	o.engine.AddChild(o.widgetBelow)
	o.engine.AddChild(o.footer)
}
