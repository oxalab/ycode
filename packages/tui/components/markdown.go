package components

import (
	"strings"

	"ycode/packages/tui"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gmdtext "github.com/yuin/goldmark/text"
)

// MarkdownTheme styles each markdown element.
type MarkdownTheme struct {
	Heading       tui.StyleFunc
	Link          tui.StyleFunc
	LinkUrl       tui.StyleFunc
	Code          tui.StyleFunc
	CodeBlock     tui.StyleFunc
	CodeBlockBorder tui.StyleFunc
	Quote         tui.StyleFunc
	QuoteBorder   tui.StyleFunc
	HR            tui.StyleFunc
	ListBullet    tui.StyleFunc
	Bold          tui.StyleFunc
	Italic        tui.StyleFunc
	Strikethrough tui.StyleFunc
	Underline     tui.StyleFunc
	HighlightCode func(code, lang string) []string
	CodeBlockIndent string
}

// DefaultTextStyle applies base styling to all markdown text.
type DefaultTextStyle struct {
	Color        tui.StyleFunc
	BgColor      tui.StyleFunc
	Bold         bool
	Italic       bool
	Strikethrough bool
	Underline    bool
}

// MarkdownOptions configures list-marker behavior.
type MarkdownOptions struct {
	PreserveOrderedListMarkers bool
}

type mdStyleContext struct {
	applyText   func(string) string
	stylePrefix string
}

// Markdown renders markdown to styled terminal lines.
type Markdown struct {
	text     string
	paddingX int
	paddingY int
	theme    MarkdownTheme
	defStyle *DefaultTextStyle
	options  MarkdownOptions

	defStylePrefix string

	cachedText  string
	cachedWidth int
	cachedLines []string
}

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

// NewMarkdown returns a Markdown component.
func NewMarkdown(text string, paddingX, paddingY int, theme MarkdownTheme, defStyle *DefaultTextStyle, options ...MarkdownOptions) *Markdown {
	opts := MarkdownOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	return &Markdown{text: text, paddingX: paddingX, paddingY: paddingY, theme: theme, defStyle: defStyle, options: opts}
}

// SetText updates the markdown (clears cache).
func (m *Markdown) SetText(text string) { m.text = text; m.invalidate() }

// Invalidate clears the cache.
func (m *Markdown) Invalidate() { m.invalidate() }

func (m *Markdown) invalidate() {
	m.cachedText = ""
	m.cachedWidth = 0
	m.cachedLines = nil
	m.defStylePrefix = ""
}

// Render produces styled lines.
func (m *Markdown) Render(width int) []string {
	if m.cachedLines != nil && m.cachedText == m.text && m.cachedWidth == width {
		return m.cachedLines
	}
	contentWidth := width - m.paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}
	if strings.TrimSpace(m.text) == "" {
		result := []string{}
		m.cachedText = m.text
		m.cachedWidth = width
		m.cachedLines = result
		return result
	}
	source := []byte(strings.ReplaceAll(m.text, "\t", "   "))
	reader := gmdtext.NewReader(source)
	doc := markdownParser.Parser().Parse(reader)

	var rawLines []string
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		nextKind := ""
		if ns := child.NextSibling(); ns != nil {
			nextKind = ns.Kind().String()
		}
		rawLines = append(rawLines, m.renderBlock(child, contentWidth, nextKind, source, m.defaultCtx(contentWidth))...)
	}

	// Wrap non-image lines.
	var wrapped []string
	for _, line := range rawLines {
		if tui.IsImageLine(line) {
			wrapped = append(wrapped, line)
			continue
		}
		wrapped = append(wrapped, tui.WrapTextWithAnsi(line, contentWidth)...)
	}

	// Pad + background.
	left := strings.Repeat(" ", m.paddingX)
	right := strings.Repeat(" ", m.paddingX)
	var bgFn tui.StyleFunc
	if m.defStyle != nil {
		bgFn = m.defStyle.BgColor
	}
	var contentLines []string
	empty := strings.Repeat(" ", width)
	for _, line := range wrapped {
		if tui.IsImageLine(line) {
			contentLines = append(contentLines, line)
			continue
		}
		lwm := left + line + right
		if bgFn != nil {
			contentLines = append(contentLines, tui.ApplyBackgroundToLine(lwm, width, bgFn))
		} else {
			pad := width - tui.VisibleWidth(lwm)
			if pad > 0 {
				lwm += strings.Repeat(" ", pad)
			}
			contentLines = append(contentLines, lwm)
		}
	}
	var emptyLines []string
	for i := 0; i < m.paddingY; i++ {
		if bgFn != nil {
			emptyLines = append(emptyLines, tui.ApplyBackgroundToLine(empty, width, bgFn))
		} else {
			emptyLines = append(emptyLines, empty)
		}
	}
	result := append(append(append([]string{}, emptyLines...), contentLines...), emptyLines...)
	if len(result) == 0 {
		result = []string{""}
	}
	m.cachedText = m.text
	m.cachedWidth = width
	m.cachedLines = result
	return result
}

