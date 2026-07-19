package tui

// engine.go ports reference/tui/src/tui.ts `class TUI` — the differential
// renderer, overlay compositor, focus management, IME cursor, Kitty-image
// lifecycle, and OSC queries. See mapping/02-engine.md.
//
// Phase 5 is delivered in slices:
//   P5a (this file): renderer + doRender diff + sync seam + OSC/cell-size
//                    queries + Kitty-image lifecycle. Overlay compositing and the
//                    focus-restore FSM are stubbed (compositeOverlays is a no-op
//                    while the overlay stack is empty; basic SetFocus only).
//   P5b: overlay layout + compositing.
//   P5c: overlay focus-restore FSM.

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	segmentReset        = "\x1b[0m\x1b]8;;\x07"
	minRenderInterval   = 16 * time.Millisecond
	kittySequencePrefix = "\x1b_G"
)

// InputListenerResult lets an input listener consume or rewrite inbound data.
type InputListenerResult struct {
	Consume bool
	Data    string
	HasData bool
}

// InputListener receives raw input before the focused component. Return nil to
// pass through, Consume to swallow, or HasData to rewrite.
type InputListener func(data string) *InputListenerResult

type cursorPosition struct{ row, col int }

// TUI manages components, overlays, focus, and differential rendering.
type TUI struct {
	*Container
	Terminal Terminal

	renderMu sync.Mutex

	previousLines         []string
	previousKittyImageIDs map[int]struct{}
	previousWidth         int
	previousHeight        int
	previousViewportTop   int
	maxLinesRendered      int
	fullRedrawCount       int

	focusedComponent Component
	inputListeners   []InputListener

	OnDebug func()

	renderRequested bool
	renderTimer     *time.Timer
	lastRenderAt    time.Time

	cursorRow          int
	hardwareCursorRow  int
	showHardwareCursor bool
	clearOnShrink      bool
	stopped            bool

	pendingOsc11Replies    int
	pendingOsc11Queries    []*osc11Query
	colorSchemeListeners   []func(TerminalColorScheme)
	colorSchemeNotifications bool

	overlays             []*overlayEntry
	focusOrderCounter    int
	overlayFocusRestore  overlayFocusRestoreState
}

type osc11Query struct {
	ch      chan *RgbColor
	timer   *time.Timer
	settled bool
}

// TUIOption configures a TUI.
type TUIOption func(*TUI)

// WithHardwareCursor enables the visible hardware cursor (IME positioning).
func WithHardwareCursor(show bool) TUIOption {
	return func(t *TUI) { t.showHardwareCursor = show }
}

// NewTUI returns a TUI backed by term.
func NewTUI(term Terminal, opts ...TUIOption) *TUI {
	t := &TUI{
		Terminal:              term,
		previousKittyImageIDs: map[int]struct{}{},
		showHardwareCursor:    osGetenv("PI_HARDWARE_CURSOR") == "1",
		clearOnShrink:         osGetenv("PI_CLEAR_ON_SHRINK") == "1",
	}
	t.Container = &Container{}
	for _, o := range opts {
		o(t)
	}
	return t
}

// FullRedraws reports the number of full-screen redraws performed (for tests).
func (t *TUI) FullRedraws() int { return t.fullRedrawCount }

// SetShowHardwareCursor toggles the hardware cursor.
func (t *TUI) SetShowHardwareCursor(enabled bool) {
	t.renderMu.Lock()
	if t.showHardwareCursor != enabled {
		t.showHardwareCursor = enabled
		if !enabled {
			t.Terminal.HideCursor()
		}
	}
	t.renderMu.Unlock()
	t.RequestRender()
}

// SetClearOnShrink toggles full redraw when content shrinks.
func (t *TUI) SetClearOnShrink(enabled bool) {
	t.renderMu.Lock()
	t.clearOnShrink = enabled
	t.renderMu.Unlock()
}

// SetFocus focuses a component (basic; overlay-restore FSM arrives in P5c).
func (t *TUI) SetFocus(c Component) {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	t.setFocusInternalLocked(c, policyClear)
}

