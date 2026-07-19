package tui

import (
	"strings"
	"testing"
)

// linesComponent renders a mutable slice of lines (no width-dependence).
type linesComponent struct{ lines []string }

func (l *linesComponent) Render(width int) []string { return l.lines }
func (l *linesComponent) Invalidate()               {}

// inputRecorder records HandleInput calls.
type inputRecorder struct{ inputs []string }

func (r *inputRecorder) Render(width int) []string { return nil }
func (r *inputRecorder) Invalidate()               {}
func (r *inputRecorder) HandleInput(data string)   { r.inputs = append(r.inputs, data) }

func newEngine(cols, rows int) (*TUI, *TestTerminal) {
	tt := NewTestTerminal(cols, rows)
	return NewTUI(tt), tt
}

func viewportHas(viewport []string, substr string) bool {
	for _, l := range viewport {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func TestEngineFirstRender(t *testing.T) {
	tui, tt := newEngine(40, 10)
	tui.AddChild(&linesComponent{lines: []string{"Line 0", "Line 1", "Line 2"}})
	tui.RenderSync()
	vp := tt.Viewport()
	if !viewportHas(vp, "Line 0") || !viewportHas(vp, "Line 1") || !viewportHas(vp, "Line 2") {
		t.Fatalf("first render viewport=%v", vp)
	}
}

func TestEngineDifferentialMiddleLine(t *testing.T) {
	tui, tt := newEngine(40, 10)
	c := &linesComponent{lines: []string{"Header", "Working...", "Footer"}}
	tui.AddChild(c)
	tui.RenderSync()

	for _, frame := range []string{"|", "/", "-", "\\"} {
		c.lines = []string{"Header", "Working " + frame, "Footer"}
		tui.RenderSync()
		vp := tt.Viewport()
		if !strings.Contains(vp[0], "Header") {
			t.Errorf("header lost: %q", vp[0])
		}
		if !strings.Contains(vp[1], "Working "+frame) {
			t.Errorf("middle not updated: %q", vp[1])
		}
		if !strings.Contains(vp[2], "Footer") {
			t.Errorf("footer lost: %q", vp[2])
		}
	}
}

func TestEngineDifferentialFirstAndLast(t *testing.T) {
	tui, tt := newEngine(40, 10)
	c := &linesComponent{lines: []string{"Line 0", "Line 1", "Line 2", "Line 3"}}
	tui.AddChild(c)
	tui.RenderSync()

	c.lines = []string{"CHANGED", "Line 1", "Line 2", "Line 3"}
	tui.RenderSync()
	vp := tt.Viewport()
	if !strings.Contains(vp[0], "CHANGED") || !strings.Contains(vp[1], "Line 1") {
		t.Errorf("first-line change: %v", vp)
	}

	c.lines = []string{"CHANGED", "Line 1", "Line 2", "CHANGED_END"}
	tui.RenderSync()
	vp = tt.Viewport()
	if !strings.Contains(vp[3], "CHANGED_END") {
		t.Errorf("last-line change: %v", vp)
	}
}

func TestEngineDifferentialNonAdjacent(t *testing.T) {
	tui, tt := newEngine(40, 10)
	c := &linesComponent{lines: []string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4"}}
	tui.AddChild(c)
	tui.RenderSync()

	c.lines = []string{"Line 0", "CHANGED 1", "Line 2", "CHANGED 3", "Line 4"}
	tui.RenderSync()
	vp := tt.Viewport()
	if !strings.Contains(vp[1], "CHANGED 1") || !strings.Contains(vp[3], "CHANGED 3") ||
		!strings.Contains(vp[0], "Line 0") || !strings.Contains(vp[2], "Line 2") || !strings.Contains(vp[4], "Line 4") {
		t.Errorf("non-adjacent: %v", vp)
	}
}

func TestEngineContentEmptyContent(t *testing.T) {
	tui, tt := newEngine(40, 10)
	c := &linesComponent{lines: []string{"Line 0", "Line 1", "Line 2"}}
	tui.AddChild(c)
	tui.RenderSync()

	c.lines = nil
	tui.RenderSync()

	c.lines = []string{"New Line 0", "New Line 1"}
	tui.RenderSync()
	vp := tt.Viewport()
	if !strings.Contains(vp[0], "New Line 0") || !strings.Contains(vp[1], "New Line 1") {
		t.Errorf("after empty restore: %v", vp)
	}
}

func TestEngineShrinkMovesViewportUp(t *testing.T) {
	tui, tt := newEngine(20, 5)
	c := &linesComponent{}
	for i := 0; i < 12; i++ {
		c.lines = append(c.lines, "Line "+itoaEngine(i))
	}
	tui.AddChild(c)
	tui.RenderSync()

	redrawsBefore := tui.FullRedraws()
	c.lines = c.lines[:7]
	tui.RenderSync()

	if tui.FullRedraws() <= redrawsBefore {
		t.Error("shrink should force a full redraw")
	}
	vp := tt.Viewport()
	want := []string{"Line 2", "Line 3", "Line 4", "Line 5", "Line 6"}
	for i := 0; i < 5; i++ {
		if !strings.Contains(vp[i], want[i]) {
			t.Errorf("row %d=%q want %q (vp=%v)", i, vp[i], want[i], vp)
		}
	}
}

func TestEngineClearOnShrink(t *testing.T) {
	tui, tt := newEngine(40, 10)
	tui.SetClearOnShrink(true)
	c := &linesComponent{lines: []string{"Line 0", "Line 1", "Line 2", "Line 3", "Line 4", "Line 5"}}
	tui.AddChild(c)
	tui.RenderSync()

	c.lines = []string{"Line 0", "Line 1"}
	tui.RenderSync()
	vp := tt.Viewport()
	if strings.TrimSpace(vp[2]) != "" || strings.TrimSpace(vp[3]) != "" {
		t.Errorf("shrunk rows not cleared: %v", vp)
	}
	if !strings.Contains(vp[0], "Line 0") || !strings.Contains(vp[1], "Line 1") {
		t.Errorf("content lost: %v", vp)
	}
}

func TestEngineWidthChangeFullRedraw(t *testing.T) {
	tui, tt := newEngine(40, 10)
	c := &linesComponent{lines: []string{"Line 0", "Line 1", "Line 2"}}
	tui.AddChild(c)
	tui.RenderSync()

	redrawsBefore := tui.FullRedraws()
	tt.Resize(60, 10)
	tui.RenderSync()
	if tui.FullRedraws() <= redrawsBefore {
		t.Error("width change should force a full redraw")
	}
}

// --- cell-size response (tui-cell-size-input subset) ---

func TestEngineCellSizeConsumed(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true})
	t.Cleanup(ResetCapabilitiesCache)

	tui, _ := newEngine(80, 24)
	rec := &inputRecorder{}
	tui.AddChild(rec)
	tui.SetFocus(rec)
	tui.Start()
	t.Cleanup(tui.Stop)

	// cell-size response consumed, dimensions set, nothing forwarded.
	tui.Terminal.(*TestTerminal).SendInput("\x1b[6;20;10t")
	if len(rec.inputs) != 0 {
		t.Errorf("cell-size response should be consumed, got %v", rec.inputs)
	}
	if got := GetCellDimensions(); got.WidthPx != 10 || got.HeightPx != 20 {
		t.Errorf("cell dims=%+v want {10 20}", got)
	}

	// later input still forwarded.
	tui.Terminal.(*TestTerminal).SendInput("q")
	if len(rec.inputs) != 1 || rec.inputs[0] != "q" {
		t.Errorf("q should be forwarded, got %v", rec.inputs)
	}
}

func TestEngineForwardsBareEscape(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true})
	t.Cleanup(ResetCapabilitiesCache)

	tui, _ := newEngine(80, 24)
	rec := &inputRecorder{}
	tui.AddChild(rec)
	tui.SetFocus(rec)
	tui.Start()
	t.Cleanup(tui.Stop)

	tui.Terminal.(*TestTerminal).SendInput("\x1b")
	if len(rec.inputs) != 1 || rec.inputs[0] != "\x1b" {
		t.Errorf("bare ESC should be forwarded, got %v", rec.inputs)
	}
}

// itoaEngine avoids importing strconv in the test for line labels.
func itoaEngine(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
