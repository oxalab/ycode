package interactive

// model_selector.go ports reference components/model-selector.ts (verified —
// see mapping/13-selectors.md). Unlike the simple SelectList selectors, this is
// a CUSTOM selector: it owns its own list rendering (selected/unselected rows
// with provider badges + current ✓ + scroll indicator + model-name footer),
// a fuzzy-filtered search Input, and an all↔scoped scope toggle. It swaps into
// the editor container via swapInSelector (same mechanism as the simple
// selectors); Escape cancels, Enter selects, Tab toggles scope.
//
// Faithful to model-selector.ts:34-338. Go is synchronous, so loadModels runs
// inline (the reference awaits getAvailable). The async bits (requestRender
// after load) are unnecessary — construction happens before the first render.

import (
	"sort"
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// modelItem is one selectable model (reference ModelItem).
type modelItem struct {
	provider string
	id       string
	name     string
	model    llm.Model[llm.Api]
}

type modelScope int

const (
	scopeAll modelScope = iota
	scopeScoped
)

const modelMaxVisible = 10

// ModelSelector is the searchable model selector (model-selector.ts).
type ModelSelector struct {
	*tui.Container
	th *theme.Theme

	search  *components.Input
	listBox *tui.Container
	scopeEl *components.Text // mutable — refreshed on scope toggle

	current     llm.Model[llm.Api]
	haveCurrent bool
	settings    *core.SettingsManager

	all      []modelItem
	scoped   []modelItem
	active   []modelItem
	filtered []modelItem
	selected int
	scope    modelScope
	hasScope bool
	errMsg   string

	onSelect func(llm.Model[llm.Api])
	onCancel func()
	focused  bool
}

// NewModelSelector builds the selector. current may be zero-valued (no model).
func NewModelSelector(current llm.Model[llm.Api], haveCurrent bool, settings *core.SettingsManager,
	registry *core.ModelRegistry, scoped []core.ScopedModelEntry, th *theme.Theme,
	onSelect func(llm.Model[llm.Api]), onCancel func()) *ModelSelector {

	m := &ModelSelector{
		Container: &tui.Container{},
		th:        th,
		current:   current,
		haveCurrent: haveCurrent,
		settings:  settings,
		onSelect:  onSelect,
		onCancel:  onCancel,
	}
	m.loadModels(registry, scoped)

	// Static structure (model-selector.ts:85-123).
	m.AddChild(newDynamicBorder(fgStyle(th, "border")))
	m.AddChild(components.NewSpacer(1))

	if m.hasScope {
		m.scopeEl = components.NewText(m.scopeText(), 0, 0, nil)
		m.AddChild(m.scopeEl)
		m.AddChild(components.NewText(m.scopeHintText(), 0, 0, nil))
	} else {
		m.AddChild(components.NewText(th.Fg("warning",
			"Only showing models from configured providers. Use /login to add providers."), 0, 0, nil))
	}
	m.AddChild(components.NewSpacer(1))

	m.search = components.NewInput()
	m.search.OnSubmit = func(string) { m.confirmSelection() } // enter fallback (selector intercepts first)
	m.AddChild(m.search)
	m.AddChild(components.NewSpacer(1))

	m.listBox = &tui.Container{}
	m.AddChild(m.listBox)
	m.AddChild(components.NewSpacer(1))
	m.AddChild(newDynamicBorder(fgStyle(th, "border")))

	m.scope = scopeAll
	if m.hasScope {
		m.scope = scopeScoped // start on scoped when scoped models exist (:81)
	}
	m.active = m.scopedItems()
	m.applyFilter("")
	// Preselect the current model once (loadModels:178-180).
	if cur := m.currentIndex(); cur >= 0 {
		m.selected = cur
		m.rebuildList()
	}
	return m
}

// loadModels fetches available models + resolves scoped entries (loadModels,
// model-selector.ts:137-181).
func (m *ModelSelector) loadModels(registry *core.ModelRegistry, scoped []core.ScopedModelEntry) {
	avail, err := registry.GetAvailable()
	if err != nil {
		m.all = nil
		m.scoped = nil
		m.errMsg = err.Error()
		return
	}
	items := make([]modelItem, 0, len(avail))
	for _, mdl := range avail {
		items = append(items, modelItem{provider: string(mdl.Provider), id: mdl.ID, name: mdl.Name, model: *mdl})
	}
	m.all = m.sortItems(items)

	m.scoped = nil
	for _, se := range scoped {
		if mm := modelFromAny(se.Model); mm != nil {
			m.scoped = append(m.scoped, modelItem{provider: string(mm.Provider), id: mm.ID, name: mm.Name, model: *mm})
		}
	}
	m.hasScope = len(m.scoped) > 0
}

// sortItems: current first, then by provider (sortModels, :183-194).
func (m *ModelSelector) sortItems(items []modelItem) []modelItem {
	out := make([]modelItem, len(items))
	copy(out, items)
	sort.SliceStable(out, func(i, j int) bool {
		aCur, bCur := m.isCurrent(out[i]), m.isCurrent(out[j])
		if aCur != bCur {
			return aCur // current model first
		}
		return strings.ToLower(out[i].provider) < strings.ToLower(out[j].provider)
	})
	return out
}

func (m *ModelSelector) isCurrent(it modelItem) bool {
	return m.haveCurrent && string(m.current.Provider) == it.provider && m.current.ID == it.id
}

func (m *ModelSelector) scopedItems() []modelItem {
	if m.scope == scopeScoped && m.hasScope {
		return m.scoped
	}
	return m.all
}

// applyFilter fuzzy-filters the active set by the search query (filterModels,
// :218-228) and rebuilds the list. It only clamps the selection — it does NOT
// jump to the current model (that would fight the user's typing); the current
// model is preselected once at construction and on scope change.
func (m *ModelSelector) applyFilter(query string) {
	if q := strings.TrimSpace(query); q != "" {
		m.filtered = tui.FuzzyFilter(m.active, q, func(it modelItem) string {
			return it.id + " " + it.provider + " " + it.provider + "/" + it.id
		})
	} else {
		m.filtered = m.active
	}
	if m.selected > len(m.filtered)-1 {
		m.selected = max(0, len(m.filtered)-1)
	}
	m.rebuildList()
}

func (m *ModelSelector) currentIndex() int {
	for i, it := range m.filtered {
		if m.isCurrent(it) {
			return i
		}
	}
	return -1
}

// rebuildList renders the visible slice of filtered models (updateList, :230-285).
func (m *ModelSelector) rebuildList() {
	m.listBox.Clear()
	if len(m.filtered) == 0 {
		if m.errMsg != "" {
			for _, line := range strings.Split(m.errMsg, "\n") {
				m.listBox.AddChild(components.NewText(m.th.Fg("error", line), 0, 0, nil))
			}
		} else {
			m.listBox.AddChild(components.NewText(m.th.Fg("muted", "  No matching models"), 0, 0, nil))
		}
		return
	}

	start := m.selected - modelMaxVisible/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(m.filtered) - modelMaxVisible; start > maxStart {
		start = max(0, maxStart)
	}
	end := start + modelMaxVisible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}

	for i := start; i < end; i++ {
		it := m.filtered[i]
		isSel := i == m.selected
		isCur := m.isCurrent(it)
		var line string
		if isSel {
			line = m.th.Fg("accent", "→ "+it.id) + " " + m.th.Fg("muted", "["+it.provider+"]")
			if isCur {
				line += m.th.Fg("success", " ✓")
			}
		} else {
			line = "  " + it.id + " " + m.th.Fg("muted", "["+it.provider+"]")
			if isCur {
				line += m.th.Fg("success", " ✓")
			}
		}
		m.listBox.AddChild(components.NewText(line, 0, 0, nil))
	}

	if start > 0 || end < len(m.filtered) {
		scroll := "  (" + itoaModel(m.selected+1) + "/" + itoaModel(len(m.filtered)) + ")"
		m.listBox.AddChild(components.NewText(m.th.Fg("muted", scroll), 0, 0, nil))
	}

	// Footer: the selected model's full name (:281-284).
	if sel := m.filtered[m.selected]; sel.name != "" {
		m.listBox.AddChild(components.NewSpacer(1))
		m.listBox.AddChild(components.NewText(m.th.Fg("muted", "  Model Name: "+sel.name), 0, 0, nil))
	}
}