// SetFocusFromInput moves focus to c WITHOUT taking the render lock. Use ONLY
// from within a focused component's HandleInput / OnSubmit / OnSelect / OnAction
// callback — the render lock is already held there (handleInput holds it across
// the dispatch), so calling SetFocus or Mutate would self-deadlock. This is the
// editor-swap primitive: it lets a slash command swap a selector into the editor
// container (and restore the editor when the selector closes) from inside an
// input callback. The engine re-renders after HandleInput returns, so no
// RequestRender is needed.
func (t *TUI) SetFocusFromInput(c Component) {
	t.setFocusInternalLocked(c, policyClear)
}

func (t *TUI) setFocusLocked(c Component) {
	if f, ok := t.focusedComponent.(Focusable); ok {
		f.SetFocused(false)
	}
	t.focusedComponent = c
	if c != nil {
		if f, ok := c.(Focusable); ok {
			f.SetFocused(true)
		}
	}
}

// AddInputListener registers a listener; the returned func removes it.
func (t *TUI) AddInputListener(fn InputListener) (cancel func()) {
	t.renderMu.Lock()
	t.inputListeners = append(t.inputListeners, fn)
	t.renderMu.Unlock()
	return func() {
		t.renderMu.Lock()
		t.removeInputListenerLocked(fn)
		t.renderMu.Unlock()
	}
}

// RemoveInputListener removes a previously-added listener.
func (t *TUI) RemoveInputListener(fn InputListener) {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	t.removeInputListenerLocked(fn)
}

func (t *TUI) removeInputListenerLocked(target InputListener) {
	// Compare by pointer identity of the func value header.
	for i := len(t.inputListeners) - 1; i >= 0; i-- {
		if inputListenerEqual(t.inputListeners[i], target) {
			t.inputListeners = append(t.inputListeners[:i], t.inputListeners[i+1:]...)
			return
		}
	}
}

// inputListenerEqual compares two InputListener func values. Funcs are not
// comparable; we use reflect (called rarely, on add/remove only).
func inputListenerEqual(a, b InputListener) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func funcAddr(f InputListener) uintptr { return reflect.ValueOf(f).Pointer() }

func colorSchemeListenerEqual(a, b func(TerminalColorScheme)) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// InsertChildBeforeLast inserts child immediately before the last child and
// requests a render. It is goroutine-safe, so it can be called from a deferred
// response (e.g. a chat bot reply scheduled via time.AfterFunc) without racing
// the render loop. Do NOT call it from a path that already holds the render lock
// (such as a component's HandleInput / OnSubmit) — the engine re-renders after
// HandleInput returns, and re-locking here would self-deadlock.
func (t *TUI) InsertChildBeforeLast(child Component) {
	t.renderMu.Lock()
	t.Children = insertBeforeLastLocked(t.Children, child)
	t.renderMu.Unlock()
	t.RequestRender()
}

// RemoveChildSafe is the goroutine-safe variant of Container.RemoveChild.
func (t *TUI) RemoveChildSafe(child Component) {
	t.renderMu.Lock()
	for i, c := range t.Children {
		if c == child {
			t.Children = append(t.Children[:i], t.Children[i+1:]...)
			break
		}
	}
	t.renderMu.Unlock()
	t.RequestRender()
}

// Mutate runs fn under the render lock, then requests a re-render. It is the
// sanctioned way for a goroutine (e.g. an AgentSession event listener) to mutate
// ANY part of the component tree — not just the last-child insert/remove covered
// by InsertChildBeforeLast/RemoveChildSafe (e.g. appending to a mid-tree chat
// container, or updating a streaming component in place). Like those helpers, do
// NOT call it from a path that already holds the render lock (HandleInput /
// OnSubmit) — re-locking self-deadlocks.
func (t *TUI) Mutate(fn func()) {
	t.renderMu.Lock()
	fn()
	t.renderMu.Unlock()
	t.RequestRender()
}

// insertBeforeLastLocked inserts child just before the slice's final element.
// Caller holds renderMu.
func insertBeforeLastLocked(children []Component, child Component) []Component {
	n := len(children)
	if n == 0 {
		return append(children, child)
	}
	children = append(children, nil)
	copy(children[n+1:], children[n:])
	children[n] = child
	return children
}

