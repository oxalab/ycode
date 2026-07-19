package tui

import "testing"

// swapOnInput is a focusable component whose HandleInput performs an editor-swap
// via SetFocusFromInput (the lock-assumed primitive) — mirroring the interactive
// TUI opening a selector from an OnSubmit/OnAction callback.
type swapOnInput struct {
	engine  *TUI
	target  Component
	swapped bool
	focused bool
}

func (s *swapOnInput) Render(int) []string { return []string{"editor"} }
func (s *swapOnInput) Invalidate()         {}
func (s *swapOnInput) HandleInput(data string) {
	s.engine.SetFocusFromInput(s.target) // under the render lock — must not deadlock
	s.swapped = true
}
func (s *swapOnInput) Focused() bool     { return s.focused }
func (s *swapOnInput) SetFocused(f bool) { s.focused = f }

// TestSetFocusFromInputNoDeadlock is the R6 regression: SetFocusFromInput called
// from within HandleInput (which holds renderMu) must transfer focus without
// self-deadlocking, and subsequent input must route to the new focus. If
// SetFocusFromInput re-locked, SendInput would hang and this test would time out.
func TestSetFocusFromInputNoDeadlock(t *testing.T) {
	tui, tt := newEngine(40, 10)
	target := &inputRecorder{}
	editor := &swapOnInput{engine: tui, target: target}
	tui.AddChild(editor)
	tui.SetFocus(editor)
	tui.Start()
	defer tui.Stop()

	// Drives handleInput synchronously → editor.HandleInput → SetFocusFromInput.
	tt.SendInput("x")

	if !editor.swapped {
		t.Fatal("HandleInput did not return — SetFocusFromInput deadlocked under the render lock")
	}
	if tui.focusedComponent != target {
		t.Errorf("focus did not transfer: focusedComponent=%p, want target", tui.focusedComponent)
	}
	// Subsequent input must route to the new focus.
	tt.SendInput("y")
	if len(target.inputs) != 1 || target.inputs[0] != "y" {
		t.Errorf("post-swap input routing = %v, want [y]", target.inputs)
	}
}
