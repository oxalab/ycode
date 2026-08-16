package components

import (
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// OAuthSelectorMode selects the dialog's purpose.
type OAuthSelectorMode string

const (
	OAuthModeLogin  OAuthSelectorMode = "login"
	OAuthModeLogout OAuthSelectorMode = "logout"
)

// AuthSelectorProvider is one row in the auth provider list.
type AuthSelectorProvider struct {
	ID       string
	Name     string
	AuthType string // "oauth" | "api_key"
}

// OAuthAuthLookup is the slice of AuthStorage the selector needs. *core.AuthStorage
// satisfies it directly, so the App can pass one with no adapter.
type OAuthAuthLookup interface {
	Get(providerID string) *core.AuthCredential
	GetAuthStatus(providerID string) core.AuthStatus
}

// OAuthSelectorCallbacks holds the result handlers supplied by the App.
type OAuthSelectorCallbacks struct {
	OnSelect func(providerID string) tea.Cmd
	OnCancel func() tea.Cmd
}

// OAuthSelectorComponent renders a searchable auth provider picker, used by the
// /login and /logout flows. Each provider row shows its current auth status
// (configured / env key / unconfigured / …).
//
// Navigation binds only ↑/↓ (not j/k) so those letters remain free for the
// search box — matching the TS reference.
//
// Port of reference/coding-agent/src/modes/interactive/components/oauth-selector.ts
type OAuthSelectorComponent struct {
	theme *theme.Theme
	width int

	mode     OAuthSelectorMode
	lookup   OAuthAuthLookup
	all      []AuthSelectorProvider
	filtered []AuthSelectorProvider
	cursor   int

	search searchInput
	cb     OAuthSelectorCallbacks
}

// NewOAuthSelectorComponent creates a new auth provider selector.
func NewOAuthSelectorComponent(
	t *theme.Theme,
	mode OAuthSelectorMode,
	lookup OAuthAuthLookup,
	providers []AuthSelectorProvider,
	callbacks OAuthSelectorCallbacks,
) *OAuthSelectorComponent {
	s := &OAuthSelectorComponent{
		theme:  t,
		mode:   mode,
		lookup: lookup,
		all:    providers,
		cb:     callbacks,
	}
	s.filtered = providers
	return s
}

// SetWidth updates the available width.
func (s *OAuthSelectorComponent) SetWidth(width int) { s.width = width }

// Init satisfies tea.Model.
func (s *OAuthSelectorComponent) Init() tea.Cmd { return nil }

// Update routes keyboard input: ↑/↓ navigation (clamped), enter, cancel, search.
func (s *OAuthSelectorComponent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	str := keyMsg.String()

	switch str {
	case "up":
		if len(s.filtered) > 0 && s.cursor > 0 {
			s.cursor--
		}
		return s, nil
	case "down":
		if len(s.filtered) > 0 && s.cursor < len(s.filtered)-1 {
			s.cursor++
		}
		return s, nil
	case "enter":
		if s.cursor >= 0 && s.cursor < len(s.filtered) {
			if s.cb.OnSelect != nil {
				return s, s.cb.OnSelect(s.filtered[s.cursor].ID)
			}
		}
		return s, nil
	case "escape", "ctrl+c":
		if s.cb.OnCancel != nil {
			return s, s.cb.OnCancel()
		}
		return s, nil
	}

	// Everything else feeds the search box.
	if s.search.handleKey(str) {
		s.applyFilter()
	}
	return s, nil
}

// View renders the title + search + provider list (status-tagged) + indicators.
func (s OAuthSelectorComponent) View() string {
	var b strings.Builder

	var title string
	if s.mode == OAuthModeLogin {
		title = "Select provider to configure:"
	} else {
		title = "Select provider to logout:"
	}
	b.WriteString(s.theme.Fg("accent", s.theme.Bold(title)) + "\n\n")
	b.WriteString(s.theme.Dim("Search: ") + s.search.value + "\n\n")

	if len(s.filtered) == 0 {
		b.WriteString(s.theme.Fg("muted", "  "+s.emptyMessage()))
		return b.String()
	}

	const maxVisible = 8
	n := len(s.filtered)
	start := s.cursor - maxVisible/2
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > n {
		end = n
		start = end - maxVisible
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < end; i++ {
		provider := s.filtered[i]
		b.WriteString(s.renderProvider(provider, i == s.cursor))
		b.WriteString("\n")
	}

	if start > 0 || end < n {
		b.WriteString(s.theme.Fg("muted", "  ("+itoa(s.cursor+1)+"/"+itoa(n)+")"))
	}
	return b.String()
}

// --- Internal ---

// applyFilter runs a fuzzy match over name + id + authType, clamping the cursor.
func (s *OAuthSelectorComponent) applyFilter() {
	q := strings.TrimSpace(strings.ToLower(s.search.value))
	if q == "" {
		s.filtered = s.all
	} else {
		filtered := make([]AuthSelectorProvider, 0, len(s.all))
		for _, p := range s.all {
			hay := strings.ToLower(p.Name + " " + p.ID + " " + p.AuthType)
			if FuzzyMatch(hay, q) {
				filtered = append(filtered, p)
			}
		}
		s.filtered = filtered
	}
	if s.cursor >= len(s.filtered) {
		s.cursor = len(s.filtered) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// emptyMessage selects the "no providers" message for the mode/filter state.
func (s OAuthSelectorComponent) emptyMessage() string {
	if len(s.all) == 0 {
		if s.mode == OAuthModeLogin {
			return "No providers available"
		}
		return "No providers logged in. Use /login first."
	}
	return "No matching providers"
}

// renderProvider renders one row: marker + name + status indicator.
func (s OAuthSelectorComponent) renderProvider(provider AuthSelectorProvider, selected bool) string {
	var line string
	if selected {
		line = s.theme.Fg("accent", "→ ") + s.theme.Fg("accent", provider.Name)
	} else {
		line = "  " + s.theme.Fg("text", provider.Name)
	}
	return line + s.formatStatusIndicator(provider)
}

// formatStatusIndicator renders the auth-status suffix for a provider row.
// Mirrors formatStatusIndicator() in the TS reference.
func (s OAuthSelectorComponent) formatStatusIndicator(provider AuthSelectorProvider) string {
	cred := s.lookup.Get(provider.ID)

	// Matching credential type → configured.
	if cred != nil && cred.Type == provider.AuthType {
		return s.theme.Fg("success", " ✓ configured")
	}
	// Credential present but type mismatch (e.g. oauth cred for an api_key provider).
	if cred != nil {
		label := "API key configured"
		if cred.Type == "oauth" {
			label = "subscription configured"
		}
		return s.theme.Fg("muted", " • ") + s.theme.Fg("warning", label)
	}
	// OAuth providers with no credential → unconfigured.
	if provider.AuthType != "api_key" {
		return s.theme.Fg("muted", " • unconfigured")
	}

	// api_key providers: fall back to env/runtime/fallback status.
	status := s.lookup.GetAuthStatus(provider.ID)
	switch status.Source {
	case "environment":
		label := status.Label
		if label == "" {
			label = "API key"
		}
		return s.theme.Fg("success", " ✓ env: "+label)
	case "runtime":
		return s.theme.Fg("success", " ✓ runtime API key")
	case "fallback":
		return s.theme.Fg("success", " ✓ custom API key")
	case "models_json_key":
		return s.theme.Fg("success", " ✓ key in models.json")
	case "models_json_command":
		return s.theme.Fg("success", " ✓ command in models.json")
	default:
		return s.theme.Fg("muted", " • unconfigured")
	}
}
