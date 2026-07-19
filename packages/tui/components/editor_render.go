package components

import (
	"strings"
	"unicode/utf8"

	"ycode/packages/tui"

	"github.com/rivo/uniseg"
)

// TextChunk represents a word-wrapped segment of a line.
type TextChunk struct {
	Text       string
	StartIndex int // rune index into the original line
	EndIndex   int // rune index (exclusive)
}

type visualLine struct {
	logicalLine int
	startCol    int // rune index
	length      int // rune count
}

type editorLayoutLine struct {
	text      string
	hasCursor bool
	cursorPos int // rune index into text
}

// WordWrapLine wraps a line into chunks that fit within maxWidth visible columns.
// Exported so editor.test.ts-style tests can call it directly.
func WordWrapLine(line string, maxWidth int, _ []TextChunk) []TextChunk {
	if line == "" || maxWidth <= 0 {
		return []TextChunk{{Text: "", StartIndex: 0, EndIndex: 0}}
	}
	lineWidth := tui.VisibleWidth(line)
	totalRunes := utf8.RuneCountInString(line)
	if lineWidth <= maxWidth {
		return []TextChunk{{Text: line, StartIndex: 0, EndIndex: totalRunes}}
	}

	type segment struct {
		text  string
		index int // rune index
	}

	var segments []segment
	runeIdx := 0
	gr := uniseg.NewGraphemes(line)
	for gr.Next() {
		segments = append(segments, segment{text: gr.Str(), index: runeIdx})
		runeIdx += utf8.RuneCountInString(gr.Str())
	}

	runes := []rune(line)
	var chunks []TextChunk
	currentWidth := 0
	chunkStart := 0
	wrapOppIdx := -1
	wrapOppWidth := 0

	for i, seg := range segments {
		gWidth := tui.VisibleWidth(seg.text)

		if currentWidth+gWidth > maxWidth {
			if wrapOppIdx >= 0 && currentWidth-wrapOppWidth+gWidth <= maxWidth {
				chunks = append(chunks, TextChunk{Text: string(runes[chunkStart:wrapOppIdx]), StartIndex: chunkStart, EndIndex: wrapOppIdx})
				chunkStart = wrapOppIdx
				currentWidth -= wrapOppWidth
			} else if chunkStart < seg.index {
				chunks = append(chunks, TextChunk{Text: string(runes[chunkStart:seg.index]), StartIndex: chunkStart, EndIndex: seg.index})
				chunkStart = seg.index
				currentWidth = 0
			}
			wrapOppIdx = -1
		}

		if gWidth > maxWidth {
			subChunks := WordWrapLine(seg.text, maxWidth, nil)
			for j := 0; j < len(subChunks)-1; j++ {
				chunks = append(chunks, TextChunk{
					Text:       subChunks[j].Text,
					StartIndex: seg.index + subChunks[j].StartIndex,
					EndIndex:   seg.index + subChunks[j].EndIndex,
				})
			}
			last := subChunks[len(subChunks)-1]
			chunkStart = seg.index + last.StartIndex
			currentWidth = tui.VisibleWidth(last.Text)
			wrapOppIdx = -1
			continue
		}

		currentWidth += gWidth

		var nextSeg *segment
		if i+1 < len(segments) {
			nextSeg = &segments[i+1]
		}
		isWs := tui.IsWhitespaceChar(firstEditorRune(seg.text))
		if nextSeg != nil {
			nextIsWs := tui.IsWhitespaceChar(firstEditorRune(nextSeg.text))
			if isWs && !nextIsWs {
				wrapOppIdx = nextSeg.index
				wrapOppWidth = currentWidth
			} else if !isWs && !nextIsWs {
				if tui.IsCJKRune(firstEditorRune(seg.text)) || tui.IsCJKRune(firstEditorRune(nextSeg.text)) {
					wrapOppIdx = nextSeg.index
					wrapOppWidth = currentWidth
				}
			}
		}
	}

	chunks = append(chunks, TextChunk{Text: string(runes[chunkStart:]), StartIndex: chunkStart, EndIndex: totalRunes})
	return chunks
}

