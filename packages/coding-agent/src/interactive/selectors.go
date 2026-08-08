package interactive

// selectors.go ports the SelectList-based selectors that swap into the editor
// container (reference components/{thinking,theme,show-images}-selector.ts —
// verified, see mapping/13-selectors.md). The shared shell is
// DynamicBorder → SelectList → DynamicBorder; the SelectList handles navigation
// (tui.select.up/down/confirm/cancel) itself, so the shell only forwards input
// and tracks focus.
//
// The more elaborate search-based selectors (model/session/oauth/scoped) and the
// overlay login dialog are P5b.

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// thinkingLevelDescriptions mirrors reference thinking-selector.ts:11-18.
var thinkingLevelDescriptions = map[sdk.ThinkingLevel]string{
	sdk.ThinkingOff:     "No reasoning",
	sdk.ThinkingMinimal: "Very brief reasoning (~1k tokens)",
	sdk.ThinkingLow:     "Light reasoning (~2k tokens)",
	sdk.ThinkingMedium:  "Moderate reasoning (~8k tokens)",
	sdk.ThinkingHigh:    "Deep reasoning (~16k tokens)",
	sdk.ThinkingXHigh:   "Maximum reasoning (~32k tokens)",
}

// thinkingLayout mirrors THINKING_SELECT_LIST_LAYOUT (thinking-selector.ts:6-9).
var thinkingLayout = components.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// simpleSelector is the shared shell for SelectList-based selectors swapped into
// the editor container: DynamicBorder → SelectList → DynamicBorder. It implements
// tui.InputHandler (forwards to the list) and tui.Focusable (focus tracking),
// mirroring the reference selectors' `extends Container implements Focusable,
// InputHandler`. *tui.Container supplies Render/AddChild/Clear.
type simpleSelector struct {
	*tui.Container
	list    *components.SelectList
	focused bool
}

// newSimpleSelector wraps a SelectList with top/bottom DynamicBorders.
func newSimpleSelector(list *components.SelectList, th *theme.Theme) *simpleSelector {
	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(list)
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	return &simpleSelector{Container: c, list: list}
}

// HandleInput forwards to the SelectList (up/down/confirm/cancel).
func (s *simpleSelector) HandleInput(data string) { s.list.HandleInput(data) }

// Focused / SetFocused implement tui.Focusable.
func (s *simpleSelector) Focused() bool      { return s.focused }
func (s *simpleSelector) SetFocused(f bool)  { s.focused = f }

// newThinkingSelector builds the thinking-level selector (thinking-selector.ts).
// onSelect/onCancel are wired to the list; the orchestrator closes the selector
// from those callbacks (swap editor back).
func newThinkingSelector(current sdk.ThinkingLevel, available []sdk.ThinkingLevel, th *theme.Theme,
	onSelect func(sdk.ThinkingLevel), onCancel func()) *simpleSelector {
	items := make([]components.SelectItem, len(available))
	curIdx := -1
	for i, lv := range available {
		items[i] = components.SelectItem{
			Value:       string(lv),
			Label:       string(lv),
			Description: thinkingLevelDescriptions[lv],
		}
		if lv == current {
			curIdx = i
		}
	}
	list := components.NewSelectList(items, len(items), buildSelectListTheme(th), thinkingLayout)
	if curIdx >= 0 {
		list.SetSelectedIndex(curIdx)
	}
	list.OnSelect = func(item components.SelectItem) { onSelect(sdk.ThinkingLevel(item.Value)) }
	list.OnCancel = onCancel
	return newSimpleSelector(list, th)
}

// newThemeSelector builds the theme selector (theme-selector.ts). The current
// theme is marked "(current)"; onPreview fires on every navigation for a live
// preview (restored on cancel by the caller via the original theme name).
func newThemeSelector(current string, th *theme.Theme,
	onSelect func(name string), onCancel func(), onPreview func(name string)) *simpleSelector {
	names := theme.GetAvailableThemes()
	items := make([]components.SelectItem, len(names))
	curIdx := -1
	for i, name := range names {
		desc := ""
		if name == current {
			desc = "(current)"
			curIdx = i
		}
		items[i] = components.SelectItem{Value: name, Label: name, Description: desc}
	}
	list := components.NewSelectList(items, len(items), buildSelectListTheme(th))
	if curIdx >= 0 {
		list.SetSelectedIndex(curIdx)
	}
	list.OnSelect = func(item components.SelectItem) { onSelect(item.Value) }
	list.OnCancel = onCancel
	if onPreview != nil {
		list.OnSelectionChange = func(item components.SelectItem) { onPreview(item.Value) }
	}
	return newSimpleSelector(list, th)
}
