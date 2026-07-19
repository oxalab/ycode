package components

// Package components holds the built-in TUI widgets, ported from
// reference/tui/src/components/. Each implements the tui.Component interface.

// Spacer renders N empty lines.
type Spacer struct{ n int }

// NewSpacer returns a spacer rendering n empty lines.
func NewSpacer(n int) *Spacer { return &Spacer{n: n} }

// SetLines updates the number of empty lines.
func (s *Spacer) SetLines(n int) { s.n = n }

// Invalidate is a no-op (no cached state).
func (s *Spacer) Invalidate() {}

// Render returns n empty strings.
func (s *Spacer) Render(width int) []string { return make([]string, s.n) }