// Start begins rendering.
func (t *TUI) Start() {
	t.renderMu.Lock()
	t.stopped = false
	term := t.Terminal
	colorNotif := t.colorSchemeNotifications
	t.renderMu.Unlock()
	_ = term.Start(t.handleInput, func() { t.RequestRender() })
	term.HideCursor()
	if colorNotif {
		term.Write("\x1b[?2031h")
	}
	t.queryCellSize()
	t.RequestRender()
}

// Stop ends rendering and restores the terminal.
func (t *TUI) Stop() {
	t.renderMu.Lock()
	t.stopped = true
	if t.renderTimer != nil {
		t.renderTimer.Stop()
		t.renderTimer = nil
	}
	if t.colorSchemeNotifications {
		t.Terminal.Write("\x1b[?2031l")
	}
	if len(t.previousLines) > 0 {
		target := len(t.previousLines)
		diff := target - t.hardwareCursorRow
		if diff > 0 {
			t.Terminal.Write("\x1b[" + strconv.Itoa(diff) + "B")
		} else if diff < 0 {
			t.Terminal.Write("\x1b[" + strconv.Itoa(-diff) + "A")
		}
		t.Terminal.Write("\r\n")
	}
	t.Terminal.ShowCursor()
	t.renderMu.Unlock()
	_ = t.Terminal.Stop()
}

// RequestRender asks for a re-render. With force it resets diff state and renders
// synchronously; otherwise it throttles to minRenderInterval.
func (t *TUI) RequestRender(force ...bool) {
	f := len(force) > 0 && force[0]
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	if f {
		t.previousLines = nil
		t.previousWidth = -1
		t.previousHeight = -1
		t.cursorRow = 0
		t.hardwareCursorRow = 0
		t.maxLinesRendered = 0
		t.previousViewportTop = 0
		if t.renderTimer != nil {
			t.renderTimer.Stop()
			t.renderTimer = nil
		}
		t.doRenderLocked()
		return
	}
	if t.renderRequested {
		return
	}
	t.renderRequested = true
	t.scheduleRenderLocked()
}

// RenderSync runs one render synchronously (test seam).
func (t *TUI) RenderSync() {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	if t.renderTimer != nil {
		t.renderTimer.Stop()
		t.renderTimer = nil
	}
	t.renderRequested = false
	t.doRenderLocked()
}

func (t *TUI) scheduleRenderLocked() {
	if t.stopped || t.renderTimer != nil || !t.renderRequested {
		return
	}
	elapsed := time.Since(t.lastRenderAt)
	delay := minRenderInterval - elapsed
	if delay < 0 {
		delay = 0
	}
	t.renderTimer = time.AfterFunc(delay, func() {
		t.renderMu.Lock()
		t.renderTimer = nil
		if t.stopped || !t.renderRequested {
			t.renderMu.Unlock()
			return
		}
		t.renderRequested = false
		t.lastRenderAt = time.Now()
		t.doRenderLocked()
		rerequest := t.renderRequested
		t.renderMu.Unlock()
		if rerequest {
			t.renderMu.Lock()
			t.scheduleRenderLocked()
			t.renderMu.Unlock()
		}
	})
}

func (t *TUI) requestRenderLocked() {
	if t.renderRequested {
		return
	}
	t.renderRequested = true
	t.scheduleRenderLocked()
}

// ---------------------------------------------------------------------------
// Input handling
// ---------------------------------------------------------------------------

