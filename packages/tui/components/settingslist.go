package components

import (
	"strings"

	"ycode/packages/tui"
)

// SettingItem is one row in a settings panel.
type SettingItem struct {
	ID          string
	Label       string
	Description string
	CurrentValue string
	Values      []string
	Submenu     func(currentValue string, done func(selected string)) tui.Component
}

// SettingsListTheme styles the settings list.
type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description tui.StyleFunc
	Cursor      string
	Hint        tui.StyleFunc
}

// SettingsListOptions enables optional features.
type SettingsListOptions struct {
	EnableSearch bool
}

// SettingsList is a settings panel with value cycling and submenus.
type SettingsList struct {
	items        []SettingItem
	filteredItems []SettingItem
	theme        SettingsListTheme
	selectedIndex int
	maxVisible    int
	onChange      func(id, newValue string)
	onCancel      func()
	searchEnabled bool
	searchInput   *Input

	submenuComponent  tui.Component
	submenuItemIndex  *int
}

// NewSettingsList returns a SettingsList.
func NewSettingsList(items []SettingItem, maxVisible int, theme SettingsListTheme, onChange func(id, newValue string), onCancel func(), options ...SettingsListOptions) *SettingsList {
	opts := SettingsListOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	s := &SettingsList{
		items: items, filteredItems: items, theme: theme,
		maxVisible: maxVisible, onChange: onChange, onCancel: onCancel,
		searchEnabled: opts.EnableSearch,
	}
	if s.searchEnabled {
		s.searchInput = NewInput()
	}
	return s
}

// UpdateValue updates an item's current value.
func (s *SettingsList) UpdateValue(id, newValue string) {
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].CurrentValue = newValue
			return
		}
	}
}

// Invalidate propagates to submenu.
func (s *SettingsList) Invalidate() {
	if inv, ok := s.submenuComponent.(tui.Invalidater); ok {
		inv.Invalidate()
	}
}

// Render produces the settings panel (or submenu if active).
func (s *SettingsList) Render(width int) []string {
	if s.submenuComponent != nil {
		return s.submenuComponent.Render(width)
	}
	return s.renderMainList(width)
}

func (s *SettingsList) renderMainList(width int) []string {
	var lines []string

	if s.searchEnabled && s.searchInput != nil {
		lines = append(lines, s.searchInput.Render(width)...)
		lines = append(lines, "")
	}

	display := s.items
	if s.searchEnabled {
		display = s.filteredItems
	}

	if len(s.items) == 0 {
		lines = append(lines, s.theme.Hint("  No settings available"))
		s.addHintLine(&lines, width)
		return lines
	}
	if len(display) == 0 {
		lines = append(lines, tui.TruncateToWidth(s.theme.Hint("  No matching settings"), width, "", false))
		s.addHintLine(&lines, width)
		return lines
	}

	start := s.selectedIndex - s.maxVisible/2
	if start < 0 {
		start = 0
	}
	maxStart := len(display) - s.maxVisible
	if maxStart < 0 {
		maxStart = 0
	}
	if start > maxStart {
		start = maxStart
	}
	end := start + s.maxVisible
	if end > len(display) {
		end = len(display)
	}

	maxLabelW := 0
	for _, item := range s.items {
		if w := tui.VisibleWidth(item.Label); w > maxLabelW {
			maxLabelW = w
		}
	}
	if maxLabelW > 30 {
		maxLabelW = 30
	}

	for i := start; i < end; i++ {
		item := display[i]
		isSel := i == s.selectedIndex
		prefix := "  "
		if isSel {
			prefix = s.theme.Cursor
		}
		prefixW := tui.VisibleWidth(prefix)

		labelPadded := item.Label + strings.Repeat(" ", maxInt(0, maxLabelW-tui.VisibleWidth(item.Label)))
		labelText := s.theme.Label(labelPadded, isSel)

		separator := "  "
		usedW := prefixW + maxLabelW + tui.VisibleWidth(separator)
		valueMaxW := width - usedW - 2
		if valueMaxW < 1 {
			valueMaxW = 1
		}
		valueText := s.theme.Value(tui.TruncateToWidth(item.CurrentValue, valueMaxW, "", false), isSel)
		lines = append(lines, tui.TruncateToWidth(prefix+labelText+separator+valueText, width, "", false))
	}

	if start > 0 || end < len(display) {
		scroll := "  (" + itoaSL(s.selectedIndex+1) + "/" + itoaSL(len(display)) + ")"
		lines = append(lines, s.theme.Hint(tui.TruncateToWidth(scroll, width-2, "", false)))
	}

	if sel, ok := s.at(s.selectedIndex, display); ok && sel.Description != "" {
		lines = append(lines, "")
		for _, wl := range tui.WrapTextWithAnsi(sel.Description, width-4) {
			lines = append(lines, s.theme.Description("  "+wl))
		}
	}

	s.addHintLine(&lines, width)
	return lines
}