// --- Style contexts ---

func (m *Markdown) defaultCtx(width int) *mdStyleContext {
	return &mdStyleContext{applyText: m.applyDefaultStyle, stylePrefix: m.getDefStylePrefix()}
}

func (m *Markdown) applyDefaultStyle(text string) string {
	if m.defStyle == nil {
		return text
	}
	styled := text
	if m.defStyle.Color != nil {
		styled = m.defStyle.Color(styled)
	}
	if m.defStyle.Bold {
		styled = m.theme.Bold(styled)
	}
	if m.defStyle.Italic {
		styled = m.theme.Italic(styled)
	}
	if m.defStyle.Strikethrough {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defStyle.Underline {
		styled = m.theme.Underline(styled)
	}
	return styled
}

func (m *Markdown) getDefStylePrefix() string {
	if m.defStylePrefix != "" || m.defStyle == nil {
		return m.defStylePrefix
	}
	sentinel := "\x00"
	styled := sentinel
	if m.defStyle.Color != nil {
		styled = m.defStyle.Color(styled)
	}
	if m.defStyle.Bold {
		styled = m.theme.Bold(styled)
	}
	if m.defStyle.Italic {
		styled = m.theme.Italic(styled)
	}
	if m.defStyle.Strikethrough {
		styled = m.theme.Strikethrough(styled)
	}
	if m.defStyle.Underline {
		styled = m.theme.Underline(styled)
	}
	idx := strings.Index(styled, sentinel)
	if idx >= 0 {
		m.defStylePrefix = styled[:idx]
	}
	return m.defStylePrefix
}

func stylePrefixFrom(fn func(string) string) string {
	sentinel := "\x00"
	styled := fn(sentinel)
	idx := strings.Index(styled, sentinel)
	if idx >= 0 {
		return styled[:idx]
	}
	return ""
}

// --- Block rendering ---

func (m *Markdown) renderBlock(node ast.Node, width int, nextKind string, source []byte, ctx *mdStyleContext) []string {
	var lines []string
	addSpace := nextKind != "" && nextKind != "ThematicBreak" && nextKind != "Paragraph"
	_ = addSpace

	switch node.Kind().String() {
	case "Heading":
		lines = m.renderHeading(node, width, source)
		if nextKind != "" && nextKind != "ThematicBreak" {
			lines = append(lines, "")
		}
	case "Paragraph":
		lines = []string{m.renderInlineChildren(node, source, ctx)}
		if nextKind != "" && nextKind != "List" && nextKind != "ThematicBreak" {
			lines = append(lines, "")
		}
	case "Text":
		lines = []string{m.renderInlineChildren(node, source, ctx)}
	case "FencedCodeBlock", "CodeBlock":
		lines = m.renderCodeBlock(node, width, source, nextKind)
	case "List":
		lines = m.renderList(node, 0, width, source, ctx)
	case "Blockquote":
		lines = m.renderBlockquote(node, width, source, nextKind)
	case "ThematicBreak":
		lines = []string{m.theme.HR(strings.Repeat("─", minIntMD(width, 80)))}
		if nextKind != "" && nextKind != "ThematicBreak" {
			lines = append(lines, "")
		}
	case "HTMLBlock":
		lines = []string{m.applyDefaultStyle(strings.TrimSpace(string(node.Text(source))))}
	case "Table":
		lines = m.renderTable(node, width, source)
	default:
		if t := node.Text(source); len(t) > 0 {
			lines = []string{string(t)}
		}
	}
	return lines
}

func (m *Markdown) renderHeading(node ast.Node, width int, source []byte) []string {
	h := node.(*ast.Heading)
	level := h.Level
	var styleFn func(string) string
	if level == 1 {
		styleFn = func(s string) string { return m.theme.Heading(m.theme.Bold(m.theme.Underline(s))) }
	} else {
		styleFn = func(s string) string { return m.theme.Heading(m.theme.Bold(s)) }
	}
	sp := stylePrefixFrom(styleFn)
	ctx := &mdStyleContext{applyText: styleFn, stylePrefix: sp}
	text := m.renderInlineChildren(node, source, ctx)
	if level >= 3 {
		return []string{styleFn(strings.Repeat("#", level)+" ") + text}
	}
	return []string{text}
}

func (m *Markdown) renderCodeBlock(node ast.Node, width int, source []byte, nextKind string) []string {
	indent := m.theme.CodeBlockIndent
	if indent == "" {
		indent = "  "
	}
	var lang string
	if fc, ok := node.(*ast.FencedCodeBlock); ok {
		lang = string(fc.Language(source))
	}
	var lines []string
	lines = append(lines, m.theme.CodeBlockBorder("```"+lang))
	if m.theme.HighlightCode != nil {
		codeText := getCodeText(node, source)
		for _, hl := range m.theme.HighlightCode(codeText, lang) {
			lines = append(lines, indent+hl)
		}
	} else {
		codeText := getCodeText(node, source)
		for _, codeLine := range strings.Split(codeText, "\n") {
			lines = append(lines, indent+m.theme.CodeBlock(codeLine))
		}
	}
	lines = append(lines, m.theme.CodeBlockBorder("```"))
	if nextKind != "" && nextKind != "ThematicBreak" {
		lines = append(lines, "")
	}
	return lines
}

func getCodeText(node ast.Node, source []byte) string {
	var sb strings.Builder
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		sb.Write(seg.Value(source))
	}
	s := sb.String()
	return strings.TrimSuffix(s, "\n")
}