func (t *TUI) handleInput(data string) {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()

	if t.consumeOsc11BackgroundResponseLocked(data) {
		return
	}
	if t.consumeTerminalColorSchemeReportLocked(data) {
		return
	}
	if len(t.inputListeners) > 0 {
		current := data
		for _, l := range t.inputListeners {
			r := l(current)
			if r != nil && r.Consume {
				return
			}
			if r != nil && r.HasData {
				current = r.Data
			}
		}
		if len(current) == 0 {
			return
		}
		data = current
	}
	if t.consumeCellSizeResponseLocked(data) {
		return
	}
	if MatchesKey(data, "shift+ctrl+d") && t.OnDebug != nil {
		t.OnDebug()
		return
	}
	t.focusRedirectLocked()
	if t.focusedComponent != nil {
		if h, ok := t.focusedComponent.(InputHandler); ok {
			if IsKeyRelease(data) {
				if kr, ok := t.focusedComponent.(KeyReleaseSubscriber); !ok || !kr.WantsKeyRelease() {
					return
				}
			}
			h.HandleInput(data)
			t.requestRenderLocked()
		}
	}
}

// ---------------------------------------------------------------------------
// Kitty image header parsing
// ---------------------------------------------------------------------------

type kittyImageHeader struct {
	ids  []int
	rows int
}

func parseKittyImageHeader(line string) *kittyImageHeader {
	start := strings.Index(line, kittySequencePrefix)
	if start < 0 {
		return nil
	}
	paramsStart := start + len(kittySequencePrefix)
	end := strings.IndexByte(line[paramsStart:], ';')
	if end < 0 {
		return nil
	}
	params := line[paramsStart : paramsStart+end]
	h := &kittyImageHeader{rows: 1}
	for _, p := range strings.Split(params, ",") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		v, err := strconv.Atoi(kv[1])
		if err != nil || v <= 0 || v > 0xffffffff {
			continue
		}
		switch kv[0] {
		case "i":
			h.ids = append(h.ids, v)
		case "r":
			h.rows = v
		}
	}
	return h
}

func extractKittyImageIDs(line string) []int {
	if h := parseKittyImageHeader(line); h != nil {
		return h.ids
	}
	return nil
}

func extractKittyImageRows(line string) int {
	if h := parseKittyImageHeader(line); h != nil {
		return h.rows
	}
	return 1
}