func (e *Editor) layoutText(contentWidth int) []editorLayoutLine {
	var layoutLines []editorLayoutLine
	if len(e.state.Lines) == 0 || (len(e.state.Lines) == 1 && e.state.Lines[0] == "") {
		return []editorLayoutLine{{text: "", hasCursor: true, cursorPos: 0}}
	}
	for i, line := range e.state.Lines {
		isCurrent := i == e.state.CursorLine
		lineVisWidth := tui.VisibleWidth(line)
		if lineVisWidth <= contentWidth {
			if isCurrent {
				layoutLines = append(layoutLines, editorLayoutLine{text: line, hasCursor: true, cursorPos: e.state.CursorCol})
			} else {
				layoutLines = append(layoutLines, editorLayoutLine{text: line})
			}
		} else {
			chunks := WordWrapLine(line, contentWidth, nil)
			for ci, chunk := range chunks {
				isLast := ci == len(chunks)-1
				var hasCursor bool
				var cp int
				if isCurrent {
					if isLast {
						hasCursor = e.state.CursorCol >= chunk.StartIndex
						cp = e.state.CursorCol - chunk.StartIndex
					} else {
						hasCursor = e.state.CursorCol >= chunk.StartIndex && e.state.CursorCol < chunk.EndIndex
						if hasCursor {
							cp = e.state.CursorCol - chunk.StartIndex
							r := []rune(chunk.Text)
							if cp > len(r) {
								cp = len(r)
							}
						}
					}
				}
				layoutLines = append(layoutLines, editorLayoutLine{text: chunk.Text, hasCursor: hasCursor, cursorPos: cp})
			}
		}
	}
	return layoutLines
}

func (e *Editor) buildVisualLineMap(width int) []visualLine {
	var vls []visualLine
	for i, line := range e.state.Lines {
		runes := []rune(line)
		visWidth := tui.VisibleWidth(line)
		if len(runes) == 0 {
			vls = append(vls, visualLine{i, 0, 0})
		} else if visWidth <= width {
			vls = append(vls, visualLine{i, 0, len(runes)})
		} else {
			chunks := WordWrapLine(line, width, nil)
			for _, c := range chunks {
				vls = append(vls, visualLine{i, c.StartIndex, c.EndIndex - c.StartIndex})
			}
		}
	}
	return vls
}

func (e *Editor) findCurrentVisualLine(vls []visualLine) int {
	return e.findVisualLineAt(vls, e.state.CursorLine, e.state.CursorCol)
}

func (e *Editor) findVisualLineAt(vls []visualLine, line, col int) int {
	for i, vl := range vls {
		if vl.logicalLine != line {
			continue
		}
		offset := col - vl.startCol
		isLast := i == len(vls)-1 || vls[i+1].logicalLine != vl.logicalLine
		if offset >= 0 && (offset < vl.length || (isLast && offset == vl.length)) {
			return i
		}
	}
	if len(vls) > 0 {
		return len(vls) - 1
	}
	return 0
}

func (e *Editor) moveToVisualLine(vls []visualLine, currentVL, targetVL int) {
	current := vls[currentVL]
	target := vls[targetVL]

	var currentVisualCol int
	if e.snappedFromCursorCol != nil {
		vlIdx := e.findVisualLineAt(vls, current.logicalLine, *e.snappedFromCursorCol)
		currentVisualCol = *e.snappedFromCursorCol - vls[vlIdx].startCol
	} else {
		currentVisualCol = e.state.CursorCol - current.startCol
	}

	isLastSource := currentVL == len(vls)-1 || vls[currentVL+1].logicalLine != current.logicalLine
	sourceMax := current.length
	if !isLastSource && sourceMax > 0 {
		sourceMax--
	}

	isLastTarget := targetVL == len(vls)-1 || vls[targetVL+1].logicalLine != target.logicalLine
	targetMax := target.length
	if !isLastTarget && targetMax > 0 {
		targetMax--
	}

	moveToCol := e.computeVerticalMoveColumn(currentVisualCol, sourceMax, targetMax)

	e.state.CursorLine = target.logicalLine
	targetCol := target.startCol + moveToCol
	runes := []rune(e.state.Lines[target.logicalLine])
	if targetCol > len(runes) {
		targetCol = len(runes)
	}
	e.state.CursorCol = targetCol

	// P10a: paste-marker atomic-snapping deferred.
	e.snappedFromCursorCol = nil
}

func (e *Editor) computeVerticalMoveColumn(currentVisualCol, sourceMax, targetMax int) int {
	hasPreferred := e.preferredVisualCol != nil
	cursorInMiddle := currentVisualCol < sourceMax
	targetTooShort := targetMax < currentVisualCol

	if !hasPreferred || cursorInMiddle {
		if targetTooShort {
			e.preferredVisualCol = intPtr(currentVisualCol)
			return targetMax
		}
		e.preferredVisualCol = nil
		return currentVisualCol
	}

	targetCantFitPreferred := targetMax < *e.preferredVisualCol
	if targetTooShort || targetCantFitPreferred {
		return targetMax
	}

	result := *e.preferredVisualCol
	e.preferredVisualCol = nil
	return result
}

