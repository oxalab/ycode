package components

import (
	"strings"

	"ycode/packages/tui"
)

const (
	selectDefaultPrimaryWidth = 32
	selectPrimaryGap          = 2
	selectMinDescriptionWidth = 10
)

// SelectItem is one selectable entry.
type SelectItem struct {
	Value       string
	Label       string
	Description string
}

// SelectListTheme styles each part of the list.
type SelectListTheme struct {
	SelectedPrefix tui.StyleFunc
	SelectedText   tui.StyleFunc
	Description    tui.StyleFunc
	ScrollInfo     tui.StyleFunc
	NoMatch        tui.StyleFunc
}

// SelectListTruncatePrimaryContext is passed to a custom truncate function.
type SelectListTruncatePrimaryContext struct {
	Text        string
	MaxWidth    int
	ColumnWidth int
	Item        SelectItem
	IsSelected  bool
}

// SelectListLayoutOptions controls column sizing.
type SelectListLayoutOptions struct {
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
	TruncatePrimary       func(SelectListTruncatePrimaryContext) string
}

// SelectList is a keyboard-navigable list with optional two-column layout.
type SelectList struct {
	items         []SelectItem
	filteredItems []SelectItem
	selectedIndex int
	maxVisible    int
	theme         SelectListTheme
	layout        SelectListLayoutOptions

	OnSelect            func(SelectItem)
	OnCancel            func()
	OnSelectionChange   func(SelectItem)
}

// NewSelectList returns a SelectList.
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout ...SelectListLayoutOptions) *SelectList {
	l := SelectListLayoutOptions{}
	if len(layout) > 0 {
		l = layout[0]
	}
	return &SelectList{items: items, filteredItems: items, maxVisible: maxVisible, theme: theme, layout: l}
}

// SetFilter filters items by value prefix (case-insensitive).
func (s *SelectList) SetFilter(filter string) {
	f := strings.ToLower(filter)
	s.filteredItems = nil
	for _, item := range s.items {
		if strings.HasPrefix(strings.ToLower(item.Value), f) {
			s.filteredItems = append(s.filteredItems, item)
		}
	}
	s.selectedIndex = 0
}

// SetSelectedIndex clamps and sets the selection.
func (s *SelectList) SetSelectedIndex(i int) {
	if len(s.filteredItems) == 0 {
		s.selectedIndex = 0
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(s.filteredItems) {
		i = len(s.filteredItems) - 1
	}
	s.selectedIndex = i
}

// GetSelectedItem returns the highlighted item.
func (s *SelectList) GetSelectedItem() (SelectItem, bool) {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.filteredItems) {
		return SelectItem{}, false
	}
	return s.filteredItems[s.selectedIndex], true
}

// Invalidate is a no-op.
func (s *SelectList) Invalidate() {}