func (t *TUI) collectKittyImageIDs(lines []string) map[int]struct{} {
	ids := map[int]struct{}{}
	for _, l := range lines {
		for _, id := range extractKittyImageIDs(l) {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func (t *TUI) getKittyImageReservedRows(lines []string, index, maxIndex int) int {
	rows := extractKittyImageRows(lines[index])
	if rows <= 1 {
		return 1
	}
	maxRows := minOf3(rows, maxIndex-index+1, len(lines)-index)
	reserved := 1
	for reserved < maxRows {
		l := lines[index+reserved]
		if IsImageLine(l) || VisibleWidth(l) > 0 {
			break
		}
		reserved++
	}
	return reserved
}

func minOf3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// ---------------------------------------------------------------------------
// doRender — differential rendering
// ---------------------------------------------------------------------------

func (t *TUI) doRenderLocked() {
	if t.stopped {
		return
	}
	width := t.Terminal.Columns()
	height := t.Terminal.Rows()
	widthChanged := t.previousWidth != 0 && t.previousWidth != width
	heightChanged := t.previousHeight != 0 && t.previousHeight != height
	previousBufferLength := height
	if t.previousHeight > 0 {
		previousBufferLength = t.previousViewportTop + t.previousHeight
	}
	prevViewportTop := previousBufferLength - height
	if prevViewportTop < 0 {
		prevViewportTop = 0
	}
	if !heightChanged {
		prevViewportTop = t.previousViewportTop
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := t.hardwareCursorRow

	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	newLines, cursorPos := t.buildLines(width, height)

	fullRender := func(clear bool) {
		t.fullRedrawCount++
		var b strings.Builder
		b.WriteString("\x1b[?2026h")
		if clear {
			for id := range t.previousKittyImageIDs {
				b.WriteString(DeleteKittyImage(id))
			}
			b.WriteString("\x1b[2J\x1b[H\x1b[3J")
		}
		for i := 0; i < len(newLines); i++ {
			if i > 0 {
				b.WriteString("\r\n")
			}
			line := newLines[i]
			if IsImageLine(line) {
				reserved := t.getKittyImageReservedRows(newLines, i, len(newLines)-1)
				if reserved > 1 && reserved <= height {
					for r := 1; r < reserved; r++ {
						b.WriteString("\r\n")
					}
					b.WriteString("\x1b[" + strconv.Itoa(reserved-1) + "A")
					b.WriteString(line)
					b.WriteString("\x1b[" + strconv.Itoa(reserved-1) + "B")
					i += reserved - 1
					continue
				}
			}
			b.WriteString(line)
		}
		b.WriteString("\x1b[?2026l")
		t.Terminal.Write(b.String())
		t.cursorRow = max(0, len(newLines)-1)
		t.hardwareCursorRow = t.cursorRow
		if clear {
			t.maxLinesRendered = len(newLines)
		} else {
			t.maxLinesRendered = max(t.maxLinesRendered, len(newLines))
		}
		bufLen := max(height, len(newLines))
		t.previousViewportTop = max(0, bufLen-height)
		t.positionHardwareCursorLocked(cursorPos, len(newLines))
		t.previousLines = newLines
		t.previousKittyImageIDs = t.collectKittyImageIDs(newLines)
		t.previousWidth = width
		t.previousHeight = height
	}

	if len(t.previousLines) == 0 && !widthChanged && !heightChanged {
		fullRender(false)
		return
	}
	if widthChanged {
		fullRender(true)
		return
	}
	if heightChanged && !isTermuxSession() {
		fullRender(true)
		return
	}
	if t.clearOnShrink && len(newLines) < t.maxLinesRendered && len(t.overlays) == 0 {
		fullRender(true)
		return
	}

	firstChanged, lastChanged := -1, -1
	maxLines := max(len(newLines), len(t.previousLines))
	for i := 0; i < maxLines; i++ {
		var oldLine, newLine string
		if i < len(t.previousLines) {
			oldLine = t.previousLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(t.previousLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(t.previousLines)
		}
		lastChanged = len(newLines) - 1
	}
	if firstChanged != -1 {
		fc, lc := t.expandChangedRangeForKittyImages(firstChanged, lastChanged, newLines)
		firstChanged, lastChanged = fc, lc
	}
	appendStart := appendedLines && firstChanged == len(t.previousLines) && firstChanged > 0

	if firstChanged == -1 {
		t.positionHardwareCursorLocked(cursorPos, len(newLines))
		t.previousViewportTop = prevViewportTop
		t.previousHeight = height
		return
	}

	if firstChanged >= len(newLines) {
		if len(t.previousLines) > len(newLines) {
			var b strings.Builder
			b.WriteString("\x1b[?2026h")
			b.WriteString(t.deleteChangedKittyImages(firstChanged, lastChanged))
			targetRow := max(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				fullRender(true)
				return
			}
			diff := computeLineDiff(targetRow)
			if diff > 0 {
				b.WriteString("\x1b[" + strconv.Itoa(diff) + "B")
			} else if diff < 0 {
				b.WriteString("\x1b[" + strconv.Itoa(-diff) + "A")
			}
			b.WriteString("\r")
			extraLines := len(t.previousLines) - len(newLines)
			if extraLines > height {
				fullRender(true)
				return
			}
			clearStartOffset := 0
			if len(newLines) != 0 {
				clearStartOffset = 1
			}
			if extraLines > 0 && clearStartOffset > 0 {
				b.WriteString("\x1b[" + strconv.Itoa(clearStartOffset) + "B")
			}
			for i := 0; i < extraLines; i++ {
				b.WriteString("\r\x1b[2K")
				if i < extraLines-1 {
					b.WriteString("\x1b[1B")
				}
			}
			moveBack := max(0, extraLines-1+clearStartOffset)
			if moveBack > 0 {
				b.WriteString("\x1b[" + strconv.Itoa(moveBack) + "A")
			}
			b.WriteString("\x1b[?2026l")
			t.Terminal.Write(b.String())
			t.cursorRow = targetRow
			t.hardwareCursorRow = targetRow
		}
		t.positionHardwareCursorLocked(cursorPos, len(newLines))
		t.previousLines = newLines
		t.previousKittyImageIDs = t.collectKittyImageIDs(newLines)
		t.previousWidth = width
		t.previousHeight = height
		t.previousViewportTop = prevViewportTop
		return
	}

	if firstChanged < prevViewportTop {
		fullRender(true)
		return
	}

	var b strings.Builder
	b.WriteString("\x1b[?2026h")
	b.WriteString(t.deleteChangedKittyImages(firstChanged, lastChanged))
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreenRow
		if moveToBottom > 0 {
			b.WriteString("\x1b[" + strconv.Itoa(moveToBottom) + "B")
		}
		scroll := moveTargetRow - prevViewportBottom
		b.WriteString(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	lineDiff := computeLineDiff(moveTargetRow)
	if lineDiff > 0 {
		b.WriteString("\x1b[" + strconv.Itoa(lineDiff) + "B")
	} else if lineDiff < 0 {
		b.WriteString("\x1b[" + strconv.Itoa(-lineDiff) + "A")
	}
	if appendStart {
		b.WriteString("\r\n")
	} else {
		b.WriteString("\r")
	}

	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			b.WriteString("\r\n")
		}
		line := newLines[i]
		if IsImageLine(line) {
			reserved := t.getKittyImageReservedRows(newLines, i, renderEnd)
			if reserved > 1 {
				imageStartScreenRow := i - viewportTop
				if imageStartScreenRow < 0 || imageStartScreenRow+reserved > height {
					fullRender(true)
					return
				}
				b.WriteString("\x1b[2K")
				for r := 1; r < reserved; r++ {
					b.WriteString("\r\n\x1b[2K")
				}
				b.WriteString("\x1b[" + strconv.Itoa(reserved-1) + "A")
				b.WriteString(line)
				b.WriteString("\x1b[" + strconv.Itoa(reserved-1) + "B")
				i += reserved - 1
				continue
			}
		}
		b.WriteString("\x1b[2K")
		b.WriteString(line)
	}

	finalCursorRow := renderEnd
	if len(t.previousLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			b.WriteString("\x1b[" + strconv.Itoa(moveDown) + "B")
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(t.previousLines) - len(newLines)
		for i := len(newLines); i < len(t.previousLines); i++ {
			b.WriteString("\r\n\x1b[2K")
		}
		b.WriteString("\x1b[" + strconv.Itoa(extraLines) + "A")
	}
	b.WriteString("\x1b[?2026l")
	t.Terminal.Write(b.String())

	t.cursorRow = max(0, len(newLines)-1)
	t.hardwareCursorRow = finalCursorRow
	t.maxLinesRendered = max(t.maxLinesRendered, len(newLines))
	t.previousViewportTop = max(prevViewportTop, finalCursorRow-height+1)
	t.positionHardwareCursorLocked(cursorPos, len(newLines))
	t.previousLines = newLines
	t.previousKittyImageIDs = t.collectKittyImageIDs(newLines)
	t.previousWidth = width
	t.previousHeight = height
}

// buildLines renders children + overlays, extracts the IME cursor, and applies
// per-line resets, returning the final frame buffer and the cursor position.
func (t *TUI) buildLines(width, height int) ([]string, *cursorPosition) {
	newLines := t.Render(width)
	if len(t.overlays) > 0 {
		newLines = t.compositeOverlays(newLines, width, height)
	}
	cursor := t.extractCursorPosition(newLines, height)
	newLines = t.applyLineResets(newLines)
	return newLines, cursor
}

// applyLineResets appends a full SGR + OSC8 reset to every non-image line.
func (t *TUI) applyLineResets(lines []string) []string {
	for i, line := range lines {
		if !IsImageLine(line) {
			lines[i] = NormalizeTerminalOutput(line) + segmentReset
		}
	}
	return lines
}

// extractCursorPosition scans the bottom `height` lines for CursorMarker, computes
// its column, and strips the marker from the line (in place).
func (t *TUI) extractCursorPosition(lines []string, height int) *cursorPosition {
	viewportTop := len(lines) - height
	if viewportTop < 0 {
		viewportTop = 0
	}
	for row := len(lines) - 1; row >= viewportTop; row-- {
		idx := strings.Index(lines[row], CursorMarker)
		if idx >= 0 {
			before := lines[row][:idx]
			col := VisibleWidth(before)
			lines[row] = before + lines[row][idx+len(CursorMarker):]
			return &cursorPosition{row: row, col: col}
		}
	}
	return nil
}

func (t *TUI) positionHardwareCursorLocked(pos *cursorPosition, totalLines int) {
	if pos == nil || totalLines <= 0 {
		t.Terminal.HideCursor()
		return
	}
	targetRow := max(0, min(pos.row, totalLines-1))
	targetCol := max(0, pos.col)
	rowDelta := targetRow - t.hardwareCursorRow
	var b strings.Builder
	if rowDelta > 0 {
		b.WriteString("\x1b[" + strconv.Itoa(rowDelta) + "B")
	} else if rowDelta < 0 {
		b.WriteString("\x1b[" + strconv.Itoa(-rowDelta) + "A")
	}
	b.WriteString("\x1b[" + strconv.Itoa(targetCol+1) + "G")
	if b.Len() > 0 {
		t.Terminal.Write(b.String())
	}
	t.hardwareCursorRow = targetRow
	if t.showHardwareCursor {
		t.Terminal.ShowCursor()
	} else {
		t.Terminal.HideCursor()
	}
}

func (t *TUI) expandChangedRangeForKittyImages(firstChanged, lastChanged int, newLines []string) (int, int) {
	ef, el := firstChanged, lastChanged
	expand := func(lines []string) {
		for i := 0; i < len(lines); i++ {
			if len(extractKittyImageIDs(lines[i])) == 0 {
				continue
			}
			blockEnd := i + t.getKittyImageReservedRows(lines, i, len(lines)-1) - 1
			if i >= firstChanged || (i <= lastChanged && blockEnd >= firstChanged) {
				if i < ef {
					ef = i
				}
				if blockEnd > el {
					el = blockEnd
				}
			}
		}
	}
	expand(t.previousLines)
	expand(newLines)
	return ef, el
}

func (t *TUI) deleteChangedKittyImages(firstChanged, lastChanged int) string {
	if firstChanged < 0 || lastChanged < firstChanged {
		return ""
	}
	ids := map[int]struct{}{}
	maxLine := min(lastChanged, len(t.previousLines)-1)
	for i := firstChanged; i <= maxLine; i++ {
		for _, id := range extractKittyImageIDs(t.previousLines[i]) {
			ids[id] = struct{}{}
		}
	}
	var b strings.Builder
	for id := range ids {
		b.WriteString(DeleteKittyImage(id))
	}
	return b.String()
}

func isTermuxSession() bool { return osGetenv("TERMUX_VERSION") != "" }

// ---------------------------------------------------------------------------
// OSC 11 background + color-scheme + cell-size queries
// ---------------------------------------------------------------------------

// QueryTerminalBackgroundColor writes an OSC 11 query and resolves with the reply.
func (t *TUI) QueryTerminalBackgroundColor(timeoutMs int) (RgbColor, bool) {
	q := &osc11Query{ch: make(chan *RgbColor, 1)}
	q.timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		t.renderMu.Lock()
		defer t.renderMu.Unlock()
		if q.settled {
			return
		}
		q.settled = true
		q.ch <- nil
	})
	t.renderMu.Lock()
	t.pendingOsc11Queries = append(t.pendingOsc11Queries, q)
	t.pendingOsc11Replies++
	t.renderMu.Unlock()
	t.Terminal.Write("\x1b]11;?\x07")
	r := <-q.ch
	if r == nil {
		return RgbColor{}, false
	}
	return *r, true
}

func (t *TUI) consumeOsc11BackgroundResponseLocked(data string) bool {
	if t.pendingOsc11Replies <= 0 || !IsOsc11BackgroundColorResponse(data) {
		return false
	}
	rgb, _ := ParseOsc11BackgroundColor(data)
	t.pendingOsc11Replies--
	if len(t.pendingOsc11Queries) > 0 {
		q := t.pendingOsc11Queries[0]
		t.pendingOsc11Queries = t.pendingOsc11Queries[1:]
		if !q.settled {
			q.settled = true
			if q.timer != nil {
				q.timer.Stop()
			}
			r := rgb
			q.ch <- &r
		}
	}
	return true
}

func (t *TUI) consumeTerminalColorSchemeReportLocked(data string) bool {
	scheme, ok := ParseTerminalColorSchemeReport(data)
	if !ok {
		return false
	}
	for _, l := range t.colorSchemeListeners {
		l(scheme)
	}
	return true
}

// OnTerminalColorSchemeChange registers a color-scheme listener.
func (t *TUI) OnTerminalColorSchemeChange(fn func(TerminalColorScheme)) (cancel func()) {
	t.renderMu.Lock()
	t.colorSchemeListeners = append(t.colorSchemeListeners, fn)
	t.renderMu.Unlock()
	return func() {
		t.renderMu.Lock()
		for i := len(t.colorSchemeListeners) - 1; i >= 0; i-- {
			if colorSchemeListenerEqual(t.colorSchemeListeners[i], fn) {
				t.colorSchemeListeners = append(t.colorSchemeListeners[:i], t.colorSchemeListeners[i+1:]...)
				break
			}
		}
		t.renderMu.Unlock()
	}
}

func (t *TUI) SetTerminalColorSchemeNotifications(enabled bool) {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	if t.colorSchemeNotifications == enabled {
		return
	}
	t.colorSchemeNotifications = enabled
	if !t.stopped {
		if enabled {
			t.Terminal.Write("\x1b[?2031h")
		} else {
			t.Terminal.Write("\x1b[?2031l")
		}
	}
}

// QueryTerminalColorScheme writes a CSI ?996n query and resolves with the reply.
func (t *TUI) QueryTerminalColorScheme(timeoutMs int) (TerminalColorScheme, bool) {
	settled := make(chan TerminalColorScheme, 1)
	var once sync.Once
	cancel := t.OnTerminalColorSchemeChange(func(s TerminalColorScheme) {
		once.Do(func() { settled <- s })
	})
	defer cancel()
	timer := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		once.Do(func() { settled <- "" })
	})
	defer timer.Stop()
	t.renderMu.Lock()
	t.Terminal.Write("\x1b[?996n")
	t.renderMu.Unlock()
	s := <-settled
	if s == "" {
		return "", false
	}
	return s, true
}