// scopeText / scopeHintText (model-selector.ts:196-204).
func (m *ModelSelector) scopeText() string {
	all := m.th.Fg("muted", "all")
	if m.scope == scopeAll {
		all = m.th.Fg("accent", "all")
	}
	sc := m.th.Fg("muted", "scoped")
	if m.scope == scopeScoped {
		sc = m.th.Fg("accent", "scoped")
	}
	return m.th.Fg("muted", "Scope: ") + all + m.th.Fg("muted", " | ") + sc
}

func (m *ModelSelector) scopeHintText() string {
	return keyHint(m.th, "tui.input.tab", "scope") + m.th.Fg("muted", " (all/scoped)")
}

// setScope toggles all↔scoped (setScope, :206-216).
func (m *ModelSelector) setScope(s modelScope) {
	if m.scope == s || !m.hasScope {
		return
	}
	m.scope = s
	m.active = m.scopedItems()
	if cur := m.currentIndex(); cur >= 0 {
		m.selected = cur
	} else {
		m.selected = 0
	}
	m.applyFilter(m.search.GetValue())
	if m.scopeEl != nil {
		m.scopeEl.SetText(m.scopeText())
	}
}

// confirmSelection fires onSelect for the highlighted model (handleSelect,
// :329-333): save default + callback.
func (m *ModelSelector) confirmSelection() {
	if len(m.filtered) == 0 {
		return
	}
	it := m.filtered[m.selected]
	if m.settings != nil {
		m.settings.SetDefaultModelAndProvider(it.provider, it.id)
	}
	if m.onSelect != nil {
		m.onSelect(it.model)
	}
}

