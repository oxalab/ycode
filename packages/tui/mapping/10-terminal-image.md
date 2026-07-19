# 10 — Terminal image (Kitty/iTerm2 graphics + capability detection)

- **TS source:** [`terminal-image.ts`](../../reference/tui/src/terminal-image.ts) (488 LOC)
- **Go target:** `termimage.go`
- **Package:** `tui`
- **Mostly pure (encoders/parsers); capability detection reads env + shells out to
  tmux.**

---

## API

| TS | Go |
|---|---|
| `type ImageProtocol = "kitty" \| "iterm2" \| null` | `type ImageProtocol string` (`""` = none) |
| `interface TerminalCapabilities { images, trueColor, hyperlinks }` | `type TerminalCapabilities struct{ Images ImageProtocol; TrueColor, Hyperlinks bool }` |
| `interface CellDimensions { widthPx, heightPx }` | `type CellDimensions struct{ WidthPx, HeightPx int }` |
| `interface ImageDimensions { widthPx, heightPx }` | `type ImageDimensions struct{ WidthPx, HeightPx int }` |
| `interface ImageRenderOptions` | `type ImageRenderOptions struct{ MaxWidthCells, MaxHeightCells *int; PreserveAspectRatio *bool; ImageID *int; MoveCursor *bool }` |
| `getCapabilities()` / `detectCapabilities()` / `resetCapabilitiesCache()` / `setCapabilities(caps)` | `GetCapabilities()` / `DetectCapabilities()` / `ResetCapabilitiesCache()` / `SetCapabilities()` |
| `getCellDimensions()` / `setCellDimensions(d)` | `GetCellDimensions()` / `SetCellDimensions(CellDimensions)` |
| `isImageLine(line)` | `IsImageLine(line string) bool` |
| `allocateImageId()` | `AllocateImageID() int` (random 1..0xffffffff) |
| `encodeKitty(base64, opts)` | `EncodeKitty(base64 string, opts KittyEncodeOptions) string` |
| `encodeITerm2(base64, opts)` | `EncodeITerm2(base64 string, opts ITerm2EncodeOptions) string` |
| `deleteKittyImage(id)` / `deleteAllKittyImages()` | `DeleteKittyImage(id int) string` / `DeleteAllKittyImages() string` |
| `calculateImageCellSize(...)` / `calculateImageRows(...)` | `CalculateImageCellSize(...)` / `CalculateImageRows(...)` |
| `getPngDimensions` / `getJpegDimensions` / `getGifDimensions` / `getWebpDimensions` | `GetPNGDimensions` / `GetJPEGDimensions` / `GetGIFDimensions` / `GetWebPDimensions` |
| `getImageDimensions(base64, mime)` | `GetImageDimensions(base64, mime string) (ImageDimensions, bool)` |
| `renderImage(base64, dims, opts)` | `RenderImage(...) (seq string, rows int, imageID int, ok bool)` |
| `hyperlink(text, url)` | `Hyperlink(text, url string) string` |
| `imageFallback(mime, dims?, filename?)` | `ImageFallback(mime string, dims *ImageDimensions, filename string) string` |

## `detectCapabilities` — env decision table (port exactly)

Reads `TERM_PROGRAM`, `TERMINAL_EMULATOR`, `TERM`, `COLORTERM`, plus specific env
vars. Order matters:

- `TMUX`/`tmux` → images=null, hyperlinks=`probeTmuxHyperlinks()` (shells `tmux
  display-message -p '#{client_termfeatures}'`, 250ms timeout, checks for
  `hyperlinks`).
- `screen` → images=null, hyperlinks=false.
- Kitty (`KITTY_WINDOW_ID`/`kitty`), Ghostty, WezTerm, Warp → kitty graphics,
  truecolor, hyperlinks.
- iTerm2 (`ITERM_SESSION_ID`/`iterm.app`) → iterm2 graphics, truecolor, hyperlinks.
- `WT_SESSION` (Windows Terminal), `vscode`, `alacritty` → no images, truecolor,
  hyperlinks.
- JetBrains (`jetbrains-jediterm`) → no images, truecolor, **no** hyperlinks.
- Unknown → conservative: no images, hyperlinks=false.

`trueColor` from `COLORTERM ∈ {truecolor, 24bit}`. Result cached
(`cachedCapabilities`); `resetCapabilitiesCache`/`setCapabilities` for tests.

## Encoders (pure)

- **`encodeKitty`** — chunked base64 (4096-byte chunks) with `a=T,f=100,q=2` params,
  `m=1/m=0` for multi-chunk, optional `c=/r=/i=/C=1`. Terminated `\x1b\\`.
- **`encodeITerm2`** — `ESC]1337;File=inline=1;width=...;height=...;name=<b64>:<data>BEL`.
- **`deleteKittyImage`** — `ESC_Ga=d,d=I,i=<id>,q=2 ESC\\`.
- **`deleteAllKittyImages`** — `a=d,d=A,q=2`.

## Dimension parsers (pure, from image headers)

Port the byte-level header readers (PNG @16/20 BE, JPEG SOF marker scan, GIF87a/89a
LE @6/8, WebP RIFF/VP8 /VP8L/VP8X). Each returns `(ImageDimensions, bool)`.

## Tests to port
- `terminal-image.test.ts` (492) — capability detection (env matrix), encoders,
  dimension parsers (sample fixtures), cell-size math.
- `regression-regional-indicator-width.test.ts` (52) — regional indicator width=2.
- `image-test.ts` demo — manual visual verification.
