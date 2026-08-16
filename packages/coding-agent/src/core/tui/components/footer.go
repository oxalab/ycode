package components

import (
	"fmt"
	"os"
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// Footer renders the status bar at the bottom of the TUI.
// Shows 2 lines:
//   - Line 1: pwd (with ~ substitution), git branch, session name
//   - Line 2: token stats (↑↓RW), cost, context %, model name + thinking level
//
// Phase 14: renders pwd and model name. Full stats in Phase 15 when
// context usage and session stats are wired.
type Footer struct {
	session    *core.AgentSession
	footerData *core.FooterDataProvider
	theme      *theme.Theme
	width      int

	// Cached state updated by the parent App
	autoCompactEnabled bool
}

// NewFooter creates a new Footer component.
func NewFooter(session *core.AgentSession, data *core.FooterDataProvider, t *theme.Theme) Footer {
	return Footer{
		session:    session,
		footerData: data,
		theme:      t,
	}
}

// SetWidth updates the footer's rendered width (called on resize).
func (f *Footer) SetWidth(width int) {
	f.width = width
}

// SetSession updates the session reference (called on session replacement).
func (f *Footer) SetSession(session *core.AgentSession) {
	f.session = session
}

// SetAutoCompactEnabled updates the auto-compact indicator.
func (f *Footer) SetAutoCompactEnabled(enabled bool) {
	f.autoCompactEnabled = enabled
}

// View renders the footer as a 2-line string.
func (f Footer) View() string {
	if f.theme == nil || f.width == 0 {
		return ""
	}

	pwdLine := f.renderPwdLine()
	statsLine := f.renderStatsLine()

	return pwdLine + "\n" + statsLine
}

// renderPwdLine renders the first footer line: cwd + git branch + session name.
func (f Footer) renderPwdLine() string {
	pwd := f.session.SessionManager.GetCwd()

	// Replace home directory with ~
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home != "" && len(pwd) >= len(home) && pwd[:len(home)] == home {
		pwd = "~" + pwd[len(home):]
	}

	// Append git branch
	if f.footerData != nil {
		branch := f.footerData.GitBranch()
		if branch != "" {
			pwd = pwd + " (" + branch + ")"
		}
	}

	// Append session name
	sessionName := f.session.SessionManager.GetSessionName()
	if sessionName != "" {
		pwd = pwd + " · " + sessionName
	}

	pwdLine := f.theme.Dim(pwd)
	if f.width > 0 && lipgloss.Width(pwdLine) > f.width {
		pwdLine = lipgloss.NewStyle().MaxWidth(f.width).Render(pwdLine)
	}

	return pwdLine
}

// renderStatsLine renders the second footer line: tokens + cost + context% + model.
func (f Footer) renderStatsLine() string {
	// Phase 14: simplified stats. Full token counting in Phase 15.
	autoIndicator := ""
	if f.autoCompactEnabled {
		autoIndicator = " (auto)"
	}

	contextDisplay := fmt.Sprintf("?%s", autoIndicator)
	modelName := "no-model"

	// Try to get model info
	model := f.session.GetModel()
	if model.ID != "" {
		modelName = model.ID
	}

	statsLeft := contextDisplay
	rightSide := modelName

	// Compose: statsLeft ... rightSide (right-aligned)
	statsLeftWidth := lipgloss.Width(statsLeft)
	rightSideWidth := lipgloss.Width(rightSide)

	minPadding := 2
	totalNeeded := statsLeftWidth + minPadding + rightSideWidth

	var statsLine string
	if totalNeeded <= f.width {
		padding := f.width - statsLeftWidth - rightSideWidth
		statsLine = f.theme.Dim(statsLeft) + strings.Repeat(" ", padding) + f.theme.Dim(rightSide)
	} else {
		// Not enough space — just show stats
		statsLine = f.theme.Dim(statsLeft)
	}

	return statsLine
}
