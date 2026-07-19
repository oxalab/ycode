package tui

import (
	"strings"
	"testing"
)

// staticOverlay renders fixed lines and records the width it was rendered at.
type staticOverlay struct {
	lines          []string
	requestedWidth int
}

func (s *staticOverlay) Render(width int) []string {
	s.requestedWidth = width
	return s.lines
}
func (s *staticOverlay) Invalidate() {}

type emptyContent struct{}

func (emptyContent) Render(int) []string { return nil }
func (emptyContent) Invalidate()         {}

func newOverlayEngine(cols, rows int) (*TUI, *TestTerminal) {
	tt := NewTestTerminal(cols, rows)
	t := NewTUI(tt)
	t.AddChild(emptyContent{})
	return t, tt
}

func TestOverlayWidthPercent(t *testing.T) {
	tui, _ := newOverlayEngine(100, 24)
	ov := &staticOverlay{lines: []string{"test"}}
	tui.ShowOverlay(ov, &OverlayOptions{Width: sizePtr(PctSize(50))})
	tui.RenderSync()
	if ov.requestedWidth != 50 {
		t.Errorf("requestedWidth=%d want 50", ov.requestedWidth)
	}
}

func TestOverlayMinWidthFloor(t *testing.T) {
	tui, _ := newOverlayEngine(100, 24)
	ov := &staticOverlay{lines: []string{"test"}}
	tui.ShowOverlay(ov, &OverlayOptions{Width: sizePtr(PctSize(10)), MinWidth: intPtr2(30)})
	tui.RenderSync()
	if ov.requestedWidth != 30 {
		t.Errorf("requestedWidth=%d want 30", ov.requestedWidth)
	}
}

func TestOverlayAnchorTopLeft(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"TOP-LEFT"}}, &OverlayOptions{Anchor: AnchorTopLeft, Width: sizePtr(AbsSize(10))})
	tui.RenderSync()
	if !strings.HasPrefix(tt.Viewport()[0], "TOP-LEFT") {
		t.Errorf("top-left: %q", tt.Viewport()[0])
	}
}

func TestOverlayAnchorBottomRight(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"BTM-RIGHT"}}, &OverlayOptions{Anchor: AnchorBottomRight, Width: sizePtr(AbsSize(10))})
	tui.RenderSync()
	last := tt.Viewport()[23]
	if !strings.Contains(last, "BTM-RIGHT") || !strings.HasSuffix(strings.TrimRight(last, " "), "BTM-RIGHT") {
		t.Errorf("bottom-right: %q", last)
	}
}

func TestOverlayMarginNumber(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"MARGIN"}}, &OverlayOptions{Anchor: AnchorTopLeft, Width: sizePtr(AbsSize(10)), Margin: 5})
	tui.RenderSync()
	vp := tt.Viewport()
	if strings.Contains(vp[0], "MARGIN") || strings.Contains(vp[4], "MARGIN") {
		t.Errorf("margin top wrong: %q / %q", vp[0], vp[4])
	}
	row := vp[5]
	idx := strings.Index(row, "MARGIN")
	if idx != 5 {
		t.Errorf("margin col=%d want 5 (row=%q)", idx, row)
	}
}

func TestOverlayOffset(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"OFFSET"}}, &OverlayOptions{Anchor: AnchorTopLeft, Width: sizePtr(AbsSize(10)), OffsetX: intPtr2(10), OffsetY: intPtr2(5)})
	tui.RenderSync()
	vp := tt.Viewport()
	idx := strings.Index(vp[5], "OFFSET")
	if idx != 10 {
		t.Errorf("offset col=%d want 10 (row=%q)", idx, vp[5])
	}
}

func TestOverlayMaxHeightTruncates(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"Line 1", "Line 2", "Line 3", "Line 4", "Line 5"}}, &OverlayOptions{MaxHeight: sizePtr(AbsSize(3))})
	tui.RenderSync()
	content := strings.Join(tt.Viewport(), "\n")
	if !strings.Contains(content, "Line 1") || !strings.Contains(content, "Line 3") {
		t.Errorf("maxHeight kept wrong lines: %q", content)
	}
	if strings.Contains(content, "Line 4") || strings.Contains(content, "Line 5") {
		t.Errorf("maxHeight should truncate Line 4/5: %q", content)
	}
}

