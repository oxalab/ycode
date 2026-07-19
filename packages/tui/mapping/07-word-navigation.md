# 07 — Word navigation

- **TS source:** [`word-navigation.ts`](../../reference/tui/src/word-navigation.ts) (117 LOC)
- **Go target:** `wordnav.go`
- **Package:** `tui`
- **Pure functions. Used by `Input` and `Editor` for Ctrl+W / Alt+D / Ctrl+Left/Right.**

Finds the next cursor offset after moving one word backward/forward, using word
segmentation plus an "atomic segment" hook (so paste markers are treated as one
unit).

---

## API

| TS | Go |
|---|---|
| `findWordBackward(text, cursor, options?)` | `FindWordBackward(text string, cursor int, opts *WordNavOptions) int` |
| `findWordForward(text, cursor, options?)` | `FindWordForward(text string, cursor int, opts *WordNavOptions) int` |
| `interface WordNavigationOptions { segment?, isAtomicSegment? }` | `type WordNavOptions struct{ Segment func(string) []WordSegment; IsAtomic func(string) bool }` |
| `Intl.SegmentData` (segment/index/isWordLike) | `type WordSegment struct{ Text string; IsWordLike bool }` |

**Segmenter:** default uses `getWordSegmenter()` (TS `Intl.Segmenter`). Go:
`rivo/uniseg` `NewWords`. The editor passes a **custom segmenter** that merges paste
markers into atomic units (`segmentWithMarkers` in editor.ts) — so the option must
stay pluggable.

## Algorithm (port exactly)

**`findWordBackward`** (cursor→):
1. segment text before cursor
2. pop trailing whitespace segments (unless atomic)
3. if last segment is atomic → skip exactly it
4. if word-like → skip the word, but stop at the last ASCII punctuation boundary
   inside it (preserves `foo.bar` → stops before `.bar`)
5. else (punctuation run) → skip contiguous non-word-non-ws segments

**`findWordForward`** is the mirror.

Punctuation boundary handling uses `PUNCTUATION_REGEX` (from utils) inside
word-like segments — ports directly.

## Tests to port
- **`word-navigation.test.ts` (191)** — basic words, dotted/colon/path, CJK mixed,
  whitespace boundaries, punctuation runs, atomic-segment (paste marker) handling
  with a custom segment map. Port 1:1 (the atomic test injects a fake segmenter —
  keep that seam).