func (m *Markdown) renderList(node ast.Node, depth, width int, source []byte, ctx *mdStyleContext) []string {
	list := node.(*ast.List)
	indent := strings.Repeat("    ", depth)
	start := 1
	if list.IsOrdered() {
		start = int(list.Start)
	}
	isTight := list.IsTight

	var lines []string
	itemIdx := 0
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind().String() == "List" {
			lines = append(lines, m.renderList(child, depth+1, width, source, ctx)...)
			continue
		}
		isLast := child.NextSibling() == nil
		var bullet string
		if list.IsOrdered() {
			bullet = itoaMD(start+itemIdx) + ". "
		} else {
			bullet = "- "
		}
		taskMarker := ""
		// Check for TaskCheckBox as first grandchild.
		if fc := child.FirstChild(); fc != nil {
			if tc := fc.FirstChild(); tc != nil && tc.Kind().String() == "TaskCheckBox" {
				if checked, ok := getTaskChecked(tc); ok {
					if checked {
						taskMarker = "[x] "
					} else {
						taskMarker = "[ ] "
					}
				}
			}
		}
		marker := bullet + taskMarker
		firstPrefix := indent + m.theme.ListBullet(marker)
		contPrefix := indent + strings.Repeat(" ", mdVisibleWidth(marker))
		itemWidth := width - mdVisibleWidth(firstPrefix)
		if itemWidth < 1 {
			itemWidth = 1
		}
		var itemLines []string
		renderedAny := false
		for itemChild := child.FirstChild(); itemChild != nil; itemChild = itemChild.NextSibling() {
			if itemChild.Kind().String() == "List" {
				itemLines = append(itemLines, m.renderList(itemChild, depth+1, width, source, ctx)...)
				renderedAny = true
				continue
			}
			blockLines := m.renderBlock(itemChild, itemWidth, "", source, ctx)
			for _, line := range blockLines {
				for _, wl := range tui.WrapTextWithAnsi(line, itemWidth) {
					prefix := contPrefix
					if !renderedAny {
						prefix = firstPrefix
					}
					itemLines = append(itemLines, prefix+wl)
					renderedAny = true
				}
			}
		}
		if !renderedAny {
			itemLines = append(itemLines, firstPrefix)
		}
		if !isTight && !isLast {
			itemLines = append(itemLines, "")
		}
		lines = append(lines, itemLines...)
		itemIdx++
	}
	return lines
}

func (m *Markdown) renderBlockquote(node ast.Node, width int, source []byte, nextKind string) []string {
	quoteStyle := func(s string) string { return m.theme.Quote(m.theme.Italic(s)) }
	sp := stylePrefixFrom(quoteStyle)
	ctx := &mdStyleContext{applyText: quoteStyle, stylePrefix: sp}
	quoteContentWidth := width - 2
	if quoteContentWidth < 1 {
		quoteContentWidth = 1
	}
	var inner []string
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		inner = append(inner, m.renderBlock(child, quoteContentWidth, "", source, ctx)...)
	}
	for len(inner) > 0 && inner[len(inner)-1] == "" {
		inner = inner[:len(inner)-1]
	}
	var lines []string
	for _, line := range inner {
		styled := line
		if sp != "" {
			styled = strings.ReplaceAll(styled, "\x1b[0m", "\x1b[0m"+sp)
		}
		styled = quoteStyle(styled)
		for _, wl := range tui.WrapTextWithAnsi(styled, quoteContentWidth) {
			lines = append(lines, m.theme.QuoteBorder("│ ")+wl)
		}
	}
	if nextKind != "" && nextKind != "ThematicBreak" {
		lines = append(lines, "")
	}
	return lines
}