func TestOverlayAbsoluteOverridesAnchor(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"ABSOLUTE"}}, &OverlayOptions{Anchor: AnchorBottomRight, Row: sizePtr(AbsSize(3)), Col: sizePtr(AbsSize(5)), Width: sizePtr(AbsSize(10))})
	tui.RenderSync()
	vp := tt.Viewport()
	idx := strings.Index(vp[3], "ABSOLUTE")
	if idx != 5 {
		t.Errorf("absolute: row3 col=%d want 5 (row=%q)", idx, vp[3])
	}
}

func TestOverlayStackedLaterOnTop(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.ShowOverlay(&staticOverlay{lines: []string{"FIRST"}}, &OverlayOptions{Anchor: AnchorTopLeft, Width: sizePtr(AbsSize(20))})
	tui.ShowOverlay(&staticOverlay{lines: []string{"SECOND"}}, &OverlayOptions{Anchor: AnchorTopLeft, Width: sizePtr(AbsSize(10))})
	tui.RenderSync()
	if !strings.HasPrefix(tt.Viewport()[0], "SECOND") {
		t.Errorf("later overlay should be on top: %q", tt.Viewport()[0])
	}
}

func TestOverlayShortContent(t *testing.T) {
	tui, tt := newOverlayEngine(80, 24)
	tui.AddChild(&linesComponent{lines: []string{"Line 1", "Line 2", "Line 3"}})
	tui.ShowOverlay(&staticOverlay{lines: []string{"OV_TOP", "OV_MID", "OV_BOT"}}, nil)
	tui.RenderSync()
	vp := tt.Viewport()
	if !containsLine(vp, "OV_TOP") {
		t.Errorf("short-content overlay not visible: %v", vp)
	}
}

func TestOverlayHideAndHasOverlay(t *testing.T) {
	tui, _ := newOverlayEngine(80, 24)
	h := tui.ShowOverlay(&staticOverlay{lines: []string{"X"}}, nil)
	tui.RenderSync()
	if !tui.HasOverlay() {
		t.Error("HasOverlay should be true after show")
	}
	h.Hide()
	tui.RenderSync()
	if tui.HasOverlay() {
		t.Error("HasOverlay should be false after hide")
	}
}

// --- compositeLineAt CJK boundary (regression-overlay-cjk-boundary) ---

func TestExtractSegmentsCJKBoundary(t *testing.T) {
	seg := ExtractSegments("abcd让EFGH", 5, 9, 11, true)
	if seg.Before != "abcd" || seg.BeforeWidth != 4 {
		t.Errorf("before=%q width=%d", seg.Before, seg.BeforeWidth)
	}
	if seg.After != "H" || seg.AfterWidth != 1 {
		t.Errorf("after=%q width=%d", seg.After, seg.AfterWidth)
	}
	// ASCII control at the same boundary keeps the 5th column.
	seg2 := ExtractSegments("abcdG EFGH", 5, 9, 11, true)
	if seg2.Before != "abcdG" || seg2.BeforeWidth != 5 {
		t.Errorf("ascii before=%q width=%d", seg2.Before, seg2.BeforeWidth)
	}
}

func TestCompositeLineAtCJKBoundary(t *testing.T) {
	tui, _ := newOverlayEngine(20, 10)
	out := tui.compositeLineAt("abcd让EFGH", "│XX│", 5, 4, 20)
	if strings.Contains(out, "让") {
		t.Errorf("wide char 让 should be excluded: %q", out)
	}
	if VisibleWidth(out) != 20 {
		t.Errorf("width=%d want 20", VisibleWidth(out))
	}
	overlay := SliceByColumn(out, 5, 4, true)
	if !strings.Contains(overlay, "│XX│") {
		t.Errorf("overlay not placed: %q (from %q)", overlay, out)
	}
}

// --- helpers ---

func sizePtr(s SizeValue) *SizeValue { return &s }
func intPtr2(n int) *int             { return &n }

func containsLine(viewport []string, substr string) bool {
	for _, l := range viewport {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
