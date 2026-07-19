package tui

// engine_overlay.go ports the overlay system of reference/tui/src/tui.ts:
// OverlayOptions, layout resolution, compositing, and ShowOverlay/HideOverlay/
// HasOverlay + OverlayHandle. See mapping/02-engine.md.
//
// P5b (this file): layout + compositing + basic show/hide focus capture.
// P5c will replace the basic focus handling with the full focus-restore FSM
// (OverlayFocusRestoreState, nonCapturing semantics) without changing layout.

import (
	"math"
	"sort"
	"strings"
)

// OverlayAnchor names a positioning anchor for an overlay.
type OverlayAnchor string

const (
	AnchorCenter      OverlayAnchor = "center"
	AnchorTopLeft     OverlayAnchor = "top-left"
	AnchorTopRight    OverlayAnchor = "top-right"
	AnchorBottomLeft  OverlayAnchor = "bottom-left"
	AnchorBottomRight OverlayAnchor = "bottom-right"
	AnchorTopCenter   OverlayAnchor = "top-center"
	AnchorBottomCenter OverlayAnchor = "bottom-center"
	AnchorLeftCenter  OverlayAnchor = "left-center"
	AnchorRightCenter OverlayAnchor = "right-center"
)

// OverlayMargin is per-edge margin from the terminal edges.
type OverlayMargin struct{ Top, Right, Bottom, Left int }

// SizeValue is an absolute cell count or a percentage of a reference dimension.
type SizeValue struct {
	abs     int
	percent float64
	isPct   bool
}

// AbsSize returns an absolute SizeValue (cells).
func AbsSize(n int) SizeValue { return SizeValue{abs: n} }

// PctSize returns a percentage SizeValue (e.g. PctSize(50) = 50%).
func PctSize(p float64) SizeValue { return SizeValue{percent: p, isPct: true} }

// OverlayOptions configures overlay sizing and positioning.
type OverlayOptions struct {
	Width    *SizeValue
	MinWidth *int
	MaxHeight *SizeValue

	Anchor   OverlayAnchor
	OffsetX  *int
	OffsetY  *int

	Row *SizeValue
	Col *SizeValue

	Margin any // int (all sides) or OverlayMargin

	Visible      func(termW, termH int) bool
	NonCapturing bool
}

// OverlayUnfocusOptions controls OverlayHandle.Unfocus.
type OverlayUnfocusOptions struct{ Target Component }

// OverlayHandle controls an overlay after it is shown.
type OverlayHandle struct {
	t         *TUI
	entry     *overlayEntry
	component Component
}

// Hide permanently removes the overlay.
func (h *OverlayHandle) Hide() {
	if h == nil {
		return
	}
	h.t.removeOverlayLocked(h.entry, true)
}

// SetHidden temporarily hides (true) or shows (false) the overlay.
func (h *OverlayHandle) SetHidden(hidden bool) {
	if h == nil {
		return
	}
	h.t.renderMu.Lock()
	defer h.t.renderMu.Unlock()
	if h.entry.hidden == hidden {
		return
	}
	h.entry.hidden = hidden
	if hidden {
		h.t.clearOverlayFocusRestoreForLocked(h.entry)
		if h.t.focusedComponent == h.component {
			top := h.t.getTopmostVisibleOverlayLocked()
			target := h.entry.preFocus
			if top != nil {
				target = top.component
			}
			h.t.setFocusInternalLocked(target, policyClear)
		}
	} else {
		if h.entry.options == nil || !h.entry.options.NonCapturing {
			if h.t.isOverlayVisibleLocked(h.entry) {
				h.entry.focusOrder = h.t.nextFocusOrder()
				h.t.setFocusInternalLocked(h.component, policyClear)
			}
		}
	}
	h.t.requestRenderLocked()
}

// IsHidden reports whether the overlay is temporarily hidden.
func (h *OverlayHandle) IsHidden() bool {
	if h == nil {
		return false
	}
	h.t.renderMu.Lock()
	defer h.t.renderMu.Unlock()
	return h.entry.hidden
}

// Focus brings the overlay to the visual front and captures focus.
func (h *OverlayHandle) Focus() {
	if h == nil {
		return
	}
	h.t.renderMu.Lock()
	defer h.t.renderMu.Unlock()
	if !containsOverlay(h.t.overlays, h.entry) || !h.t.isOverlayVisibleLocked(h.entry) {
		return
	}
	h.entry.focusOrder = h.t.nextFocusOrder()
	h.t.setFocusInternalLocked(h.component, policyClear)
	h.t.requestRenderLocked()
}

