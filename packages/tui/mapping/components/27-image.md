# Components 27 — Image

- **TS source:** [`image.ts`](../../../reference/tui/src/components/image.ts) (126 LOC)
- **Go target:** `components/image.go`
- **Package:** `components`
- **Depends on:** `tui` termimage (`AllocateImageID`, `GetCapabilities`,
  `GetCellDimensions`, `GetImageDimensions`, `ImageFallback`, `RenderImage`).

Renders inline images for Kitty/iTerm2-capable terminals; falls back to a text
placeholder otherwise.

---

## API

| TS | Go |
|---|---|
| `interface ImageTheme { fallbackColor }` | `type ImageTheme struct{ FallbackColor StyleFunc }` |
| `interface ImageOptions { maxWidthCells?, maxHeightCells?, filename?, imageId? }` | `type ImageOptions struct{ MaxWidthCells, MaxHeightCells *int; Filename string; ImageID *int }` |
| `class Image` | `type Image struct{ base64, mime string; dims tui.ImageDimensions; theme ImageTheme; opts ImageOptions; imageID *int; cache }` |
| `new Image(base64, mime, theme, options?, dimensions?)` | `NewImage(base64, mime string, theme ImageTheme, opts ImageOptions, dims ...tui.ImageDimensions)` |
| `getImageId()` | `GetImageID() (int, bool)` |
| `invalidate()` (clear cache) | `Invalidate()` |
| `render(width)` | `Render(width)` |

## Render logic (port exactly)

- `maxWidth = max(1, min(width-2, opts.maxWidthCells ?? 60))`
- `defaultMaxHeight = ceil(maxWidth * cellW / cellH)` (from `GetCellDimensions`)
- If `caps.Images` set:
  - Kitty: allocate an image ID once (`AllocateImageID`) if none; `RenderImage`
    with `moveCursor=false`; output `[sequence]` + `(rows-1)` empty lines so the
    engine accounts for the image height.
  - iTerm2: output `(rows-1)` empty lines, then a line that moves cursor up
    `(rows-1)` (`\x1b[NA`) + sequence (so TUI cursor accounting stays in scroll area).
  - On `RenderImage` failure → fallback.
- Else → `ImageFallback(mime, dims, filename)` styled with `theme.FallbackColor`.
- Cache by `width`.

The multi-row empty-line layout is **consumed by the engine's Kitty-image reserved-rows
logic** (`getKittyImageReservedRows` in engine.go) — must stay in sync.

## Tests to port
- `terminal-image.test.ts` covers the underlying encoders/parsers; the component's
  row-accounting is verified by `image-test.ts` (visual demo). Add a unit test
  asserting the row count returned for a known dimension × cell size, against both
  kitty and iterm2 capability stubs (`tui.SetCapabilities`).
