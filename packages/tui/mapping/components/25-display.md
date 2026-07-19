# Components 25 — Display primitives (Text, Box, TruncatedText, Spacer)

The four simple layout components. All implement `Component`; the first three cache
render output keyed by `(text, width)` and implement `Invalidate()`.

- **Package:** `components` (`ycode/packages/tui/components`)
- **Depends on:** `tui` (Component/Container/Invalidater), `tui.VisibleWidth`,
  `tui.WrapTextWithAnsi`, `tui.TruncateToWidth`, `tui.ApplyBackgroundToLine`.

---

## 25a. `Text` — `text.ts` (106 LOC) → `components/text.go`

Multi-line text with word-wrap + optional background, with padding.

| TS | Go |
|---|---|
| `class Text` | `type Text struct{ text string; paddingX, paddingY int; customBg *StyleFunc; cached... }` |
| `constructor(text="", paddingX=1, paddingY=1, customBgFn?)` | `NewText(text string, paddingX, paddingY int, bg ...StyleFunc) *Text` |
| `setText(t)` (clears cache) | `SetText(t string)` |
| `setCustomBgFn(fn?)` | `SetCustomBg(bg StyleFunc)` |
| `invalidate()` | `Invalidate()` |
| `render(width)` | `Render(width int) []string` |

**Render:** normalize tabs→3 spaces; `contentWidth = max(1, width - paddingX*2)`;
`WrapTextWithAnsi` (no padding); add left/right padding; if `bg` set,
`ApplyBackgroundToLine(line, width, bg)` else pad with spaces; prepend/append
`paddingY` empty bg-filled lines. Cache by `(text==cachedText && width==cachedWidth)`.
Returns `[]` for empty/whitespace text.

## 25b. `Box` — `box.ts` (137 LOC) → `components/box.go`

A `Container`-like that pads + backgrounds all children.

| TS | Go |
|---|---|
| `class Box` (`children`, `paddingX/Y`, `bgFn`, cache) | `type Box struct{ Children []tui.Component; paddingX, paddingY int; bg StyleFunc; cache *boxCache }` |
| `constructor(paddingX=1, paddingY=1, bgFn?)` | `NewBox(...)` |
| `addChild/removeChild/clear` (invalidate cache) | same |
| `setBgFn(fn?)` | `SetBg(StyleFunc)` |
| `invalidate()` (children + cache) | `Invalidate()` |
| `render(width)` | `Render(width)` |

**Render:** `contentWidth = max(1, width - paddingX*2)`; render each child at
`contentWidth`, left-pad each line; **bgFn-change detection by sampling**
`bgFn("test")` against cached sample; `matchCache` compares width + bgSample +
childLines equality; else rebuild: top padding rows, content rows (each
`applyBg`), bottom padding rows. `applyBg` pads to width then `ApplyBackgroundToLine`.

## 25c. `TruncatedText` — `truncated-text.ts` (65 LOC) → `components/truncatedtext.go`

Single-line text truncated to width.

| TS | Go |
|---|---|
| `class TruncatedText` (`text`, `paddingX=0`, `paddingY=0`) | `type TruncatedText struct{...}` |
| constructor | `NewTruncatedText(text string, paddingX, paddingY int)` |
| `invalidate()` (no-op) | `Invalidate()` |
| `render(width)` | `Render(width)` |

**Render:** paddingY empty lines (space-padded to width); take first line (split on
`\n`); `TruncateToWidth(line, max(1, width - paddingX*2))`; add left/right padding;
pad final line to exactly `width`. Every output line is exactly `width` visible cols.

## 25d. `Spacer` — `spacer.ts` (28 LOC) → `components/spacer.go`

N empty lines.

| TS | Go |
|---|---|
| `class Spacer` (`lines=1`) | `type Spacer struct{ lines int }` |
| constructor | `NewSpacer(lines int) *Spacer` |
| `setLines(n)` | `SetLines(int)` |
| `invalidate()` (no-op) | `Invalidate()` |
| `render(width)` → N× `""` | `Render(width) []string` |

## Tests to port
- `truncated-text.test.ts` (129) — pad-to-width exactness, vertical padding,
  truncation + ellipsis + reset-before-ellipsis, ANSI preservation, newline handling,
  empty text. Uses `chalk`; port assertions on ANSI presence (force `level:3`).
- Text/Box/Spacer are exercised indirectly (chat demo, markdown). Add focused tests
  for the caching + bgFn-sample-change path.
