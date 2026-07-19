# Test inventory — `markdown.test.ts`

- **Source:** [`test/markdown.test.ts`](../../../../reference/tui/test/markdown.test.ts) (1422 LOC, ~60 cases)
- **Verifies:** [`src/components/markdown.ts`](../../../../reference/tui/src/components/markdown.ts) → [components/22-markdown.md](../components/22-markdown.md)
- **Harness:** `Markdown.render(width)` direct + `VirtualTerminal` for style-leak/underline-padding cases; `setCapabilities`/`resetCapabilitiesCache` for hyperlink behavior; `defaultMarkdownTheme` fixture; chalk `level:3`.
- **Go target:** `components/markdown_test.go` (table-driven; assert on `stripAnsi` output + ANSI-code presence).

## Test groups (what each pins)

### Lists (18 cases)
- simple/deeply nested/ordered nested — indentation (`    ` per level), `- `/`N. ` markers.
- **ordered-marker normalization by default** (`1./1./1.` → `1./2./3.`) — exact `deepStrictEqual`.
- **`preserveOrderedListMarkers:true`** keeps source markers (`4./3.`, `10)/7)`, `+/*/−/+`).
- mixed ordered+unordered nested; **loose-list blank lines** between items (exact output).
- **task-list markers** `- [ ]`/`- [x]`.
- **numbering preserved across non-indented code blocks** (LLM output) — `token.start` keeps `1./2./3.`.
- **wrapped continuation indentation**: unordered (`  `), ordered (`   `), multi-digit ordered (`    `), nested under unordered, nested under ordered, wrapped blockquote-in-list (`│ `), wrapped code-block-in-list — all exact output.

### Tables (14 cases)
- simple table; **row dividers** (`┼` count = header + rows); **column width ≥ longest word**; alignment (`:---`/`:---:`/`---:`); varying column widths.
- **cell wrapping** when table > width (every line ≤ width); long-cell multi-line wrap preserving content; **long unbroken token wrapping** (URL) without breaking borders (pinned to `hyperlinks:false`); **styled inline-code wrapping** in cells (yellow `\x1b[33m`, borders intact); extremely narrow width (≤15, no crash, ≤width); natural-fit; **paddingX respected**; **no trailing blank line** when table is last block.

### Pre-styled text (thinking traces) (3)
- `DefaultTextStyle {color: gray, italic}` **survives inline code** (gray `\x1b[90m` + italic `\x1b[3m` present, code yellow).
- survives **bold** (bold `\x1b[1m`).
- **no style leak into following lines** when rendered in TUI — `getCellItalic(row,0)==0` on the line after markdown (VirtualTerminal).

### Spacing (4 groups: code/divider/heading/blockquote)
Each: exactly **one blank line** before the following block; **no trailing blank** when the block is last.

### Blockquotes (6)
- **lazy continuation** + **explicit multiline** get consistent quote styling (italic `\x1b[3m`), and **default message color NOT applied** to blockquotes (magenta/cyan/yellow absent).
- list content inside blockquotes; **long-line wrapping** adds `│ ` border to every wrapped line; wrapped lines keep styling; **inline formatting (bold/code) inside quote** + quote styling reapplied after.

### Headings with inline code (4)
- **heading styling restored after inline code** — bold `\x1b[1m` + cyan `\x1b[36m` re-applied before text following a code span (the style-prefix mechanism); H1 also re-applies underline `\x1b[4m`; after bold too.
- **H1 underline does not leak into padding** when code is last token — `getCellUnderline(row, col)==0` for padding cols (VirtualTerminal).

### Strikethrough (2)
- `~~text~~` → strikethrough (`\x1b[9m`); `~text~` stays literal (no strikethrough). Pins the **`StrictStrikethroughTokenizer`**.

### Links (7)
- autolinked **email** not duplicated (no `mailto:` shown); **bare URL** appears once.
- **no hyperlinks capability** → URL in parentheses `(https://…)`, incl. `mailto:` form.
- **hyperlinks capability** → OSC 8 open `\x1b]8;;<url>\x1b\\` + close `\x1b]8;;\x1b\\`; for `[text](url)`, mailto, and bare URL; URL NOT also printed inline.

### HTML-like tags (2)
- `<thinking>…</thinking>` in prose rendered as text (content not hidden); HTML in code blocks visible.

### Streaming code fences (1, 6 sub-cases)
- **`trimPartialClosingFences`** — partial closing ``` `` ``` / `~~~~` stabilized so code blocks don't shrink/flicker as the final fence char streams in. Exact-output cases incl. ```` ```ts\nconst x=1;\n`` `` ```` vs complete. Partial render line-count == complete render line-count.

## Port notes
- The `VirtualTerminal` cases (italic/underline-leak) need the Go virtual terminal harness ([15-appendix.md](../15-appendix.md)) — or assert on the emitted ANSI stream + the engine's per-line reset (`SEGMENT_RESET`).
- ANSI-code-presence assertions translate directly; exact-output `deepStrictEqual` cases (lists, fences, spacing) are the strictest — goldmark's token stream must match `marked`'s closely; verify per case.
- `defaultMarkdownTheme` fixture ports to a Go test theme ([15-appendix.md](../15-appendix.md)).
