package tui

// testterminal_test.go provides a TestTerminal for engine tests: it implements
// Terminal, records every Write, and interprets the ANSI stream into a viewport
// grid (a minimal emulator covering the escape subset the engine emits). This is
// the Go analog of reference/tui/test/virtual-terminal.ts (@xterm/headless).

import (
	"strings"
	"sync"
)

// TestTerminal is a Terminal that records output and exposes the resulting
// viewport. It is safe for single-threaded test use (the engine renders under its
// own lock; tests drive it synchronously via TUI.RenderSync).
type TestTerminal struct {
	mu sync.Mutex

	cols int
	rows int

	inputHandler  func(string)
	resizeHandler func()

	out     strings.Builder // raw byte stream
	screen  [][]byte        // viewport cell rows (chars)
	cursorX int
	cursorY int

	writeLog *strings.Builder // optional PI_TUI_WRITE_LOG analog
}

// NewTestTerminal returns a TestTerminal of the given size.
func NewTestTerminal(cols, rows int) *TestTerminal {
	t := &TestTerminal{cols: cols, rows: rows}
	t.resetScreen()
	return t
}

func (t *TestTerminal) resetScreen() {
	t.screen = make([][]byte, t.rows)
	for i := range t.screen {
		t.screen[i] = make([]byte, t.cols)
	}
}

// Terminal interface ---------------------------------------------------------

func (t *TestTerminal) Start(onInput func(string), onResize func()) error {
	t.inputHandler = onInput
	t.resizeHandler = onResize
	return nil
}
func (t *TestTerminal) Stop() error {
	t.inputHandler = nil
	t.resizeHandler = nil
	return nil
}
func (t *TestTerminal) DrainInput(maxMs, idleMs int) error { return nil }

func (t *TestTerminal) Write(data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out.WriteString(data)
	t.interpret(data)
}

func (t *TestTerminal) Columns() int { return t.cols }
func (t *TestTerminal) Rows() int    { return t.rows }
func (t *TestTerminal) KittyProtocolActive() bool { return false }

func (t *TestTerminal) MoveBy(lines int) {
	if lines > 0 {
		t.cursorY += lines
	} else if lines < 0 {
		t.cursorY += lines
	}
}
func (t *TestTerminal) HideCursor()       {}
func (t *TestTerminal) ShowCursor()       {}
func (t *TestTerminal) ClearLine()        {}
func (t *TestTerminal) ClearFromCursor()  {}
func (t *TestTerminal) ClearScreen()      {}
func (t *TestTerminal) SetTitle(string)   {}
func (t *TestTerminal) SetProgress(bool)  {}

// Test helpers --------------------------------------------------------------

func (t *TestTerminal) SendInput(data string) {
	if t.inputHandler != nil {
		t.inputHandler(data)
	}
}
func (t *TestTerminal) SendResize() {
	if t.resizeHandler != nil {
		t.resizeHandler()
	}
}
func (t *TestTerminal) Resize(cols, rows int) {
	t.mu.Lock()
	t.cols = cols
	t.rows = rows
	t.resetScreen()
	t.mu.Unlock()
	t.SendResize()
}

// Writes returns the raw ANSI stream written so far.
func (t *TestTerminal) Writes() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.String()
}

// ClearWrites resets the recorded stream (keep the viewport).
func (t *TestTerminal) ClearWrites() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out.Reset()
}

// Viewport returns the current screen content, one string per row, each padded to
// full width with trailing spaces stripped per row (rows retain their width).
func (t *TestTerminal) Viewport() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, t.rows)
	for i := 0; i < t.rows; i++ {
		out[i] = strings.TrimRight(string(t.screen[i]), "\x00")
	}
	return out
}

// minimal ANSI interpreter covering the engine's escape subset.
func (t *TestTerminal) interpret(data string) {
	i := 0
	for i < len(data) {
		b := data[i]
		if b == '\x1b' {
			// escape: parse CSI/OSC/APC
			if i+1 < len(data) && data[i+1] == '[' {
				// CSI
				j := i + 2
				for j < len(data) && !isCsiFinalByte(data[j]) {
					j++
				}
				if j < len(data) {
					final := data[j]
					params := data[i+2 : j]
					t.applyCSI(params, final)
					i = j + 1
					continue
				}
				i++ // incomplete
				continue
			}
			if i+1 < len(data) && (data[i+1] == ']' || data[i+1] == '_' || data[i+1] == 'P') {
				// OSC/APC/DCS: skip to ST (ESC \) or BEL
				opener := data[i+1]
				j := i + 2
				for j < len(data) {
					if data[j] == '\x07' {
						j++
						break
					}
					if data[j] == '\x1b' && j+1 < len(data) && data[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				_ = opener
				i = j
				continue
			}
			// other ESC + char (e.g. ESC \ standalone) — skip 2
			i += 2
			continue
		}
		switch b {
		case '\r':
			t.cursorX = 0
			i++
		case '\n':
			if t.cursorY >= t.rows-1 {
				// scroll up at the bottom row (matches a real terminal)
				copy(t.screen, t.screen[1:])
				t.screen[t.rows-1] = make([]byte, t.cols)
			} else {
				t.cursorY++
			}
			i++
		default:
			t.putByte(b)
			i++
		}
	}
}

func isCsiFinalByte(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

func (t *TestTerminal) applyCSI(params string, final byte) {
	switch final {
	case 'H':
		// home / absolute position
		t.cursorX = 0
		t.cursorY = 0
	case 'A':
		t.cursorY -= csiParamInt(params, 1)
	case 'B':
		t.cursorY += csiParamInt(params, 1)
	case 'C':
		t.cursorX += csiParamInt(params, 1)
	case 'D':
		t.cursorX -= csiParamInt(params, 1)
	case 'G':
		t.cursorX = csiParamInt(params, 1) - 1
	case 'K':
		t.clearLineRight()
	case 'J':
		t.clearScreenFrom()
	case 'm':
		// SGR — ignored for text content
	}
	t.cursorX = clampi(t.cursorX, 0, t.cols-1)
	t.cursorY = clampi(t.cursorY, 0, t.rows-1)
}

func (t *TestTerminal) putByte(b byte) {
	if t.cursorY >= 0 && t.cursorY < t.rows && t.cursorX >= 0 && t.cursorX < t.cols {
		t.screen[t.cursorY][t.cursorX] = b
	}
	t.cursorX++
	if t.cursorX >= t.cols {
		t.cursorX = t.cols - 1 // emulate no auto-wrap (engine controls wrapping)
	}
}

func (t *TestTerminal) clearLineRight() {
	if t.cursorY >= 0 && t.cursorY < t.rows {
		for x := t.cursorX; x < t.cols; x++ {
			t.screen[t.cursorY][x] = 0
		}
	}
}

func (t *TestTerminal) clearScreenFrom() {
	for y := range t.screen {
		for x := range t.screen[y] {
			t.screen[y][x] = 0
		}
	}
	t.cursorX = 0
	t.cursorY = 0
}

func csiParamInt(params string, def int) int {
	// params like "" (def), "2", "1;5" (take first)
	parts := strings.SplitN(params, ";", 2)
	p := strings.TrimPrefix(strings.TrimPrefix(parts[0], "?"), "")
	if p == "" {
		return def
	}
	n := 0
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return def
		}
		n = n*10 + int(p[i]-'0')
	}
	return n
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
