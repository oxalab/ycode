# 09 — Terminal colors (OSC 11 / color-scheme parsing)

- **TS source:** [`terminal-colors.ts`](../../reference/tui/src/terminal-colors.ts) (73 LOC)
- **Go target:** `termcolors.go`
- **Package:** `tui`
- **Pure parsers. Tiny, but used by the engine for bg-color queries and
  light/dark-scheme detection.**

---

## API

| TS | Go |
|---|---|
| `interface RgbColor { r,g,b }` | `type RgbColor struct{ R, G, B uint8 }` (TS uses 0–255 ints) |
| `type TerminalColorScheme = "dark" \| "light"` | `type TerminalColorScheme string` (`"dark"`/`"light"`) |
| `isOsc11BackgroundColorResponse(data)` | `IsOsc11BackgroundColorResponse(data string) bool` |
| `parseOsc11BackgroundColor(data)` | `ParseOsc11BackgroundColor(data string) (RgbColor, bool)` |
| `parseTerminalColorSchemeReport(data)` | `ParseTerminalColorSchemeReport(data string) (TerminalColorScheme, bool)` |

## Parsing rules (port exactly)

- **OSC 11 response** regex: `^\x1b]11;([^\x07\x1b]*)(?:\x07|\x1b\\)$` (case-insensitive).
  Value forms:
  - `#RRGGBB` (6 hex) → direct.
  - `#RRRRGGGGBBBB` (12 hex) → scale each 16-bit channel to 8-bit
    (`round(value/0xffff*0xff)`); `parseOscHexChannel` handles arbitrary channel
    length `16**len - 1`.
  - `rgb:R/G/B` (or `rgba:`) → per-channel hex with the same scaling.
- **Color-scheme report** regex: `^\x1b[\?997;(1|2)n$` → `1`=dark, `2`=light. Any
  other value → no match.
- Strict anchoring (leading/trailing junk rejected).

## Tests to port
- `terminal-colors.test.ts` (249) — parser cases + the **OSC 11 query lifecycle**
  via the engine (`queryTerminalBackgroundColor`): resolves on reply, consumes reply
  before listeners/components, handles unparseable reply, dispatches non-matching
  input normally while waiting, keeps consuming late replies after timeout. (The
  query lifecycle portion belongs to the engine test but is driven through these
  parsers.)
