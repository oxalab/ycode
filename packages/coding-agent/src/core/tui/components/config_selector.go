package components

import (
	"path/filepath"
	"sort"
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// configResourceType is the kind of package resource a row represents.
type configResourceType string

const (
	cfgResExtensions configResourceType = "extensions"
	cfgResSkills     configResourceType = "skills"
	cfgResPrompts    configResourceType = "prompts"
	cfgResThemes     configResourceType = "themes"
)

// configResourceTypeLabels mirrors RESOURCE_TYPE_LABELS in the TS reference.
var configResourceTypeLabels = map[configResourceType]string{
	cfgResExtensions: "Extensions",
	cfgResSkills:     "Skills",
	cfgResPrompts:    "Prompts",
	cfgResThemes:     "Themes",
}

// configResourceTypeOrder fixes the subgroup display order within a group.
var configResourceTypeOrder = map[configResourceType]int{
	cfgResExtensions: 0,
	cfgResSkills:     1,
	cfgResPrompts:    2,
	cfgResThemes:     3,
}

// configItem is one toggleable resource row.
type configItem struct {
	path         string
	enabled      bool
	metadata     core.PathMetadata
	resourceType configResourceType
	displayName  string
}

// configSubgroup is a labeled bucket of items of one resource type.
type configSubgroup struct {
	resType configResourceType
	label   string
	items   []configItem
}

// configGroup is a top-level grouping (one package, or one top-level scope).
type configGroup struct {
	label     string
	scope     core.SourceScope
	origin    core.SourceOrigin
	source    string
	subgroups []*configSubgroup
}

// flatConfigEntry is one line of the rendered list.
type flatConfigEntry struct {
	kind   string // "group" | "subgroup" | "item"
	group  *configGroup
	sub    *configSubgroup
	item   *configItem
}

// ConfigToggleReport describes a toggle for the App to apply.
type ConfigToggleReport struct {
	ResourceType configResourceType
	Path         string
	Metadata     core.PathMetadata
	Enabled      bool
}

// ConfigSelectorCallbacks reports toggles and close to the App.
type ConfigSelectorCallbacks struct {
	OnToggle func(report ConfigToggleReport) tea.Cmd
	OnClose  func() tea.Cmd
	OnExit   func() tea.Cmd
}

// ConfigSelectorComponent renders a grouped, toggleable list of package
// resources (extensions, skills, prompts, themes) with a search filter.
//
// The toggle *display* is handled here; the actual SettingsManager mutation
// is delegated to OnToggle (the App owns the persistence, since the Go core
// lacks some per-resource setters — those land in 16.18).
//
// Port of reference/coding-agent/src/modes/interactive/components/config-selector.ts
type ConfigSelectorComponent struct {
	theme *theme.Theme
	width int

	groups []*configGroup
	flat   []flatConfigEntry
	filtered []flatConfigEntry
	cursor   int
	maxVisible int

	search searchInput
	cb     ConfigSelectorCallbacks
}

// NewConfigSelectorComponent builds the grouped list from resolved paths.
func NewConfigSelectorComponent(
	t *theme.Theme,
	resolved *core.ResolvedPaths,
	callbacks ConfigSelectorCallbacks,
) *ConfigSelectorComponent {
	c := &ConfigSelectorComponent{
		theme:      t,
		cb:         callbacks,
		maxVisible: 15,
	}
	c.groups = buildConfigGroups(resolved)
	c.buildFlat()
	c.filtered = c.flat
	c.cursor = c.firstItemIndex(c.filtered)
	return c
}

// SetWidth updates the available width.
func (c *ConfigSelectorComponent) SetWidth(width int) { c.width = width }

// Init satisfies tea.Model.
func (c *ConfigSelectorComponent) Init() tea.Cmd { return nil }

// Update routes keyboard input: navigation (item-only), toggle, search, close.
func (c *ConfigSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	str := keyMsg.String()

	switch str {
	case "up", "k":
		c.cursor = c.findNextItem(c.cursor, -1)
		return c, nil
	case "down", "j":
		c.cursor = c.findNextItem(c.cursor, 1)
		return c, nil
	case "pgup":
		c.cursor = c.findItemFrom(c.cursor-c.maxVisible, 1)
		return c, nil
	case "pgdown":
		c.cursor = c.findItemFrom(c.cursor+c.maxVisible, -1)
		return c, nil
	case " ", "enter":
		return c, c.toggleSelected()
	case "escape":
		if c.cb.OnClose != nil {
			return c, c.cb.OnClose()
		}
		return c, nil
	case "ctrl+c":
		if c.cb.OnExit != nil {
			return c, c.cb.OnExit()
		}
		return c, nil
	}

	if c.search.handleKey(str) {
		c.filterItems()
	}
	return c, nil
}

// View renders the header + search + grouped list + scroll indicator.
func (c ConfigSelectorComponent) View() string {
	var b strings.Builder
	b.WriteString(c.theme.Bold("Resource Configuration") + "\n")
	b.WriteString(c.theme.Fg("muted", "  Type to filter resources") + "\n\n")
	b.WriteString(c.theme.Dim("  Search: ") + c.search.value + "\n\n")

	if len(c.filtered) == 0 {
		b.WriteString(c.theme.Fg("muted", "  No resources found"))
		return b.String()
	}

	n := len(c.filtered)
	start := c.cursor - c.maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + c.maxVisible
	if end > n {
		end = n
		start = end - c.maxVisible
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < end; i++ {
		b.WriteString(c.renderEntry(c.filtered[i], i == c.cursor))
		b.WriteString("\n")
	}

	if start > 0 || end < n {
		itemCount := 0
		currentItem := 0
		for i, e := range c.filtered {
			if e.kind == "item" {
				itemCount++
				if i < c.cursor {
					currentItem++
				}
			}
		}
		b.WriteString(c.theme.Fg("dim", "  ("+itoa(currentItem+1)+"/"+itoa(itemCount)+")"))
	}
	return b.String()
}

// --- Internal: group building ---

// buildConfigGroups mirrors buildGroups() in the TS reference: group by
// origin/scope/source/baseDir, subgroup by resource type, sort groups and items.
func buildConfigGroups(resolved *core.ResolvedPaths) []*configGroup {
	if resolved == nil {
		return nil
	}
	groupMap := make(map[string]*configGroup)
	var order []string

	add := func(entries []core.ResolvedPathEntry, rt configResourceType) {
		for _, re := range entries {
			md := re.Metadata
			groupKey := string(md.Origin) + ":" + string(md.Scope) + ":" + md.Source + ":" + md.BaseDir
			g, ok := groupMap[groupKey]
			if !ok {
				g = &configGroup{
					label:  configGroupLabel(md),
					scope:  md.Scope,
					origin: md.Origin,
					source: md.Source,
				}
				groupMap[groupKey] = g
				order = append(order, groupKey)
			}
			subKey := string(rt)
			var sub *configSubgroup
			for i := range g.subgroups {
				if string(g.subgroups[i].resType) == subKey {
					sub = g.subgroups[i]
					break
				}
			}
			if sub == nil {
				g.subgroups = append(g.subgroups, &configSubgroup{
					resType: rt,
					label:   configResourceTypeLabels[rt],
				})
				sub = g.subgroups[len(g.subgroups)-1]
			}
			sub.items = append(sub.items, configItem{
				path:         re.Path,
				enabled:      re.Enabled,
				metadata:     md,
				resourceType: rt,
				displayName:  configDisplayName(re.Path, rt),
			})
		}
	}

	add(resolved.Extensions, cfgResExtensions)
	add(resolved.Skills, cfgResSkills)
	add(resolved.Prompts, cfgResPrompts)
	add(resolved.Themes, cfgResThemes)

	groups := make([]*configGroup, 0, len(order))
	for _, k := range order {
		groups = append(groups, groupMap[k])
	}

	// Sort groups: packages first, then top-level; user before project; then source.
	sort.SliceStable(groups, func(a, b int) bool {
		ga, gb := groups[a], groups[b]
		if ga.origin != gb.origin {
			return ga.origin == core.OriginPackage
		}
		if ga.scope != gb.scope {
			return ga.scope == core.ScopeUser
		}
		return ga.source < gb.source
	})

	// Sort subgroups by type order; items by display name.
	for _, g := range groups {
		sort.SliceStable(g.subgroups, func(a, b int) bool {
			return configResourceTypeOrder[g.subgroups[a].resType] < configResourceTypeOrder[g.subgroups[b].resType]
		})
		for _, sg := range g.subgroups {
			sort.SliceStable(sg.items, func(a, b int) bool {
				return sg.items[a].displayName < sg.items[b].displayName
			})
		}
	}
	return groups
}

// configGroupLabel mirrors getGroupLabel() in the TS reference.
func configGroupLabel(md core.PathMetadata) string {
	if md.Origin == core.OriginPackage {
		return md.Source + " (" + string(md.Scope) + ")"
	}
	// Top-level resources.
	if md.Source == "auto" {
		if md.BaseDir != "" {
			if md.Scope == core.ScopeUser {
				return "User (" + formatBaseDir(md.BaseDir) + ")"
			}
			return "Project (" + formatBaseDir(md.BaseDir) + ")"
		}
		if md.Scope == core.ScopeUser {
			return "User (~/.ycode/)"
		}
		return "Project (.ycode/)"
	}
	if md.Scope == core.ScopeUser {
		return "User settings"
	}
	return "Project settings"
}

// formatBaseDir replaces the home prefix with ~ and normalizes separators.
func formatBaseDir(baseDir string) string {
	home := homeDir()
	var display string
	if baseDir == home {
		display = "~"
	} else if home != "" && strings.HasPrefix(baseDir, home) {
		display = "~" + strings.ReplaceAll(baseDir[len(home):], "\\", "/")
	} else {
		display = strings.ReplaceAll(baseDir, "\\", "/")
	}
	if !strings.HasSuffix(display, "/") {
		display += "/"
	}
	return display
}

// configDisplayName derives a display name from a path, mirroring the TS logic.
func configDisplayName(path string, rt configResourceType) string {
	fileName := filepath.Base(path)
	parentFolder := filepath.Base(filepath.Dir(path))
	if rt == cfgResExtensions && parentFolder != "extensions" {
		return parentFolder + "/" + fileName
	}
	if rt == cfgResSkills && fileName == "SKILL.md" {
		return parentFolder
	}
	return fileName
}

// --- Internal: flattening ---

func (c *ConfigSelectorComponent) buildFlat() {
	c.flat = c.flat[:0]
	for _, g := range c.groups {
		c.flat = append(c.flat, flatConfigEntry{kind: "group", group: g})
		for _, sg := range g.subgroups {
			c.flat = append(c.flat, flatConfigEntry{kind: "subgroup", sub: sg, group: g})
			for i := range sg.items {
				c.flat = append(c.flat, flatConfigEntry{kind: "item", item: &sg.items[i]})
			}
		}
	}
}

// firstItemIndex returns the index of the first "item" entry (or 0).
func (c *ConfigSelectorComponent) firstItemIndex(entries []flatConfigEntry) int {
	for i, e := range entries {
		if e.kind == "item" {
			return i
		}
	}
	return 0
}

// findNextItem moves the cursor to the next/previous "item" entry.
func (c *ConfigSelectorComponent) findNextItem(from, direction int) int {
	idx := from + direction
	for idx >= 0 && idx < len(c.filtered) {
		if c.filtered[idx].kind == "item" {
			return idx
		}
		idx += direction
	}
	return from
}

// findItemFrom jumps near a target index then walks back to the nearest item.
func (c *ConfigSelectorComponent) findItemFrom(target, direction int) int {
	if target < 0 {
		target = 0
	}
	if target >= len(c.filtered) {
		target = len(c.filtered) - 1
	}
	idx := target
	for idx >= 0 && idx < len(c.filtered) {
		if c.filtered[idx].kind == "item" {
			return idx
		}
		idx += direction
	}
	return c.cursor
}

// --- Internal: filtering ---

// filterItems keeps group/subgroup headers whose items match, plus matching items.
func (c *ConfigSelectorComponent) filterItems() {
	q := strings.TrimSpace(strings.ToLower(c.search.value))
	if q == "" {
		c.filtered = c.flat
		c.cursor = c.firstItemIndex(c.filtered)
		return
	}

	matchingItems := make(map[*configItem]bool)
	for _, e := range c.flat {
		if e.kind != "item" {
			continue
		}
		hay := strings.ToLower(e.item.displayName + " " + string(e.item.resourceType) + " " + e.item.path)
		if strings.Contains(hay, q) {
			matchingItems[e.item] = true
		}
	}

	matchingSubs := make(map[*configSubgroup]bool)
	matchingGroups := make(map[*configGroup]bool)
	for _, g := range c.groups {
		for _, sg := range g.subgroups {
			for i := range sg.items {
				if matchingItems[&sg.items[i]] {
					matchingSubs[sg] = true
					matchingGroups[g] = true
				}
			}
		}
	}

	c.filtered = c.filtered[:0]
	for _, e := range c.flat {
		switch e.kind {
		case "group":
			if matchingGroups[e.group] {
				c.filtered = append(c.filtered, e)
			}
		case "subgroup":
			if matchingSubs[e.sub] {
				c.filtered = append(c.filtered, e)
			}
		case "item":
			if matchingItems[e.item] {
				c.filtered = append(c.filtered, e)
			}
		}
	}
	c.cursor = c.firstItemIndex(c.filtered)
}

// --- Internal: toggle ---

func (c *ConfigSelectorComponent) toggleSelected() tea.Cmd {
	if c.cursor < 0 || c.cursor >= len(c.filtered) {
		return nil
	}
	e := c.filtered[c.cursor]
	if e.kind != "item" || e.item == nil {
		return nil
	}
	e.item.enabled = !e.item.enabled
	if c.cb.OnToggle != nil {
		return c.cb.OnToggle(ConfigToggleReport{
			ResourceType: e.item.resourceType,
			Path:         e.item.path,
			Metadata:     e.item.metadata,
			Enabled:      e.item.enabled,
		})
	}
	return nil
}

// --- Internal: rendering ---

func (c ConfigSelectorComponent) renderEntry(e flatConfigEntry, selected bool) string {
	switch e.kind {
	case "group":
		return "  " + c.theme.Fg("accent", c.theme.Bold(e.group.label))
	case "subgroup":
		return "    " + c.theme.Fg("muted", e.sub.label)
	case "item":
		cursor := "  "
		if selected {
			cursor = "> "
		}
		checkbox := c.theme.Fg("dim", "[ ]")
		if e.item.enabled {
			checkbox = c.theme.Fg("success", "[x]")
		}
		name := e.item.displayName
		if selected {
			name = c.theme.Bold(name)
		}
		return cursor + "    " + checkbox + " " + name
	}
	return ""
}