// renderTable renders a GFM table with aligned columns, borders, and a bold
// header row. (Port of the reference's table rendering.)
func (m *Markdown) renderTable(node ast.Node, width int, source []byte) []string {
	var rows [][]string
	var headerCells []string
	hasHeader := false

	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind().String() {
		case "TableHeader":
			hasHeader = true
			for row := child.FirstChild(); row != nil; row = row.NextSibling() {
				var cells []string
				for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
					cells = append(cells, m.renderInlineChildren(cell, source, m.defaultCtx(width)))
				}
				headerCells = cells
			}
		case "TableRow":
			var cells []string
			for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
				cells = append(cells, m.renderInlineChildren(cell, source, m.defaultCtx(width)))
			}
			rows = append(rows, cells)
		}
	}

	allRows := rows
	if hasHeader && len(headerCells) > 0 {
		allRows = append([][]string{headerCells}, rows...)
	}
	if len(allRows) == 0 {
		return nil
	}

	numCols := len(allRows[0])
	colWidths := make([]int, numCols)
	for _, row := range allRows {
		for j := 0; j < len(row) && j < numCols; j++ {
			if w := tui.VisibleWidth(row[j]); w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	border := m.theme.CodeBlockBorder
	var lines []string
	for i, row := range allRows {
		var parts []string
		for j := 0; j < numCols; j++ {
			cell := ""
			if j < len(row) {
				cell = row[j]
			}
			cell += strings.Repeat(" ", colWidths[j]-tui.VisibleWidth(cell))
			if i == 0 && hasHeader {
				cell = m.theme.Bold(cell)
			}
			parts = append(parts, cell)
		}
		lines = append(lines, border("|")+" "+strings.Join(parts, " "+border("|")+" ")+" "+border("|"))

		if i == 0 && hasHeader {
			var seps []string
			for j := 0; j < numCols; j++ {
				seps = append(seps, strings.Repeat("-", colWidths[j]+2))
			}
			lines = append(lines, border("|")+strings.Join(seps, border("|"))+border("|"))
		}
	}
	lines = append(lines, "")
	return lines
}

// --- Inline rendering ---

func (m *Markdown) renderInlineChildren(node ast.Node, source []byte, ctx *mdStyleContext) string {
	var result string
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		result += m.renderInlineNode(child, source, ctx)
	}
	for ctx.stylePrefix != "" && strings.HasSuffix(result, ctx.stylePrefix) {
		result = strings.TrimSuffix(result, ctx.stylePrefix)
	}
	return result
}

func (m *Markdown) renderInlineNode(node ast.Node, source []byte, ctx *mdStyleContext) string {
	switch node.Kind().String() {
	case "Text":
		text := string(node.Text(source))
		parts := strings.Split(text, "\n")
		var sb strings.Builder
		for i, s := range parts {
			if i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(ctx.applyText(s))
		}
		return sb.String()
	case "Emph":
		content := m.renderInlineChildren(node, source, ctx)
		return m.theme.Italic(content) + ctx.stylePrefix
	case "Strong":
		content := m.renderInlineChildren(node, source, ctx)
		return m.theme.Bold(content) + ctx.stylePrefix
	case "CodeSpan":
		return m.theme.Code(strings.TrimSpace(string(node.Text(source)))) + ctx.stylePrefix
	case "Link":
		text := m.renderInlineChildren(node, source, ctx)
		styled := m.theme.Link(m.theme.Underline(text))
		href := ""
		if l, ok := node.(*ast.Link); ok {
			href = string(l.Destination)
		}
		if tui.GetCapabilities().Hyperlinks {
			return tui.Hyperlink(styled, href) + ctx.stylePrefix
		}
		compareHref := href
		if strings.HasPrefix(compareHref, "mailto:") {
			compareHref = compareHref[7:]
		}
		textContent := strings.TrimSpace(string(node.Text(source)))
		if textContent == href || textContent == compareHref {
			return styled + ctx.stylePrefix
		}
		return styled + m.theme.LinkUrl(" ("+href+")") + ctx.stylePrefix
	case "Strikethrough":
		content := m.renderInlineChildren(node, source, ctx)
		return m.theme.Strikethrough(content) + ctx.stylePrefix
	case "Image":
		return "[image]"
	case "Softbreak", "Hardbreak":
		return "\n"
	case "HTMLInline":
		return ctx.applyText(string(node.Text(source)))
	}
	return ctx.applyText(string(node.Text(source)))
}

// --- helpers ---

func getTaskChecked(node ast.Node) (bool, bool) {
	// goldmark TaskList extension uses ast.TaskCheckBox with a checked attribute.
	// The node is of type *ast.TaskCheckBox but it's an internal type.
	// We check by reading the source text between brackets.
	return false, false // TODO: wire TaskCheckBox.IsChecked via reflection or source parse
}

func itoaMD(n int) string {
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

func minIntMD(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mdVisibleWidth(s string) int { return tui.VisibleWidth(s) }
