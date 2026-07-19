package tui

// UndoStack is a generic undo stack with clone-on-push semantics, matching
// reference undo-stack.ts (which uses structuredClone).
//
// Because Go cannot deep-copy an arbitrary generic value, NewUndoStack takes a
// clone function used on every Push. Pass nil to store values as-is (safe only
// for value types whose contents the caller never mutates, or when the caller
// passes an already-detached copy).
type UndoStack[S any] struct {
	stack []S
	clone func(S) S
}

// NewUndoStack returns a new undo stack. If clone is non-nil it is applied to
// every state pushed so snapshots are detached from later mutation.
func NewUndoStack[S any](clone func(S) S) *UndoStack[S] {
	return &UndoStack[S]{clone: clone}
}

// Push pushes a (cloned, if a clone func was provided) state onto the stack.
func (u *UndoStack[S]) Push(state S) {
	if u.clone != nil {
		state = u.clone(state)
	}
	u.stack = append(u.stack, state)
}

// Pop removes and returns the most recent snapshot.
func (u *UndoStack[S]) Pop() (S, bool) {
	var zero S
	if len(u.stack) == 0 {
		return zero, false
	}
	last := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return last, true
}

// Clear removes all snapshots.
func (u *UndoStack[S]) Clear() { u.stack = u.stack[:0] }

// Len returns the number of snapshots.
func (u *UndoStack[S]) Len() int { return len(u.stack) }
