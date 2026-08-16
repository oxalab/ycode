package components

import (
	"fmt"
	"strings"

	"ycode/packages/coding-agent/src/core/tools"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// bashPreviewLines is the line limit when collapsed (not expanded).
const bashPreviewLines = 20

// bashStatus represents the execution status of a bash command.
type bashStatus int

const (
	bashStatusRunning bashStatus = iota
	bashStatusComplete
	bashStatusCancelled
	bashStatusError
)

// BashExecutionComponent renders a bash command execution with streaming output.
//
// Layout (vertical stack):
//
//	┌─── top border (bashMode or dim color) ───┐
//	│ $ <command>                               │
//	│ <output lines or preview>                 │
//	│ <status: running spinner / exit / cancel> │
//	└─── bottom border ─────────────────────────┘
//
// Port of reference/coding-agent/src/modes/interactive/components/bash-execution.ts
type BashExecutionComponent struct {
	theme    *theme.Theme
	command  string
	width    int

	// State
	outputLines []string
	status      bashStatus
	exitCode    *int
	expanded    bool

	// Completion metadata
	truncationResult *tools.TruncationResult
	fullOutputPath   string

	// excludeFromContext toggles dim border (!! prefix commands)
	excludeFromContext bool
}

// NewBashExecutionComponent creates a new bash execution component.
func NewBashExecutionComponent(command string, t *theme.Theme, excludeFromContext bool) *BashExecutionComponent {
	return &BashExecutionComponent{
		theme:              t,
		command:            command,
		status:             bashStatusRunning,
		excludeFromContext: excludeFromContext,
	}
}

// SetWidth updates the available width.
func (b *BashExecutionComponent) SetWidth(width int) {
	b.width = width
}

// AppendOutput appends a chunk of streaming output (ANSI codes stripped, line endings normalized).
func (b *BashExecutionComponent) AppendOutput(chunk string) {
	clean := stripANSI(chunk)
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "\n")

	newLines := strings.Split(clean, "\n")
	if len(b.outputLines) > 0 && len(newLines) > 0 {
		// Append first chunk to last line (incomplete line continuation)
		b.outputLines[len(b.outputLines)-1] += newLines[0]
		b.outputLines = append(b.outputLines, newLines[1:]...)
	} else {
		b.outputLines = append(b.outputLines, newLines...)
	}
}

// SetComplete marks the command as finished with the given exit code and status.
func (b *BashExecutionComponent) SetComplete(exitCode *int, cancelled bool, truncationResult *tools.TruncationResult, fullOutputPath string) {
	b.exitCode = exitCode
	if cancelled {
		b.status = bashStatusCancelled
	} else if exitCode != nil && *exitCode != 0 {
		b.status = bashStatusError
	} else {
		b.status = bashStatusComplete
	}
	b.truncationResult = truncationResult
	b.fullOutputPath = fullOutputPath
}

// SetExpanded toggles full output vs preview.
func (b *BashExecutionComponent) SetExpanded(expanded bool) {
	b.expanded = expanded
}

// GetOutput returns the raw accumulated output.
func (b BashExecutionComponent) GetOutput() string {
	return strings.Join(b.outputLines, "\n")
}

// GetCommand returns the executed command.
func (b BashExecutionComponent) GetCommand() string {
	return b.command
}

// View renders the bash execution component.
func (b BashExecutionComponent) View() string {
	width := b.width
	if width <= 0 {
		width = 80
	}

	// Border color: dim for excluded commands, bashMode otherwise
	colorKey := "bashMode"
	if b.excludeFromContext {
		colorKey = "dim"
	}
	borderColor := func(s string) string { return b.theme.Fg(theme.ThemeColor(colorKey), s) }

	// Top border
	topBorder := borderColor(strings.Repeat("─", width))

	// Command header
	header := b.theme.Fg(theme.ThemeColor(colorKey), b.theme.Bold("$ "+b.command))

	// Build the content between borders
	var contentLines []string
	contentLines = append(contentLines, header)

	// Apply context truncation
	fullOutput := strings.Join(b.outputLines, "\n")
	contextTrunc := tools.TruncateTail(fullOutput, tools.TruncationOptions{
		MaxLines: &[]int{tools.DefaultMaxLines}[0],
		MaxBytes: &[]int{tools.DefaultMaxBytes}[0],
	})

	availableLines := []string{}
	if contextTrunc.Content != "" {
		availableLines = strings.Split(contextTrunc.Content, "\n")
	}

	// Output
	if len(availableLines) > 0 {
		if b.expanded {
			// Show all lines
			styled := make([]string, len(availableLines))
			for i, line := range availableLines {
				styled[i] = b.theme.Fg("muted", line)
			}
			contentLines = append(contentLines, "\n"+strings.Join(styled, "\n"))
		} else {
			// Preview: last N lines
			start := 0
			if len(availableLines) > bashPreviewLines {
				start = len(availableLines) - bashPreviewLines
			}
			previewLines := availableLines[start:]
			styled := make([]string, len(previewLines))
			for i, line := range previewLines {
				styled[i] = b.theme.Fg("muted", line)
			}
			contentLines = append(contentLines, "\n"+strings.Join(styled, "\n"))
		}
	}

	// Status line
	hiddenLineCount := len(availableLines) - bashPreviewLines
	if hiddenLineCount < 0 {
		hiddenLineCount = 0
	}

	var statusParts []string

	if b.status == bashStatusRunning {
		statusParts = append(statusParts, b.theme.Fg("muted", "Running..."))
	} else {
		// Hidden lines indicator
		if hiddenLineCount > 0 {
			expandHint := "... " + fmt.Sprintf("%d more lines", hiddenLineCount)
			if b.expanded {
				expandHint = "(ctrl+o to collapse)"
			} else {
				expandHint += " (ctrl+o to expand)"
			}
			statusParts = append(statusParts, b.theme.Fg("muted", expandHint))
		}

		switch b.status {
		case bashStatusCancelled:
			statusParts = append(statusParts, b.theme.Fg("warning", "(cancelled)"))
		case bashStatusError:
			exitStr := "?"
			if b.exitCode != nil {
				exitStr = fmt.Sprintf("%d", *b.exitCode)
			}
			statusParts = append(statusParts, b.theme.Fg("error", "(exit "+exitStr+")"))
		}

		// Truncation warning
		wasTruncated := (b.truncationResult != nil && b.truncationResult.Truncated) || contextTrunc.Truncated
		if wasTruncated && b.fullOutputPath != "" {
			statusParts = append(statusParts, b.theme.Fg("warning", "Output truncated. Full output: "+b.fullOutputPath))
		}
	}

	if len(statusParts) > 0 {
		contentLines = append(contentLines, "\n"+strings.Join(statusParts, "\n"))
	}

	content := strings.Join(contentLines, "\n")

	// Bottom border
	bottomBorder := borderColor(strings.Repeat("─", width))

	return lipgloss.JoinVertical(lipgloss.Left, topBorder, content, bottomBorder)
}

// stripANSI removes ANSI escape codes from a string.
// This is a minimal implementation covering SGR and cursor codes.
func stripANSI(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inEscape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEscape = true
			continue
		}
		if inEscape {
			// CSI sequence ends with a letter (0x40-0x7E)
			if c == '[' {
				continue
			}
			if c >= 0x40 && c <= 0x7E {
				inEscape = false
			}
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}
