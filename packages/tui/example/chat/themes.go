// Package main is the chat demo. themes.go ports reference/tui/test/test-themes.ts
// (defaultSelectListTheme / defaultMarkdownTheme / defaultEditorTheme).
//
// The reference builds themes with chalk at level 3 (truecolor). The port follows
// mapping §4 ("prefer hand-built ANSI so output matches chalk") and composes the
// same SGR codes chalk emits: foreground colors open with their 3x code and close
// with `\x1b[39m` (default fg); attributes open with their code and close with the
// matching "off" code (bold/dim → 22, italic → 23, underline → 24, strike → 29).
package main

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
)

// --- chalk-equivalent style primitives (truecolor SGR codes) ---

func styleFg(code, s string) string  { return "\x1b[" + code + "m" + s + "\x1b[39m" }
func styleAttr(code, off, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[" + off + "m"
}

func blue(s string) string   { return styleFg("34", s) }
func cyan(s string) string   { return styleFg("36", s) }
func yellow(s string) string { return styleFg("33", s) }
func green(s string) string  { return styleFg("32", s) }
func dim(s string) string    { return styleAttr("2", "22", s) }
func bold(s string) string   { return styleAttr("1", "22", s) }
func italic(s string) string { return styleAttr("3", "23", s) }
func underline(s string) string { return styleAttr("4", "24", s) }
func strike(s string) string { return styleAttr("9", "29", s) }
func boldCyan(s string) string { return bold(cyan(s)) }

// --- test-themes.ts ---

var defaultSelectListTheme = components.SelectListTheme{
	SelectedPrefix: func(s string) string { return blue(s) },
	SelectedText:   func(s string) string { return bold(s) },
	Description:    func(s string) string { return dim(s) },
	ScrollInfo:     func(s string) string { return dim(s) },
	NoMatch:        func(s string) string { return dim(s) },
}

var defaultMarkdownTheme = components.MarkdownTheme{
	Heading:        func(s string) string { return boldCyan(s) },
	Link:           func(s string) string { return blue(s) },
	LinkUrl:        func(s string) string { return dim(s) },
	Code:           func(s string) string { return yellow(s) },
	CodeBlock:      func(s string) string { return green(s) },
	CodeBlockBorder: func(s string) string { return dim(s) },
	Quote:          func(s string) string { return italic(s) },
	QuoteBorder:    func(s string) string { return dim(s) },
	HR:             func(s string) string { return dim(s) },
	ListBullet:     func(s string) string { return cyan(s) },
	Bold:           func(s string) string { return bold(s) },
	Italic:         func(s string) string { return italic(s) },
	Strikethrough:  func(s string) string { return strike(s) },
	Underline:      func(s string) string { return underline(s) },
}

var defaultEditorTheme = components.EditorTheme{
	BorderColor: func(s string) string { return dim(s) },
	SelectList:  defaultSelectListTheme,
}

// keep tui import used (StyleFunc is referenced via the component theme types).
var _ tui.StyleFunc
