# Test inventory — terminal image suites

Covers two files: capability detection + image encoders, and the `isImageLine` crash
regression.

## `terminal-image.test.ts` (492 LOC)
- **Source:** [`test/terminal-image.test.ts`](../../../../reference/tui/test/terminal-image.test.ts)
- **Verifies:** [`src/terminal-image.ts`](../../../../reference/tui/src/terminal-image.ts) → [10-terminal-image.md](../10-terminal-image.md)
- **Harness:** env-mocking helper `withEnv` (clears/s restores the capability env keys); `setCapabilities`/`setCellDimensions`/`resetCapabilitiesCache`; `Image` component.
- **Go target:** `termimage_test.go`.

### Groups
- **`isImageLine`** (iTerm2 + Kitty): detects sequence at start / with text before / middle of long line / end / minimal; bug-regression 304k-char line; no-image-capability; ANSI before/after; negative cases (plain text, ANSI-only, cursor codes, partial sequences missing ESC, empty, newlines); mixed (Kitty+iTerm2, multi-segment); file-path-keyword false positive (`/path/.../File_1337_...`).
- **`detectCapabilities`** env matrix (~20): unknown→`{images:null,hyperlinks:false}`; tmux forwards-or-not via injected `tmuxForwardsHyperlink` callback (incl. `TERM=tmux*`); `screen*`→no hyperlinks; Ghostty (+cmux doesn't disable); Kitty; WezTerm; Warp via `TERM_PROGRAM`/`WARP_SESSION_ID`/`WARP_TERMINAL_SESSION_UUID`; Warp-inside-tmux→images null; iTerm2; VSCode; Windows Terminal (truecolor+hyperlinks, no images); JetBrains (truecolor, **no** hyperlinks); WT-through-tmux loses truecolor; `COLORTERM=truecolor` trusted through tmux.
- **Kitty cursor movement**: `encodeKitty` `moveCursor:false` → `C=1` param; `deleteKittyImage`/`deleteAllKittyImages` suppress replies (`q=2`); `renderImage` default (no `C=1`) vs `moveCursor:false` (`C=1`); `maxHeightCells` reduces width (`c=1,r=5`); **`Image` component caps height to a square pixel box by default**; first-line placement + empty padding rows (`getImageId` numeric, `,C=1,`, `,i=N`, ends `\x1b\\`).
- **`hyperlink`** OSC8: wraps text, preserves inner ANSI, empty text, `file://` URIs.

## `bug-regression-isimageline-startswith-bug.test.ts` (237 LOC)
- **Source:** [`test/bug-regression-isimageline-startswith-bug.test.ts`](../../../../reference/tui/test/bug-regression-isimageline-startswith-bug.test.ts)
- **Pins the historical crash:** old `isImageLine` used `startsWith(getImageEscapePrefix())`; when terminal had no image support the prefix was null → returned false → TUI width-checked a 304k-char line → crashed. Fix: use `includes()` of the Kitty/iTerm2 prefixes anywhere.
- Cases: old-impl returns false (documented); new-impl detects Kitty/iTerm2 in start/prefix/suffix/middle/300k positions; tool-output integration; ANSI-before; **no crash on 304,401-char line**; exact crash-log dimension (58,649 chars); negatives (plain 100k text, file paths with `1337`/`File`/`_G`).

### Port note
`isImageLine` is the **crash guard** — it must use substring search (`strings.Contains` on `\x1b_G` and `\x1b]1337;File=`), never a capability-gated prefix. Port the regression test verbatim (it documents the failure mode in comments).
