package interactive

// context_panel.go implements the /context command: a read-only panel swapped
// into the editor container that visualizes how the model's context window is
// allocated across categories (system prompt, tools, memory files, skills,
// messages, free space, autocompact buffer).
//
// The visualization mirrors the reference design: a grid of small colored
// icons (each icon ≈ a fixed token slice) followed by a legend mapping each
// color to its category with an estimated token count + percentage.
//
// Token counts are ESTIMATES via the same chars/4 heuristic the compaction
// package uses (compaction.EstimateTokens). The provider only reports a single
// aggregate Usage per turn, so per-category attribution is necessarily
// approximate — every row is labeled "est". The anchor "total used" is the
// real number from GetContextUsage() (the same one the footer shows).
//
// The panel is a snapshot computed at construction time (open); closing and
// reopening refreshes the numbers.

import (
	"fmt"
	"strings"

	"ycode/packages/tui"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// ============================================================================
// Token estimation helpers
// ============================================================================

// estimateStringTokens applies the chars/4 heuristic to a plain string. This
// is the string analogue of compaction.EstimateTokens (which takes an
// llm.Message); kept local so the compaction API stays message-shaped.
func estimateStringTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// estimateStringTokensFromChars applies the chars/4 rule directly to a char
// count (used when the caller already accumulated lengths across many fields).
func estimateStringTokensFromChars(chars int) int {
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

// estimateToolsTokens sums the token cost of the given tool definitions by
// serializing each tool's name + description + parameters JSON schema.
func estimateToolsTokens(tools []core.ToolDefinitionEntry) int {
	var chars int
	for _, t := range tools {
		d := t.Definition
		chars += len(d.Name) + len(d.Description)
		if len(d.Parameters) > 0 {
			chars += len(d.Parameters)
		}
	}
	return estimateStringTokensFromChars(chars)
}

// ============================================================================
// Category breakdown model
// ============================================================================

// categoryBreakdown is one row of the /context panel.
type categoryBreakdown struct {
	label     string            // legend label
	tokens    int               // estimated tokens (0 for derived rows)
	icon      string            // grid glyph for this category
	colorSlot theme.ThemeColor  // theme slot for the legend swatch + count text
}

// contextBreakdown is the full snapshot used to render the panel.
type contextBreakdown struct {
	categories    []categoryBreakdown // ordered: system prompt, system tools, mcp/tools, memory, skills, messages
	freeTokens    int                 // derived: contextWindow − used − reserve
	contextWindow int                 // model context window
	usedTotal     int                 // real used total from GetContextUsage (anchor)
	reserveTokens int                 // autocompact buffer
}

// estimateContextBreakdown computes the per-category token breakdown from the
// live session + settings. All category rows are estimates; usedTotal is the
// real anchor from GetContextUsage.
func estimateContextBreakdown(s *core.AgentSession, settings *core.SettingsManager) contextBreakdown {
	var b contextBreakdown

	// Context window (fall back to model's).
	cu := s.GetContextUsage()
	b.contextWindow = cu.ContextWindow
	if b.contextWindow == 0 {
		b.contextWindow = s.GetModel().ContextWindow
	}
	if cu.Tokens != nil {
		b.usedTotal = *cu.Tokens
	}
	if settings != nil {
		b.reserveTokens = settings.GetCompactionReserveTokens()
	}

	// --- System prompt ---
	b.categories = append(b.categories, categoryBreakdown{
		label:     "system prompt",
		tokens:    estimateStringTokens(s.GetSystemPrompt()),
		icon:      "🧠",
		colorSlot: "accent",
	})

	// --- Tools: split builtin vs extension/sdk, subdivide MCP by name prefix ---
	var builtinTools, extTools []core.ToolDefinitionEntry
	for _, t := range s.GetAllTools() {
		src := t.SourceInfo.Source
		switch {
		case strings.HasPrefix(src, "builtin:"):
			builtinTools = append(builtinTools, t)
		case strings.HasPrefix(src, "extension:"), strings.HasPrefix(src, "sdk:"):
			extTools = append(extTools, t)
		}
	}
	b.categories = append(b.categories, categoryBreakdown{
		label:     "system tools",
		tokens:    estimateToolsTokens(builtinTools),
		icon:      "⚙️",
		colorSlot: "borderAccent",
	})

	// Extension tools (MCP + sdk); MCP ones are not separately tagged in the
	// registry, so we keep them in one bucket. Token cost is summed directly
	// here (already split out above) to avoid a second pass.
	var extChars int
	for _, t := range extTools {
		extChars += len(t.Definition.Name) + len(t.Definition.Description)
		if len(t.Definition.Parameters) > 0 {
			extChars += len(t.Definition.Parameters)
		}
	}
	b.categories = append(b.categories, categoryBreakdown{
		label:     "mcp/extension tools",
		tokens:    estimateStringTokensFromChars(extChars),
		icon:      "🧩",
		colorSlot: "success",
	})

	// --- Memory files (AGENTS.md etc.) ---
	var memChars int
	for _, f := range s.GetContextFiles() {
		memChars += len(f.Content)
	}
	b.categories = append(b.categories, categoryBreakdown{
		label:     "memory files",
		tokens:    estimateStringTokensFromChars(memChars),
		icon:      "📝",
		colorSlot: "warning",
	})

	// --- Skills ---
	b.categories = append(b.categories, categoryBreakdown{
		label:     "skills",
		tokens:    estimateStringTokens(core.FormatSkillForPrompt(s.GetSkills())),
		icon:      "✨",
		colorSlot: "thinkingLow",
	})

	// --- Messages: sum EstimateTokens across the branch entries ---
	var msgTokens int
	if s.SessionManager != nil {
		for _, entry := range s.SessionManager.GetEntries() {
			if entry.Type != compaction.EntryTypeMessage {
				continue
			}
			msgTokens += compaction.EstimateTokens(entry.Message)
		}
	}
	b.categories = append(b.categories, categoryBreakdown{
		label:     "messages",
		tokens:    msgTokens,
		icon:      "💬",
		colorSlot: "text",
	})

	// --- Free space (derived) ---
	used := 0
	for _, c := range b.categories {
		used += c.tokens
	}
	if b.contextWindow > 0 {
		free := b.contextWindow - used - b.reserveTokens
		if free < 0 {
			free = 0
		}
		b.freeTokens = free
	}

	return b
}

// ============================================================================
// Panel component
// ============================================================================

// contextPanel is the read-only /context visualization swapped into the editor
// container. It implements tui.Component (via *tui.Container), tui.InputHandler,
// and tui.Focusable, mirroring simpleSelector (selectors.go:41).
type contextPanel struct {
	*tui.Container
	breakdown contextBreakdown
	th        *theme.Theme
	onCancel  func()
	focused   bool
}

// newContextPanel builds the panel. onCancel closes it (restores the editor).
func newContextPanel(b contextBreakdown, th *theme.Theme, onCancel func()) *contextPanel {
	return &contextPanel{
		Container: &tui.Container{},
		breakdown: b,
		th:        th,
		onCancel:  onCancel,
	}
}

// HandleInput closes the panel on Escape / Ctrl+C (read-only; no navigation).
func (p *contextPanel) HandleInput(data string) {
	if tui.MatchesKey(data, "escape") || tui.MatchesKey(data, "ctrl+c") {
		if p.onCancel != nil {
			p.onCancel()
		}
	}
}

// Focused / SetFocused implement tui.Focusable.
func (p *contextPanel) Focused() bool     { return p.focused }
func (p *contextPanel) SetFocused(f bool) { p.focused = f }

// Render builds the panel line-by-line. Every line is truncated to width with
// tui.TruncateToWidth — the engine hard-errors on width overflow.
func (p *contextPanel) Render(width int) []string {
	if width < 4 {
		return []string{}
	}
	th := p.th
	b := p.breakdown
	lines := []string{}

	// Top border with an inline title.
	lines = append(lines, tui.TruncateToWidth(p.titledBorder(width, " Context Window Usage "), width, "", false))

	// Header: total / window (percent).
	if b.contextWindow > 0 {
		used := b.usedTotal
		if used == 0 {
			// Fall back to the sum of category estimates when no real usage yet.
			for _, c := range b.categories {
				used += c.tokens
			}
		}
		pct := 0
		if b.contextWindow > 0 {
			pct = used * 100 / b.contextWindow
		}
		header := fmt.Sprintf("  %s / %s used  (%d%%)", formatTokens(used), formatTokens(b.contextWindow), pct)
		lines = append(lines, tui.TruncateToWidth(th.Fg("dim", header), width, "", false))
	} else {
		lines = append(lines, tui.TruncateToWidth(th.Fg("dim", "  context window unknown"), width, "", false))
	}
	lines = append(lines, "")

	// --- Icon grid ---
	for _, gl := range p.renderIconGrid(width) {
		lines = append(lines, tui.TruncateToWidth(gl, width, "", false))
	}
	lines = append(lines, "")

	// --- Legend ---
	lines = append(lines, tui.TruncateToWidth(th.Fg("dim", "  Legend:"), width, "", false))
	for _, c := range b.categories {
		lines = append(lines, p.renderLegendRow(c, b.contextWindow, width))
	}
	// Free space row.
	lines = append(lines, p.renderLegendRow(
		categoryBreakdown{label: "free space", tokens: b.freeTokens, icon: "🟢", colorSlot: "dim"},
		b.contextWindow, width))
	// Autocompact buffer row (reserved, not a share of used).
	lines = append(lines, p.renderLegendRow(
		categoryBreakdown{label: "autocompact buffer", tokens: b.reserveTokens, icon: "🛡️", colorSlot: "muted"},
		b.contextWindow, width))

	lines = append(lines, "")
	lines = append(lines, tui.TruncateToWidth(th.Fg("dim", "  [Esc] close   (category rows are estimates)"), width, "", false))

	// Bottom border.
	lines = append(lines, tui.TruncateToWidth(p.plainBorder(width), width, "", false))

	return lines
}

// titledBorder renders a "── Title ───" horizontal rule, themed, sized to width.
func (p *contextPanel) titledBorder(width int, title string) string {
	color := fgStyle(p.th, "border")
	tw := tui.VisibleWidth(title)
	dashCount := width - tw
	if dashCount < 2 {
		dashCount = 2
	}
	left := dashCount / 2
	right := dashCount - left
	return color(strings.Repeat("─", left) + title + strings.Repeat("─", right))
}

// plainBorder renders a full-width themed horizontal rule.
func (p *contextPanel) plainBorder(width int) string {
	color := fgStyle(p.th, "border")
	if width < 1 {
		width = 1
	}
	return color(strings.Repeat("─", width))
}

// renderIconGrid lays out the colored icon grid. Each icon represents ~1% of
// the context window; icons wrap to fit width. Emoji glyphs ignore SGR color,
// so the legend swatch (●) is what actually carries each category's color.
func (p *contextPanel) renderIconGrid(width int) []string {
	b := p.breakdown
	if b.contextWindow <= 0 {
		return []string{}
	}
	th := p.th

	// Build the flat list of (icon, color) cells across categories + free.
	type cell struct {
		icon      string
		colorSlot theme.ThemeColor
	}
	var cells []cell
	addCells := func(tokens int, icon string, colorSlot theme.ThemeColor) {
		if tokens <= 0 {
			return
		}
		n := tokens * 100 / b.contextWindow // ~1 icon per 1% of the window
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			cells = append(cells, cell{icon, colorSlot})
		}
	}
	for _, c := range b.categories {
		addCells(c.tokens, c.icon, c.colorSlot)
	}
	addCells(b.freeTokens, "🟢", "dim")
	if len(cells) == 0 {
		return []string{}
	}

	// Width accounting: emoji glyphs are width-2; space separator is width-1.
	const iconW = 2
	iconsPerRow := width / (iconW + 1)
	if iconsPerRow < 1 {
		iconsPerRow = 1
	}

	var lines []string
	var sb strings.Builder
	count := 0
	flush := func() {
		lines = append(lines, sb.String())
		sb.Reset()
		count = 0
	}
	for _, c := range cells {
		sb.WriteString(th.Fg(c.colorSlot, c.icon))
		count++
		if count >= iconsPerRow {
			flush()
		} else {
			sb.WriteString(" ")
		}
	}
	if count > 0 {
		flush()
	}
	return lines
}

// renderLegendRow builds one "● label   ~count  (pct%)  est" line.
func (p *contextPanel) renderLegendRow(c categoryBreakdown, contextWindow, width int) string {
	th := p.th
	swatch := th.Fg(c.colorSlot, "●")
	countStr := formatTokens(c.tokens)
	pctStr := "-"
	if contextWindow > 0 {
		pctStr = fmt.Sprintf("%d%%", c.tokens*100/contextWindow)
	}
	left := fmt.Sprintf("  %s %s", swatch, c.label)
	right := fmt.Sprintf("~%s  (%s)  est", countStr, pctStr)
	leftW := tui.VisibleWidth(left)
	rightW := tui.VisibleWidth(right)
	pad := width - leftW - rightW
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + th.Fg("dim", right)
}
