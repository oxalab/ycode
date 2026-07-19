package tui

import (
	"reflect"
	"testing"
)

// stubComponent is a minimal Component for testing Container.
type stubComponent struct {
	lines []string
}

func (s *stubComponent) Render(width int) []string { return s.lines }
func (s *stubComponent) String() string             { return "stub" }

// focusableStub implements Focusable.
type focusableStub struct {
	stubComponent
	focused bool
}

func (f *focusableStub) Focused() bool    { return f.focused }
func (f *focusableStub) SetFocused(v bool) { f.focused = v }

func TestContainerRender(t *testing.T) {
	c := &Container{}
	c.AddChild(&stubComponent{lines: []string{"a", "b"}})
	c.AddChild(&stubComponent{lines: []string{"c"}})
	got := c.Render(10)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v", got)
	}
}

func TestContainerRemoveAndClear(t *testing.T) {
	a := &stubComponent{lines: []string{"a"}}
	b := &stubComponent{lines: []string{"b"}}
	c := &Container{}
	c.AddChild(a)
	c.AddChild(b)
	c.RemoveChild(a)
	if len(c.Children) != 1 || c.Children[0] != b {
		t.Errorf("after remove: %v", c.Children)
	}
	c.Clear()
	if len(c.Children) != 0 {
		t.Errorf("after clear: %v", c.Children)
	}
}

func TestIsFocusable(t *testing.T) {
	var comp Component = &stubComponent{lines: nil}
	if IsFocusable(comp) {
		t.Error("stub should not be focusable")
	}
	f := &focusableStub{}
	var fcomp Component = f
	if !IsFocusable(fcomp) {
		t.Error("focusableStub should be focusable")
	}
	if _, ok := AsFocusable(fcomp); !ok {
		t.Error("AsFocusable failed")
	}
	f.SetFocused(true)
	if !f.Focused() {
		t.Error("SetFocused/Focused roundtrip")
	}
}
