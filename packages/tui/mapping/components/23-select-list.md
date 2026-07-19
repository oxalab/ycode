# Components 23 — SelectList

- **TS source:** [`select-list.ts`](../../../reference/tui/src/components/select-list.ts) (229 LOC)
- **Go target:** `components/selectlist.go`
- **Package:** `components`
- **Depends on:** `tui.Keybindings`, `tui.VisibleWidth`, `tui.TruncateToWidth`.
- **Used by:** `Editor` (autocomplete dropdown), `SettingsList` (via submenus).

Keyboard-navigable list with primary/description two-column layout + scroll info.

---

## API

| TS | Go |
|---|---|
| `interface SelectItem { value, label, description? }` | `type SelectItem struct{ Value, Label, Description string }` |
| `interface SelectListTheme { selectedPrefix, selectedText, description, scrollInfo, noMatch }` | `type SelectListTheme struct{ SelectedPrefix, SelectedText, Description, ScrollInfo, NoMatch StyleFunc }` |
| `interface SelectListTruncatePrimaryContext` | `type TruncatePrimaryContext struct{ Text string; MaxWidth, ColumnWidth int; Item SelectItem; IsSelected bool }` |
| `interface SelectListLayoutOptions { minPrimaryColumnWidth?, maxPrimaryColumnWidth?, truncatePrimary? }` | `type SelectListLayoutOptions struct{ MinPrimaryColumnWidth, MaxPrimaryColumnWidth int; TruncatePrimary func(TruncatePrimaryContext) string }` |
| `class SelectList` | `type SelectList struct{...}` |
| `new SelectList(items, maxVisible, theme, layout={})` | `NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout ...SelectListLayoutOptions)` |
| callbacks `onSelect/onCancel/onSelectionChange` | fields `OnSelect func(SelectItem)`, `OnCancel func()`, `OnSelectionChange func(SelectItem)` |
| `setFilter(filter)` (case-insensitive prefix; resets index) | `SetFilter(string)` |
| `setSelectedIndex(i)` (clamped) | `SetSelectedIndex(int)` |
| `invalidate()` (no-op) | `Invalidate()` |
| `render(width)` | `Render(width)` |
| `handleInput(data)` | `HandleInput(data)` |
| `getSelectedItem()` | `GetSelectedItem() (SelectItem, bool)` |

## Behavior

- **Filter:** `item.value` lowercase `startsWith` filter lowercase.
- **Visible window:** `startIndex = clamp(selected - floor(maxVisible/2), 0,
  len-maxVisible)`; render `maxVisible` items.
- **Row layout** (`renderItem`): prefix `→ ` (selected) / `  `; if description and
  width>40, two-column with `primaryColumnWidth` (computed from widest primary,
  clamped to layout bounds, default 32, gap 2); else truncate primary to `width-4`.
  Selected row → `theme.selectedText(whole row)`; unselected → primary + styled
  description.
- **Scroll indicator** `(i+1/len)` when window doesn't show all, truncated to width.
- **Input:** up/down **wrap around**; enter → `OnSelect`; escape/ctrl+c → `OnCancel`.
  All via `Keybindings.Matches(data, "tui.select.*")`.
- **Custom truncation:** `layout.truncatePrimary` hook (used by Editor for slash
  commands).

## Tests to port
- `select-list.test.ts` (116) — multiline-description collapse, description alignment
  when primary truncated, min/max primary column width, custom `truncatePrimary`
  preserving alignment. Port assertions on visible-column offsets.