// HandleInput processes key input.
func (s *SettingsList) HandleInput(data string) {
	if s.submenuComponent != nil {
		if h, ok := s.submenuComponent.(tui.InputHandler); ok {
			h.HandleInput(data)
		}
		return
	}

	kb := tui.GetKeybindings()
	display := s.items
	if s.searchEnabled {
		display = s.filteredItems
	}

	if kb.Matches(data, "tui.select.up") {
		if len(display) == 0 {
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(display) - 1
		} else {
			s.selectedIndex--
		}
	} else if kb.Matches(data, "tui.select.down") {
		if len(display) == 0 {
			return
		}
		if s.selectedIndex == len(display)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
	} else if kb.Matches(data, "tui.select.confirm") || data == " " {
		s.activateItem()
	} else if kb.Matches(data, "tui.select.cancel") {
		if s.onCancel != nil {
			s.onCancel()
		}
	} else if s.searchEnabled && s.searchInput != nil {
		sanitized := strings.ReplaceAll(data, " ", "")
		if sanitized == "" {
			return
		}
		s.searchInput.HandleInput(sanitized)
		s.applyFilter(s.searchInput.GetValue())
	}
}

func (s *SettingsList) activateItem() {
	item, ok := s.at(s.selectedIndex, nil)
	if !ok {
		return
	}
	if item.Submenu != nil {
		idx := s.selectedIndex
		s.submenuItemIndex = &idx
		s.submenuComponent = item.Submenu(item.CurrentValue, func(selected string) {
			if selected != "" {
				item.CurrentValue = selected
				if s.onChange != nil {
					s.onChange(item.ID, selected)
				}
			}
			s.closeSubmenu()
		})
	} else if len(item.Values) > 0 {
		curIdx := 0
		for i, v := range item.Values {
			if v == item.CurrentValue {
				curIdx = i
				break
			}
		}
		next := (curIdx + 1) % len(item.Values)
		item.CurrentValue = item.Values[next]
		if s.onChange != nil {
			s.onChange(item.ID, item.Values[next])
		}
	}
}

func (s *SettingsList) closeSubmenu() {
	s.submenuComponent = nil
	if s.submenuItemIndex != nil {
		s.selectedIndex = *s.submenuItemIndex
		s.submenuItemIndex = nil
	}
}

func (s *SettingsList) applyFilter(query string) {
	s.filteredItems = tui.FuzzyFilter(s.items, query, func(item SettingItem) string { return item.Label })
	s.selectedIndex = 0
}

func (s *SettingsList) addHintLine(lines *[]string, width int) {
	*lines = append(*lines, "")
	hint := "  Enter/Space to change · Esc to cancel"
	if s.searchEnabled {
		hint = "  Type to search · Enter/Space to change · Esc to cancel"
	}
	*lines = append(*lines, tui.TruncateToWidth(s.theme.Hint(hint), width, "", false))
}

func (s *SettingsList) at(index int, display []SettingItem) (SettingItem, bool) {
	if display == nil {
		if s.searchEnabled {
			display = s.filteredItems
		} else {
			display = s.items
		}
	}
	if index < 0 || index >= len(display) {
		return SettingItem{}, false
	}
	return display[index], true
}
