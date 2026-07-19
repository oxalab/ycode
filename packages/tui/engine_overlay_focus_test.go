package tui

import "testing"

// focusableOverlay is a Focusable component that records HandleInput + focus state.
type focusableOverlay struct {
	lines   []string
	focused bool
	inputs  []string
}

func (f *focusableOverlay) Render(int) []string  { return f.lines }
func (f *focusableOverlay) Invalidate()          {}
func (f *focusableOverlay) HandleInput(d string) { f.inputs = append(f.inputs, d) }
func (f *focusableOverlay) Focused() bool        { return f.focused }
func (f *focusableOverlay) SetFocused(b bool)    { f.focused = b }

func newFocusEngine() (*TUI, *TestTerminal) {
	tt := NewTestTerminal(80, 24)
	t := NewTUI(tt)
	t.AddChild(emptyContent{})
	return t, tt
}

func TestFocusNonCapturingPreservesOnCreation(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	ov := &focusableOverlay{lines: []string{"OVERLAY"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	tui.ShowOverlay(ov, &OverlayOptions{NonCapturing: true})
	if !editor.focused || ov.focused {
		t.Errorf("non-capturing should preserve editor focus: editor=%v ov=%v", editor.focused, ov.focused)
	}
}

func TestFocusHandleTransfersAndRestores(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	ov := &focusableOverlay{lines: []string{"OVERLAY"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	h := tui.ShowOverlay(ov, &OverlayOptions{NonCapturing: true})
	h.Focus()
	if editor.focused || !ov.focused || !h.IsFocused() {
		t.Errorf("Focus should transfer: editor=%v ov=%v", editor.focused, ov.focused)
	}
	h.Unfocus()
	if !editor.focused || ov.focused || h.IsFocused() {
		t.Errorf("Unfocus should restore editor: editor=%v ov=%v", editor.focused, ov.focused)
	}
}

func TestFocusNonCapturingSetHiddenDoesNotAutoFocus(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	ov := &focusableOverlay{lines: []string{"OVERLAY"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	h := tui.ShowOverlay(ov, &OverlayOptions{NonCapturing: true})
	h.SetHidden(true)
	h.SetHidden(false)
	if !editor.focused || ov.focused {
		t.Errorf("setHidden(false) on non-capturing should not auto-focus: editor=%v ov=%v", editor.focused, ov.focused)
	}
}

func TestFocusHideWhenFocusedRestores(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	ov := &focusableOverlay{lines: []string{"OVERLAY"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	h := tui.ShowOverlay(ov, &OverlayOptions{NonCapturing: true})
	h.Focus()
	h.Hide()
	if !editor.focused || ov.focused {
		t.Errorf("hide when focused should restore editor: editor=%v ov=%v", editor.focused, ov.focused)
	}
}

func TestFocusCapturingRemovedRestoresPastNonCapturing(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	nc := &focusableOverlay{lines: []string{"NC"}}
	cap := &focusableOverlay{lines: []string{"CAP"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	tui.ShowOverlay(nc, &OverlayOptions{NonCapturing: true})
	hCap := tui.ShowOverlay(cap, nil) // capturing → focuses cap
	if !cap.focused {
		t.Fatal("capturing overlay should be focused")
	}
	hCap.Hide()
	if !editor.focused || nc.focused {
		t.Errorf("removing capturing should restore editor (past non-capturing): editor=%v nc=%v", editor.focused, nc.focused)
	}
}

func TestFocusInputRedirectsWhenFocusedOverlayInvisible(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	fallback := &focusableOverlay{lines: []string{"FALLBACK"}}
	nc := &focusableOverlay{lines: []string{"NC"}}
	primary := &focusableOverlay{lines: []string{"PRIMARY"}}
	isVisible := true
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	tui.ShowOverlay(fallback, nil)
	tui.ShowOverlay(nc, &OverlayOptions{NonCapturing: true})
	tui.ShowOverlay(primary, &OverlayOptions{Visible: func(w, h int) bool { return isVisible }})
	if !primary.focused {
		t.Fatal("primary should be focused")
	}
	isVisible = false
	tui.Terminal.(*TestTerminal).SendInput("x")
	if len(primary.inputs) != 0 || len(nc.inputs) != 0 || len(fallback.inputs) != 1 || fallback.inputs[0] != "x" {
		t.Errorf("input should redirect to fallback: primary=%v nc=%v fallback=%v", primary.inputs, nc.inputs, fallback.inputs)
	}
	if !fallback.focused {
		t.Error("fallback should be focused after redirect")
	}
}

func TestFocusSetNullClearsRestore(t *testing.T) {
	tui, _ := newFocusEngine()
	ov := &focusableOverlay{lines: []string{"OVERLAY"}}
	tui.Start()
	defer tui.Stop()

	tui.ShowOverlay(ov, nil) // focuses ov
	tui.SetFocus(nil)
	if ov.focused {
		t.Error("overlay should lose focus after SetFocus(nil)")
	}
	tui.Terminal.(*TestTerminal).SendInput("x")
	if len(ov.inputs) != 0 {
		t.Errorf("no focus target should receive input: ov=%v", ov.inputs)
	}
}

func TestFocusTemporarilyInvisibleRestoresWhenVisible(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	ov := &focusableOverlay{lines: []string{"OVERLAY"}}
	isVisible := true
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	tui.ShowOverlay(ov, &OverlayOptions{Visible: func(w, h int) bool { return isVisible }})
	tui.SetFocus(editor) // focus steal while ov visible
	isVisible = false
	tui.Terminal.(*TestTerminal).SendInput("x") // editor receives (ov invisible)
	if len(editor.inputs) != 1 || editor.inputs[0] != "x" {
		t.Errorf("editor should receive while ov invisible: %v", editor.inputs)
	}
	isVisible = true
	tui.Terminal.(*TestTerminal).SendInput("y") // ov regains focus
	if len(ov.inputs) != 1 || ov.inputs[0] != "y" {
		t.Errorf("ov should regain focus when visible: %v", ov.inputs)
	}
}

func TestFocusHideOverlayNoReassignWhenTopmostNonCapturing(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	cap := &focusableOverlay{lines: []string{"CAP"}}
	nc := &focusableOverlay{lines: []string{"NC"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	tui.ShowOverlay(cap, nil)
	tui.ShowOverlay(nc, &OverlayOptions{NonCapturing: true})
	if !cap.focused {
		t.Fatal("capturing should be focused")
	}
	tui.HideOverlay() // pops nc (topmost, non-capturing)
	if !cap.focused {
		t.Error("hideOverlay of non-capturing topmost should not reassign focus from capturing")
	}
}

func TestFocusMultipleCapturingRestoreThroughRemovals(t *testing.T) {
	tui, _ := newFocusEngine()
	editor := &focusableOverlay{lines: []string{"EDITOR"}}
	c1 := &focusableOverlay{lines: []string{"C1"}}
	c2 := &focusableOverlay{lines: []string{"C2"}}
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	h1 := tui.ShowOverlay(c1, nil)
	h2 := tui.ShowOverlay(c2, nil)
	if !c2.focused {
		t.Fatal("c2 should be focused")
	}
	h2.Hide()
	if !c1.focused {
		t.Errorf("after c2 hide, c1 should be focused: %v", c1.focused)
	}
	h1.Hide()
	if !editor.focused {
		t.Errorf("after c1 hide, editor should be focused: %v", editor.focused)
	}
}
