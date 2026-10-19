package interactive

// bash_execution.go ports reference components/bash-execution.ts (verified —
// see mapping/12-tools.md), P3b slice: a command framed by two DynamicBorders
// with a capped output preview and a status line. Streaming (live AppendOutput
// via EventToolExecutionUpdate) and the animated Loader (vs the static
// "Running..." text here) are wired. The collapsed preview uses
// TruncateToVisualLines (visual_truncate.go) so it shows the last N visual
// lines after word-wrap, not just the last N logical lines.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

const (
	bashPreviewLines    = 20   // collapsed preview (last N lines)
	bashMaxContextLines = 2000 // context cap (reference DEFAULT_MAX_LINES)
	bashMaxContextBytes = 50 * 1024
)

// ansiStripRe strips ANSI escape sequences from bash output — the npm
// ansi-regex@6.2.2 pattern (the reference's strip-ansi): OSC ... BEL/ESC-\,
// CSI/C1 with private markers, colon params, full final-byte class.
var ansiStripRe = regexp.MustCompile(`(?:\x1b\][\s\S]*?(?:\x07|\x1b\\|\x9c))|[\x1b\x9b][[\]()#;?]*(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]`)

type bashStatus int

const (
	bashRunning bashStatus = iota
	bashComplete
	bashCancelled
	bashError
)

// BashExecution renders a bash command between two borders with output + status.
type BashExecution struct {
	*tui.Container
	content   *tui.Container
	engine    *tui.TUI
	th        *theme.Theme
	command   string
	exclude   bool
	output    []string
	status    bashStatus
	expanded  bool
	exitCode  *int
	truncated bool // output exceeded the context cap and was trimmed
	loader    *components.Loader // animated spinner while running (reference :54-61)
}

// NewBashExecution builds a running bash component. engine drives the spinner
// animation (nil → static "Running..." fallback).
func NewBashExecution(engine *tui.TUI, command string, exclude bool, th *theme.Theme) *BashExecution {
	content := &tui.Container{}
	b := &BashExecution{engine: engine, th: th, command: command, exclude: exclude, status: bashRunning, content: content}
	c := &tui.Container{}
	c.AddChild(components.NewSpacer(1))
	c.AddChild(newDynamicBorder(b.borderColor()))
	c.AddChild(content)
	c.AddChild(newDynamicBorder(b.borderColor()))
	b.Container = c
	b.rebuild()
	return b
}

// stopLoader stops the animated spinner (once output arrives or the command
// completes) and clears the reference.
func (b *BashExecution) stopLoader() {
	if b.loader != nil {
		b.loader.Stop()
		b.loader = nil
	}
}

func (b *BashExecution) borderColor() tui.StyleFunc {
	key := theme.ThemeColor("bashMode")
	if b.exclude {
		key = "dim"
	}
	return func(s string) string { return b.th.Fg(key, s) }
}

