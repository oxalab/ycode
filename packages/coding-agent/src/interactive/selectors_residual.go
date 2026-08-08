package interactive

// selectors_residual.go ports the post-P8 residual SELECTOR commands:
//
//   - /logout        (reference showOAuthSelector("logout") :4477) → provider
//                    chooser → AuthStorage.Logout
//   - /fork          (reference showUserMessageSelector :4103) → user-message
//                    chooser → runtime.Fork at that entry
//   - /tree          (reference showTreeSelector :4166) → session-history tree →
//                    runtime.Fork at the chosen branch
//   - /scoped-models (reference showModelsSelector :4026) → multi-select of
//                    available models → AgentSession.SetScopedModels
//
// /logout and /fork reuse the SelectList shell (newSimpleSelector). /tree and
// /scoped-models need custom rendering (nested tree rows / toggle check-marks),
// so they own their list like ModelSelector. Every command traces to a real Go
// backend; scoped-models persistence to settings.json has NO Go setter (even the
// BT TUI defers it — app_selectors.go:157), so it applies in-memory to the live
// session with an honest notice.

import (
	"fmt"
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
	oauth "ycode/packages/llm/src/utils/oauth"
)

// --- /logout ----------------------------------------------------------------

// openLogoutSelector swaps in a provider chooser (reference
// showOAuthSelector("logout")). Selecting a provider revokes its saved auth via
// AuthStorage.Logout (auth_storage.go:425) — it does NOT touch env/model.json.
func (o *Orchestrator) openLogoutSelector() {
	providers := oauth.GetOAuthProviders()
	if len(providers) == 0 {
		o.addNotice(o.th.Fg("muted", "No OAuth providers registered."))
		return
	}
	items := make([]components.SelectItem, len(providers))
	for i, p := range providers {
		items[i] = components.SelectItem{Value: p.ID(), Label: p.Name()}
	}
	list := components.NewSelectList(items, len(items), buildSelectListTheme(o.th))
	list.OnSelect = func(item components.SelectItem) {
		auth := o.session.GetModelRegistry().GetAuthStorage()
		auth.Logout(item.Value)
		o.closeSelector()
		o.addNotice(o.th.Fg("dim", "Logged out of "+item.Label+"."))
	}
	list.OnCancel = o.closeSelector
	o.swapInSelector(newSimpleSelector(list, o.th))
}

// --- /fork ------------------------------------------------------------------

// openUserMessageSelector swaps in a chooser of previous USER messages
// (reference showUserMessageSelector). Unlike the BT TUI (which opens it empty —
// "sourcing pending"), this walks the session entries so forking actually works.
// Selecting a message forks at that entry (runtime.Fork, Position:"at" — same as
// the BT forkMessageResult handling at app_selectors.go:161-167).
func (o *Orchestrator) openUserMessageSelector() {
	type umItem struct{ id, preview string }
	var userMsgs []umItem
	for _, entry := range o.session.SessionManager.GetEntries() {
		if entry.Type != compaction.EntryTypeMessage {
			continue
		}
		m, ok := entry.Message.(llm.UserMessage)
		if !ok {
			continue
		}
		text := strings.TrimSpace(userMessageText(m))
		if text == "" {
			continue
		}
		userMsgs = append(userMsgs, umItem{id: entry.ID, preview: truncateSessionLabel(text)})
	}
	if len(userMsgs) == 0 {
		o.addNotice(o.th.Fg("muted", "No user messages to fork from yet."))
		return
	}
	items := make([]components.SelectItem, len(userMsgs))
	for i, um := range userMsgs {
		items[i] = components.SelectItem{Value: um.id, Label: um.preview}
	}
	list := components.NewSelectList(items, 10, buildSelectListTheme(o.th))
	list.OnSelect = func(item components.SelectItem) { o.forkAtEntry(item.Value) }
	list.OnCancel = o.closeSelector
	o.swapInSelector(newSimpleSelector(list, o.th))
}

// forkAtEntry forks the session at the given entry ID on a goroutine (file I/O),
// then re-binds the orchestrator to the new session (shared rebindSession).
func (o *Orchestrator) forkAtEntry(entryID string) {
	o.closeSelector()
	go func() {
		result, err := o.runtime.Fork(entryID, &core.ForkOptions{Position: "at"})
		if err != nil {
			o.engine.Mutate(func() {
				o.addNotice(o.th.Fg("error", "Failed to fork: "+err.Error()))
			})
			o.engine.RequestRender()
			return
		}
		if result == nil || result.Cancelled {
			return
		}
		o.rebindSession()
	}()
}

