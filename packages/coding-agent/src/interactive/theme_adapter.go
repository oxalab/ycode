package interactive

// theme_adapter.go bridges the semantic theme system
// (packages/coding-agent/src/core/tui/theme — the port of reference theme/theme.ts,
// ~50 named color slots) to pi-tui's per-component theme structs
// (components.MarkdownTheme / EditorTheme / SelectListTheme of tui.StyleFunc).
//
// Every slot name here is verified against reference/coding-agent/src/modes/
// interactive/components/*.ts — see mapping/02-theme-colors.md.

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// liveTheme resolves the theme a closure should render with at CALL time: the
// current global when initialized, else the captured fallback (tests). This is
// the Go translation of the reference's live theme Proxy (theme/theme.ts —
// closures call theme.fg through the proxy, so setTheme recolors existing
// components); without it, SetTheme's pointer swap strands every component
// built before the switch on the old instance.
func liveTheme(fallback *theme.Theme) *theme.Theme {
	if cur := theme.Current(); cur != nil {
		return cur
	}
	return fallback
}

// fgStyle wraps a semantic theme foreground slot as a pi-tui StyleFunc.
func fgStyle(th *theme.Theme, slot theme.ThemeColor) tui.StyleFunc {
	return func(s string) string { return liveTheme(th).Fg(slot, s) }
}

// strikeStyle / underlineStyle: the semantic theme has no dedicated slots for
// these (reference chalk.strikethrough/underline); emit the SGR codes directly.
func strikeStyle(s string) string { return "\x1b[9m" + s + "\x1b[29m" }
func underlineStyle(s string) string { return "\x1b[4m" + s + "\x1b[24m" }

// buildEditorTheme builds a pi-tui EditorTheme. The editor border defaults to
// the "off" thinking-level color, matching reference interactive-mode.ts
// updateEditorBorderColor() at thinkingLevel "off" (thinkingOff slot).
// (Bash-mode + thinking-level recoloring lands in P2.)
func buildEditorTheme(th *theme.Theme) components.EditorTheme {
	return components.EditorTheme{
		BorderColor: fgStyle(th, "thinkingOff"),
		SelectList:  buildSelectListTheme(th),
	}
}

// buildSelectListTheme maps the semantic theme onto pi-tui's SelectListTheme.
func buildSelectListTheme(th *theme.Theme) components.SelectListTheme {
	return components.SelectListTheme{
		SelectedPrefix: fgStyle(th, "accent"),
		SelectedText:   func(s string) string { return th.Bold(s) },
		Description:    fgStyle(th, "muted"),
		ScrollInfo:     fgStyle(th, "dim"),
		NoMatch:        fgStyle(th, "muted"),
	}
}

// buildSettingsListTheme maps the semantic theme onto pi-tui's SettingsListTheme.
// Selected label = accent+bold; value = muted; cursor = accent "→ "; hint/desc dim.
func buildSettingsListTheme(th *theme.Theme) components.SettingsListTheme {
	return components.SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return th.Bold(th.Fg("accent", text))
			}
			return th.Fg("text", text)
		},
		Value:       func(text string, selected bool) string { return th.Fg("muted", text) },
		Description: fgStyle(th, "muted"),
		Cursor:      th.Fg("accent", "→ "),
		Hint:        fgStyle(th, "dim"),
	}
}

// buildMarkdownTheme maps the md* theme slots onto pi-tui's MarkdownTheme, and
// wires chroma syntax highlighting (HighlightCode → syntax* slots) for code blocks.
// All closures resolve the live theme at call time (see liveTheme) so a /theme
// switch recolors existing transcript components.
func buildMarkdownTheme(th *theme.Theme) components.MarkdownTheme {
	return components.MarkdownTheme{
		Heading:         fgStyle(th, "mdHeading"),
		Link:            fgStyle(th, "mdLink"),
		LinkUrl:         fgStyle(th, "mdLinkUrl"),
		Code:            fgStyle(th, "mdCode"),
		CodeBlock:       fgStyle(th, "mdCodeBlock"),
		CodeBlockBorder: fgStyle(th, "mdCodeBlockBorder"),
		Quote:           fgStyle(th, "mdQuote"),
		QuoteBorder:     fgStyle(th, "mdQuoteBorder"),
		HR:              fgStyle(th, "mdHr"),
		ListBullet:      fgStyle(th, "mdListBullet"),
		Bold:            func(s string) string { return liveTheme(th).Bold(s) },
		Italic:          func(s string) string { return liveTheme(th).Italic(s) },
		Strikethrough:   strikeStyle,
		Underline:       underlineStyle,
		HighlightCode:   func(code, lang string) []string { return highlightCode(code, lang, liveTheme(th)) },
	}
}