func (t *TUI) queryCellSize() {
	if GetCapabilities().Images == "" {
		return
	}
	t.Terminal.Write("\x1b[16t")
}

var cellSizeResponseRe = regexp.MustCompile(`^\x1b\[6;(\d+);(\d+)t$`)

func (t *TUI) consumeCellSizeResponseLocked(data string) bool {
	m := cellSizeResponseRe.FindStringSubmatch(data)
	if m == nil {
		return false
	}
	heightPx, _ := strconv.Atoi(m[1])
	widthPx, _ := strconv.Atoi(m[2])
	if heightPx <= 0 || widthPx <= 0 {
		return true
	}
	SetCellDimensions(CellDimensions{WidthPx: widthPx, HeightPx: heightPx})
	t.invalidateLocked()
	t.requestRenderLocked()
	return true
}

func (t *TUI) invalidateLocked() {
	for _, c := range t.Children {
		if inv, ok := c.(Invalidater); ok {
			inv.Invalidate()
		}
	}
}

// ---------------------------------------------------------------------------
// Overlays — stubbed for P5a (empty stack). P5b/P5c fill compositeOverlays +
// ShowOverlay/HideOverlay/HasOverlay + the focus-restore FSM.
// ---------------------------------------------------------------------------

// overlayEntry is one overlay on the stack.
type overlayEntry struct {
	component  Component
	options    *OverlayOptions
	preFocus   Component
	hidden     bool
	focusOrder int
}

// OverlayOptions, OverlayHandle, ShowOverlay/HideOverlay/HasOverlay,
// resolveOverlayLayout, and compositeOverlays/compositeLineAt live in
// engine_overlay.go (P5b). The focus-restore FSM (P5c) refines ShowOverlay/
// OverlayHandle focus behavior.