// --- /tree ------------------------------------------------------------------

// openTreeSelector swaps in the session-history tree (reference showTreeSelector).
// A faithful CORE port of tree-selector.ts: flatten the nested tree with
// └─/├─ gutters, navigate up/down, mark the current leaf, select to fork at that
// branch (runtime.Fork, Position:"at"). Fold/filter/label-edit (the full TS
// surface) are intentionally out of scope — the branch-switching interaction is
// what /tree is for.
func (o *Orchestrator) openTreeSelector() {
	roots := o.session.SessionManager.GetTree()
	if len(roots) == 0 {
		o.addNotice(o.th.Fg("muted", "No session history yet."))
		return
	}
	sel := newTreeSelector(roots, o.session.SessionManager.GetLeafId(), o.th,
		func(entryID string) { o.forkAtEntry(entryID) }, // OnSelect
		o.closeSelector, // onCancel
	)
	o.swapInSelector(sel)
}

// treeRow is one flattened tree row (depth + connector ancestry).
type treeRow struct {
	entryID   string
	depth     int
	isLast    bool   // last sibling → └─, else ├─
	gutters   []bool // per ancestor level: │ (true) or blank (false)
	label     string
	isCurrent bool
}

// treeSelector renders the flattened session tree with navigation (core of
// tree-selector.ts). It implements tui.Component/InputHandler/Focusable like the
// other custom selectors.
type treeSelector struct {
	*tui.Container
	th      *theme.Theme
	listBox *tui.Container

	rows    []treeRow
	cursor  int
	maxVis  int

	onSelect func(entryID string)
	onCancel func()
	focused  bool
}

const treeMaxVisible = 12

// newTreeSelector flattens the roots and builds the bordered shell.
func newTreeSelector(roots []*core.SessionTreeNode, currentLeafID string, th *theme.Theme,
	onSelect func(entryID string), onCancel func()) *treeSelector {
	ts := &treeSelector{
		Container: &tui.Container{},
		th:        th,
		maxVis:    treeMaxVisible,
		onSelect:  onSelect,
		onCancel:  onCancel,
	}
	ts.flatten(roots, 0, nil, currentLeafID)

	ts.AddChild(newDynamicBorder(fgStyle(th, "border")))
	ts.AddChild(components.NewText(th.Fg("dim", "Session history — pick a branch to switch to"), 0, 0, nil))
	ts.AddChild(components.NewSpacer(1))
	ts.listBox = &tui.Container{}
	ts.AddChild(ts.listBox)
	ts.AddChild(components.NewSpacer(1))
	ts.AddChild(newDynamicBorder(fgStyle(th, "border")))
	ts.refreshList()
	return ts
}

// flatten recursively walks the tree, producing connector/gutter metadata per row
// (the visual ancestry tree). Port of tree-selector.ts flatten.
func (ts *treeSelector) flatten(nodes []*core.SessionTreeNode, depth int, gutters []bool, currentLeafID string) {
	for i, n := range nodes {
		isLast := i == len(nodes)-1
		label := n.Label
		if label == "" {
			label = nodeFallbackLabel(n)
		}
		ts.rows = append(ts.rows, treeRow{
			entryID:   nodeID(n),
			depth:     depth,
			isLast:    isLast,
			gutters:   append([]bool(nil), gutters...),
			label:     label,
			isCurrent: nodeID(n) == currentLeafID,
		})
		// Descend: the new ancestor level draws │ unless THIS node was last.
		nextGutters := append([]bool(nil), gutters...)
		nextGutters = append(nextGutters, !isLast)
		if len(n.Children) > 0 {
			ts.flatten(n.Children, depth+1, nextGutters, currentLeafID)
		}
	}
}

// refreshList renders the visible slice of tree rows around the cursor.
func (ts *treeSelector) refreshList() {
	ts.listBox.Clear()
	if len(ts.rows) == 0 {
		ts.listBox.AddChild(components.NewText(ts.th.Fg("muted", "  (empty)"), 0, 0, nil))
		return
	}
	start := ts.cursor - ts.maxVis/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(ts.rows) - ts.maxVis; start > maxStart {
		start = max(0, maxStart)
	}
	end := start + ts.maxVis
	if end > len(ts.rows) {
		end = len(ts.rows)
	}
	for i := start; i < end; i++ {
		ts.listBox.AddChild(components.NewText(ts.renderRow(ts.rows[i], i == ts.cursor), 0, 0, nil))
	}
}

