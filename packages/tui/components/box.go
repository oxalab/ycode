package components

import (
	"strings"

	"ycode/packages/tui"
)

// Box is a container that applies padding and an optional background to all children.
type Box struct {
	Children []tui.Component
	paddingX int
	paddingY int
	bgFn     tui.StyleFunc

	cache *boxCache
}

type boxCache struct {
	childLines []string
	width      int
	bgSample   string
	lines      []string
}

// NewBox returns a Box.
func NewBox(paddingX, paddingY int, bgFn tui.StyleFunc) *Box {
	return &Box{paddingX: paddingX, paddingY: paddingY, bgFn: bgFn}
}

// AddChild appends a component (clears cache).
func (b *Box) AddChild(c tui.Component) {
	b.Children = append(b.Children, c)
	b.invalidateCache()
}

// RemoveChild removes the first occurrence of c (clears cache).
func (b *Box) RemoveChild(c tui.Component) {
	for i, ch := range b.Children {
		if ch == c {
			b.Children = append(b.Children[:i], b.Children[i+1:]...)
			break
		}
	}
	b.invalidateCache()
}

// Clear removes all children.
func (b *Box) Clear() {
	b.Children = nil
	b.invalidateCache()
}

// SetBg changes the background styler.
func (b *Box) SetBg(bg tui.StyleFunc) { b.bgFn = bg }

// Invalidate propagates to children and clears the cache.
func (b *Box) Invalidate() {
	b.invalidateCache()
	for _, c := range b.Children {
		if inv, ok := c.(tui.Invalidater); ok {
			inv.Invalidate()
		}
	}
}

func (b *Box) invalidateCache() { b.cache = nil }

func (b *Box) matchCache(width int, childLines []string, bgSample string) bool {
	c := b.cache
	if c == nil || c.width != width || c.bgSample != bgSample || len(c.childLines) != len(childLines) {
		return false
	}
	for i := range childLines {
		if c.childLines[i] != childLines[i] {
			return false
		}
	}
	return true
}

// Render pads and backgrounds the children.
func (b *Box) Render(width int) []string {
	if len(b.Children) == 0 {
		return nil
	}
	contentWidth := width - b.paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}
	leftPad := strings.Repeat(" ", b.paddingX)

	var childLines []string
	for _, child := range b.Children {
		for _, line := range child.Render(contentWidth) {
			childLines = append(childLines, leftPad+line)
		}
	}
	if len(childLines) == 0 {
		return nil
	}

	bgSample := ""
	if b.bgFn != nil {
		bgSample = b.bgFn("test")
	}
	if b.matchCache(width, childLines, bgSample) {
		return b.cache.lines
	}

	var result []string
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}
	for _, line := range childLines {
		result = append(result, b.applyBg(line, width))
	}
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}

	b.cache = &boxCache{childLines: childLines, width: width, bgSample: bgSample, lines: result}
	return result
}

func (b *Box) applyBg(line string, width int) string {
	pad := width - tui.VisibleWidth(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	if b.bgFn != nil {
		return tui.ApplyBackgroundToLine(line, width, b.bgFn)
	}
	return line
}