// --- Render ---

func (e *Editor) Render(width int) []string {
	maxPad := (width - 1) / 2
	if maxPad < 0 {
		maxPad = 0
	}
	paddingX := e.paddingX
	if paddingX > maxPad {
		paddingX = maxPad
	}
	contentWidth := width - paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}
	layoutWidth := contentWidth
	if paddingX == 0 {
		layoutWidth--
	}
	if layoutWidth < 1 {
		layoutWidth = 1
	}

	e.lastWidth = layoutWidth
	layoutLines := e.layoutText(layoutWidth)

	rows := 24
	if e.tui != nil {
		rows = e.tui.Terminal.Rows()
	}
	maxVisible := rows * 30 / 100
	if maxVisible < 5 {
		maxVisible = 5
	}

	cursorLineIdx := -1
	for i, ll := range layoutLines {
		if ll.hasCursor {
			cursorLineIdx = i
			break
		}
	}
	if cursorLineIdx == -1 {
		cursorLineIdx = 0
	}

	if cursorLineIdx < e.scrollOffset {
		e.scrollOffset = cursorLineIdx
	} else if cursorLineIdx >= e.scrollOffset+maxVisible {
		e.scrollOffset = cursorLineIdx - maxVisible + 1
	}
	maxScroll := len(layoutLines) - maxVisible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.scrollOffset > maxScroll {
		e.scrollOffset = maxScroll
	}
	if e.scrollOffset < 0 {
		e.scrollOffset = 0
	}

	end := e.scrollOffset + maxVisible
	if end > len(layoutLines) {
		end = len(layoutLines)
	}
	visible := layoutLines[e.scrollOffset:end]

	leftPad := strings.Repeat(" ", paddingX)
	rightPad := leftPad
	horizontal := e.BorderColor("─")

	var result []string

	if e.scrollOffset > 0 {
		indicator := "─── ↑ " + itoaE(e.scrollOffset) + " more "
		remaining := width - tui.VisibleWidth(indicator)
		if remaining >= 0 {
			result = append(result, e.BorderColor(indicator+strings.Repeat("─", remaining)))
		} else {
			result = append(result, e.BorderColor(tui.TruncateToWidth(indicator, width, "", false)))
		}
	} else {
		result = append(result, strings.Repeat(horizontal, width))
	}

	for _, ll := range visible {
		displayText := ll.text
		lineVisWidth := tui.VisibleWidth(ll.text)

		if ll.hasCursor && ll.cursorPos >= 0 {
			runes := []rune(displayText)
			before := ""
			if ll.cursorPos <= len(runes) {
				before = string(runes[:ll.cursorPos])
			}
			after := ""
			if ll.cursorPos < len(runes) {
				after = string(runes[ll.cursorPos:])
			}

			marker := ""
			if e.focused {
				marker = tui.CursorMarker
			}

			firstGrapheme := " "
			restAfter := ""
			if after != "" {
				gr := uniseg.NewGraphemes(after)
				if gr.Next() {
					firstGrapheme = gr.Str()
					restAfter = after[len(firstGrapheme):]
				}
			} else {
				lineVisWidth++
			}

			cursor := "\x1b[7m" + firstGrapheme + "\x1b[0m"
			displayText = before + marker + cursor + restAfter
		}

		padding := ""
		if p := contentWidth - lineVisWidth; p > 0 {
			padding = strings.Repeat(" ", p)
		}
		result = append(result, leftPad+displayText+padding+rightPad)
	}

	linesBelow := len(layoutLines) - end
	if linesBelow > 0 {
		indicator := "─── ↓ " + itoaE(linesBelow) + " more "
		remaining := width - tui.VisibleWidth(indicator)
		if remaining >= 0 {
			result = append(result, e.BorderColor(indicator+strings.Repeat("─", maxIntEd(0, remaining))))
		} else {
			result = append(result, e.BorderColor(tui.TruncateToWidth(indicator, width, "", false)))
		}
	} else {
		result = append(result, strings.Repeat(horizontal, width))
	}

	// Autocomplete list.
	if e.autoState != "" && e.autoList != nil {
		autoLines := e.autoList.Render(contentWidth)
		for _, line := range autoLines {
			lw := tui.VisibleWidth(line)
			lp := ""
			if p := contentWidth - lw; p > 0 {
				lp = strings.Repeat(" ", p)
			}
			result = append(result, leftPad+line+lp+rightPad)
		}
	}

	return result
}

func firstEditorRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func maxIntEd(a, b int) int {
	if a > b {
		return a
	}
	return b
}