// renderRow composes a single tree row: gutters + connector + label (+ current ✓).
func (ts *treeSelector) renderRow(r treeRow, selected bool) string {
	var b strings.Builder
	// Ancestry gutters.
	for _, g := range r.gutters {
		if g {
			b.WriteString("│  ")
		} else {
			b.WriteString("   ")
		}
	}
	// Connector for this node's depth.
	if r.depth > 0 {
		if r.isLast {
			b.WriteString("└─ ")
		} else {
			b.WriteString("├─ ")
		}
	}
	prefix := "  "
	if selected {
		prefix = "→ "
	}
	b.WriteString(prefix)
	label := "• " + r.label
	if r.isCurrent {
		label += " (current)"
	}
	if selected {
		return b.String() + ts.th.Fg("accent", label)
	}
	out := b.String() + label
	if r.isCurrent {
		return ts.th.Fg("success", out)
	}
	return out
}

// HandleInput: up/down navigate (wrap), enter select, escape cancel (raw key data,
// same as the model selector).
func (ts *treeSelector) HandleInput(data string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.up"):
		if len(ts.rows) == 0 {
			return
		}
		if ts.cursor == 0 {
			ts.cursor = len(ts.rows) - 1
		} else {
			ts.cursor--
		}
		ts.refreshList()
	case kb.Matches(data, "tui.select.down"):
		if len(ts.rows) == 0 {
			return
		}
		if ts.cursor == len(ts.rows)-1 {
			ts.cursor = 0
		} else {
			ts.cursor++
		}
		ts.refreshList()
	case kb.Matches(data, "tui.select.confirm"):
		if ts.onSelect != nil && len(ts.rows) > 0 {
			ts.onSelect(ts.rows[ts.cursor].entryID)
		}
	case kb.Matches(data, "tui.select.cancel"):
		if ts.onCancel != nil {
			ts.onCancel()
		}
	}
}

func (ts *treeSelector) Focused() bool     { return ts.focused }
func (ts *treeSelector) SetFocused(f bool) { ts.focused = f }

// nodeID returns the entry ID of a tree node (its entry may be nil for synthetic
// roots).
func nodeID(n *core.SessionTreeNode) string {
	if n == nil || n.Entry == nil {
		return ""
	}
	return n.Entry.ID
}

// nodeFallbackLabel derives a preview label from a node's message content when
// the node has no explicit Label.
func nodeFallbackLabel(n *core.SessionTreeNode) string {
	if n == nil || n.Entry == nil {
		return "(session)"
	}
	switch m := n.Entry.Message.(type) {
	case llm.UserMessage:
		if t := strings.TrimSpace(userMessageText(m)); t != "" {
			return truncateSessionLabel(t)
		}
		return "user message"
	case llm.AssistantMessage:
		return "assistant message"
	}
	return "(entry)"
}

// --- /scoped-models ---------------------------------------------------------

// openScopedModelsSelector swaps in a multi-select of available models for Ctrl+P
// cycling (reference showModelsSelector). The selection applies in-memory to the
// live session via AgentSession.SetScopedModels (agent_session.go:395); persisting
// it to settings.json has no Go setter (deferred even in the BT TUI), so an honest
// notice is shown.
func (o *Orchestrator) openScopedModelsSelector() {
	registry := o.session.GetModelRegistry()
	all := registry.GetAll()
	if len(all) == 0 {
		o.addNotice(o.th.Fg("muted", "No models available. Use /login to add a provider."))
		return
	}

	// Pre-check the current scoped models.
	enabled := map[string]bool{}
	for _, se := range o.session.GetScopedModels() {
		if mm := modelFromAny(se.Model); mm != nil {
			enabled[scopedKey(mm.Provider, mm.ID)] = true
		}
	}

	items := make([]toggleItem, len(all))
	for i, m := range all {
		items[i] = toggleItem{
			id:       m.ID,
			provider: string(m.Provider),
			name:     m.Name,
			checked:  enabled[scopedKey(m.Provider, m.ID)],
			model:    *m,
		}
	}

	sel := newToggleSelector(items, o.th,
		func(selected []llm.Model[llm.Api]) { // onPersist
			entries := make([]core.ScopedModelEntry, len(selected))
			for i := range selected {
				mm := selected[i]
				entries[i] = core.ScopedModelEntry{Model: &mm}
			}
			o.session.SetScopedModels(entries)
			o.closeSelector()
			o.addNotice(o.th.Fg("dim",
				fmt.Sprintf("Updated %d model(s) for cycling (in-memory; settings.json persistence not yet backed).", len(selected))))
		},
		o.closeSelector, // onCancel
	)
	o.swapInSelector(sel)
}

