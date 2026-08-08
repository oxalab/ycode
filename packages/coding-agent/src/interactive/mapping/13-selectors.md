# 13 — Selectors + dialogs

> Verified against reference source (cited `file:line`). Files under
> `reference/coding-agent/src/modes/interactive/components/`. Selectors are
> **swapped into `editorContainer`** (not overlays) via
> `interactive-mode.ts:3745-3749` (`showSelector`: `done()` clears
> `editorContainer`, re-adds the editor, `setFocus(editor)`). All selectors
> `implements Focusable` + `InputHandler`.

## (A) Common selector pattern — verified from `model-selector.ts:74-135, 287-327`

```
DynamicBorder(top) → Spacer(1) → hint Text(s) → search Input → Spacer(1)
  → list Container → Spacer(1) → DynamicBorder(bottom)
```
- **search `Input`** (`:102-112`): `onSubmit` selects `filteredModels[selectedIndex]`.
- **navigation** (`:300-321`): `tui.select.up`/`tui.select.down` (wrap-around), `tui.select.confirm` (select), `tui.select.cancel` (cancel).
- **scope toggle**: `tui.input.tab` (`:289-298`) — only when scoped items exist.
- **Focusable** (`:38-45`): propagates `focused` to the search `Input` (IME cursor).
- **filtering**: `fuzzyFilter(items, query, searchableTextFn)` (`:220-224`).
- **Selected row** (`:249-260`): `fg("accent","→ ") + fg("accent", id) + fg("muted","[provider]") + fg("success"," ✓" if current)`; unselected = `"  "+id+[provider]+✓`. Scroll indicator `fg("muted"," (idx/len)")` (`:266-269`).
- **Common theme slots:** `accent` (selected/cursor), `muted` (badges/hints/no-match), `warning` (hints), `success` (current ✓), `error` (load error), `border` (DynamicBorder default), `selectedBg` (row bg via SelectList variants).

## (B) Per-selector variations (grep-verified unless noted)

| Selector | Lists | Built on | Scope/special keys (file:line) | Distinctive slots |
|---|---|---|---|---|
| `model-selector.ts` | models (`provider/id`) | custom + `fuzzyFilter` (`:220`) | `tui.input.tab` all↔scoped (`:289`); scope hint (`:196-204`) | accent, muted, success (✓), warning |
| `session-selector.ts` | sessions | `new SessionList(...)` (`:763`) | `tui.input.tab` current↔all (`:537`); `app.session.delete` (`:562`); `app.session.rename` (`:568`) | accent, muted, error, selectedBg |
| `theme-selector.ts` | themes | `new SelectList(themeItems, 10, getSelectListTheme(), THEME_SELECT_LIST_LAYOUT)` (`:38`) | — | accent (live preview via `OnPreview`) |
| `settings-selector.ts` | settings (nested) | `new SettingsList(...)` (`:111, :452`) + `new SelectList(...)` (`:161`) for submenus | — | accent, muted |
| `thinking-selector.ts` | thinking levels | `new SelectList(...)` (`:44`) | — | accent |
| `scoped-models-selector.ts` | models (enable/disable) | custom + `fuzzyFilter` (`:185`) | `app.models.save` (`:323`); enable/disable + reorder *«read fully at port time»* | accent, success, dim, warning |
| `config-selector.ts` | resources (grouped) | custom checkboxes | Space toggle *«read fully at port time»* | accent, muted, success, dim |
| `tree-selector.ts` | session tree entries | custom ASCII tree | `app.tree.filter.default` (`:940`), `app.tree.filter.all` (`:960`); fold/unfold + label-edit *«read fully»* | accent, warning, dim, muted, selectedBg |
| `oauth-selector.ts` | auth providers | custom + `fuzzyFilter(allProviders, name+id+authType)` (`:98`) | — | accent, success, muted, warning |
| `show-images-selector.ts` | Yes/No | `new SelectList(items, 5, …, SHOW_IMAGES_SELECT_LIST_LAYOUT)` (`:28`) | — | accent |
| `user-message-selector.ts` | user messages (fork source) | custom | — *«read fully»* | accent, muted |
| `extension-selector.ts` | string options (extension) | custom | optional `new CountdownTimer(...)` (`:49`) | accent |

## (C) Dialogs / inputs (read in full)

