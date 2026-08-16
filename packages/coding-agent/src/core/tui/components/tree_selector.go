package components

import (
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	llm "ycode/packages/llm/src"
)

// treeFilterMode controls which entries the tree shows.
type treeFilterMode int

const (
	treeFilterDefault treeFilterMode = iota
	treeFilterAll
)

// flatNode is one row in the rendered tree.
type flatNode struct {
	node        *core.SessionTreeNode
	depth       int
	isLast      bool // last sibling among parent's children (└─ vs ├─)
	gutters     []bool // per ancestor level: draw │ (true) or spaces (false)
	hasChildren bool
	isFolded    bool
	isOnPath    bool // node lies on the active path to the current leaf
	isCurrent   bool // node is the current leaf
}

// TreeSelector renders a session history tree with expand/collapse and
// keyboard navigation. Used by the session selector.
//
// Layout per row:
//
//	[cursor] [gutters...] [connector] [fold marker] [• path?] [label/content]
//
// Port of reference/coding-agent/src/modes/interactive/components/tree-selector.ts
// (core navigation/flattening/rendering; filter cycling & label editing are
// simplified — search + default filter here, to be expanded later).
type TreeSelector struct {
	theme         *theme.Theme
	width         int
	roots         []*core.SessionTreeNode
	currentLeafID string

	flat        []*flatNode
	cursor      int
	maxVisible  int
	folded      map[string]bool
	activePath  map[string]bool // entry IDs on path to current leaf

	filter treeFilterMode

	onSelect func(entryID string) tea.Cmd
	onCancel func() tea.Cmd
}

// TreeSelectorCallbacks holds the result handlers supplied by the App.
type TreeSelectorCallbacks struct {
	OnSelect func(entryID string) tea.Cmd
	OnCancel func() tea.Cmd
}

// NewTreeSelector creates a new tree selector.
func NewTreeSelector(
	t *theme.Theme,
	roots []*core.SessionTreeNode,
	currentLeafID string,
	maxVisible int,
	callbacks TreeSelectorCallbacks,
) *TreeSelector {
	if maxVisible <= 0 {
		maxVisible = 10
	}
	ts := &TreeSelector{
		theme:         t,
		roots:         roots,
		currentLeafID: currentLeafID,
		maxVisible:    maxVisible,
		folded:        make(map[string]bool),
		activePath:    make(map[string]bool),
		onSelect:      callbacks.OnSelect,
		onCancel:      callbacks.OnCancel,
	}
	ts.computeActivePath()
	ts.flatten()
	ts.cursor = ts.findCurrentIndex()
	return ts
}

// SetWidth updates the available width.
func (t *TreeSelector) SetWidth(width int) { t.width = width }

// Init satisfies tea.Model.
func (t *TreeSelector) Init() tea.Cmd { return nil }

// Update handles navigation and selection keys.
func (t *TreeSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return t, nil
	}

	switch keyMsg.String() {
	case "up", "k":
		t.moveCursor(-1)
		return t, nil
	case "down", "j":
		t.moveCursor(1)
		return t, nil
	case "left", "h":
		t.foldOrUp()
		return t, nil
	case "right", "l":
		t.unfoldOrDown()
		return t, nil
	case "enter":
		if t.cursor >= 0 && t.cursor < len(t.flat) {
			fn := t.flat[t.cursor]
			if fn.node != nil && fn.node.Entry != nil {
				if t.onSelect != nil {
					return t, t.onSelect(fn.node.Entry.ID)
				}
			}
		}
		return t, nil
	case "escape", "ctrl+c":
		if t.onCancel != nil {
			return t, t.onCancel()
		}
		return t, nil
	}

	return t, nil
}

// View renders the tree within a bordered box.
func (t TreeSelector) View() string {
	width := t.width
	if width <= 0 {
		width = 60
	}

	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width - 2)

	if len(t.flat) == 0 {
		return border.Render(t.theme.Dim("  No history"))
	}

	lines := t.renderVisibleWindow()
	return border.Render(strings.Join(lines, "\n"))
}

// --- Internal: tree flattening ---

// computeActivePath marks every ancestor of currentLeafID as on-path.
func (t *TreeSelector) computeActivePath() {
	if t.currentLeafID == "" {
		return
	}
	// Walk from the leaf up to the root, collecting ancestor IDs.
	leaf := t.findNodeByID(t.currentLeafID)
	for leaf != nil && leaf.Entry != nil {
		t.activePath[leaf.Entry.ID] = true
		parent := t.findNodeByID(leaf.Entry.ParentID)
		leaf = parent
	}
}

// findNodeByID searches the whole tree for an entry ID.
func (t *TreeSelector) findNodeByID(id string) *core.SessionTreeNode {
	if id == "" {
		return nil
	}
	for _, root := range t.roots {
		if found := findNodeByIDRecursive(root, id); found != nil {
			return found
		}
	}
	return nil
}

