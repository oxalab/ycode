package components

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// sessionScope toggles between current-folder and all sessions.
type sessionScope int

const (
	sessionScopeCurrent sessionScope = iota
	sessionScopeAll
)

// sessionSortMode controls ordering.
type sessionSortMode int

const (
	sessionSortThreaded sessionSortMode = iota // threaded tree (parent/child)
	sessionSortRecent                          // flat, newest first
	sessionSortRelevance                       // fuzzy match score
)

// sessionNameFilter filters by named vs all.
type sessionNameFilter int

const (
	sessionNameAll sessionNameFilter = iota
	sessionNameNamed
)

// flatSessionNode is one row in the rendered session list.
type flatSessionNode struct {
	session          *core.SessionInfo
	depth            int
	isLast           bool
	ancestorContinue []bool // per ancestor level: draw │ (true) or spaces
}

// SessionSelectorComponent renders a session picker with a threaded tree view,
// search, scope toggle (current/all), sort modes, and named filter.
//
// Port of reference/coding-agent/src/modes/interactive/components/session-selector.ts
// (core browsing/filtering/sorting/selection; delete & rename are stubbed for later).
type SessionSelectorComponent struct {
	theme    *theme.Theme
	width    int
	maxVisible int

	// raw session pools per scope
	currentSessions []*core.SessionInfo
	allSessions     []*core.SessionInfo

	// view state
	filtered []*flatSessionNode
	cursor   int
	scope    sessionScope
	sortMode sessionSortMode
	nameFilter sessionNameFilter
	showPath bool

	search textinput.Model

	currentSessionPath string

	onSelect func(sessionPath string) tea.Cmd
	onCancel func() tea.Cmd
}

// SessionSelectorCallbacks holds the result handlers supplied by the App.
type SessionSelectorCallbacks struct {
	OnSelect func(sessionPath string) tea.Cmd
	OnCancel func() tea.Cmd
}

// NewSessionSelectorComponent creates a new session selector.
// currentSessions are the sessions in the current folder; allSessions are
// across all folders. currentSessionPath marks the active session (cannot be
// deleted; highlighted distinctly).
func NewSessionSelectorComponent(
	t *theme.Theme,
	currentSessions, allSessions []*core.SessionInfo,
	currentSessionPath string,
	callbacks SessionSelectorCallbacks,
) *SessionSelectorComponent {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "Search sessions..."
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 50

	s := &SessionSelectorComponent{
		theme:              t,
		currentSessions:    currentSessions,
		allSessions:        allSessions,
		currentSessionPath: currentSessionPath,
		scope:              sessionScopeCurrent,
		sortMode:           sessionSortThreaded,
		nameFilter:         sessionNameAll,
		maxVisible:         10,
		search:             ti,
		onSelect:           callbacks.OnSelect,
		onCancel:           callbacks.OnCancel,
	}
	s.refilter()
	return s
}

// SetWidth updates the available width.
func (s *SessionSelectorComponent) SetWidth(width int) {
	s.width = width
	if width > 2 {
		s.search.Width = width - 2
	}
}

// Init satisfies tea.Model.
func (s *SessionSelectorComponent) Init() tea.Cmd { return nil }

// Update handles navigation, toggles, search typing, and selection.
func (s *SessionSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}

	switch keyMsg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
		return s, nil
	case "down", "j":
		if s.cursor < len(s.filtered)-1 {
			s.cursor++
		}
		return s, nil
	case "pgup":
		s.cursor -= s.maxVisible
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil
	case "pgdown":
		s.cursor += s.maxVisible
		if s.cursor >= len(s.filtered) {
			s.cursor = len(s.filtered) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil
	case "tab":
		s.toggleScope()
		return s, nil
	case "ctrl+s":
		s.cycleSortMode()
		return s, nil
	case "ctrl+n":
		s.toggleNameFilter()
		return s, nil
	case "ctrl+p":
		s.showPath = !s.showPath
		return s, nil
	case "enter":
		if s.cursor >= 0 && s.cursor < len(s.filtered) {
			path := s.filtered[s.cursor].session.Path
			if s.onSelect != nil {
				return s, s.onSelect(path)
			}
		}
		return s, nil
	case "escape", "ctrl+c":
		if s.onCancel != nil {
			return s, s.onCancel()
		}
		return s, nil
	}

	// Typing → search input → refilter.
	var cmd tea.Cmd
	s.search, cmd = s.search.Update(msg)
	s.refilter()
	return s, cmd
}

// View renders the header + search + session list within a bordered box.
func (s SessionSelectorComponent) View() string {
	width := s.width
	if width <= 0 {
		width = 60
	}

	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("accent")).
		Padding(0, 1).
		Width(width - 2)

	var sections []string
	sections = append(sections, s.renderHeader())
	sections = append(sections, s.search.View())
	sections = append(sections, strings.Join(s.renderList(), "\n"))

	return border.Render(strings.Join(sections, "\n"))
}

// --- Internal: toggles ---

func (s *SessionSelectorComponent) toggleScope() {
	if s.scope == sessionScopeCurrent {
		s.scope = sessionScopeAll
	} else {
		s.scope = sessionScopeCurrent
	}
	s.refilter()
}

