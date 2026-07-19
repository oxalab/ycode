package components

import (
	"strings"
	"sync"

	"ycode/packages/tui"
)

// Text displays multi-line text with word wrapping, padding, and an optional
// background. Render output is cached by (text, width). A mutex guards the cache
// so a Loader's ticker goroutine can SetText concurrently with the engine's Render.
type Text struct {
	mu       sync.Mutex
	text     string
	paddingX int
	paddingY int
	bgFn     tui.StyleFunc

	cachedText  string
	cachedWidth int
	cachedLines []string
}

// NewText returns a Text component.
func NewText(text string, paddingX, paddingY int, bgFn tui.StyleFunc) *Text {
	return &Text{text: text, paddingX: paddingX, paddingY: paddingY, bgFn: bgFn}
}

// SetText updates the text (clears cache).
func (t *Text) SetText(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.text = text
	t.invalidateCache()
}

// SetCustomBg changes the background styler (clears cache).
func (t *Text) SetCustomBg(bg tui.StyleFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bgFn = bg
	t.invalidateCache()
}

// Invalidate clears the render cache.
func (t *Text) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.invalidateCache()
}

func (t *Text) invalidateCache() {
	t.cachedText = ""
	t.cachedWidth = 0
	t.cachedLines = nil
}

// Render wraps and pads the text.
func (t *Text) Render(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cachedLines != nil && t.cachedText == t.text && t.cachedWidth == width {
		return t.cachedLines
	}

	result := []string{}
	if strings.TrimSpace(t.text) == "" {
		t.cachedText = t.text
		t.cachedWidth = width
		t.cachedLines = result
		return result
	}

	normalized := strings.ReplaceAll(t.text, "\t", "   ")
	contentWidth := width - t.paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}
	wrapped := tui.WrapTextWithAnsi(normalized, contentWidth)

	left := strings.Repeat(" ", t.paddingX)
	right := strings.Repeat(" ", t.paddingX)
	var content []string
	for _, line := range wrapped {
		lineWithMargins := left + line + right
		if t.bgFn != nil {
			content = append(content, tui.ApplyBackgroundToLine(lineWithMargins, width, t.bgFn))
		} else {
			pad := width - tui.VisibleWidth(lineWithMargins)
			if pad > 0 {
				lineWithMargins += strings.Repeat(" ", pad)
			}
			content = append(content, lineWithMargins)
		}
	}

	empty := strings.Repeat(" ", width)
	emptyLines := []string{}
	for i := 0; i < t.paddingY; i++ {
		if t.bgFn != nil {
			emptyLines = append(emptyLines, tui.ApplyBackgroundToLine(empty, width, t.bgFn))
		} else {
			emptyLines = append(emptyLines, empty)
		}
	}
	result = append(result, emptyLines...)
	result = append(result, content...)
	result = append(result, emptyLines...)

	if len(result) == 0 {
		result = []string{""}
	}
	t.cachedText = t.text
	t.cachedWidth = width
	t.cachedLines = result
	return result
}