// scopedKey is the map key for a model in the enabled set.
func scopedKey(provider llm.Provider, id string) string {
	return string(provider) + "/" + id
}

// toggleItem is one checkable model row.
type toggleItem struct {
	id, provider, name string
	checked            bool
	model              llm.Model[llm.Api]
}

// toggleSelector is a multi-select (reference showModelsSelector): up/down move
// the cursor, space toggles a row's membership, enter persists, escape cancels.
type toggleSelector struct {
	*tui.Container
	th      *theme.Theme
	listBox *tui.Container

	items  []toggleItem
	cursor int
	maxVis int

	onPersist func(selected []llm.Model[llm.Api])
	onCancel  func()
	focused   bool
}

const toggleMaxVisible = 12

// newToggleSelector builds the bordered shell + the list.
func newToggleSelector(items []toggleItem, th *theme.Theme,
	onPersist func([]llm.Model[llm.Api]), onCancel func()) *toggleSelector {
	ts := &toggleSelector{
		Container: &tui.Container{},
		th:        th,
		items:     items,
		maxVis:    toggleMaxVisible,
		onPersist: onPersist,
		onCancel:  onCancel,
	}
	ts.AddChild(newDynamicBorder(fgStyle(th, "border")))
	ts.AddChild(components.NewText(th.Fg("dim", "Enable/disable models for Ctrl+P cycling"), 0, 0, nil))
	ts.AddChild(components.NewSpacer(1))
	ts.listBox = &tui.Container{}
	ts.AddChild(ts.listBox)
	ts.AddChild(components.NewSpacer(1))
	ts.AddChild(components.NewText(keyHint(th, "tui.select.confirm", "save")+
		th.Fg("muted", "  space toggle  ")+keyHint(th, "tui.select.cancel", "cancel"), 0, 0, nil))
	ts.AddChild(newDynamicBorder(fgStyle(th, "border")))
	ts.refreshList()
	return ts
}

func (ts *toggleSelector) refreshList() {
	ts.listBox.Clear()
	if len(ts.items) == 0 {
		ts.listBox.AddChild(components.NewText(ts.th.Fg("muted", "  (no models)"), 0, 0, nil))
		return
	}
	start := ts.cursor - ts.maxVis/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(ts.items) - ts.maxVis; start > maxStart {
		start = max(0, maxStart)
	}
	end := start + ts.maxVis
	if end > len(ts.items) {
		end = len(ts.items)
	}
	for i := start; i < end; i++ {
		ts.listBox.AddChild(components.NewText(ts.renderRow(ts.items[i], i == ts.cursor), 0, 0, nil))
	}
}

func (ts *toggleSelector) renderRow(it toggleItem, selected bool) string {
	mark := "☐"
	if it.checked {
		mark = "☑"
	}
	prefix := "  "
	if selected {
		prefix = "→ "
	}
	body := prefix + mark + " " + it.id + " " + ts.th.Fg("muted", "["+it.provider+"]")
	if it.checked {
		body = ts.th.Fg("success", body)
	}
	return body
}

// HandleInput: up/down nav (wrap), space toggles the current row, enter persists,
// escape cancels (matches the reference enable/disable + save flow).
func (ts *toggleSelector) HandleInput(data string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.up"):
		if len(ts.items) == 0 {
			return
		}
		if ts.cursor == 0 {
			ts.cursor = len(ts.items) - 1
		} else {
			ts.cursor--
		}
		ts.refreshList()
	case kb.Matches(data, "tui.select.down"):
		if len(ts.items) == 0 {
			return
		}
		if ts.cursor == len(ts.items)-1 {
			ts.cursor = 0
		} else {
			ts.cursor++
		}
		ts.refreshList()
	case kb.Matches(data, "tui.select.confirm"):
		ts.persist()
	case kb.Matches(data, "tui.select.cancel"):
		if ts.onCancel != nil {
			ts.onCancel()
		}
	default:
		if data == " " { // space toggles the current row
			ts.toggleCurrent()
		}
	}
}

func (ts *toggleSelector) toggleCurrent() {
	if len(ts.items) == 0 {
		return
	}
	ts.items[ts.cursor].checked = !ts.items[ts.cursor].checked
	ts.refreshList()
}

func (ts *toggleSelector) persist() {
	if ts.onPersist == nil {
		return
	}
	var selected []llm.Model[llm.Api]
	for _, it := range ts.items {
		if it.checked {
			selected = append(selected, it.model)
		}
	}
	ts.onPersist(selected)
}

func (ts *toggleSelector) Focused() bool     { return ts.focused }
func (ts *toggleSelector) SetFocused(f bool) { ts.focused = f }