### login-dialog (`login-dialog.ts`, 197 lines)
- **Role:** OAuth/API-key modal. `LoginDialogComponent(tui, providerId, onComplete, providerNameOverride?, titleOverride?)`. Title = `"Login to {provider}"` (`:41`).
- **Structure** (`:44-67`): `DynamicBorder` → `Text(fg("accent", bold(title)), 1, 0)` → `contentContainer` → `DynamicBorder`. An `Input` (onSubmit resolves, onEscape cancels) is added into `contentContainer` by the show-* methods.
- **`showAuth(url, instructions?)`** (`:87-107`): clear; `Spacer(1)`; `Text(fg("accent", OSC8-linked url))`; `Text(fg("dim", OSC8-linked "Cmd+click to open" | "Ctrl+click to open"))` (`:93`); optional `Spacer(1)`+`Text(fg("warning", instructions))`; `exec("open"|"start"|"xdg-open" url)` (`:103-104`).
- **`showPrompt(message, placeholder?)`** (`:129-151`): `Spacer(1)`+`Text(fg("text", message))` + optional `Text(fg("dim","e.g., "+placeholder))` + `input` + `Text("(cancel, submit)" keyHints)`. Does **not** clear (preserves URL). Returns `Promise<string>`.
- **`showManualInput(prompt)`** (`:112-123`), **`showInfo(lines)`** (`:156-165`), **`showWaiting(message)`** (`:170-175`), **`showProgress(message)`** (`:180-183`).
- **`handleInput`** (`:185-195`): `tui.select.cancel` → cancel; else `input.handleInput`.
- **Theme slots:** `accent` (title/URL), `dim` (hints/waiting), `warning` (instructions), `text` (prompt message), `border`.
- **pi-tui:** `Container`, `Input`, `Text`, `Spacer`, `DynamicBorder`, OSC8 hyperlink.

### extension-editor (`extension-editor.ts`, 148 lines)
- **Role:** multi-line editor for extensions, with external-editor support. `ExtensionEditorComponent(tui, keybindings, title, prefill, onSubmit, onCancel, options?)`.
- **Structure** (`:58-92`): `DynamicBorder` → `Spacer(1)` → `Text(fg("accent", title))` → `Spacer(1)` → `Editor(getEditorTheme(), options)` (prefill; `onSubmit`→callback) → `Spacer(1)` → `Text(hint: submit + newline + cancel + [external editor if `$VISUAL`/`$EDITOR`])` → `Spacer(1)` → `DynamicBorder`.
- **`handleInput`** (`:95-111`): `tui.select.cancel` → cancel; `app.editor.external` → `openExternalEditor`; else `editor.handleInput`.
- **`openExternalEditor`** (`:113-146`): writes tmp file, `tui.stop()`, `spawnSync($VISUAL|$EDITOR, tmpfile)`, reads back, `tui.start()`, `requestRender(true)`.
- **pi-tui:** `Container`, `Editor`, `Text`, `Spacer`, `DynamicBorder`.

### extension-input (`extension-input.ts`, 88 lines)
- **Role:** single-line input, optional timeout. `ExtensionInputComponent(title, placeholder, onSubmit, onCancel, { tui?, timeout? })`.
- **Structure** (`:47-71`): `DynamicBorder` → `Spacer(1)` → `titleText(fg("accent", title))` → `Spacer(1)` → [optional `CountdownTimer(timeout, onTick=title "{title} ({s}s)", onExpire=cancel)`] → `input` → `Spacer(1)` → `Text(submit + cancel keyHints)` → `Spacer(1)` → `DynamicBorder`.
- **`handleInput`** (`:73-82`): `tui.select.confirm` or `"\n"` → submit; `tui.select.cancel` → cancel; else `input.handleInput`.
- **pi-tui:** `Container`, `Input`, `Text`, `Spacer`, `DynamicBorder`, `CountdownTimer`.

### custom-editor (`custom-editor.ts`, 81 lines)
- **Role:** the production editor — extends pi-tui `Editor` with app keybinding interception. `CustomEditor(tui, EditorTheme, keybindings, options?)`.
- **Additions** (`:9-16`): `actionHandlers: Map<AppKeybinding, () => void>` + `onAction(action, handler)`; optional `onEscape`, `onCtrlD`, `onPasteImage`, `onExtensionShortcut`.
- **`handleInput`** (`:30-79`): `onExtensionShortcut?` → `app.clipboard.pasteImage` → `app.interrupt` (**only if `!isShowingAutocomplete()`**; `onEscape ?? actionHandlers.get("app.interrupt")`) → `app.exit` (**only if `getText().length===0`**; `onCtrlD ?? handler`) → other `actionHandlers` (excluding interrupt/exit) → `super.handleInput(data)`.
- **pi-tui:** `components.Editor`, `tui.KeybindingsManager` (from `core.AppKeybindings`).

## Port notes
- Most selectors = a search `Input` + a `SelectList` (pi-tui) + scope state. Build a shared `selectorShell` helper (DynamicBorder+Spacer+Input+list) reused by all.
- `session-selector` and `tree-selector` are the complex ones (custom tree rendering, multi-mode) — port fresh; budget extra time in P5.
- `SelectList`/`SettingsList`/`SessionList` map to pi-tui `components.SelectList` (+ a `SettingsList`-style wrapper for nested settings).
- All key actions (`tui.select.*`, `tui.input.*`, `app.session.*`, `app.models.*`, `app.tree.*`, `app.tools.expand`, `app.editor.external`, `app.clipboard.pasteImage`, `app.interrupt`, `app.exit`) come from the reused `core.AppKeybindings` → `tui.KeybindingsManager`.