func (s *SessionSelectorComponent) cycleSortMode() {
	// threaded → recent → relevance → threaded
	if s.sortMode == sessionSortThreaded {
		s.sortMode = sessionSortRecent
	} else if s.sortMode == sessionSortRecent {
		s.sortMode = sessionSortRelevance
	} else {
		s.sortMode = sessionSortThreaded
	}
	s.refilter()
}

func (s *SessionSelectorComponent) toggleNameFilter() {
	if s.nameFilter == sessionNameAll {
		s.nameFilter = sessionNameNamed
	} else {
		s.nameFilter = sessionNameAll
	}
	s.refilter()
}

// --- Internal: filtering ---

// activeSessions returns the pool for the current scope.
func (s *SessionSelectorComponent) activeSessions() []*core.SessionInfo {
	if s.scope == sessionScopeAll {
		return s.allSessions
	}
	return s.currentSessions
}

// refilter rebuilds the flat node list from the active pool.
func (s *SessionSelectorComponent) refilter() {
	pool := s.activeSessions()

	query := strings.TrimSpace(s.search.Value())

	if s.sortMode == sessionSortThreaded && query == "" {
		// Threaded mode without search: tree view (apply name filter only).
		var filtered []*core.SessionInfo
		for _, sess := range pool {
			if s.nameFilter == sessionNameNamed && !hasSessionName(sess) {
				continue
			}
			filtered = append(filtered, sess)
		}
		roots := buildSessionTree(filtered)
		s.filtered = flattenSessionTree(roots)
	} else {
		// Flat list with full query parsing (regex / phrases / fuzzy),
		// filtered and sorted by mode.
		sorted := filterAndSortSessions(pool, s.search.Value(), s.sortMode, s.nameFilter)
		s.filtered = make([]*flatSessionNode, len(sorted))
		for i, sess := range sorted {
			s.filtered[i] = &flatSessionNode{session: sess, depth: 0, isLast: true}
		}
	}

	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// --- Internal: rendering ---

// renderHeader renders the status line (scope/sort/name) + hint line.
func (s SessionSelectorComponent) renderHeader() string {
	scopeText := s.theme.Fg("muted", "○ Current Folder | ○ All")
	if s.scope == sessionScopeCurrent {
		scopeText = s.theme.Fg("accent", "◉ Current Folder") + s.theme.Fg("muted", " | ○ All")
	} else {
		scopeText = s.theme.Fg("muted", "○ Current Folder | ") + s.theme.Fg("accent", "◉ All")
	}

	sortLabel := "Threaded"
	if s.sortMode == sessionSortRecent {
		sortLabel = "Recent"
	} else if s.sortMode == sessionSortRelevance {
		sortLabel = "Relevance"
	}
	sortText := s.theme.Fg("muted", "Sort: ") + s.theme.Fg("accent", sortLabel)

	nameLabel := "All"
	if s.nameFilter == sessionNameNamed {
		nameLabel = "Named"
	}
	nameText := s.theme.Fg("muted", "Name: ") + s.theme.Fg("accent", nameLabel)

	return scopeText + "  " + nameText + "  " + sortText
}

// renderList renders the visible window of session rows.
func (s SessionSelectorComponent) renderList() []string {
	if len(s.filtered) == 0 {
		msg := "  No sessions found"
		if s.scope == sessionScopeCurrent {
			msg = "  No sessions in current folder. Press Tab to view all."
		}
		return []string{s.theme.Fg("muted", msg)}
	}

	n := len(s.filtered)
	start := s.cursor - s.maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + s.maxVisible
	if end > n {
		end = n
		start = end - s.maxVisible
		if start < 0 {
			start = 0
		}
	}

	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		lines = append(lines, s.renderRow(s.filtered[i], i == s.cursor))
	}

	if start > 0 || end < n {
		lines = append(lines, s.theme.Fg("muted", "  ("+itoa(s.cursor+1)+"/"+itoa(n)+")"))
	}
	return lines
}

// renderRow renders one session with tree prefix, label, and right-side meta.
func (s SessionSelectorComponent) renderRow(node *flatSessionNode, selected bool) string {
	sess := node.session

	// Cursor marker.
	cursor := "  "
	if selected {
		cursor = s.theme.Fg("accent", "› ")
	}

	// Tree prefix.
	prefix := s.treePrefix(node)
	prefixStyled := ""
	if prefix != "" {
		prefixStyled = s.theme.Fg("dim", prefix)
	}

	// Label: name or first message.
	display := sess.Name
	hasName := display != ""
	if display == "" {
		display = sess.FirstMessage
	}
	display = sanitizeDisplay(display)

	// Right-side: message count + age (+ optional cwd/path).
	right := itoa(sess.MessageCount) + " " + formatSessionAge(sess.Modified)
	if s.scope == sessionScopeAll && sess.Cwd != "" {
		right = shortenPath(sess.Cwd) + " " + right
	}
	if s.showPath {
		right = shortenPath(sess.Path) + " " + right
	}

	// Color the label by state.
	colorKey := ""
	isCurrent := s.currentSessionPath != "" && canonicalize(sess.Path) == canonicalize(s.currentSessionPath)
	if isCurrent {
		colorKey = "accent"
	} else if hasName {
		colorKey = "warning"
	}
	label := display
	if colorKey != "" {
		label = s.theme.Fg(theme.ThemeColor(colorKey), label)
	}
	if selected {
		label = s.theme.Bold(label)
	}

	left := cursor + prefixStyled + label
	rightStyled := s.theme.Fg("dim", right)

	// Pad between left and right.
	gap := s.width - lipgloss.Width(left) - lipgloss.Width(rightStyled) - 2
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + rightStyled

	// Highlight selected row with selectedBg.
	if selected {
		line = s.theme.Bg("selectedBg", line)
	}
	return line
}