// AppendOutput appends a streamed chunk (reference appendOutput :80-96), then
// enforces the context cap (reference truncateTail: 2000 lines / 50KB).
func (b *BashExecution) AppendOutput(chunk string) {
	clean := strings.ReplaceAll(chunk, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	clean = ansiStripRe.ReplaceAllString(clean, "")
	newLines := strings.Split(clean, "\n")
	if len(b.output) > 0 && len(newLines) > 0 {
		b.output[len(b.output)-1] += newLines[0]
		b.output = append(b.output, newLines[1:]...)
	} else {
		b.output = append(b.output, newLines...)
	}
	b.capContext()
	b.stopLoader() // output has arrived — the streaming output replaces the spinner
	b.rebuild()
}

// capContext trims the stored output to the last bashMaxContextLines / under
// bashMaxContextBytes (keeping the tail, like the reference) and flags truncation.
func (b *BashExecution) capContext() {
	trimmed := false
	for len(b.output) > bashMaxContextLines {
		b.output = b.output[1:]
		trimmed = true
	}
	for b.outputBytes() > bashMaxContextBytes && len(b.output) > 1 {
		b.output = b.output[1:]
		trimmed = true
	}
	if trimmed {
		b.truncated = true
	}
}

func (b *BashExecution) outputBytes() int {
	n := 0
	for _, l := range b.output {
		n += len(l) + 1
	}
	return n
}

// SetComplete finalizes the status. A non-zero exitCode drives the error state
// and is rendered as "(exit N)" (reference setComplete :98-117,190).
func (b *BashExecution) SetComplete(exitCode *int, cancelled, isError bool) {
	b.stopLoader()
	b.exitCode = exitCode
	switch {
	case cancelled:
		b.status = bashCancelled
	case isError || (exitCode != nil && *exitCode != 0):
		b.status = bashError
	default:
		b.status = bashComplete
	}
	b.rebuild()
}

// SetExpanded toggles full output vs preview.
func (b *BashExecution) SetExpanded(e bool) { b.expanded = e; b.rebuild() }

func (b *BashExecution) rebuild() {
	colorKey := theme.ThemeColor("bashMode")
	if b.exclude {
		colorKey = "dim"
	}
	b.content.Clear()
	b.content.AddChild(components.NewText(b.th.Fg(colorKey, b.th.Bold("$ "+b.command)), 1, 0, nil))

	// Output (capped to the last N logical lines when collapsed).
	if len(b.output) > 0 {
		preview := b.output
		hidden := 0
		if !b.expanded && len(preview) > bashPreviewLines {
			hidden = len(preview) - bashPreviewLines
			preview = preview[len(preview)-bashPreviewLines:]
		}

		if b.expanded {
			b.content.AddChild(components.NewText("\n"+b.th.Fg("muted", strings.Join(preview, "\n")), 1, 0, nil))
		} else {
			// Collapsed: wrap the preview to width and show the last N visual
			// lines (reference bash-execution.ts:148-166). Leading "\n" mirrors
			// the reference's `"\n"+styledOutput` so the preview starts one row
			// below the command header.
			styledInput := "\n" + b.th.Fg("muted", strings.Join(preview, "\n"))
			b.content.AddChild(newVisualTruncateText(styledInput, bashPreviewLines, 1))
		}

		if status := b.statusLine(hidden); status != "" {
			b.content.AddChild(components.NewText("\n"+status, 1, 0, nil))
		}
	} else if b.status == bashRunning {
		// Animated spinner until output arrives (reference bash-execution.ts:54-61).
		// NewLoader/SetMessage are render-lock-safe (no synchronous RequestRender).
		if b.engine != nil {
			if b.loader == nil {
				b.loader = components.NewLoader(b.engine,
					fgStyle(b.th, "accent"), fgStyle(b.th, "muted"), "Running...", nil)
			}
			b.content.AddChild(b.loader)
			b.content.AddChild(components.NewText("  ("+keyHint(b.th, "tui.select.cancel", "to cancel")+")", 1, 0, nil))
		} else {
			b.content.AddChild(components.NewText(b.th.Fg("muted", "Running..."), 1, 0, nil))
		}
	}
}

// statusLine builds the post-output status (hidden-lines hint + error/cancelled +
// exit code + truncation notice).
func (b *BashExecution) statusLine(hidden int) string {
	var parts []string
	if b.expanded {
		parts = append(parts, "("+keyHint(b.th, "app.tools.expand", "to collapse")+")")
	} else if hidden > 0 {
		parts = append(parts, b.th.Fg("muted", "... "+strconv.Itoa(hidden)+" more lines")+
			" ("+keyHint(b.th, "app.tools.expand", "to expand")+")")
	}
	if b.truncated {
		parts = append(parts, b.th.Fg("warning", "Output truncated"))
	}
	switch b.status {
	case bashError:
		if b.exitCode != nil {
			parts = append(parts, b.th.Fg("error", fmt.Sprintf("(exit %d)", *b.exitCode)))
		} else {
			parts = append(parts, b.th.Fg("error", "(error)"))
		}
	case bashCancelled:
		parts = append(parts, b.th.Fg("warning", "(cancelled)"))
	case bashRunning:
		parts = append(parts, b.th.Fg("muted", "Running..."))
	}
	return strings.Join(parts, " ")
}