// HandleInput navigates the list.
func (s *SelectList) HandleInput(data string) {
	kb := tui.GetKeybindings()
	if kb.Matches(data, "tui.select.up") {
		if s.selectedIndex == 0 {
			s.selectedIndex = len(s.filteredItems) - 1
		} else {
			s.selectedIndex--
		}
		s.notifySelectionChange()
	} else if kb.Matches(data, "tui.select.down") {
		if s.selectedIndex == len(s.filteredItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.notifySelectionChange()
	} else if kb.Matches(data, "tui.select.confirm") {
		if item, ok := s.GetSelectedItem(); ok && s.OnSelect != nil {
			s.OnSelect(item)
		}
	} else if kb.Matches(data, "tui.select.cancel") {
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
}

func (s *SelectList) notifySelectionChange() {
	if item, ok := s.GetSelectedItem(); ok && s.OnSelectionChange != nil {
		s.OnSelectionChange(item)
	}
}

// Render produces the visible list rows.
func (s *SelectList) Render(width int) []string {
	if len(s.filteredItems) == 0 {
		return []string{s.theme.NoMatch("  No matching commands")}
	}
	primaryCol := s.getPrimaryColumnWidth()
	start := s.selectedIndex - s.maxVisible/2
	if start < 0 {
		start = 0
	}
	maxStart := len(s.filteredItems) - s.maxVisible
	if maxStart < 0 {
		maxStart = 0
	}
	if start > maxStart {
		start = maxStart
	}
	end := start + s.maxVisible
	if end > len(s.filteredItems) {
		end = len(s.filteredItems)
	}
	var lines []string
	for i := start; i < end; i++ {
		item := s.filteredItems[i]
		isSel := i == s.selectedIndex
		desc := ""
		if item.Description != "" {
			desc = strings.ReplaceAll(strings.ReplaceAll(item.Description, "\r", " "), "\n", " ")
			desc = strings.TrimSpace(desc)
		}
		lines = append(lines, s.renderItem(item, isSel, width, desc, primaryCol))
	}
	if start > 0 || end < len(s.filteredItems) {
		scroll := "  (" + itoaSL(s.selectedIndex+1) + "/" + itoaSL(len(s.filteredItems)) + ")"
		lines = append(lines, s.theme.ScrollInfo(tui.TruncateToWidth(scroll, width-2, "", false)))
	}
	return lines
}

func (s *SelectList) renderItem(item SelectItem, isSel bool, width int, desc string, primaryCol int) string {
	prefix := "  "
	if isSel {
		prefix = "→ "
	}
	prefixW := tui.VisibleWidth(prefix)

	if desc != "" && width > 40 {
		effCol := primaryCol
		if effCol > width-prefixW-4 {
			effCol = width - prefixW - 4
		}
		if effCol < 1 {
			effCol = 1
		}
		maxPrimary := effCol - selectPrimaryGap
		if maxPrimary < 1 {
			maxPrimary = 1
		}
		tv := s.truncatePrimary(item, isSel, maxPrimary, effCol)
		tvW := tui.VisibleWidth(tv)
		spacing := effCol - tvW
		if spacing < 1 {
			spacing = 1
		}
		descStart := prefixW + tvW + spacing
		remaining := width - descStart - 2
		if remaining > selectMinDescriptionWidth {
			td := tui.TruncateToWidth(desc, remaining, "", false)
			if isSel {
				return s.theme.SelectedText(prefix + tv + strings.Repeat(" ", spacing) + td)
			}
			return prefix + tv + s.theme.Description(strings.Repeat(" ", spacing)+td)
		}
	}
	maxW := width - prefixW - 2
	if maxW < 1 {
		maxW = 1
	}
	tv := s.truncatePrimary(item, isSel, maxW, maxW)
	if isSel {
		return s.theme.SelectedText(prefix + tv)
	}
	return prefix + tv
}

func (s *SelectList) getPrimaryColumnWidth() int {
	minW, maxW := s.layout.MinPrimaryColumnWidth, s.layout.MaxPrimaryColumnWidth
	if minW == 0 {
		minW = selectDefaultPrimaryWidth
	}
	if maxW == 0 {
		maxW = selectDefaultPrimaryWidth
	}
	if minW > maxW {
		minW, maxW = maxW, minW
	}
	if minW < 1 {
		minW = 1
	}
	if maxW < 1 {
		maxW = 1
	}
	widest := 0
	for _, item := range s.filteredItems {
		w := tui.VisibleWidth(s.displayValue(item)) + selectPrimaryGap
		if w > widest {
			widest = w
		}
	}
	if widest < minW {
		return minW
	}
	if widest > maxW {
		return maxW
	}
	return widest
}

func (s *SelectList) truncatePrimary(item SelectItem, isSel bool, maxW, colW int) string {
	dv := s.displayValue(item)
	if s.layout.TruncatePrimary != nil {
		custom := s.layout.TruncatePrimary(SelectListTruncatePrimaryContext{
			Text: dv, MaxWidth: maxW, ColumnWidth: colW, Item: item, IsSelected: isSel,
		})
		return tui.TruncateToWidth(custom, maxW, "", false)
	}
	return tui.TruncateToWidth(dv, maxW, "", false)
}

func (s *SelectList) displayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

func itoaSL(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
