package interactive

// highlight.go wires chroma syntax highlighting into the Markdown renderer's
// HighlightCode hook. Chroma tokenizes the code; each token type maps to one of
// the theme's syntax* color slots (syntaxKeyword, syntaxString, etc.). Returns
// one ANSI-colored line per line of code; nil on failure (the Markdown renderer
// falls back to the unhighlighted code-block style).

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// highlightCode tokenizes `code` with the chroma lexer for `lang` (or Analyse to
// guess), then colors each token via the theme's syntax* slots. Returns nil if no
// lexer / tokenisation fails (the caller falls back to plain code-block styling).
func highlightCode(code, lang string, th *theme.Theme) []string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		if analysed := lexers.Analyse(code); analysed != nil {
			lexer = analysed
		}
	}
	if lexer == nil {
		return nil
	}
	return tokenizeLines(chroma.Coalesce(lexer), code, th)
}

// highlightWritePreview highlights file content for the write-tool preview,
// picking the lexer by file path (reference getLanguageFromPath + highlightCode).
// Returns nil when no lexer matches the path — the caller falls back to plain
// lines.
func highlightWritePreview(content, path string, th *theme.Theme) []string {
	lexer := lexers.Match(path)
	if lexer == nil {
		return nil
	}
	return tokenizeLines(chroma.Coalesce(lexer), content, th)
}

// tokenizeLines renders code through the lexer into one ANSI-colored line per
// code line (nil on tokenisation failure).
func tokenizeLines(lexer chroma.Lexer, code string, th *theme.Theme) []string {
	iter, err := lexer.Tokenise(nil, code)
	if err != nil {
		return nil
	}

	var lines []string
	var current strings.Builder
	for {
		token := iter()
		if token == (chroma.Token{}) {
			break
		}
		value := token.Value
		if slot := chromaToSlot(token.Type); slot != "" && th != nil {
			value = th.Fg(slot, value)
		}
		// A token may span multiple lines — split and emit line-by-line.
		parts := strings.Split(value, "\n")
		for i, part := range parts {
			current.WriteString(part)
			if i < len(parts)-1 {
				lines = append(lines, current.String())
				current.Reset()
			}
		}
	}
	lines = append(lines, current.String())
	return lines
}

// chromaToSlot maps a chroma TokenType to the corresponding theme syntax* slot.
// Uses the type's string representation (e.g. "KeywordDeclaration" → prefix
// "Keyword") — robust across chroma's hierarchical type tree.
func chromaToSlot(t chroma.TokenType) theme.ThemeColor {
	s := t.String()
	switch {
	case strings.HasPrefix(s, "Comment"):
		return "syntaxComment"
	case strings.HasPrefix(s, "Keyword"):
		return "syntaxKeyword"
	case strings.HasPrefix(s, "String"):
		return "syntaxString"
	case strings.HasPrefix(s, "Number"):
		return "syntaxNumber"
	case strings.HasPrefix(s, "Operator"):
		return "syntaxOperator"
	case strings.HasPrefix(s, "Punctuation"):
		return "syntaxPunctuation"
	case strings.HasPrefix(s, "NameFunction") || strings.HasPrefix(s, "NameDecorator"):
		return "syntaxFunction"
	case strings.HasPrefix(s, "NameClass") || strings.HasPrefix(s, "NameType") || strings.HasPrefix(s, "NameBuiltin"):
		return "syntaxType"
	case strings.HasPrefix(s, "Name"):
		return "syntaxVariable"
	case strings.HasPrefix(s, "LiteralNumber"):
		return "syntaxNumber"
	case strings.HasPrefix(s, "LiteralString"):
		return "syntaxString"
	case strings.HasPrefix(s, "Literal"):
		return "syntaxNumber"
	default:
		return "" // plain text — no coloring
	}
}