func findNodeByIDRecursive(node *core.SessionTreeNode, id string) *core.SessionTreeNode {
	if node == nil || node.Entry == nil {
		return nil
	}
	if node.Entry.ID == id {
		return node
	}
	for _, child := range node.Children {
		if found := findNodeByIDRecursive(child, id); found != nil {
			return found
		}
	}
	return nil
}

// flatten builds the visible node list, respecting fold state.
func (t *TreeSelector) flatten() {
	t.flat = t.flat[:0]
	var stack []bool // ancestor "last sibling" flags for gutter drawing
	for i, root := range t.roots {
		t.flattenNode(root, 0, i == len(t.roots)-1, stack)
	}
}

func (t *TreeSelector) flattenNode(node *core.SessionTreeNode, depth int, isLast bool, gutters []bool) {
	if node == nil {
		return
	}
	id := ""
	if node.Entry != nil {
		id = node.Entry.ID
	}
	fn := &flatNode{
		node:        node,
		depth:       depth,
		isLast:      isLast,
		gutters:     append([]bool{}, gutters...),
		hasChildren: len(node.Children) > 0,
		isFolded:    t.folded[id],
		isOnPath:    id != "" && t.activePath[id],
		isCurrent:   id != "" && id == t.currentLeafID,
	}
	t.flat = append(t.flat, fn)

	// Skip children if folded.
	if fn.isFolded {
		return
	}
	for i, child := range node.Children {
		// Gutters for children: this level draws │ unless this node is the last.
		childGutters := append([]bool{}, gutters...)
		childGutters = append(childGutters, !isLast)
		t.flattenNode(child, depth+1, i == len(node.Children)-1, childGutters)
	}
}

// findCurrentIndex locates the current leaf in the flattened list.
func (t *TreeSelector) findCurrentIndex() int {
	if t.currentLeafID == "" {
		return 0
	}
	for i, fn := range t.flat {
		if fn.isCurrent {
			return i
		}
	}
	return 0
}

// --- Internal: navigation ---

// moveCursor moves the cursor by delta, clamping to the list bounds.
func (t *TreeSelector) moveCursor(delta int) {
	n := len(t.flat)
	if n == 0 {
		t.cursor = 0
		return
	}
	t.cursor += delta
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor >= n {
		t.cursor = n - 1
	}
}

// foldOrUp folds the current node if it has visible children; otherwise
// moves the cursor up to the nearest branch ancestor.
func (t *TreeSelector) foldOrUp() {
	if t.cursor < 0 || t.cursor >= len(t.flat) {
		return
	}
	fn := t.flat[t.cursor]
	id := entryID(fn.node)
	if fn.hasChildren && !fn.isFolded {
		t.folded[id] = true
		t.flatten()
		t.clampCursor()
		return
	}
	// Move up to nearest ancestor at a lower depth (branch segment start).
	target := t.cursor - 1
	for target >= 0 {
		if t.flat[target].depth < fn.depth {
			t.cursor = target
			return
		}
		target--
	}
	t.cursor = 0
}

// unfoldOrDown unfolds the current node if folded; otherwise moves down into
// its first child.
func (t *TreeSelector) unfoldOrDown() {
	if t.cursor < 0 || t.cursor >= len(t.flat) {
		return
	}
	fn := t.flat[t.cursor]
	id := entryID(fn.node)
	if fn.isFolded {
		delete(t.folded, id)
		t.flatten()
		return
	}
	// Move to the first child (next index at greater depth).
	target := t.cursor + 1
	if target < len(t.flat) && t.flat[target].depth > fn.depth {
		t.cursor = target
	}
}

