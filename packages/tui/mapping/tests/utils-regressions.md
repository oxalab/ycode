# Test inventory — `regression-regional-indicator-width.test.ts`

- **Source:** [`test/regression-regional-indicator-width.test.ts`](../../../../reference/tui/test/regression-regional-indicator-width.test.ts) (52 LOC)
- **Verifies:** [`src/utils.ts`](../../../../reference/tui/src/utils.ts) `visibleWidth` / `wrapTextWithAnsi` → [06-utils.md](../06-utils.md)
- **Pure — no terminal harness.**
- **Go target:** `utils_test.go` (regional-indicator sub-tests).

## What it pins
Regional indicator symbols (U+1F1E6..U+1F1FF, flag halves) must measure **width 2 even
when isolated / partial**, because during streaming a full flag `🇨🇳` often appears
as a half `🇨` first — if measured as width 1 while the terminal renders it width 2,
differential rendering drifts and leaves stale characters.

Cases:
- partial flag `🇨` → width 2; `      - 🇨` → width 10.
- **wrap before overflow**: `wrapTextWithAnsi("      - 🇨", 9)` → 2 lines (7 + 2) — must wrap so terminal auto-wrap doesn't mismatch.
- **every** regional indicator singleton (U+1F1E6–U+1F1FF) → width 2.
- full flag pairs (`🇯🇵 🇺🇸 🇬🇧 🇨🇳 🇩🇪 🇫🇷`) → width 2.
- common streaming-emoji intermediates (`👍 👍🏻 ✅ ⚡ ⚡️ 👨 👨‍💻 🏳️‍🌈`) → width 2.

## Port note
This is the explicit pin for the `graphemeWidth` rule "regional indicators = 2" in
[06-utils.md](../06-utils.md). The Go port must reproduce this in its custom grapheme
width function over `rivo/uniseg` — `go-runewidth`'s `RuneWidth` may not treat a lone
regional indicator as 2, so do **not** rely on it alone for this class.
