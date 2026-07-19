# 06 — Utils (text width / wrap / truncate / slice)

- **TS source:** [`utils.ts`](../../reference/tui/src/utils.ts) (1188 LOC)
- **Go target:** `utils.go`
- **Package:** `tui`
- **Pure functions over strings — the text-measurement engine. Critical path for the
  whole library (every component + the renderer depend on it).**

---

## Unicode width strategy

TS uses `Intl.Segmenter` (grapheme + word granularity) + `get-east-asian-width` +
Unicode property regexes (`\p{RGI_Emoji}`, zero-width, etc.). Go equivalents:

| TS mechanism | Go equivalent |
|---|---|
| `Intl.Segmenter(granularity: "grapheme")` | `github.com/rivo/uniseg` `NewGraphemes(s)` |
| `Intl.Segmenter(granularity: "word")` | `rivo/uniseg` `NewWords(s)` (returns `WordBreak` + `IsWordLike`) |
| `get-east-asian-width(cp)` | `github.com/mattn/go-runewidth` `RuneWidth(r)` — but **see note** |
| `\p{RGI_Emoji}` regex | `rivo/uniseg` has emoji properties, or precompute a range set; `graphemeWidth` uses a pre-filter `couldBeEmoji` |
| `zeroWidthRegex`, `leadingNonPrintingRegex` | custom `unicode.Is` checks (`IsControl`, `IsMark`, `IsCf`, surrogate detection) |

> **EAW note:** pi-tui computes width via `eastAsianWidth(cp)` directly (not a
> ready-made string-width lib). `mattn/go-runewidth`'s `RuneWidth` returns the same
> EAW values but treats emoji/combining slightly differently. We must replicate
> pi-tui's `graphemeWidth` rules (regional indicators = 2, Thai/Lao AM vowels = 1,
> emoji = 2, trailing fullwidth forms additive). **Port `graphemeWidth` ourselves
> over `uniseg` graphemes**, not just `RuneWidth`, to match byte-for-byte — the
> truncate/wrap tests assert exact output.

## Public API

| TS export | Go |
|---|---|
| `visibleWidth(str)` | `VisibleWidth(s string) int` |
| `truncateToWidth(text, maxWidth, ellipsis="…", pad=false)` | `TruncateToWidth(s string, maxWidth int, ellipsis string, pad bool) string` (keep defaults) |
| `wrapTextWithAnsi(text, width)` | `WrapTextWithAnsi(text string, width int) []string` |
| `sliceByColumn(line, startCol, length, strict=false)` | `SliceByColumn(line string, startCol, length int, strict bool) string` |
| `sliceWithWidth(line, startCol, length, strict=false)` | `SliceWithWidth(...) (text string, width int)` |
| `extractSegments(line, beforeEnd, afterStart, afterLen, strictAfter=false)` | `ExtractSegments(...) (before string, beforeWidth int, after string, afterWidth int)` |
| `normalizeTerminalOutput(str)` | `NormalizeTerminalOutput(s string) string` |
| `extractAnsiCode(str, pos)` | `ExtractAnsiCode(s string, pos int) (code string, length int, ok bool)` |
| `applyBackgroundToLine(line, width, bgFn)` | `ApplyBackgroundToLine(line string, width int, bg StyleFunc) string` |
| `isWhitespaceChar(char)`, `isPunctuationChar(char)` | `IsWhitespaceChar(r)`, `IsPunctuationChar(r)` |
| `getGraphemeSegmenter()`, `getWordSegmenter()` | (Go: don't share; create per-call or pool) |
| `PUNCTUATION_REGEX` | `var punctuation = func(r rune) bool {...}` (no regex needed) |
| `cjkBreakRegex` | `func IsCJKBreak(r rune) bool` using `uniseg` script checks |

## Internal helpers to port

| TS | Go |
|---|---|
| `couldBeEmoji(segment)` | emoji pre-filter by codepoint ranges |
| `isPrintableAscii(str)` | fast path for ASCII |
| `graphemeWidth(segment)` | **the core width rule** — port exactly (tab=3, regional indicator=2, emoji=2, EAW, Thai/Lao AM additive) |
| `truncateFragmentToWidth`, `finalizeTruncatedResult` | same |
| `AnsiCodeTracker` class | `type ansiCodeTracker struct{...}` — see below |
| `updateTrackerFromText`, `splitIntoTokensWithAnsi` | same |
| `wrapSingleLine`, `breakLongWord` | same |
| width cache (`WIDTH_CACHE_SIZE=512` LRU) | `sync.Map`/`lru.Cache` keyed by string; or skip caching initially, add if profiled |

## `AnsiCodeTracker` (SGR + OSC8 state across wraps)

Tracks active SGR attributes (bold/dim/italic/underline/blink/inverse/hidden/
strikethrough + fg/bg color codes) and the active OSC8 hyperlink, so that wrapped
continuation lines can re-open styling and lines can close only underline/hyperlink
at line-end (not a full reset — preserves background color in padding).

```go
type ansiCodeTracker struct {
    bold, dim, italic, underline, blink, inverse, hidden, strikethrough bool
    fgColor, bgColor string  // "31" or "38;5;240" or "38;2;r;g;b"
    activeHyperlink *activeHyperlink // params, url, terminator (BEL or ST)
}
func (t *ansiCodeTracker) Process(code string)
func (t *ansiCodeTracker) Reset()
func (t *ansiCodeTracker) Clear()
func (t *ansiCodeTracker) GetActiveCodes() string
func (t *ansiCodeTracker) HasActiveCodes() bool
func (t *ansiCodeTracker) GetLineEndReset() string  // underline-off + OSC8 close only
```

Port `parseOsc8Hyperlink`/`formatOsc8Hyperlink`/`formatOsc8Close` — preserve the
BEL-vs-ST terminator (OAuth URLs use BEL; some terminals only make BEL-terminated
links clickable). A pooled instance (`pooledStyleTracker`) backs `ExtractSegments`.

## `extractAnsiCode` — CSI/OSC/APC scanner

Recognizes: CSI (`ESC [` … final byte in `m G K H J`), OSC (`ESC ]` … BEL/ST), APC
(`ESC _` … BEL/ST). Used by `visibleWidth` (strip) and the tracker. **Must match the
renderer's `isImageLine`/Kitty handling** (terminal-image.go).

## Tests to port
- **`wrap-ansi.test.ts` (246)** — underline/background preservation across wraps,
  OSC8 hyperlink re-emission + BEL terminator, CJK break, regional-indicator width,
  trailing-whitespace truncation.
- **`truncate-to-width.test.ts` (76)** — huge-unicode, ANSI preserve+reset,
  malformed-ANSI no-hang, wide-ellipsis clipping, contiguous-prefix (not skip+resume),
  pad, Thai/Lao AM normalization.
- **`tab-width.test.ts` (28)** — `sliceWithWidth`/`extractSegments` tab accounting.
- `bug-regression-isimageline-startswith-bug.test.ts` (237) — image-line detection
  interaction with width.
