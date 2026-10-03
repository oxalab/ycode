package components

import (
	"strconv"
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	llm "ycode/packages/llm/src"
)

// ModelItem is one row in the model selector: provider + id + the underlying model.
type ModelItem struct {
	Provider string
	ID       string
	Model    *llm.Model[llm.Api]
}

// modelScope toggles between all configured models and the scoped subset.
type modelScope int

const (
	modelScopeAll modelScope = iota
	modelScopeScoped
)

// ModelSelectorComponent renders a searchable picker for available models,
// with an all/scoped toggle. Built on the generic Selector.
//
// Port of reference/coding-agent/src/modes/interactive/components/model-selector.ts
type ModelSelectorComponent struct {
	theme          *theme.Theme
	width          int
	settingsMgr    *core.SettingsManager
	modelRegistry  *core.ModelRegistry
	currentModel   *llm.Model[llm.Api]
	scopedModels   []core.ScopedModelEntry

	selector Selector[ModelItem]

	// model pools
	allModels    []ModelItem
	scopedItems  []ModelItem
	activeModels []ModelItem

	// errorMessage surfaces registry load failures (e.g. bad models.json);
	// shown even when the model list is non-empty, matching the reference.
	errorMessage string

	scope modelScope
}

// ModelSelectorCallbacks holds the result handlers supplied by the App.
type ModelSelectorCallbacks struct {
	OnSelect func(model llm.Model[llm.Api]) tea.Cmd
	OnCancel func() tea.Cmd
}

// NewModelSelectorComponent creates a new model selector. It loads models
// synchronously from the registry (the registry is already populated).
func NewModelSelectorComponent(
	t *theme.Theme,
	currentModel *llm.Model[llm.Api],
	settingsMgr *core.SettingsManager,
	modelRegistry *core.ModelRegistry,
	scopedModels []core.ScopedModelEntry,
	callbacks ModelSelectorCallbacks,
) *ModelSelectorComponent {
	m := &ModelSelectorComponent{
		theme:         t,
		settingsMgr:   settingsMgr,
		modelRegistry: modelRegistry,
		currentModel:  currentModel,
		scopedModels:  scopedModels,
	}

	// Default scope: scoped if scoped models exist, else all.
	if len(scopedModels) > 0 {
		m.scope = modelScopeScoped
	}

	m.loadModels()

	// Build the underlying selector with our filter/render/callbacks.
	m.selector = NewSelector[ModelItem](
		m.activeModels,
		t,
		m.filterItem,
		m.renderItem,
		func(item ModelItem) tea.Cmd {
			if item.Model != nil {
				// Persist the new default model.
				if settingsMgr != nil {
					settingsMgr.SetDefaultModelAndProvider(item.Provider, item.ID)
				}
				if callbacks.OnSelect != nil {
					return callbacks.OnSelect(*item.Model)
				}
			}
			return nil
		},
		callbacks.OnCancel,
		SelectorOptions[ModelItem]{
			Title:         "Select Model",
			EmptyText:     "No matching models",
			PreselectIndex: m.findCurrentIndex(),
			FooterFn:      m.renderFooter,
		},
	)
	m.selector.SetIsSelectedFn(m.isCurrent)

	return m
}

// SetWidth updates the available width.
func (m *ModelSelectorComponent) SetWidth(width int) {
	m.width = width
	m.selector.SetWidth(width)
}

// Init satisfies tea.Model.
func (m *ModelSelectorComponent) Init() tea.Cmd { return m.selector.Init() }

// Update forwards messages to the selector, intercepting the scope toggle (tab).
func (m *ModelSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		// Tab toggles all/scoped scope (only when scoped models exist).
		if keyMsg.String() == "tab" && len(m.scopedItems) > 0 {
			m.toggleScope()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.selector, cmd = m.selector.Update(msg)
	return m, cmd
}

// View renders the selector with a scope header line.
func (m ModelSelectorComponent) View() string {
	width := m.width
	if width <= 0 {
		width = 60
	}

	// Build the scope header.
	var header string
	if len(m.scopedItems) > 0 {
		allText := m.theme.Fg("muted", "all")
		scopedText := m.theme.Fg("muted", "scoped")
		if m.scope == modelScopeAll {
			allText = m.theme.Fg("accent", "all")
		} else {
			scopedText = m.theme.Fg("accent", "scoped")
		}
		header = m.theme.Fg("muted", "Scope: ") + allText + m.theme.Fg("muted", " | ") + scopedText +
			m.theme.Fg("muted", "  (tab to toggle)")
	} else {
		header = m.theme.Fg("warning", "Only showing models from configured providers. Use /login to add providers.")
	}

	return header + "\n" + m.selector.View()
}

// --- Internal ---

// loadModels populates allModels and scopedItems from the registry.
func (m *ModelSelectorComponent) loadModels() {
	if m.modelRegistry == nil {
		m.activeModels = nil
		return
	}

	m.modelRegistry.Refresh()

	// Check for models.json errors; built-in models still work when it fails.
	m.errorMessage = m.modelRegistry.GetError()

	available, err := m.modelRegistry.GetAvailable()
	if err != nil || len(available) == 0 {
		m.allModels = nil
	} else {
		items := make([]ModelItem, 0, len(available))
		for _, mdl := range available {
			items = append(items, ModelItem{
				Provider: string(mdl.Provider),
				ID:       mdl.ID,
				Model:    mdl,
			})
		}
		m.sortModels(items)
		m.allModels = items
	}

	// Build scoped items (refresh each scoped model from the registry).
	m.scopedItems = make([]ModelItem, 0, len(m.scopedModels))
	for _, scoped := range m.scopedModels {
		var provider, id string
		switch mm := scoped.Model.(type) {
		case *llm.Model[llm.Api]:
			provider = string(mm.Provider)
			id = mm.ID
		case llm.Model[llm.Api]:
			provider = string(mm.Provider)
			id = mm.ID
		}
		if provider == "" || id == "" {
			continue
		}
		refreshed := m.modelRegistry.Find(provider, id)
		if refreshed != nil {
			m.scopedItems = append(m.scopedItems, ModelItem{
				Provider: provider,
				ID:       id,
				Model:    refreshed,
			})
		}
	}

	if m.scope == modelScopeScoped {
		m.activeModels = m.scopedItems
	} else {
		m.activeModels = m.allModels
	}
}

// sortModels sorts: current model first, then by provider.
func (m *ModelSelectorComponent) sortModels(items []ModelItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			if modelsLess(m.currentModel, items[j-1], items[j]) {
				items[j-1], items[j] = items[j], items[j-1]
			} else {
				break
			}
		}
	}
}

