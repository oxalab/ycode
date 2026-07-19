# Components 24 — SettingsList

- **TS source:** [`settings-list.ts`](../../../reference/tui/src/components/settings-list.ts) (250 LOC)
- **Go target:** `components/settingslist.go`
- **Package:** `components`
- **Depends on:** `tui` (`FuzzyFilter`, `Keybindings`, `VisibleWidth`,
  `TruncateToWidth`, `WrapTextWithAnsi`), `components.Input`.

Settings panel: label/value rows with value cycling, submenus, and optional search.

---

## API

| TS | Go |
|---|---|
| `interface SettingItem { id, label, description?, currentValue, values?, submenu? }` | `type SettingItem struct{ ID, Label, Description, CurrentValue string; Values []string; Submenu func(current string, done func(selected string)) tui.Component }` |
| `interface SettingsListTheme { label(text,selected), value(text,selected), description, cursor, hint }` | `type SettingsListTheme struct{ Label func(string,bool) string; Value func(string,bool) string; Description, Hint StyleFunc; Cursor string }` |
| `interface SettingsListOptions { enableSearch? }` | `type SettingsListOptions struct{ EnableSearch bool }` |
| `class SettingsList` | `type SettingsList struct{...}` |
| `new SettingsList(items, maxVisible, theme, onChange, onCancel, options={})` | `NewSettingsList(...)` |
| `updateValue(id, newValue)` | `UpdateValue(id, newValue string)` |
| `invalidate()` (submenu) | `Invalidate()` |
| `render(width)` | `Render(width)` |
| `handleInput(data)` | `HandleInput(data)` |

## Behavior

- **Submenu active** → render + delegate all input to `submenuComponent`; its
  `onCancel`/`done` closes it (`closeSubmenu` restores selection).
- **Search mode** (`enableSearch`): embeds an `Input`; rendered above the list; typed
  chars (spaces stripped) filter via `FuzzyFilter(items, q, label)`.
- **List render:** label padded to `min(30, maxLabelWidth)`; value truncated to
  remaining width; description (wrapped) below the selected row; hint line; scroll
  indicator. Windowed `maxVisible`.
- **Input:** up/down wrap; enter/space → `activateItem` (open submenu OR cycle
  `values`); escape → `OnCancel`; in search mode, other keys feed the search input.

## Submenu callback seam
TS submenu signature: `(currentValue, done) => Component` where `done(selected?)`
commits + closes. Keep this exact seam in Go (a `func(string, func(string)) tui.Component`)
so existing submenu builders port unchanged. `onChange(id, newValue)` fires on cycle
or submenu commit.

## Tests to port
- No standalone settings-list test. Behavior is exercised via the app. Add focused
  tests for: value cycling, submenu open/commit/cancel/restore-selection, search
  filtering, description wrapping + alignment.
