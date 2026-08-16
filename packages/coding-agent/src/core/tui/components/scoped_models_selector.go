package components

import (
	"strings"

	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	llm "ycode/packages/llm/src"
)

// enabledIds models the enabled-model set: nil means "all enabled" (no
// explicit filter); a non-nil slice is an explicit, ordered list (order
// matters — it's the Ctrl+P cycling order).
type enabledIds []string

// scopedModelItem is one row: provider/id + the underlying model + enabled flag.
type scopedModelItem struct {
	fullID  string // "provider/id"
	id      string
	provider string
	name    string
	enabled bool
}

// ScopedModelsConfig is the input snapshot: every available model plus the
// current enabled set (nil = all enabled).
type ScopedModelsConfig struct {
	AllModels      []*llm.Model[llm.Api]
	EnabledModelIDs enabledIds // nil = all enabled
}

// ScopedModelsCallbacks reports changes to the App.
//
// OnChange fires on every toggle/reorder/clear/enable-all (session-only, no
// persist). OnPersist fires when the user saves with Ctrl+S.
type ScopedModelsCallbacks struct {
	OnChange  func(enabled []string) tea.Cmd
	OnPersist func(enabled []string) tea.Cmd
	OnCancel  func() tea.Cmd
}

// ScopedModelsSelectorComponent lets the user enable/disable models for
// Ctrl+P cycling and reorder them. Changes are session-only until explicitly
// saved with Ctrl+S.
//
// Port of reference/coding-agent/src/modes/interactive/components/scoped-models-selector.ts
type ScopedModelsSelectorComponent struct {
	theme *theme.Theme
	width int

	modelsByID map[string]*llm.Model[llm.Api]
	allIDs     []string
	enabled    enabledIds // nil = all enabled
	isDirty    bool

	items    []scopedModelItem // sorted display list
	filtered []int             // indices into items
	cursor   int

	search searchInput
	cb     ScopedModelsCallbacks
}

// NewScopedModelsSelectorComponent creates a new scoped models selector.
func NewScopedModelsSelectorComponent(
	t *theme.Theme,
	config ScopedModelsConfig,
	callbacks ScopedModelsCallbacks,
) *ScopedModelsSelectorComponent {
	s := &ScopedModelsSelectorComponent{
		theme:      t,
		modelsByID: make(map[string]*llm.Model[llm.Api], len(config.AllModels)),
		cb:         callbacks,
	}
	for _, m := range config.AllModels {
		if m == nil {
			continue
		}
		fullID := string(m.Provider) + "/" + m.ID
		s.modelsByID[fullID] = m
		s.allIDs = append(s.allIDs, fullID)
	}
	if config.EnabledModelIDs == nil {
		s.enabled = nil
	} else {
		s.enabled = append(enabledIds(nil), config.EnabledModelIDs...)
	}
	s.refresh()
	return s
}

// SetWidth updates the available width.
func (s *ScopedModelsSelectorComponent) SetWidth(width int) { s.width = width }

// Init satisfies tea.Model.
func (s *ScopedModelsSelectorComponent) Init() tea.Cmd { return nil }

// Update routes keyboard input: navigation, toggles, reorder, save, cancel.
func (s *ScopedModelsSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	str := keyMsg.String()

	switch str {
	case "up", "k":
		s.moveCursor(-1)
		return s, nil
	case "down", "j":
		s.moveCursor(1)
		return s, nil
	case "enter":
		return s, s.toggleFocused()
	case "ctrl+a":
		return s, s.enableAllFocused()
	case "ctrl+x":
		return s, s.clearAllFocused()
	case "ctrl+t":
		return s, s.toggleProviderFocused()
	case "ctrl+o", "[": // reorder up
		return s, s.reorderFocused(-1)
	case "ctrl+p", "]": // reorder down
		return s, s.reorderFocused(1)
	case "ctrl+s":
		return s, s.save()
	case "escape":
		if s.cb.OnCancel != nil {
			return s, s.cb.OnCancel()
		}
		return s, nil
	case "ctrl+c":
		if s.search.value != "" {
			s.search.value = ""
			s.refresh()
			return s, nil
		}
		if s.cb.OnCancel != nil {
			return s, s.cb.OnCancel()
		}
		return s, nil
	}

	if s.search.handleKey(str) {
		s.refresh()
	}
	return s, nil
}

