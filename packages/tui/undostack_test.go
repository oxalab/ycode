package tui

import "testing"

func TestUndoStackBasic(t *testing.T) {
	type state struct{ v int }
	u := NewUndoStack(func(s state) state { return s }) // value type: clone is copy
	if _, ok := u.Pop(); ok {
		t.Fatal("empty pop should be false")
	}
	u.Push(state{1})
	u.Push(state{2})
	if u.Len() != 2 {
		t.Fatalf("len=%d", u.Len())
	}
	s, ok := u.Pop()
	if !ok || s.v != 2 {
		t.Fatalf("pop=%+v ok=%v", s, ok)
	}
	s, ok = u.Pop()
	if !ok || s.v != 1 {
		t.Fatalf("pop=%+v ok=%v", s, ok)
	}
	if _, ok := u.Pop(); ok {
		t.Fatal("over-pop should be false")
	}
}

func TestUndoStackCloneOnPush(t *testing.T) {
	// Slice-containing state: clone must detach so later mutation doesn't leak.
	type state struct{ lines []string }
	clone := func(s state) state {
		out := state{lines: make([]string, len(s.lines))}
		copy(out.lines, s.lines)
		return out
	}
	u := NewUndoStack(clone)
	original := state{lines: []string{"a", "b"}}
	u.Push(original)
	original.lines[0] = "MUTATED"
	got, ok := u.Pop()
	if !ok || got.lines[0] != "a" {
		t.Fatalf("snapshot leaked mutation: %+v", got)
	}
}

func TestUndoStackClear(t *testing.T) {
	u := NewUndoStack[int](nil)
	u.Push(1)
	u.Push(2)
	u.Clear()
	if u.Len() != 0 {
		t.Fatalf("len=%d after clear", u.Len())
	}
}