// modelsLess defines the model sort order: current model first, then provider.
func modelsLess(current *llm.Model[llm.Api], a, b ModelItem) bool {
	aCurrent := modelsEqual(current, a.Model)
	bCurrent := modelsEqual(current, b.Model)
	if aCurrent && !bCurrent {
		return true
	}
	if !aCurrent && bCurrent {
		return false
	}
	return a.Provider < b.Provider
}

// modelsEqual compares two models by provider + id.
func modelsEqual(a, b *llm.Model[llm.Api]) bool {
	if a == nil || b == nil {
		return false
	}
	return string(a.Provider) == string(b.Provider) && a.ID == b.ID
}

// findCurrentIndex returns the index of the current model in activeModels,
// or 0 if not found.
func (m *ModelSelectorComponent) findCurrentIndex() int {
	for i, item := range m.activeModels {
		if modelsEqual(m.currentModel, item.Model) {
			return i
		}
	}
	return 0
}

// isCurrent reports whether an item is the currently-active model.
func (m *ModelSelectorComponent) isCurrent(item ModelItem) bool {
	return modelsEqual(m.currentModel, item.Model)
}

// toggleScope switches between all/scoped and refreshes the selector's items.
func (m *ModelSelectorComponent) toggleScope() {
	if len(m.scopedItems) == 0 {
		return
	}
	if m.scope == modelScopeAll {
		m.scope = modelScopeScoped
		m.activeModels = m.scopedItems
	} else {
		m.scope = modelScopeAll
		m.activeModels = m.allModels
	}
	m.selector.SetItems(m.activeModels)
}

// filterItem matches an item against the query across id/provider.
func (m *ModelSelectorComponent) filterItem(item ModelItem, query string) bool {
	return FuzzyMatchAny(query,
		item.ID,
		item.Provider,
		item.Provider+"/"+item.ID,
	)
}

// renderItem renders one model row: "id [provider]".
func (m *ModelSelectorComponent) renderItem(item ModelItem, selected bool) string {
	idText := item.ID
	providerBadge := m.theme.Fg("muted", "["+item.Provider+"]")
	if selected {
		idText = m.theme.Fg("accent", item.ID)
		providerBadge = m.theme.Fg("muted", "["+item.Provider+"]")
	}
	return idText + " " + providerBadge
}

// registryError returns the registry's current load error (test visibility).
func (m *ModelSelectorComponent) registryError() string {
	if m.modelRegistry == nil {
		return ""
	}
	return m.modelRegistry.GetError()
}

// renderFooter renders the trailing lines of the list: error lines when the
// registry failed to load, otherwise a muted footer naming the highlighted
// model. Mirrors the reference's updateList tail.
func (m *ModelSelectorComponent) renderFooter(filtered []ModelItem, cursor int) []string {
	if m.errorMessage != "" {
		var lines []string
		for _, line := range strings.Split(m.errorMessage, "\n") {
			lines = append(lines, m.theme.Fg("error", line))
		}
		return lines
	}
	if len(filtered) == 0 {
		return nil
	}
	idx := cursor
	if idx < 0 || idx >= len(filtered) {
		idx = len(filtered) - 1
	}
	if idx < 0 {
		return nil
	}
	selected := filtered[idx]
	if selected.Model == nil {
		return nil
	}
	return []string{
		"",
		m.theme.Fg("muted", "  Model Name: "+selected.Model.Name),
	}
}

// String returns a debug representation.
func (m ModelSelectorComponent) String() string {
	return "ModelSelector{scope: " + scopeName(m.scope) + ", count: " + strconv.Itoa(len(m.activeModels)) + "}"
}

// scopeName returns the string name of a modelScope.
func scopeName(s modelScope) string {
	if s == modelScopeScoped {
		return "scoped"
	}
	return "all"
}