// clampCursor keeps the cursor in range after a re-flatten.
func (t *TreeSelector) clampCursor() {
	if t.cursor >= len(t.flat) {
		t.cursor = len(t.flat) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// --- Internal: rendering ---

// renderVisibleWindow returns the centered visible slice of rows + scroll info.
func (t TreeSelector) renderVisibleWindow() []string {
	n := len(t.flat)
	start := t.cursor - t.maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + t.maxVisible
	if end > n {
		end = n
		start = end - t.maxVisible
		if start < 0 {
			start = 0
		}
	}

	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		lines = append(lines, t.renderRow(t.flat[i], i == t.cursor))
	}

	if start > 0 || end < n {
		lines = append(lines, t.theme.Dim("  ("+itoa(t.cursor+1)+"/"+itoa(n)+")"))
	}
	return lines
}

// renderRow renders one flat node with gutters, connector, fold marker, label.
func (t TreeSelector) renderRow(fn *flatNode, selected bool) string {
	// Cursor marker.
	cursor := "  "
	if selected {
		cursor = t.theme.Fg("accent", "› ")
	}

	// Gutters: for each ancestor level, "│  " or "   ".
	var gutters strings.Builder
	for _, g := range fn.gutters {
		if g {
			gutters.WriteString(t.theme.Fg("borderMuted", "│  "))
		} else {
			gutters.WriteString("   ")
		}
	}

	// Connector for non-root nodes.
	connector := ""
	if fn.depth > 0 {
		if fn.isLast {
			connector = t.theme.Fg("borderMuted", "└─ ")
		} else {
			connector = t.theme.Fg("borderMuted", "├─ ")
		}
	}

	// Fold marker.
	fold := "  "
	if fn.hasChildren {
		if fn.isFolded {
			fold = t.theme.Fg("muted", "⊞ ")
		} else {
			fold = t.theme.Fg("muted", "⊟ ")
		}
	}

	// Path marker.
	pathMark := ""
	if fn.isOnPath && !fn.isCurrent {
		pathMark = t.theme.Fg("accent", "• ")
	}

	// Label/content.
	label := t.renderEntryLabel(fn)

	return cursor + gutters.String() + connector + fold + pathMark + label
}

// renderEntryLabel formats a node's content by entry type.
func (t TreeSelector) renderEntryLabel(fn *flatNode) string {
	if fn.node == nil || fn.node.Entry == nil {
		return ""
	}
	entry := fn.node.Entry

	// Custom label takes precedence.
	if fn.node.Label != "" {
		return t.theme.Fg("warning", "["+fn.node.Label+"] ") + t.entryPreview(entry)
	}

	switch entry.Type {
	case compaction.EntryTypeMessage:
		return t.renderMessagePreview(entry)
	case compaction.EntryTypeCompaction:
		return t.theme.Fg("customMessageLabel", "[compaction] ") +
			t.theme.Dim("("+itoa(entry.TokensBefore/1000)+"k tokens)")
	case compaction.EntryTypeBranchSummary:
		return t.theme.Fg("customMessageLabel", "[branch summary] ") +
			t.theme.Dim(truncatePreview(entry.Summary))
	case compaction.EntryTypeModelChange:
		return t.theme.Fg("customMessageLabel", "[model change]")
	case compaction.EntryTypeThinkingLevelChange:
		return t.theme.Fg("customMessageLabel", "[thinking level change]")
	case compaction.EntryTypeCustomMessage:
		return t.theme.Fg("customMessageLabel", "["+entry.CustomType+"] ") +
			t.theme.Dim(truncatePreview(entry.Summary))
	case compaction.EntryTypeSessionInfo:
		return t.theme.Fg("muted", "[session info]")
	default:
		return t.theme.Dim("("+string(entry.Type)+")")
	}
}

// renderMessagePreview formats a message entry based on its role.
func (t TreeSelector) renderMessagePreview(entry *compaction.SessionEntry) string {
	if entry.Message == nil {
		return t.theme.Dim("(empty message)")
	}
	role, preview := messageRoleAndText(entry.Message)

	switch role {
	case "user":
		return t.theme.Fg("userMessageText", "user: ") + t.theme.Dim(truncatePreview(preview))
	case "assistant":
		if preview == "" {
			return t.theme.Dim("assistant: (no content)")
		}
		return t.theme.Fg("accent", "assistant: ") + t.theme.Dim(truncatePreview(preview))
	case "tool":
		return t.theme.Fg("toolTitle", "[tool result] ") + t.theme.Dim(truncatePreview(preview))
	default:
		return t.theme.Dim(role + ": " + truncatePreview(preview))
	}
}

// entryPreview returns a short text preview for label/custom entries.
func (t TreeSelector) entryPreview(entry *compaction.SessionEntry) string {
	if entry == nil {
		return ""
	}
	return truncatePreview(entry.Summary)
}

// messageRoleAndText extracts the role string and a text preview from a
// message by type-switching on the concrete llm message types.
func messageRoleAndText(msg llm.Message) (string, string) {
	switch m := msg.(type) {
	case llm.UserMessage:
		return string(m.Role), contentBlocksText(m.Content)
	case llm.AssistantMessage:
		return string(m.Role), contentBlocksText(m.Content)
	case llm.ToolResultMessage[any]:
		return string(m.Role), contentBlocksText(m.Content)
	}
	return "", ""
}

// contentBlocksText concatenates text content blocks into a single string.
func contentBlocksText(blocks []llm.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if tc, ok := b.(llm.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, " ")
}

// truncatePreview shortens a string for a one-line tree row.
func truncatePreview(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	const max = 60
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// entryID safely extracts an entry ID from a node.
func entryID(node *core.SessionTreeNode) string {
	if node == nil || node.Entry == nil {
		return ""
	}
	return node.Entry.ID
}
