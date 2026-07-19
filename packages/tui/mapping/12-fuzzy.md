# 12 — Fuzzy matching

- **TS source:** [`fuzzy.ts`](../../reference/tui/src/fuzzy.ts) (137 LOC)
- **Go target:** `fuzzy.go`
- **Package:** `tui`
- **Pure. Used by `autocomplete` + `SettingsList` search.**

Subsequence matcher: all query chars must appear **in order**; lower score = better.

---

## API

| TS | Go |
|---|---|
| `interface FuzzyMatch { matches, score }` | `type FuzzyMatch struct{ Matches bool; Score int }` |
| `fuzzyMatch(query, text)` | `FuzzyMatch(query, text string) FuzzyMatch` |
| `fuzzyFilter<T>(items, query, getText)` | `FuzzyFilter[T](items []T, query string, getText func(T) string) []T` (Go generics) |

## Scoring (port exactly)

Per matched char in `text` (case-folded):
- **consecutive** with previous match: `score -= consecutiveCount * 5` (escalating bonus).
- **gap** after a prior match: `score += (i - lastMatchIndex - 1) * 2`.
- **word-boundary** match (prev char in `[\s\-_./:]`): `score -= 10`.
- **position** penalty: `score += i * 0.1`.
- exact full match (`query === text`): `score -= 100`.

**Alpha-numeric swap fallback:** if the primary match fails, try swapping a leading
`[a-z]+[0-9]+` or `[0-9]+[a-z]+` group (`codex52` ↔ `52codex`) and re-match with
`+5` penalty. Lets `codex52` match `gpt-5.2-codex`.

**`fuzzyFilter`:** split query on `[\s/]+` into tokens (whitespace **and** slash);
every token must match; sum scores; sort ascending. Lets
`openai-codex/gpt-5.5` match `{id:"gpt-5.5", provider:"openai-codex"}` against
text `"gpt-5.5 openai-codex"`.

## Tests to port
- **`fuzzy.test.ts` (112)** — empty query, too-long query, exact match, in-order
  requirement, case-insensitive, consecutive > scattered, word-boundary, alpha-numeric
  swap, filter sort/stability, slash-separated reordered text, custom `getText`.
  Port as table-driven.