// HandleInput: tab=scope, up/down=nav (wrap), enter=confirm, escape=cancel,
// else=search+filter (model-selector.ts:287-327).
func (m *ModelSelector) HandleInput(data string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.input.tab"):
		if m.hasScope {
			next := scopeAll
			if m.scope == scopeAll {
				next = scopeScoped
			}
			m.setScope(next)
		}
	case kb.Matches(data, "tui.select.up"):
		if len(m.filtered) == 0 {
			return
		}
		if m.selected == 0 {
			m.selected = len(m.filtered) - 1
		} else {
			m.selected--
		}
		m.rebuildList()
	case kb.Matches(data, "tui.select.down"):
		if len(m.filtered) == 0 {
			return
		}
		if m.selected == len(m.filtered)-1 {
			m.selected = 0
		} else {
			m.selected++
		}
		m.rebuildList()
	case kb.Matches(data, "tui.select.confirm"):
		m.confirmSelection()
	case kb.Matches(data, "tui.select.cancel"):
		if m.onCancel != nil {
			m.onCancel()
		}
	default:
		m.search.HandleInput(data)
		m.applyFilter(m.search.GetValue())
	}
}

// Focused / SetFocused implement tui.Focusable — propagate to the search Input
// so the hardware cursor tracks it (model-selector.ts:37-45).
func (m *ModelSelector) Focused() bool      { return m.focused }
func (m *ModelSelector) SetFocused(f bool)  { m.focused = f; m.search.SetFocused(f) }

// modelFromAny coerces a ScopedModelEntry.Model (any) to *llm.Model, handling
// both value and pointer storage.
func modelFromAny(v any) *llm.Model[llm.Api] {
	switch mm := v.(type) {
	case *llm.Model[llm.Api]:
		return mm
	case llm.Model[llm.Api]:
		return &mm
	}
	return nil
}

// itoaModel is a dependency-free int→string for the scroll indicator.
func itoaModel(n int) string {
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