// View renders the header + search + list + footer hint.
func (s ScopedModelsSelectorComponent) View() string {
	var b strings.Builder
	b.WriteString(s.theme.Fg("accent", s.theme.Bold("Model Configuration")) + "\n")
	b.WriteString(s.theme.Fg("muted", "  Session-only. Ctrl+S to save to settings.") + "\n\n")
	b.WriteString(s.theme.Dim("  Search: ") + s.search.value + "\n\n")

	if len(s.filtered) == 0 {
		b.WriteString(s.theme.Fg("muted", "  No matching models"))
		return b.String()
	}

	n := len(s.filtered)
	start := s.cursor - 8/2
	if start < 0 {
		start = 0
	}
	end := start + 8
	if end > n {
		end = n
		start = end - 8
		if start < 0 {
			start = 0
		}
	}

	allEnabled := s.enabled == nil
	for i := start; i < end; i++ {
		item := s.items[s.filtered[i]]
		selected := i == s.cursor
		b.WriteString(s.renderRow(item, selected, allEnabled))
		b.WriteString("\n")
	}
	if start > 0 || end < n {
		b.WriteString(s.theme.Fg("muted", "  ("+itoa(s.cursor+1)+"/"+itoa(n)+")") + "\n")
	}

	// Model name preview line.
	item := s.items[s.filtered[s.cursor]]
	if item.name != "" {
		b.WriteString("\n" + s.theme.Fg("muted", "  Model Name: "+item.name) + "\n")
	}

	b.WriteString("\n" + s.footerText())
	return b.String()
}

// --- Internal: state helpers (pure, mirroring the TS functions) ---

// scopedIsEnabled reports whether an id is enabled.
func scopedIsEnabled(ids enabledIds, id string) bool {
	if ids == nil {
		return true
	}
	for _, e := range ids {
		if e == id {
			return true
		}
	}
	return false
}

// scopedToggle toggles a single id. nil (all-enabled) → start with only id.
func scopedToggle(ids enabledIds, id string) enabledIds {
	if ids == nil {
		return enabledIds{id}
	}
	out := make(enabledIds, 0, len(ids))
	found := false
	for _, e := range ids {
		if e == id {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, id)
	}
	return out
}

// scopedEnableAll enables every id (or just targets). nil stays nil.
func scopedEnableAll(ids enabledIds, allIDs, targets []string) enabledIds {
	if ids == nil {
		return nil
	}
	set := make(map[string]bool, len(ids))
	for _, e := range ids {
		set[e] = true
	}
	for _, id := range targets {
		if !set[id] {
			ids = append(ids, id)
			set[id] = true
		}
	}
	if len(ids) == len(allIDs) {
		return nil
	}
	return ids
}

// scopedClearAll removes targets (or everything if nil targets) from the
// enabled set.
func scopedClearAll(ids enabledIds, allIDs, targets []string) enabledIds {
	if ids == nil {
		if targets == nil {
			return enabledIds{}
		}
		// Disable only the targets; keep the rest.
		out := make(enabledIds, 0, len(allIDs))
		targetSet := make(map[string]bool, len(targets))
		for _, t := range targets {
			targetSet[t] = true
		}
		for _, id := range allIDs {
			if !targetSet[id] {
				out = append(out, id)
			}
		}
		return out
	}
	targetSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		targetSet[t] = true
	}
	out := make(enabledIds, 0, len(ids))
	for _, id := range ids {
		if !targetSet[id] {
			out = append(out, id)
		}
	}
	return out
}

// scopedMove swaps an enabled id's position by delta.
func scopedMove(ids enabledIds, id string, delta int) enabledIds {
	if ids == nil {
		return nil
	}
	idx := -1
	for i, e := range ids {
		if e == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ids
	}
	newIdx := idx + delta
	if newIdx < 0 || newIdx >= len(ids) {
		return ids
	}
	ids[idx], ids[newIdx] = ids[newIdx], ids[idx]
	return ids
}

// getSortedIDs returns the display order: enabled-first (in enabled order),
// then the rest.
func getSortedIDs(ids enabledIds, allIDs []string) []string {
	if ids == nil {
		return allIDs
	}
	enabledSet := make(map[string]bool, len(ids))
	for _, e := range ids {
		enabledSet[e] = true
	}
	out := make([]string, 0, len(allIDs))
	out = append(out, ids...)
	for _, id := range allIDs {
		if !enabledSet[id] {
			out = append(out, id)
		}
	}
	return out
}

// --- Internal: refresh / list building ---

func (s *ScopedModelsSelectorComponent) refresh() {
	s.items = s.items[:0]
	for _, id := range getSortedIDs(s.enabled, s.allIDs) {
		m, ok := s.modelsByID[id]
		if !ok {
			continue // stale id (e.g. after logout)
		}
		s.items = append(s.items, scopedModelItem{
			fullID:   id,
			id:       m.ID,
			provider: string(m.Provider),
			name:     m.Name,
			enabled:  scopedIsEnabled(s.enabled, id),
		})
	}
	s.applyFilter()
}

