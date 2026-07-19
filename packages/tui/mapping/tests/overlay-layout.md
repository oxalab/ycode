# Test inventory — `overlay-options.test.ts`

- **Source:** [`test/overlay-options.test.ts`](../../../../reference/tui/test/overlay-options.test.ts) (541 LOC, ~20 cases)
- **Verifies:** [`src/tui.ts`](../../../../reference/tui/src/tui.ts) overlay **layout math** (`resolveOverlayLayout`/`resolveAnchorRow/Col`/`parseSizeValue` + `compositeOverlays`) → [02-engine.md](../02-engine.md). Companion to [overlay-focus.md](overlay-focus.md) (which covers the focus FSM).
- **Harness:** `VirtualTerminal`; `StaticOverlay` (records the `requestedWidth` it was asked to render at, for width assertions); `EmptyContent`; `renderAndFlush`; viewport row/col checks.
- **Go target:** `engine_overlay_layout_test.go`.

## Groups (what each pins)

### Width overflow protection (6)
- **truncates overlay lines exceeding declared width** (100-char line, `width:20`) — no crash, no line over terminal width.
- complex ANSI (RGB bg + fg + OSC8 link repeated) under `width:60` — no crash.
- overlay composited on **styled base content** (bold+RGB-red full-width) — overlay visible.
- **wide CJK chars at overlay boundary** (odd width 15) — no crash.
- overlay positioned at **terminal edge** (`col:60,width:20` in 80-col) — no crash.
- overlay on base with **OSC8 hyperlink** content — no crash.

### Width percentage (2)
- `width:"50%"` of 100-col terminal → component rendered at width 50 (`requestedWidth==50`).
- `width:"10%"` + `minWidth:30` → **minWidth floor wins** (30).

### Anchor positioning (3)
- `top-left` → row 0 col 0; `bottom-right` → last row, ends at last column; `top-center` → first row, col ~30–40 (centered).

### Margin (3)
- **negative margins clamped to 0** (still row 0 col 0).
- `margin:5` (number) → row 5, col 5.
- `margin:{top:2,left:3}` (object) → row 2, col 3.

### Offset (1)
- `offsetX:10,offsetY:5` from `top-left` → row 5, col 10.

### Percentage positioning (3)
- `row:"50%",col:"50%"` → centered (row 10–13 in 24-row); `row:"0%"` → top; `row:"100%"` → bottom (last row).

### maxHeight (2)
- `maxHeight:3` → truncates to 3 lines (Line 4/5 absent).
- `maxHeight:"50%"` (10-row term) → 5 lines (L6+ absent).

### Absolute positioning (1)
- `row:3,col:5` **overrides** `anchor:"bottom-right"` → row 3, col 5.

### Stacked overlays (3)
- later overlay on top (SECOND visible over FIRST-OVERLAY); different positions don't interfere (top-left + bottom-right both visible); `hideOverlay()` pops in stack order (FIRST visible after hiding SECOND).

## Port note
The width/anchor/margin/offset/percentage/absolute rules map 1:1 to `resolveOverlayLayout`
([02-engine.md](../02-engine.md)). The exact pixel cases here (row/col assertions) are
unforgiving — `parseSizeValue` (percent floor math) and `resolveAnchorRow/Col` switch
tables must match. The 6 overflow-protection cases are **crash guards** (compositeLineAt
strict-width clamping + wide-char boundary handling) — port them as "doesn't panic +
overlay visible" smoke tests, plus a viewport-substring check.
