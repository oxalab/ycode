package interactive

// footer.go ports reference components/footer.ts (verified — see
// mapping/10-chrome.md), the P4 status bar. It is a plain tui.Component
// (Render(width) → []string) that reads live session state on every render:
// cumulative token usage from the session entries, context % from
// GetContextUsage, the git branch from the FooterDataProvider, and the current
// model + thinking level. Line 1 = pwd/branch/session; line 2 = token+context
// stats (left) right-aligned against the model/thinking line; line 3 (optional,
// deferred — no Go data source yet) = extension statuses.
//
// Faithfulness notes vs footer.ts:
//   - statsLeft/remainder are dimmed SEPARATELY (:200-202) because statsLeft can
//     contain an error/warning color segment whose reset would clear an outer
//     dim wrapper. We avoid byte-slicing statsLine (the stats parts include
//     multibyte ↑↓) by rebuilding the remainder from the padding string instead.
//   - context % colored error>90 / warning>70 (:132-138); "?/…​" when unknown.
//   - provider `(provider) ` prefix only when >1 available provider (:167-173).
//   - extension-status line 3 is omitted: Go's FooterDataProvider has no
//     per-extension status map (FooterData.ExtensionStatus is a single string).
//     Wired when its data source exists (P7 extension surfaces).

import (
	"math"
	"os"
	"strconv"
	"strings"

	llm "ycode/packages/llm/src"
	"ycode/packages/tui"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// Footer is the 2-line status bar (pwd; token/context/model stats). It mirrors
// reference FooterComponent; render reads session state fresh each frame.
type Footer struct {
	session  *core.AgentSession
	provider *core.FooterDataProvider
	th       *theme.Theme
	auto     bool
}

// NewFooter builds the footer. The provider count is computed live on each
// Render (the provider set changes mid-session: /login, add-provider,
// logout, scoped models).
func NewFooter(session *core.AgentSession, provider *core.FooterDataProvider, th *theme.Theme, autoCompact bool) *Footer {
	return &Footer{
		session:  session,
		provider: provider,
		th:       th,
		auto:     autoCompact,
	}
}

// SetSession updates the session reference (called on session replacement).
func (f *Footer) SetSession(session *core.AgentSession) { f.session = session }

// SetAutoCompactEnabled toggles the " (auto)" context indicator.
func (f *Footer) SetAutoCompactEnabled(enabled bool) { f.auto = enabled }

// Render implements tui.Component. Faithful port of footer.ts:65-219.
func (f *Footer) Render(width int) []string {
	if f.session == nil || f.session.SessionManager == nil {
		return []string{}
	}

	// Cumulative usage across ALL session entries (not just post-compaction).
	var in, out, cacheRead, cacheWrite int
	var cost float64
	for _, entry := range f.session.SessionManager.GetEntries() {
		if entry.Type != compaction.EntryTypeMessage {
			continue
		}
		if am, ok := asAssistant(entry.Message); ok {
			u := am.Usage
			in += u.Input
			out += u.Output
			cacheRead += u.CacheRead
			cacheWrite += u.CacheWrite
			cost += u.Cost.Total
		}
	}

	// Context usage (nil Percent → unknown, shown as "?").
	cu := f.session.GetContextUsage()
	contextWindow := cu.ContextWindow
	model := f.session.GetModel()
	if contextWindow == 0 {
		contextWindow = model.ContextWindow
	}
	contextPercentValue := 0.0
	contextPercentStr := "?"
	if cu.Percent != nil {
		contextPercentValue = float64(*cu.Percent)
		contextPercentStr = strconv.FormatFloat(contextPercentValue, 'f', 1, 64)
	}

	// pwd: ~ for home, (branch) if present, • sessionName if set (footer.ts:92-109).
	pwd := f.session.SessionManager.GetCwd()
	if home := homeDir(); home != "" && strings.HasPrefix(pwd, home) {
		pwd = "~" + pwd[len(home):]
	}
	if f.provider != nil {
		if branch := f.provider.GitBranch(); branch != "" {
			pwd = pwd + " (" + branch + ")"
		}
	}
	if name := f.session.SessionManager.GetSessionName(); name != "" {
		pwd = pwd + " • " + name
	}

	// statsParts (footer.ts:112-139).
	var parts []string
	if in != 0 {
		parts = append(parts, "↑"+formatTokens(in))
	}
	if out != 0 {
		parts = append(parts, "↓"+formatTokens(out))
	}
	if cacheRead != 0 {
		parts = append(parts, "R"+formatTokens(cacheRead))
	}
	if cacheWrite != 0 {
		parts = append(parts, "W"+formatTokens(cacheWrite))
	}

	usingSubscription := false
	if model.ID != "" {
		m := model // take address of a local copy for IsUsingOAuth
		usingSubscription = f.session.GetModelRegistry().IsUsingOAuth(&m)
	}
	if cost != 0 || usingSubscription {
		costStr := "$" + strconv.FormatFloat(cost, 'f', 3, 64)
		if usingSubscription {
			costStr += " (sub)"
		}
		parts = append(parts, costStr)
	}

	autoIndicator := ""
	if f.auto {
		autoIndicator = " (auto)"
	}
	var contextPercentDisplay string
	if contextPercentStr == "?" {
		contextPercentDisplay = "?/" + formatTokens(contextWindow) + autoIndicator
	} else {
		contextPercentDisplay = contextPercentStr + "%/" + formatTokens(contextWindow) + autoIndicator
	}
	switch {
	case contextPercentValue > 90:
		parts = append(parts, f.th.Fg("error", contextPercentDisplay))
	case contextPercentValue > 70:
		parts = append(parts, f.th.Fg("warning", contextPercentDisplay))
	default:
		parts = append(parts, contextPercentDisplay)
	}

	statsLeft := strings.Join(parts, " ")

	// Right side: model + thinking, optionally prefixed with "(provider) "
	// when >1 provider is available (footer.ts:143-173).
	modelName := model.ID
	if modelName == "" {
		modelName = "no-model"
	}

	statsLeftWidth := tui.VisibleWidth(statsLeft)
	if statsLeftWidth > width {
		statsLeft = tui.TruncateToWidth(statsLeft, width, "...", false)
		statsLeftWidth = tui.VisibleWidth(statsLeft)
	}

	const minPadding = 2
	rightSide := modelName
	if model.Reasoning {
		level := string(f.session.GetThinkingLevel())
		if level == "" {
			level = "off"
		}
		if level == "off" {
			rightSide = modelName + " • thinking off"
		} else {
			rightSide = modelName + " • " + level
		}
	}
	if provCount := distinctAvailableProviders(f.session); provCount > 1 && model.ID != "" {
		withProv := "(" + string(model.Provider) + ") " + rightSide
		if statsLeftWidth+minPadding+tui.VisibleWidth(withProv) <= width {
			rightSide = withProv
		}
	}

	// Compose statsLine = statsLeft + padding + rightSide (footer.ts:175-195).
	// Keep the padding string so we can dim statsLeft and the remainder
	// separately without byte-slicing a multibyte statsLeft (footer.ts:200-202).
	rightSideWidth := tui.VisibleWidth(rightSide)
	totalNeeded := statsLeftWidth + minPadding + rightSideWidth
	var padding, rightContent string
	switch {
	case totalNeeded <= width:
		padding = strings.Repeat(" ", width-statsLeftWidth-rightSideWidth)
		rightContent = rightSide
	default:
		availableForRight := width - statsLeftWidth - minPadding
		if availableForRight > 0 {
			rightContent = tui.TruncateToWidth(rightSide, availableForRight, "", false)
			padding = strings.Repeat(" ", max(0, width-statsLeftWidth-tui.VisibleWidth(rightContent)))
		}
		// else: not enough space for the right side — padding/rightContent stay
		// empty, so statsLine reduces to just dim(statsLeft).
	}

	pwdLine := tui.TruncateToWidth(f.th.Fg("dim", pwd), width, f.th.Fg("dim", "..."), false)
	statsLine := f.th.Fg("dim", statsLeft) + f.th.Fg("dim", padding+rightContent)
	return []string{pwdLine, statsLine}
}

// formatTokens mirrors footer.ts:21-27.
func formatTokens(count int) string {
	switch {
	case count < 1000:
		return strconv.Itoa(count)
	case count < 10000:
		return strconv.FormatFloat(float64(count)/1000, 'f', 1, 64) + "k"
	case count < 1000000:
		return strconv.Itoa(int(math.Round(float64(count)/1000))) + "k"
	case count < 10000000:
		return strconv.FormatFloat(float64(count)/1000000, 'f', 1, 64) + "M"
	default:
		return strconv.Itoa(int(math.Round(float64(count)/1000000))) + "M"
	}
}

// distinctAvailableProviders counts unique providers among the models the
// footer's provider indicator is based on (footer.ts getAvailableProviderCount
// via updateAvailableProviderCount). Computed live on each render — the
// provider set changes mid-session (login/logout/add-provider/scoped models).
// Mirrors TS getModelCandidates (interactive-mode.ts:3942-3945): scoped models
// take precedence over the registry when set.
func distinctAvailableProviders(session *core.AgentSession) int {
	if session == nil {
		return 0
	}
	// Scoped models shadow the registry (TS: scopedModels.length > 0 early
	// return). Entries hold *llm.Model[llm.Api]; skip anything else.
	if scoped := session.GetScopedModels(); len(scoped) > 0 {
		seen := make(map[string]struct{}, len(scoped))
		for _, entry := range scoped {
			if m, ok := entry.Model.(*llm.Model[llm.Api]); ok {
				seen[string(m.Provider)] = struct{}{}
			}
		}
		return len(seen)
	}
	reg := session.GetModelRegistry()
	if reg == nil {
		return 0
	}
	avail, err := reg.GetAvailable()
	if err != nil {
		return 0
	}
	return countUniqueProviders(avail)
}

// countUniqueProviders counts distinct providers in a model slice.
func countUniqueProviders(models []*llm.Model[llm.Api]) int {
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		seen[string(m.Provider)] = struct{}{}
	}
	return len(seen)
}

// homeDir returns the user home for ~ substitution (HOME / USERPROFILE).
func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}
