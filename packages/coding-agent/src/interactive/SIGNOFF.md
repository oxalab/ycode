# P8c — Sign-off parity checklist

> Run through this on a **real terminal** (not piped stdin) against the pi-tui
> (now the default) AND the legacy Bubble-Tea TUI (`YCODE_TUI=bt`). The goal:
> confirm the pi-tui matches the BT TUI on every common interaction, so the BT
> TUI can be deleted in a follow-up change.
>
> Bootstrap once (needed for both): `ycode` → `/login` → `/model`.

## 1. Editor / input fidelity (the original driver — most important)
- [ ] Undo / redo (`Ctrl+Z` / `Ctrl+Y` style; check the base Editor's bindings)
- [ ] Kill-ring: `Ctrl+K` (kill to end of line), `Ctrl+Y` (yank), `Alt+Y` (yank-pop)
- [ ] Input history: `↑` / `↓` recall previous prompts
- [ ] Word navigation: `Alt+B` / `Alt+F` (and `Alt+←` / `Alt+→`)
- [ ] Word delete: `Alt+Backspace` / `Alt+D`
- [ ] Line start/end: `Ctrl+A` / `Ctrl+E`
- [ ] Paste (bracketed paste from clipboard)
- [ ] `@` file/path autocomplete + `/` slash-command autocomplete (Tab)
- [ ] Multi-line: `Shift+Enter` for newline; `Enter` submits

## 2. Transcript rendering
- [ ] Streamed assistant text (Markdown: headings, code, lists, links)
- [ ] Thinking blocks (show/hide via thinking level)
- [ ] Tool calls render with name + args; result colors as a diff for edits
- [ ] Bash `!cmd` output streams; exit-code shows success/error color
- [ ] Compaction summary box appears on context compaction
- [ ] User messages in their bubble

## 3. Selectors (each opens on the command, Esc cancels back to editor)
- [ ] `/login` → provider chooser → OAuth flow (URL + browser open)
- [ ] `/model` → search + scope toggle (Tab) + provider badges + current ✓
- [ ] `/theme` → live preview; cancel restores original
- [ ] `/thinking` → levels with descriptions
- [ ] `/settings` → cycle values; thinking/theme submenus
- [ ] `/switch` (and `/resume`) → session list; switch rebuilds transcript

## 4. Slash routing + cycling
- [ ] `/compact`, `/new`, `/name <x>`, `/help`, `/quit` behave as expected
- [ ] Unknown `/cmd` falls through to the model
- [ ] `Ctrl+P` / `Shift+Ctrl+P` cycle models; `Ctrl+L` opens model selector
- [ ] `Shift+Tab` cycles thinking; `Ctrl+T` toggles thinking
- [ ] `Ctrl+C` exits; `Ctrl+D` on empty editor exits

## 5. Widgets / lifecycle
- [ ] Auto-retry countdown ("Retrying in Ns...") on a retryable failure
- [ ] **Working spinner** in the status area while the assistant streams / context compacts (P4b)
- [ ] **Pending queue** previews ("Steering:" / "Follow-up:") appear when messages queue while busy (P4b)
- [ ] Footer: pwd/branch/session line 1; token/context/model stats line 2
- [ ] Session switch re-renders footer + transcript correctly
- [ ] No crashes on the empty-state path (no model, no entries)

## 6. Edge cases (regression-prone)
- [ ] Submitting a plain prompt with **no model** shows the `/login` hint (not a silent fail or crash)
- [ ] Resizing the terminal doesn't corrupt the render
- [ ] Rapid input doesn't deadlock (no `program.Send` bridge — confirm responsive)

## 7. Residual commands (P9)
- [ ] `/copy` copies the last assistant message to the clipboard; empty → "No agent messages"
- [ ] `/session` renders name/file/id + message + token stats; `/export [path]` writes HTML (`.jsonl` → notice)
- [ ] `/clone` forks at the leaf; `/reload` reloads resources (both blocked while streaming)
- [ ] `/hotkeys` lists the active keybindings; `/name` (no arg) shows the current name
- [ ] `/logout` removes a provider's auth; `/fork` picks a user message to fork from; `/tree` navigates history to switch branch; `/scoped-models` toggles Ctrl+P models (in-memory)

---

## Known gaps (intentional, not regressions — do NOT block deletion on these)
- `/share` (needs a `gh`-gist exec wrapper) and `/changelog` (needs a `CHANGELOG.md` asset + parser) → "not yet supported" notice.
- `/scoped-models` persists **in-memory only** — settings.json persistence has no Go setter (deferred even in the BT TUI).
- Session **delete/rename** in `/switch` — no Go SessionManager API.
- Extension input/editor widgets exist but aren't event-wired (no Go extension-input event source).
- Per-tool **themed** renderers (Go core/tools return plain strings).

## Decision gate
- All of **sections 1–7** pass on the pi-tui ≥ the BT TUI → ✅ proceed to delete the BT TUI (separate change, after a soak).
- Any section 1–7 item **regresses** → file it as a fix before deletion.