// treePrefix builds the indentation + connector prefix for a node.
func (s SessionSelectorComponent) treePrefix(node *flatSessionNode) string {
	if node.depth == 0 {
		return ""
	}
	var b strings.Builder
	for _, cont := range node.ancestorContinue {
		if cont {
			b.WriteString("│  ")
		} else {
			b.WriteString("   ")
		}
	}
	if node.isLast {
		b.WriteString("└─ ")
	} else {
		b.WriteString("├─ ")
	}
	return b.String()
}

// ============================================================================
// Tree building (parent/child session relationships)
// ============================================================================

type sessionTreeNode struct {
	session  *core.SessionInfo
	children []*sessionTreeNode
}

// buildSessionTree groups sessions by parentSessionPath, roots sorted by modified desc.
func buildSessionTree(sessions []*core.SessionInfo) []*sessionTreeNode {
	byPath := make(map[string]*sessionTreeNode, len(sessions))
	for _, sess := range sessions {
		p := canonicalize(sess.Path)
		if p == "" {
			p = sess.Path
		}
		byPath[p] = &sessionTreeNode{session: sess}
	}

	var roots []*sessionTreeNode
	for _, sess := range sessions {
		p := canonicalize(sess.Path)
		if p == "" {
			p = sess.Path
		}
		node := byPath[p]
		parent := canonicalize(sess.ParentSessionPath)
		if parent != "" {
			if parentNode, ok := byPath[parent]; ok {
				parentNode.children = append(parentNode.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	var sortNodes func(nodes []*sessionTreeNode)
	sortNodes = func(nodes []*sessionTreeNode) {
		sort.SliceStable(nodes, func(a, b int) bool {
			return nodes[a].session.Modified.After(nodes[b].session.Modified)
		})
		for _, n := range nodes {
			sortNodes(n.children)
		}
	}
	sortNodes(roots)
	return roots
}

// flattenSessionTree walks the tree into a flat display list.
func flattenSessionTree(roots []*sessionTreeNode) []*flatSessionNode {
	var result []*flatSessionNode
	var walk func(node *sessionTreeNode, depth int, ancestorContinue []bool, isLast bool)
	walk = func(node *sessionTreeNode, depth int, ancestorContinue []bool, isLast bool) {
		result = append(result, &flatSessionNode{
			session:          node.session,
			depth:            depth,
			isLast:           isLast,
			ancestorContinue: append([]bool{}, ancestorContinue...),
		})
		for i, child := range node.children {
			childIsLast := i == len(node.children)-1
			cont := false
			if depth > 0 {
				cont = !isLast
			}
			walk(child, depth+1, append(append([]bool{}, ancestorContinue...), cont), childIsLast)
		}
	}
	for i, root := range roots {
		walk(root, 0, nil, i == len(roots)-1)
	}
	return result
}

// ============================================================================
// Sorting & helpers
// ============================================================================
//
// Session filtering and sorting live in session_selector_search.go
// (filterAndSortSessions, parseSearchQuery, matchSession). The helpers below
// are rendering-only utilities.

// hasSessionName reports whether a session has a non-empty name.
func hasSessionName(sess *core.SessionInfo) bool {
	return sess != nil && strings.TrimSpace(sess.Name) != ""
}

// formatSessionAge renders a relative age string (now/5m/3h/2d/1w/3mo/1y).
func formatSessionAge(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Minute {
		return "now"
	}
	if diff < time.Hour {
		return itoa(int(diff/time.Minute)) + "m"
	}
	if diff < 24*time.Hour {
		return itoa(int(diff/time.Hour)) + "h"
	}
	days := int(diff / (24 * time.Hour))
	if days < 7 {
		return itoa(days) + "d"
	}
	if days < 30 {
		return itoa(days/7) + "w"
	}
	if days < 365 {
		return itoa(days/30) + "mo"
	}
	return itoa(days/365) + "y"
}

// shortenPath replaces the home directory prefix with ~.
func shortenPath(p string) string {
	if p == "" {
		return p
	}
	home := homeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// sanitizeDisplay strips control characters for one-line display.
func sanitizeDisplay(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// canonicalize resolves a path to its absolute form for comparison.
func canonicalize(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// homeDir returns the user's home directory (cross-platform).
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
