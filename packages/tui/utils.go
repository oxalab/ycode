package tui

// This file is a Go port of reference/tui/src/utils.ts — the text-measurement
// and ANSI-aware wrapping/truncation/slicing engine. Width rules (regional
// indicators = 2, Thai/Lao AM = 1, emoji = 2, tab = 3) are reproduced over
// rivo/uniseg graphemes; runewidth provides the base East-Asian width.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// StyleFunc styles a string by returning it wrapped with ANSI codes. It mirrors
// the TS (text: string) => string theme functions.
type StyleFunc func(string) string

const (
	// ansiReset is the full SGR reset.
	ansiReset = "\x1b[0m"
)

// ---------------------------------------------------------------------------
// Width helpers
// ---------------------------------------------------------------------------

func isPrintableAscii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// utf16Len returns the number of UTF-16 code units needed to encode the runes,
// matching JS string .length semantics (used by the reference emoji prefilter).
func utf16Len(runes []rune) int {
	n := 0
	for _, r := range runes {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isZeroWidthRune reports whether r is a control/format/mark/surrogate code.
func isZeroWidthRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsMark(r) || unicode.Is(unicode.Cs, r)
}

// isZeroWidthSegment reports whether the entire segment is zero-width.
func isZeroWidthSegment(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if !isZeroWidthRune(r) {
			return false
		}
	}
	return true
}

// stripLeadingNonPrinting removes leading control/format/mark/surrogate runes.
func stripLeadingNonPrinting(s string) string {
	for i, r := range s {
		if !isZeroWidthRune(r) {
			return s[i:]
		}
	}
	return ""
}

// couldBeEmoji is a fast prefilter mirroring the reference heuristic.
func couldBeEmoji(seg string) bool {
	runes := []rune(seg)
	if len(runes) == 0 {
		return false
	}
	cp := runes[0]
	if (cp >= 0x1f000 && cp <= 0x1fbff) ||
		(cp >= 0x2300 && cp <= 0x23ff) ||
		(cp >= 0x2600 && cp <= 0x27bf) ||
		(cp >= 0x2b50 && cp <= 0x2b55) ||
		strings.ContainsRune(seg, '️') ||
		utf16Len(runes) > 2 {
		return true
	}
	return false
}

// isEmojiCodepoint approximates the RGI_Emoji property for single-rune clusters.
// Regional indicators (0x1f1e6..0x1f1ff) are deliberately excluded so a lone
// indicator falls through to the regional-indicator width rule.
func isEmojiCodepoint(cp rune) bool {
	switch {
	case cp >= 0x1f300 && cp <= 0x1faff:
		return true
	case cp >= 0x1f000 && cp <= 0x1f02f:
		return true
	case cp >= 0x2600 && cp <= 0x26ff:
		return true
	case cp >= 0x2700 && cp <= 0x27bf:
		return true
	case cp >= 0x2b50 && cp <= 0x2b55:
		return true
	case cp == 0x231a || cp == 0x231b:
		return true
	case cp >= 0x23f0 && cp <= 0x23fa:
		return true
	case cp == 0x23e9 || cp == 0x23ea || cp == 0x23eb || cp == 0x23ec:
		return true
	case cp == 0x200d: // ZWJ handled via multi-rune path
		return false
	}
	return false
}

// isRgiEmoji approximates the reference \p{RGI_Emoji} test.
func isRgiEmoji(seg string) bool {
	if strings.ContainsRune(seg, '️') {
		return true
	}
	runes := []rune(seg)
	if len(runes) > 1 {
		return true // ZWJ / skin-tone / flag sequences
	}
	if len(runes) == 0 {
		return false
	}
	return isEmojiCodepoint(runes[0])
}

