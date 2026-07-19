# Test inventory — engine renderer / resize / shrink / overlay compositing

Consolidates the smaller engine-behavior suites (each is short) + the big
`tui-render.test.ts`. All verify [`src/tui.ts`](../../../../reference/tui/src/tui.ts) → [02-engine.md](../02-engine.md).

## `tui-render.test.ts` (767 LOC)
- **Harness:** `VirtualTerminal` + `LoggingVirtualTerminal` (records raw `write` bytes); `TestComponent` with mutable `lines`; `withEnv` for TERMUX; `tui.fullRedraws` counter; `getCellItalic` via xterm buffer.
- **Go target:** `engine_render_test.go`.

### Kitty image cleanup (7)
- **clears reserved rows before drawing** appended placements (asserts `\x1b[2K\r\n\x1b[2K\x1b[1A<seq>\x1b[1B` present, and `<seq>\r\n\x1b[2K` absent).
- **falls back to full redraw** when the image pre-clear would scroll (2-row viewport → `\x1b[2J`).
- **reserves rows before drawing during full-redraw fallback** (`\r\n\r\n\x1b[2A<seq>\x1b[2B`).
- **no cursor-up placement for images taller than viewport** (#4461 first-row path).
- **deletes changed image IDs before drawing moved placements** (delete < draw ordering).
- **redraws image lines when an earlier reserved row changes** (delete + redraw, no full redraw).
- **deletes previous image IDs during full redraws** (delete < `\x1b[2J` ordering).

### Resize handling (3)
- height change → full redraw (non-Termux); **Termux skips** full redraw on height change (no `\x1b[2J`/`\x1b[3J`, latest content stays visible through multiple toggles); width change → full redraw.

### Content shrinkage (3, `clearOnShrink`)
- shrinks significantly / to single line / to empty → full redraw clears the freed rows.

### Differential rendering (9)
- cursor tracking correct when content shrinks with unchanged remaining lines (proven by a follow-up middle-line change landing on the right row).
- **only a middle line changes** (spinner) — header/footer preserved.
- **per-line SGR reset** (italic on line 0 doesn't leak to line 1).
- first-line-only / last-line-only / multiple-non-adjacent changes render correctly.
- content→empty→content round-trip.
- **full redraw when deleted lines move viewport upward** (exact viewport equality).
- append after shrink stays on the differential path (no extra full redraw) once viewport reset.
- **clears stale content** when `maxLinesRendered` was inflated by a transient component (exact 10-row viewport).

## `tui-shrink.test.ts` (44)
- `tui.clear()` then render → all previously-rendered lines cleared (no stale "first/second/third").

## `tui-cell-size-input.test.ts` (81)
- TUI sends a cell-size query (`CSI 16 t`) at startup when image-capable; **forwards bare ESC** even with a pending query; **consumes the cell-size response** (`\x1b[6;20;10t` → `setCellDimensions{10,20}`) and still forwards later user input. (Pins `consumeCellSizeResponse` + `queryCellSize`.)

## `tui-overlay-style-leak.test.ts` (80)
- **no style leak when a trailing SGR reset sits beyond the last visible column** (no overlay) — `getCellItalic(row1,col0)==0`.
- **no leak when overlay slicing drops trailing SGR resets** (overlay composites over a styled base). Pins `compositeLineAt` + the per-line `SEGMENT_RESET`.

## `overlay-short-content.test.ts` (61)
- overlay renders when content is **shorter than terminal height** (overlay composited at a screen-relative row even though content only fills 3 of 24 rows).

## `regression-overlay-cjk-boundary.test.ts` (68)
- **overlay starting inside a wide grapheme**: `extractSegments` excludes the wide char from `before` (keeps width bookkeeping exact); `compositeLineAt` (accessed via cast) places the overlay at the requested column, dropping the split wide char, with exact width 20 and 4-wide overlay. ASCII boundary behaves normally for comparison. Pins the CJK-overlay-compositing CHANGELOG fix.

## Port note
These need the Go **virtual terminal harness** (record writes, expose viewport, xterm-style cell attribute reads for italic/underline) OR assertion directly on the emitted ANSI byte stream — the byte-substring assertions (`includes("\x1b[2K...")`, delete-before-draw ordering) translate cleanly without a full xterm. The `getCellItalic`/`getCellUnderline` cases are the only ones needing real cell-state; they can instead assert that the renderer emitted `\x1b[0m` (full reset) at line end, which is the actual fix being tested.
