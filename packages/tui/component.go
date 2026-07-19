package tui

// This file ports the interface layer of reference/tui/src/tui.ts (Component,
// Focusable, CursorMarker, Container). See mapping/01-component-core.md.

// Component is the core renderable unit. Every TUI element implements it.
type Component interface {
	// Render returns one string per line; each line must not exceed width visible
	// columns or the engine will error.
	Render(width int) []string
}

// Invalidater is implemented by components that cache render output. The engine
// calls Invalidate on theme change / forced re-render.
type Invalidater interface {
	Invalidate()
}

// InputHandler is implemented by components that accept keyboard input when focused.
type InputHandler interface {
	HandleInput(data string)
}

// KeyReleaseSubscriber opts a component into receiving Kitty key-release events
// (filtered out by default).
type KeyReleaseSubscriber interface {
	WantsKeyRelease() bool
}

// Focusable components emit CursorMarker when focused so the engine can position
// the hardware cursor for IME candidate windows.
type Focusable interface {
	Focused() bool
	SetFocused(bool)
}

// CursorMarker is a zero-width APC sequence components emit at the cursor
// position when focused. The engine scans for it, strips it, and positions the
// hardware cursor there.
const CursorMarker = "\x1b_pi:c\x07"

// IsFocusable reports whether c implements Focusable.
func IsFocusable(c Component) bool {
	_, ok := c.(Focusable)
	return ok
}

// AsFocusable returns the Focusable view of c if it implements it.
func AsFocusable(c Component) (Focusable, bool) {
	f, ok := c.(Focusable)
	return f, ok
}

// Container groups child components, rendering them top-to-bottom.
type Container struct {
	Children []Component
}

// AddChild appends a component.
func (c *Container) AddChild(child Component) {
	c.Children = append(c.Children, child)
}

// RemoveChild removes the first occurrence of a component.
func (c *Container) RemoveChild(child Component) {
	for i, ch := range c.Children {
		if ch == child {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			return
		}
	}
}

// Clear removes all children.
func (c *Container) Clear() { c.Children = nil }

// Invalidate propagates to children that cache render output.
func (c *Container) Invalidate() {
	for _, child := range c.Children {
		if inv, ok := child.(Invalidater); ok {
			inv.Invalidate()
		}
	}
}

// Render concatenates each child's rendered lines.
func (c *Container) Render(width int) []string {
	var lines []string
	for _, child := range c.Children {
		lines = append(lines, child.Render(width)...)
	}
	return lines
}