// Unfocus releases focus, honoring any pending blocked-restore. Ports the
// reference OverlayHandle.unfocus FSM.
func (h *OverlayHandle) Unfocus(opts ...*OverlayUnfocusOptions) {
	if h == nil {
		return
	}
	h.t.renderMu.Lock()
	defer h.t.renderMu.Unlock()
	isFocused := h.t.focusedComponent == h.component
	restoreState := h.t.overlayFocusRestore
	hasPendingRestore := restoreState.status != restoreInactive && restoreState.overlay == h.entry
	if !isFocused && !hasPendingRestore {
		return
	}
	hasOpt := len(opts) > 0 && opts[0] != nil
	if restoreState.status == restoreBlocked && restoreState.overlay == h.entry && h.t.focusedComponent == restoreState.blockedBy {
		if hasOpt {
			h.t.overlayFocusRestore = overlayFocusRestoreState{
				status:    restoreBlocked,
				overlay:   h.entry,
				blockedBy: restoreState.blockedBy,
				resume:    overlayResume{status: resumeFocusTarget, target: opts[0].Target},
			}
		} else {
			h.t.clearOverlayFocusRestoreLocked()
		}
		h.t.requestRenderLocked()
		return
	}
	h.t.clearOverlayFocusRestoreForLocked(h.entry)
	if isFocused || hasOpt {
		top := h.t.getTopmostVisibleOverlayLocked()
		fallback := h.entry.preFocus
		if top != nil && top != h.entry {
			fallback = top.component
		}
		target := fallback
		if hasOpt {
			target = opts[0].Target
		}
		h.t.setFocusInternalLocked(target, policyClear)
	}
	h.t.requestRenderLocked()
}

// IsFocused reports whether the overlay currently has focus.
func (h *OverlayHandle) IsFocused() bool {
	if h == nil {
		return false
	}
	h.t.renderMu.Lock()
	defer h.t.renderMu.Unlock()
	return h.t.focusedComponent == h.component
}

func (t *TUI) nextFocusOrder() int {
	t.focusOrderCounter++
	return t.focusOrderCounter
}

// ShowOverlay displays an overlay with optional positioning. The overlay captures
// focus unless options.NonCapturing is true (and the full FSM of P5c refines the
// restore behavior).
func (t *TUI) ShowOverlay(c Component, opts *OverlayOptions) *OverlayHandle {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	entry := &overlayEntry{
		component:  c,
		options:    opts,
		preFocus:   t.focusedComponent,
		focusOrder: t.nextFocusOrder(),
	}
	t.overlays = append(t.overlays, entry)
	if opts == nil || !opts.NonCapturing {
		if t.isOverlayVisibleLocked(entry) {
			t.setFocusInternalLocked(c, policyClear)
		}
	}
	t.Terminal.HideCursor()
	t.requestRenderLocked()
	return &OverlayHandle{t: t, entry: entry, component: c}
}

// HideOverlay removes the topmost overlay and restores focus.
func (t *TUI) HideOverlay() {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	if len(t.overlays) == 0 {
		return
	}
	t.removeOverlayLocked(t.overlays[len(t.overlays)-1], true)
}

// HasOverlay reports whether any visible overlay is active.
func (t *TUI) HasOverlay() bool {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	for _, e := range t.overlays {
		if t.isOverlayVisibleLocked(e) {
			return true
		}
	}
	return false
}