// graphemeWidth returns the terminal width of a single grapheme cluster.
func graphemeWidth(seg string) int {
	if seg == "\t" {
		return 3
	}
	if isZeroWidthSegment(seg) {
		return 0
	}
	if couldBeEmoji(seg) && isRgiEmoji(seg) {
		return 2
	}
	base := stripLeadingNonPrinting(seg)
	r, _ := utf8.DecodeRuneInString(base)
	if base == "" || r == utf8.RuneError {
		return 0
	}
	// Regional indicators render full-width even when isolated.
	if r >= 0x1f1e6 && r <= 0x1f1ff {
		return 2
	}
	width := eastAsianWidth(r)
	// Trailing fullwidth forms and Thai/Lao AM vowels that segment with a base.
	rest := base[utf8.RuneLen(r):]
	for _, c := range rest {
		if c >= 0xff00 && c <= 0xffef {
			width += eastAsianWidth(c)
		} else if c == 0x0e33 || c == 0x0eb3 {
			width += 1
		}
	}
	return width
}

func eastAsianWidth(r rune) int { return runewidth.RuneWidth(r) }

// VisibleWidth returns the visible width of s in terminal columns, ignoring
// ANSI/OSC/APC escape codes and counting tabs as 3 columns.
func VisibleWidth(s string) int {
	if len(s) == 0 {
		return 0
	}
	if isPrintableAscii(s) {
		return len(s)
	}
	clean := s
	if strings.ContainsRune(clean, '\t') {
		clean = strings.ReplaceAll(clean, "\t", "   ")
	}
	if strings.ContainsRune(clean, '\x1b') {
		var b strings.Builder
		for i := 0; i < len(clean); {
			if _, length, ok := extractAnsiCodeAt(clean, i); ok {
				i += length
				continue
			}
			r, size := utf8.DecodeRuneInString(clean[i:])
			b.WriteRune(r)
			i += size
		}
		clean = b.String()
	}
	width := 0
	gr := uniseg.NewGraphemes(clean)
	for gr.Next() {
		width += graphemeWidth(gr.Str())
	}
	return width
}

// ---------------------------------------------------------------------------
// normalizeTerminalOutput — Thai/Lao AM vowel decomposition
// ---------------------------------------------------------------------------

var (
	thaiLaoAmReplacer = strings.NewReplacer(
		"ำ", "ํา", // Thai AM
		"ຳ", "ໍາ", // Lao AM
	)
)

// NormalizeTerminalOutput decomposes precomposed Thai/Lao AM vowels to their
// compatibility forms, which render more consistently during differential paint.
func NormalizeTerminalOutput(s string) string {
	if !strings.ContainsAny(s, "ำຳ") {
		return s
	}
	return thaiLaoAmReplacer.Replace(s)
}

// ---------------------------------------------------------------------------
// extractAnsiCode — CSI/OSC/APC escape sequence scanner
// ---------------------------------------------------------------------------

// ExtractAnsiCode returns the ANSI escape sequence beginning at pos, if any.
func ExtractAnsiCode(s string, pos int) (code string, length int, ok bool) {
	return extractAnsiCodeAt(s, pos)
}

func extractAnsiCodeAt(s string, pos int) (code string, length int, ok bool) {
	if pos >= len(s) || s[pos] != '\x1b' {
		return "", 0, false
	}
	if pos+1 >= len(s) {
		return "", 0, false
	}
	next := s[pos+1]
	switch next {
	case '[':
		j := pos + 2
		for j < len(s) && !isCsiFinal(s[j]) {
			j++
		}
		if j < len(s) {
			return s[pos : j+1], j + 1 - pos, true
		}
		return "", 0, false
	case ']':
		j := pos + 2
		for j < len(s) {
			if s[j] == '\x07' {
				return s[pos : j+1], j + 1 - pos, true
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return s[pos : j+2], j + 2 - pos, true
			}
			j++
		}
		return "", 0, false
	case '_':
		j := pos + 2
		for j < len(s) {
			if s[j] == '\x07' {
				return s[pos : j+1], j + 1 - pos, true
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return s[pos : j+2], j + 2 - pos, true
			}
			j++
		}
		return "", 0, false
	}
	return "", 0, false
}