func (s *ScopedModelsSelectorComponent) applyFilter() {
	q := strings.ToLower(s.search.value)
	s.filtered = s.filtered[:0]
	for i, item := range s.items {
		hay := item.id + " " + item.provider + " " + item.name
		if q == "" || FuzzyMatch(hay, q) {
			s.filtered = append(s.filtered, i)
		}
	}
	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// --- Internal: actions ---

func (s *ScopedModelsSelectorComponent) moveCursor(delta int) {
	n := len(s.filtered)
	if n == 0 {
		s.cursor = 0
		return
	}
	s.cursor += delta
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= n {
		s.cursor = n - 1
	}
}

func (s *ScopedModelsSelectorComponent) focusedItem() (scopedModelItem, bool) {
	if s.cursor < 0 || s.cursor >= len(s.filtered) {
		return scopedModelItem{}, false
	}
	return s.items[s.filtered[s.cursor]], true
}

func (s *ScopedModelsSelectorComponent) toggleFocused() tea.Cmd {
	item, ok := s.focusedItem()
	if !ok {
		return nil
	}
	s.enabled = scopedToggle(s.enabled, item.fullID)
	s.isDirty = true
	s.refresh()
	return s.notify()
}

func (s *ScopedModelsSelectorComponent) enableAllFocused() tea.Cmd {
	targets := s.filteredTargetIDs()
	s.enabled = scopedEnableAll(s.enabled, s.allIDs, targets)
	s.isDirty = true
	s.refresh()
	return s.notify()
}

func (s *ScopedModelsSelectorComponent) clearAllFocused() tea.Cmd {
	targets := s.filteredTargetIDs()
	s.enabled = scopedClearAll(s.enabled, s.allIDs, targets)
	s.isDirty = true
	s.refresh()
	return s.notify()
}

func (s *ScopedModelsSelectorComponent) toggleProviderFocused() tea.Cmd {
	item, ok := s.focusedItem()
	if !ok {
		return nil
	}
	var providerIDs []string
	for _, id := range s.allIDs {
		if m := s.modelsByID[id]; m != nil && string(m.Provider) == item.provider {
			providerIDs = append(providerIDs, id)
		}
	}
	allOn := true
	for _, id := range providerIDs {
		if !scopedIsEnabled(s.enabled, id) {
			allOn = false
			break
		}
	}
	if allOn {
		s.enabled = scopedClearAll(s.enabled, s.allIDs, providerIDs)
	} else {
		s.enabled = scopedEnableAll(s.enabled, s.allIDs, providerIDs)
	}
	s.isDirty = true
	s.refresh()
	return s.notify()
}

func (s *ScopedModelsSelectorComponent) reorderFocused(delta int) tea.Cmd {
	if s.enabled == nil {
		return nil
	}
	item, ok := s.focusedItem()
	if !ok || !scopedIsEnabled(s.enabled, item.fullID) {
		return nil
	}
	curIdx := -1
	for i, e := range s.enabled {
		if e == item.fullID {
			curIdx = i
			break
		}
	}
	newIdx := curIdx + delta
	if curIdx < 0 || newIdx < 0 || newIdx >= len(s.enabled) {
		return nil
	}
	s.enabled = scopedMove(s.enabled, item.fullID, delta)
	s.isDirty = true
	s.cursor += delta
	s.refresh()
	return s.notify()
}

func (s *ScopedModelsSelectorComponent) save() tea.Cmd {
	s.isDirty = false
	if s.cb.OnPersist != nil {
		return s.cb.OnPersist([]string(copyEnabled(s.enabled)))
	}
	return nil
}

func (s *ScopedModelsSelectorComponent) notify() tea.Cmd {
	if s.cb.OnChange != nil {
		return s.cb.OnChange([]string(copyEnabled(s.enabled)))
	}
	return nil
}

// copyEnabled returns a defensive copy (or nil) of the enabled set.
func copyEnabled(ids enabledIds) enabledIds {
	if ids == nil {
		return nil
	}
	return append(enabledIds(nil), ids...)
}

// filteredTargetIDs returns the ids of the currently filtered rows, or nil if
// there's no search (meaning "all").
func (s *ScopedModelsSelectorComponent) filteredTargetIDs() []string {
	if s.search.value == "" {
		return nil
	}
	out := make([]string, 0, len(s.filtered))
	for _, idx := range s.filtered {
		out = append(out, s.items[idx].fullID)
	}
	return out
}

// --- Internal: rendering ---

func (s ScopedModelsSelectorComponent) renderRow(item scopedModelItem, selected, allEnabled bool) string {
	marker := "  "
	idText := item.id
	if selected {
		marker = s.theme.Fg("accent", "→ ")
		idText = s.theme.Fg("accent", item.id)
	}
	provider := s.theme.Fg("muted", " ["+item.provider+"]")

	status := ""
	if !allEnabled {
		if item.enabled {
			status = s.theme.Fg("success", " ✓")
		} else {
			status = s.theme.Fg("dim", " ✗")
		}
	}
	return marker + idText + provider + status
}

// footerText renders the hint line, with an "(unsaved)" tag when dirty.
func (s ScopedModelsSelectorComponent) footerText() string {
	enabledCount := len(s.allIDs)
	allEnabled := s.enabled == nil
	if !allEnabled {
		enabledCount = len(s.enabled)
	}
	countText := itoa(enabledCount) + "/" + itoa(len(s.allIDs)) + " enabled"
	if allEnabled {
		countText = "all enabled"
	}
	parts := []string{"Enter toggle", "Ctrl+A all", "Ctrl+X clear", "Ctrl+T provider", "Ctrl+O/P reorder", "Ctrl+S save", countText}
	line := s.theme.Fg("dim", "  "+strings.Join(parts, " · "))
	if s.isDirty {
		line += " " + s.theme.Fg("warning", "(unsaved)")
	}
	return line
}