// removeOverlayLocked removes an entry and restores focus. With retargetPreFocus
// true it also relinks any overlay whose preFocus pointed at the removed entry.
func (t *TUI) removeOverlayLocked(entry *overlayEntry, retargetPreFocus bool) {
	idx := -1
	for i, e := range t.overlays {
		if e == entry {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	t.clearOverlayFocusRestoreForLocked(entry)
	if retargetPreFocus {
		t.retargetOverlayPreFocusLocked(entry)
	}
	t.overlays = append(t.overlays[:idx], t.overlays[idx+1:]...)
	if t.focusedComponent == entry.component {
		top := t.getTopmostVisibleOverlayLocked()
		if top != nil {
			t.setFocusInternalLocked(top.component, policyClear)
		} else {
			t.setFocusInternalLocked(entry.preFocus, policyClear)
		}
	}
	if len(t.overlays) == 0 {
		t.Terminal.HideCursor()
	}
	t.requestRenderLocked()
}

func containsOverlay(stack []*overlayEntry, entry *overlayEntry) bool {
	for _, e := range stack {
		if e == entry {
			return true
		}
	}
	return false
}

func (t *TUI) isOverlayVisibleLocked(entry *overlayEntry) bool {
	if entry.hidden {
		return false
	}
	if entry.options != nil && entry.options.Visible != nil {
		return entry.options.Visible(t.Terminal.Columns(), t.Terminal.Rows())
	}
	return true
}

// getTopmostVisibleOverlayLocked returns the visual-frontmost visible capturing
// overlay (highest focusOrder), or nil.
func (t *TUI) getTopmostVisibleOverlayLocked() *overlayEntry {
	var topmost *overlayEntry
	for _, o := range t.overlays {
		if o.options != nil && o.options.NonCapturing {
			continue
		}
		if !t.isOverlayVisibleLocked(o) {
			continue
		}
		if topmost == nil || o.focusOrder > topmost.focusOrder {
			topmost = o
		}
	}
	return topmost
}

// ---------------------------------------------------------------------------
// Layout resolution
// ---------------------------------------------------------------------------

type overlayLayout struct {
	width    int
	row      int
	col      int
	maxHeight *int
}

func (t *TUI) resolveOverlayLayout(opts *OverlayOptions, overlayHeight, termWidth, termHeight int) overlayLayout {
	o := opts
	if o == nil {
		o = &OverlayOptions{}
	}

	// Margin (clamp to non-negative).
	margin := OverlayMargin{}
	switch m := o.Margin.(type) {
	case int:
		margin = OverlayMargin{Top: m, Right: m, Bottom: m, Left: m}
	case OverlayMargin:
		margin = m
	}
	marginTop := max(0, margin.Top)
	marginRight := max(0, margin.Right)
	marginBottom := max(0, margin.Bottom)
	marginLeft := max(0, margin.Left)

	availWidth := max(1, termWidth-marginLeft-marginRight)
	availHeight := max(1, termHeight-marginBottom-marginTop)

	// Width.
	width, widthOK := parseSizeValue(o.Width, termWidth)
	if !widthOK {
		width = min(80, availWidth)
	}
	if o.MinWidth != nil {
		width = max(width, *o.MinWidth)
	}
	width = max(1, min(width, availWidth))

	// maxHeight.
	var maxHeight *int
	if mh, ok := parseSizeValue(o.MaxHeight, termHeight); ok {
		mh = max(1, min(mh, availHeight))
		maxHeight = &mh
	}

	effectiveHeight := overlayHeight
	if maxHeight != nil {
		effectiveHeight = min(overlayHeight, *maxHeight)
	}

	// Row.
	var row int
	switch {
	case o.Row != nil:
		if o.Row.isPct {
			maxRow := max(0, availHeight-effectiveHeight)
			row = marginTop + int(math.Floor(float64(maxRow)*o.Row.percent/100))
		} else {
			row = o.Row.abs
		}
	default:
		row = resolveAnchorRow(anchorOr(o.Anchor, AnchorCenter), effectiveHeight, availHeight, marginTop)
	}

	// Col.
	var col int
	switch {
	case o.Col != nil:
		if o.Col.isPct {
			maxCol := max(0, availWidth-width)
			col = marginLeft + int(math.Floor(float64(maxCol)*o.Col.percent/100))
		} else {
			col = o.Col.abs
		}
	default:
		col = resolveAnchorCol(anchorOr(o.Anchor, AnchorCenter), width, availWidth, marginLeft)
	}

	if o.OffsetY != nil {
		row += *o.OffsetY
	}
	if o.OffsetX != nil {
		col += *o.OffsetX
	}

	// Clamp within bounds (respecting margins).
	row = max(marginTop, min(row, termHeight-marginBottom-effectiveHeight))
	col = max(marginLeft, min(col, termWidth-marginRight-width))

	return overlayLayout{width: width, row: row, col: col, maxHeight: maxHeight}
}

func parseSizeValue(v *SizeValue, reference int) (int, bool) {
	if v == nil {
		return 0, false
	}
	if v.isPct {
		return int(math.Floor(float64(reference) * v.percent / 100)), true
	}
	return v.abs, true
}

func anchorOr(a OverlayAnchor, def OverlayAnchor) OverlayAnchor {
	if a == "" {
		return def
	}
	return a
}

func resolveAnchorRow(anchor OverlayAnchor, height, availHeight, marginTop int) int {
	switch anchor {
	case AnchorTopLeft, AnchorTopCenter, AnchorTopRight:
		return marginTop
	case AnchorBottomLeft, AnchorBottomCenter, AnchorBottomRight:
		return marginTop + availHeight - height
	case AnchorLeftCenter, AnchorCenter, AnchorRightCenter:
		return marginTop + (availHeight-height)/2
	}
	return marginTop
}

func resolveAnchorCol(anchor OverlayAnchor, width, availWidth, marginLeft int) int {
	switch anchor {
	case AnchorTopLeft, AnchorLeftCenter, AnchorBottomLeft:
		return marginLeft
	case AnchorTopRight, AnchorRightCenter, AnchorBottomRight:
		return marginLeft + availWidth - width
	case AnchorTopCenter, AnchorCenter, AnchorBottomCenter:
		return marginLeft + (availWidth-width)/2
	}
	return marginLeft
}

// ---------------------------------------------------------------------------
// Compositing
// ---------------------------------------------------------------------------

// compositeOverlays splices visible overlays into the base content lines.
func (t *TUI) compositeOverlays(lines []string, termWidth, termHeight int) []string {
	if len(t.overlays) == 0 {
		return lines
	}
	result := append([]string(nil), lines...)

	type rendered struct {
		lines []string
		row   int
		col   int
		w     int
	}

	visible := []*overlayEntry{}
	for _, e := range t.overlays {
		if t.isOverlayVisibleLocked(e) {
			visible = append(visible, e)
		}
	}
	sort.SliceStable(visible, func(i, j int) bool {
		return visible[i].focusOrder < visible[j].focusOrder
	})

	var renderedOverlays []rendered
	minLinesNeeded := len(result)
	for _, entry := range visible {
		layout0 := t.resolveOverlayLayout(entry.options, 0, termWidth, termHeight)
		width := layout0.width
		maxHeight := layout0.maxHeight

		overlayLines := entry.component.Render(width)
		if maxHeight != nil && len(overlayLines) > *maxHeight {
			overlayLines = overlayLines[:*maxHeight]
		}

		layout := t.resolveOverlayLayout(entry.options, len(overlayLines), termWidth, termHeight)
		renderedOverlays = append(renderedOverlays, rendered{overlayLines, layout.row, layout.col, width})
		if layout.row+len(overlayLines) > minLinesNeeded {
			minLinesNeeded = layout.row + len(overlayLines)
		}
	}

	workingHeight := max(len(result), termHeight, minLinesNeeded)
	for len(result) < workingHeight {
		result = append(result, "")
	}
	viewportStart := max(0, workingHeight-termHeight)

	for _, r := range renderedOverlays {
		for i := 0; i < len(r.lines); i++ {
			idx := viewportStart + r.row + i
			if idx < 0 || idx >= len(result) {
				continue
			}
			ol := r.lines[i]
			if VisibleWidth(ol) > r.w {
				ol = SliceByColumn(ol, 0, r.w, true)
			}
			result[idx] = t.compositeLineAt(result[idx], ol, r.col, r.w, termWidth)
		}
	}
	return result
}

// compositeLineAt splices overlayLine into baseLine starting at startCol.
func (t *TUI) compositeLineAt(baseLine, overlayLine string, startCol, overlayWidth, totalWidth int) string {
	if IsImageLine(baseLine) {
		return baseLine
	}
	afterStart := startCol + overlayWidth
	base := ExtractSegments(baseLine, startCol, afterStart, totalWidth-afterStart, true)
	overlay := sliceWithWidth(overlayLine, 0, overlayWidth, true)

	beforePad := max(0, startCol-base.BeforeWidth)
	overlayPad := max(0, overlayWidth-overlay.width)
	afterTarget := max(0, totalWidth-max(startCol, base.BeforeWidth)-max(overlayWidth, overlay.width))
	afterPad := max(0, afterTarget-base.AfterWidth)

	r := segmentReset
	result := base.Before + strings.Repeat(" ", beforePad) + r + overlay.text + strings.Repeat(" ", overlayPad) + r + base.After + strings.Repeat(" ", afterPad)

	if VisibleWidth(result) <= totalWidth {
		return result
	}
	return SliceByColumn(result, 0, totalWidth, true)
}