func isCsiFinal(b byte) bool {
	switch b {
	case 'm', 'G', 'K', 'H', 'J':
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// AnsiCodeTracker — preserve SGR + OSC 8 styling across line breaks
// ---------------------------------------------------------------------------

type osc8Terminator string

const (
	osc8BEL  osc8Terminator = "\x07"
	osc8ST   osc8Terminator = "\x1b\\"
)

type activeHyperlink struct {
	params     string
	url        string
	terminator osc8Terminator
}

// AnsiCodeTracker tracks active SGR attributes and an OSC 8 hyperlink so that
// wrapped continuation lines can re-open styling and lines can close only
// underline/hyperlink at line end (preserving background color in padding).
type AnsiCodeTracker struct {
	bold          bool
	dim           bool
	italic        bool
	underline     bool
	blink         bool
	inverse       bool
	hidden        bool
	strikethrough bool
	fgColor       string // e.g. "31" or "38;5;240" or "38;2;r;g;b"
	bgColor       string
	hyperlink     *activeHyperlink
}

// NewAnsiCodeTracker returns a fresh tracker.
func NewAnsiCodeTracker() *AnsiCodeTracker { return &AnsiCodeTracker{} }

// Reset clears SGR state (hyperlink state survives an SGR reset).
func (t *AnsiCodeTracker) Reset() {
	t.bold, t.dim, t.italic, t.underline = false, false, false, false
	t.blink, t.inverse, t.hidden, t.strikethrough = false, false, false, false
	t.fgColor = ""
	t.bgColor = ""
}

// Clear clears all state including the hyperlink.
func (t *AnsiCodeTracker) Clear() {
	t.Reset()
	t.hyperlink = nil
}

// Process updates tracker state from a single ANSI code.
func (t *AnsiCodeTracker) Process(code string) {
	if hl, ok := parseOsc8Hyperlink(code); ok {
		if hl == nil {
			t.hyperlink = nil // close
		} else {
			t.hyperlink = hl
		}
		return
	}
	if !strings.HasSuffix(code, "m") {
		return
	}
	inner := code[2 : len(code)-1] // strip "\x1b[" and "m"
	if inner == "" || inner == "0" {
		t.Reset()
		return
	}
	parts := strings.Split(inner, ";")
	for i := 0; i < len(parts); i++ {
		c, ok := parseDec(parts[i])
		if !ok {
			continue
		}
		switch {
		case c == 38 || c == 48:
			// 38;5;N  /  38;2;R;G;B  (and the 48 background variants)
			if i+1 < len(parts) && parts[i+1] == "5" && i+2 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				if c == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 2
			} else if i+1 < len(parts) && parts[i+1] == "2" && i+4 < len(parts) {
				colorCode := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				if c == 38 {
					t.fgColor = colorCode
				} else {
					t.bgColor = colorCode
				}
				i += 4
			}
		case c == 0:
			t.Reset()
		case c == 1:
			t.bold = true
		case c == 2:
			t.dim = true
		case c == 3:
			t.italic = true
		case c == 4:
			t.underline = true
		case c == 5:
			t.blink = true
		case c == 7:
			t.inverse = true
		case c == 8:
			t.hidden = true
		case c == 9:
			t.strikethrough = true
		case c == 21:
			t.bold = false
		case c == 22:
			t.bold = false
			t.dim = false
		case c == 23:
			t.italic = false
		case c == 24:
			t.underline = false
		case c == 25:
			t.blink = false
		case c == 27:
			t.inverse = false
		case c == 28:
			t.hidden = false
		case c == 29:
			t.strikethrough = false
		case c == 39:
			t.fgColor = ""
		case c == 49:
			t.bgColor = ""
		case (c >= 30 && c <= 37) || (c >= 90 && c <= 97):
			t.fgColor = parts[i]
		case (c >= 40 && c <= 47) || (c >= 100 && c <= 107):
			t.bgColor = parts[i]
		}
	}
}

// GetActiveCodes rebuilds the ANSI prefix representing the current state.
func (t *AnsiCodeTracker) GetActiveCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline {
		codes = append(codes, "4")
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strikethrough {
		codes = append(codes, "9")
	}
	if t.fgColor != "" {
		codes = append(codes, t.fgColor)
	}
	if t.bgColor != "" {
		codes = append(codes, t.bgColor)
	}
	out := ""
	if len(codes) > 0 {
		out = "\x1b[" + strings.Join(codes, ";") + "m"
	}
	if t.hyperlink != nil {
		out += formatOsc8Hyperlink(t.hyperlink)
	}
	return out
}

// HasActiveCodes reports whether any styling is active.
func (t *AnsiCodeTracker) HasActiveCodes() bool {
	return t.bold || t.dim || t.italic || t.underline || t.blink || t.inverse || t.hidden || t.strikethrough ||
		t.fgColor != "" || t.bgColor != "" || t.hyperlink != nil
}

// GetLineEndReset returns the codes needed at line end: underline-off (so it does
// not bleed into padding) and OSC 8 close (re-opened on the next line).
func (t *AnsiCodeTracker) GetLineEndReset() string {
	out := ""
	if t.underline {
		out += "\x1b[24m"
	}
	if t.hyperlink != nil {
		out += formatOsc8Close(t.hyperlink.terminator)
	}
	return out
}

func parseOsc8Hyperlink(code string) (*activeHyperlink, bool) {
	if !strings.HasPrefix(code, "\x1b]8;") {
		return nil, false
	}
	// Distinguish open (has url) from close (empty url).
	var term osc8Terminator
	var body string
	if strings.HasSuffix(code, "\x07") {
		term = osc8BEL
		body = code[4 : len(code)-1]
	} else if strings.HasSuffix(code, "\x1b\\") {
		term = osc8ST
		body = code[4 : len(code)-2]
	} else {
		return nil, false
	}
	sep := strings.IndexByte(body, ';')
	if sep < 0 {
		return nil, false
	}
	params := body[:sep]
	url := body[sep+1:]
	if url == "" {
		return nil, true // close
	}
	return &activeHyperlink{params: params, url: url, terminator: term}, true
}

func formatOsc8Hyperlink(h *activeHyperlink) string {
	return "\x1b]8;" + h.params + ";" + h.url + string(h.terminator)
}

func formatOsc8Close(term osc8Terminator) string {
	return "\x1b]8;;" + string(term)
}

func parseDec(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

func updateTrackerFromText(text string, t *AnsiCodeTracker) {
	for i := 0; i < len(text); {
		if code, length, ok := extractAnsiCodeAt(text, i); ok {
			t.Process(code)
			i += length
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
	}
}

// ---------------------------------------------------------------------------
// wrapTextWithAnsi — word wrap preserving ANSI codes across line breaks
// ---------------------------------------------------------------------------

// WrapTextWithAnsi wraps text to width, preserving active ANSI codes across line
// breaks. It does NOT pad or apply backgrounds. Each returned line is <= width.
func WrapTextWithAnsi(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	inputLines := strings.Split(text, "\n")
	result := []string{}
	tracker := NewAnsiCodeTracker()

	for idx, inputLine := range inputLines {
		prefix := ""
		if len(result) > 0 {
			prefix = tracker.GetActiveCodes()
		}
		for _, l := range wrapSingleLine(prefix+inputLine, width) {
			result = append(result, l)
		}
		// Update tracker with codes from this source line for the next iteration.
		updateTrackerFromText(inputLine, tracker)
		_ = idx
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if VisibleWidth(line) <= width {
		return []string{line}
	}
	wrapped := []string{}
	tracker := NewAnsiCodeTracker()
	tokens := splitIntoTokensWithAnsi(line)

	currentLine := ""
	currentWidth := 0

	for _, token := range tokens {
		tokenWidth := VisibleWidth(token)
		isWhitespace := strings.TrimSpace(token) == ""

		// Token itself is too long — break it grapheme by grapheme.
		if tokenWidth > width && !isWhitespace {
			if currentLine != "" {
				if reset := tracker.GetLineEndReset(); reset != "" {
					currentLine += reset
				}
				wrapped = append(wrapped, currentLine)
				currentLine = ""
				currentWidth = 0
			}
			broken := breakLongWord(token, width, tracker)
			for i := 0; i < len(broken)-1; i++ {
				wrapped = append(wrapped, broken[i])
			}
			currentLine = broken[len(broken)-1]
			currentWidth = VisibleWidth(currentLine)
			continue
		}

		totalNeeded := currentWidth + tokenWidth
		if totalNeeded > width && currentWidth > 0 {
			lineToWrap := strings.TrimRight(currentLine, " ")
			if reset := tracker.GetLineEndReset(); reset != "" {
				lineToWrap += reset
			}
			wrapped = append(wrapped, lineToWrap)
			if isWhitespace {
				currentLine = tracker.GetActiveCodes()
				currentWidth = 0
			} else {
				currentLine = tracker.GetActiveCodes() + token
				currentWidth = tokenWidth
			}
		} else {
			currentLine += token
			currentWidth += tokenWidth
		}
		updateTrackerFromText(token, tracker)
	}

	if currentLine != "" {
		wrapped = append(wrapped, currentLine)
	}

	if len(wrapped) == 0 {
		return []string{""}
	}
	// Trailing whitespace can cause lines to exceed the requested width.
	out := make([]string, len(wrapped))
	for i, l := range wrapped {
		out[i] = strings.TrimRight(l, " ")
	}
	return out
}

// splitIntoTokensWithAnsi splits text into whitespace/word/CJK tokens while
// keeping ANSI codes attached to the following visible content.
func splitIntoTokensWithAnsi(text string) []string {
	var tokens []string
	var current string
	var pendingAnsi strings.Builder
	currentKind := 0 // 0 = none, 1 = space, 2 = word

	flushCurrent := func() {
		if current != "" {
			tokens = append(tokens, current)
			current = ""
			currentKind = 0
		}
	}

	i := 0
	for i < len(text) {
		if code, length, ok := extractAnsiCodeAt(text, i); ok {
			pendingAnsi.WriteString(code)
			i += length
			continue
		}
		// Gather a run of plain text up to the next ANSI code.
		end := i
		for end < len(text) {
			if _, _, ok := extractAnsiCodeAt(text, end); ok {
				break
			}
			end++
		}
		gr := uniseg.NewGraphemes(text[i:end])
		for gr.Next() {
			seg := gr.Str()
			isSpace := seg == " "
			if !isSpace && isCJKRune([]rune(seg)) {
				flushCurrent()
				tokens = append(tokens, pendingAnsi.String()+seg)
				pendingAnsi.Reset()
				continue
			}
			kind := 1
			if !isSpace {
				kind = 2
			}
			if current != "" && currentKind != kind {
				flushCurrent()
			}
			if pendingAnsi.Len() > 0 {
				current += pendingAnsi.String()
				pendingAnsi.Reset()
			}
			currentKind = kind
			current += seg
		}
		i = end
	}

	// Attach any remaining pending ANSI codes.
	if pendingAnsi.Len() > 0 {
		if current != "" {
			current += pendingAnsi.String()
		} else if len(tokens) > 0 {
			tokens[len(tokens)-1] += pendingAnsi.String()
		} else {
			current = pendingAnsi.String()
		}
	}
	if current != "" {
		tokens = append(tokens, current)
	}
	return tokens
}

func breakLongWord(word string, width int, tracker *AnsiCodeTracker) []string {
	var lines []string
	currentLine := tracker.GetActiveCodes()
	currentWidth := 0

	// Walk ANSI codes and graphemes, breaking when the width is exceeded.
	i := 0
	for i < len(word) {
		if code, length, ok := extractAnsiCodeAt(word, i); ok {
			currentLine += code
			tracker.Process(code)
			i += length
			continue
		}
		end := i
		for end < len(word) {
			if _, _, ok := extractAnsiCodeAt(word, end); ok {
				break
			}
			end++
		}
		gr := uniseg.NewGraphemes(word[i:end])
		for gr.Next() {
			seg := gr.Str()
			if seg == "" {
				continue
			}
			gw := VisibleWidth(seg)
			if currentWidth+gw > width {
				if reset := tracker.GetLineEndReset(); reset != "" {
					currentLine += reset
				}
				lines = append(lines, currentLine)
				currentLine = tracker.GetActiveCodes()
				currentWidth = 0
			}
			currentLine += seg
			currentWidth += gw
		}
		i = end
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// ---------------------------------------------------------------------------
// truncateToWidth
// ---------------------------------------------------------------------------

// TruncateToWidth truncates text to maxWidth visible columns, optionally
// appending an ellipsis and/or padding to exactly maxWidth. ANSI codes are
// preserved and properly closed.
func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}
	if text == "" {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}
	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		textWidth := VisibleWidth(text)
		if textWidth <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-textWidth)
			}
			return text
		}
		clipped := truncateFragmentToWidth(ellipsis, maxWidth)
		if clipped.width == 0 {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return finalizeTruncatedResult("", 0, clipped.text, clipped.width, maxWidth, pad)
	}

	if isPrintableAscii(text) {
		if len(text) <= maxWidth {
			if pad {
				return text + strings.Repeat(" ", maxWidth-len(text))
			}
			return text
		}
		target := maxWidth - ellipsisWidth
		return finalizeTruncatedResult(text[:target], target, ellipsis, ellipsisWidth, maxWidth, pad)
	}

	target := maxWidth - ellipsisWidth
	result := ""
	pendingAnsi := ""
	visibleSoFar := 0
	keptWidth := 0
	keepContiguous := true
	overflowed := false
	exhausted := false

	hasAnsi := strings.ContainsRune(text, '\x1b')
	hasTabs := strings.ContainsRune(text, '\t')

	if !hasAnsi && !hasTabs {
		gr := uniseg.NewGraphemes(text)
		for gr.Next() {
			seg := gr.Str()
			w := graphemeWidth(seg)
			if keepContiguous && keptWidth+w <= target {
				result += seg
				keptWidth += w
			} else {
				keepContiguous = false
			}
			visibleSoFar += w
			if visibleSoFar > maxWidth {
				overflowed = true
				break
			}
		}
		exhausted = !overflowed
	} else {
		i := 0
		for i < len(text) {
			if code, length, ok := extractAnsiCodeAt(text, i); ok {
				pendingAnsi += code
				i += length
				continue
			}
			if text[i] == '\t' {
				if keepContiguous && keptWidth+3 <= target {
					if pendingAnsi != "" {
						result += pendingAnsi
						pendingAnsi = ""
					}
					result += "\t"
					keptWidth += 3
				} else {
					keepContiguous = false
					pendingAnsi = ""
				}
				visibleSoFar += 3
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
				i++
				continue
			}
			end := i
			for end < len(text) && text[end] != '\t' {
				if _, _, ok := extractAnsiCodeAt(text, end); ok {
					break
				}
				end++
			}
			gr := uniseg.NewGraphemes(text[i:end])
			for gr.Next() {
				seg := gr.Str()
				w := graphemeWidth(seg)
				if keepContiguous && keptWidth+w <= target {
					if pendingAnsi != "" {
						result += pendingAnsi
						pendingAnsi = ""
					}
					result += seg
					keptWidth += w
				} else {
					keepContiguous = false
					pendingAnsi = ""
				}
				visibleSoFar += w
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
			}
			if overflowed {
				break
			}
			i = end
		}
		exhausted = i >= len(text)
	}

	if !overflowed && exhausted {
		if pad {
			return text + strings.Repeat(" ", max(0, maxWidth-visibleSoFar))
		}
		return text
	}
	return finalizeTruncatedResult(result, keptWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

func truncateFragmentToWidth(text string, maxWidth int) struct {
	text  string
	width int
} {
	out := struct {
		text  string
		width int
	}{}
	if maxWidth <= 0 || text == "" {
		return out
	}
	if isPrintableAscii(text) {
		clipped := text
		if len(clipped) > maxWidth {
			clipped = clipped[:maxWidth]
		}
		return struct {
			text  string
			width int
		}{clipped, len(clipped)}
	}
	hasAnsi := strings.ContainsRune(text, '\x1b')
	hasTabs := strings.ContainsRune(text, '\t')
	if !hasAnsi && !hasTabs {
		gr := uniseg.NewGraphemes(text)
		result := ""
		width := 0
		for gr.Next() {
			seg := gr.Str()
			w := graphemeWidth(seg)
			if width+w > maxWidth {
				break
			}
			result += seg
			width += w
		}
		return struct {
			text  string
			width int
		}{result, width}
	}
	result := ""
	width := 0
	pendingAnsi := ""
	i := 0
	for i < len(text) {
		if code, length, ok := extractAnsiCodeAt(text, i); ok {
			pendingAnsi += code
			i += length
			continue
		}
		if text[i] == '\t' {
			if width+3 > maxWidth {
				break
			}
			if pendingAnsi != "" {
				result += pendingAnsi
				pendingAnsi = ""
			}
			result += "\t"
			width += 3
			i++
			continue
		}
		end := i
		for end < len(text) && text[end] != '\t' {
			if _, _, ok := extractAnsiCodeAt(text, end); ok {
				break
			}
			end++
		}
		gr := uniseg.NewGraphemes(text[i:end])
		for gr.Next() {
			seg := gr.Str()
			w := graphemeWidth(seg)
			if width+w > maxWidth {
				return struct {
					text  string
					width int
				}{result, width}
			}
			if pendingAnsi != "" {
				result += pendingAnsi
				pendingAnsi = ""
			}
			result += seg
			width += w
		}
		i = end
	}
	return struct {
		text  string
		width int
	}{result, width}
}

func finalizeTruncatedResult(prefix string, prefixWidth int, ellipsis string, ellipsisWidth, maxWidth int, pad bool) string {
	visibleWidth := prefixWidth + ellipsisWidth
	var result string
	if len(ellipsis) > 0 {
		result = prefix + ansiReset + ellipsis + ansiReset
	} else {
		result = prefix + ansiReset
	}
	if pad {
		result += strings.Repeat(" ", max(0, maxWidth-visibleWidth))
	}
	return result
}

// ---------------------------------------------------------------------------
// sliceByColumn / sliceWithWidth / extractSegments
// ---------------------------------------------------------------------------

// SliceByColumn returns the visible columns [startCol, startCol+length) of line.
// If strict is true, wide characters that would extend past the range are excluded.
func SliceByColumn(line string, startCol, length int, strict bool) string {
	return sliceWithWidth(line, startCol, length, strict).text
}

type sliceResult struct {
	text  string
	width int
}

func sliceWithWidth(line string, startCol, length int, strict bool) sliceResult {
	if length <= 0 {
		return sliceResult{}
	}
	endCol := startCol + length
	var result strings.Builder
	resultWidth := 0
	currentCol := 0
	pendingAnsi := ""

	i := 0
	for i < len(line) {
		if code, length, ok := extractAnsiCodeAt(line, i); ok {
			if currentCol >= startCol && currentCol < endCol {
				result.WriteString(code)
			} else if currentCol < startCol {
				pendingAnsi += code
			}
			i += length
			continue
		}
		end := i
		for end < len(line) {
			if _, _, ok := extractAnsiCodeAt(line, end); ok {
				break
			}
			end++
		}
		gr := uniseg.NewGraphemes(line[i:end])
		for gr.Next() {
			seg := gr.Str()
			w := graphemeWidth(seg)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi != "" {
					result.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				result.WriteString(seg)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				break
			}
		}
		i = end
		if currentCol >= endCol {
			break
		}
	}
	return sliceResult{result.String(), resultWidth}
}

// SegmentParts is the result of ExtractSegments.
type SegmentParts struct {
	Before       string
	BeforeWidth  int
	After        string
	AfterWidth   int
}

// ExtractSegments extracts the "before" (< beforeEnd) and "after"
// [afterStart, afterStart+afterLen) segments of a line in a single pass, used
// for overlay compositing. After inherits styling active before the overlay.
func ExtractSegments(line string, beforeEnd, afterStart, afterLen int, strictAfter bool) SegmentParts {
	var before, after strings.Builder
	beforeWidth, afterWidth := 0, 0
	currentCol := 0
	pendingAnsiBefore := ""
	afterStarted := false
	afterEnd := afterStart + afterLen

	tracker := NewAnsiCodeTracker()

	i := 0
	for i < len(line) {
		if code, length, ok := extractAnsiCodeAt(line, i); ok {
			tracker.Process(code)
			if currentCol < beforeEnd {
				pendingAnsiBefore += code
			} else if currentCol >= afterStart && currentCol < afterEnd && afterStarted {
				after.WriteString(code)
			}
			i += length
			continue
		}
		end := i
		for end < len(line) {
			if _, _, ok := extractAnsiCodeAt(line, end); ok {
				break
			}
			end++
		}
		gr := uniseg.NewGraphemes(line[i:end])
		for gr.Next() {
			seg := gr.Str()
			w := graphemeWidth(seg)
			if currentCol < beforeEnd && currentCol+w <= beforeEnd {
				if pendingAnsiBefore != "" {
					before.WriteString(pendingAnsiBefore)
					pendingAnsiBefore = ""
				}
				before.WriteString(seg)
				beforeWidth += w
			} else if currentCol >= afterStart && currentCol < afterEnd {
				fits := !strictAfter || currentCol+w <= afterEnd
				if fits {
					if !afterStarted {
						after.WriteString(tracker.GetActiveCodes())
						afterStarted = true
					}
					after.WriteString(seg)
					afterWidth += w
				}
			}
			currentCol += w
			done := false
			if afterLen <= 0 {
				done = currentCol >= beforeEnd
			} else {
				done = currentCol >= afterEnd
			}
			if done {
				break
			}
		}
		i = end
		done := false
		if afterLen <= 0 {
			done = currentCol >= beforeEnd
		} else {
			done = currentCol >= afterEnd
		}
		if done {
			break
		}
	}
	return SegmentParts{before.String(), beforeWidth, after.String(), afterWidth}
}

// ---------------------------------------------------------------------------
// applyBackgroundToLine, char classification, CJK break
// ---------------------------------------------------------------------------

// ApplyBackgroundToLine pads line to width and applies the background styler to
// the padded content.
func ApplyBackgroundToLine(line string, width int, bg StyleFunc) string {
	visibleLen := VisibleWidth(line)
	padding := strings.Repeat(" ", max(0, width-visibleLen))
	return bg(line + padding)
}

// IsWhitespaceChar reports whether r is whitespace.
func IsWhitespaceChar(r rune) bool {
	return unicode.IsSpace(r)
}

// IsPunctuationChar reports whether r is ASCII punctuation used for word
// boundaries.
func IsPunctuationChar(r rune) bool {
	return isPunctuationRune(r)
}

func isPunctuationRune(r rune) bool {
	switch r {
	case '(', ')', '{', '}', '[', ']', '<', '>', '.', ',', ';', ':', '\'', '"', '!', '?',
		'+', '-', '=', '*', '/', '\\', '|', '&', '%', '^', '$', '#', '@', '~', '`':
		return true
	}
	return false
}

// IsCJKRune reports whether r is a Han/Hiragana/Katakana/Hangul/Bopomofo char,
// used to allow line-breaking between any adjacent CJK characters.
func IsCJKRune(r rune) bool { return isCJKRune([]rune{r}) }

func isCJKRune(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	r := runes[0]
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Bopomofo, r) {
		return true
	}
	return false
}
