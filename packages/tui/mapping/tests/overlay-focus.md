# Test inventory — `overlay-non-capturing.test.ts`

- **Source:** [`test/overlay-non-capturing.test.ts`](../../../../reference/tui/test/overlay-non-capturing.test.ts) (1202 LOC, ~35 cases)
- **Verifies:** [`src/tui.ts`](../../../../reference/tui/src/tui.ts) overlay stack + **focus-restore state machine** → [02-engine.md](../02-engine.md)
- **Harness:** `VirtualTerminal(80,24)`; `FocusableOverlay` (records `handleInput` + `focused`), `StaticOverlay`, `EmptyContent`; helper `renderAndFlush` (`requestRender(true)` + `waitForRender`); assertions on `focused` flags + recorded `inputs[]` after `sendInput`.
- **Go target:** `engine_overlay_test.go`. These are the **most behavior-dense engine tests** — port carefully; the focus FSM ([02-engine.md](../02-engine.md) `setFocusInternal` + `OverlayFocusRestoreState`) is fully specified here.

## Test groups (what each pins)

### Focus management (24)
- **non-capturing overlay preserves focus on creation** (editor stays focused, overlay not).
- `focus()` transfers; `unfocus()` restores previous.
- `setHidden(false)` on non-capturing does **not** auto-focus.
- `hide()` when not-focused doesn't change focus; when focused restores correctly.
- capturing overlay removed with non-capturing below → restores to editor.
- **sub-overlay cleanup then `hideOverlay()`** restores focus + routes input to editor.
- removed focused child overlay does **not** become parent fallback.
- **microtask-deferred sub-overlay pattern** (simulates `showExtensionCustom`: timer shown sync, controller pushed via `Promise.then`) restores focus + input to editor after close. → exercises the async interleaving the FSM must handle.
- **handleInput redirection skips non-capturing when focused overlay turns invisible** (`visible:` callback) → routes to next visible capturing.
- **active base-focus replacement** receives close input **before** overlay restore (replacement set via `setFocus` from inside overlay's `handleInput`, then replacement returns focus → overlay reclaims subsequent input).
- replacement that is itself another overlay's preFocus still receives input.
- **blocked replacement** can move focus internally (first→second replacement) before overlay restore.
- removed replacement restores overlay even when overlay preFocus differs from next focus.
- **`unfocus({target})`** releases a blocked overlay while replacement stays focused.
- handleInput restores a **visible focused overlay after base focus steal** (`setFocus(replacement); setFocus(editor)`).
- restores an **explicitly focused raw sub-overlay** (non-capturing, `.focus()`'d) after base steal.
- **passive non-capturing overlay does not regain input** after base focus.
- **explicitly focused non-capturing regains input** after base steal.
- `unfocus()` prevents a visible overlay from regaining input.
- `setFocus(null)` clears the visible-overlay restore.
- **blocked replacement `setFocus(null)` resumes the visible overlay**.
- **temporarily invisible focused overlay** falls back without losing restore eligibility (input to editor while hidden, to overlay when shown again).
- temporarily invisible overlay with **null preFocus** restores when visible again.
- **cyclic overlay preFocus ancestry does not hang** (`isOverlayFocusAncestor` visited-set).
- handleInput restores the **focus-order top** overlay after base steal.
- `hideOverlay()` does not reassign focus when topmost is non-capturing.
- **multiple capturing + non-capturing** restore focus through sequential removals.
- capturing overlay `unfocus()` falls back to preFocus.

### No-op guards (4)
- `focus()` on hidden overlay is no-op; `focus()` after `hide()` is no-op; `unfocus()` without focus is no-op; `unfocus()` with null preFocus clears focus and does not route input back.

### Focus-cycle prevention (4)
- toggle focus between two non-capturing overlays then `unfocus()` → editor.
- **explicit `unfocus({target})`** supports cycling between 3 overlays + editor (records per-overlay inputs).
- **`unfocus({target:null})`** clears focus without restoring overlays.
- hiding a focused overlay falls back to the **next visual-frontmost** overlay.

### Rendering order (6) — `focusOrder` / z-stack
- `focus()` on already-focused overlay **bumps visual order** to top.
- default order follows creation order.
- `focus()` on a lower overlay renders it on top.
- focusing a middle overlay puts it on top, preserving others' relative order; removal restores the rest.
- capturing overlay hidden then shown **renders on top after unhide**.
- `unfocus()` does **not** change visual order until another overlay is focused.

## Port notes
- The async cases (microtask-deferred, `renderAndFlush`) map to the engine's render-request goroutine/timer + a `waitForRender` synchronization point. Go tests need an equivalent "flush render" helper that blocks until the engine has emitted one frame.
- These tests pin the **`OverlayFocusRestoreState`** sum-type transitions (inactive/eligible/blocked, resume=restore-overlay/focus-target) and `getTopmostVisibleOverlay`/`isOverlayFocusAncestor`/`retargetOverlayPreFocus`. Port the FSM first ([02-engine.md](../02-engine.md)), then port these as its acceptance suite.
- `FocusableOverlay` (records inputs + focused flag) is a reusable test helper — put in `internal/enginetest` or a `testhelp` file shared with `tui-render`/`overlay-options` tests.
