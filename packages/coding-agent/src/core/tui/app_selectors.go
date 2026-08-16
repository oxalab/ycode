package tui

// Phase 16: selector overlay management.
//
// Every Phase 16 selector/dialog is callback-driven: it emits a result via a
// returned tea.Cmd, and the App processes that result in its own Update loop
// (handleSelectorDone). This keeps state mutation in the value-copy Update flow
// rather than in pointer-captured closures.

import (
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	llm "ycode/packages/llm/src"
)

// overlayComponent is a tea.Model that also accepts width updates. Every Phase
// 16 selector/dialog component implements this.
type overlayComponent interface {
	tea.Model
	SetWidth(int)
}

// ============================================================================
// Result protocol
// ============================================================================

// Each result type below is the payload of a selectorDoneMsg, identifying what
// the user selected. handleSelectorDone switches on the concrete type.
type (
	themeChangeResult       struct{ name string }
	themePreviewResult      struct{ name string }
	modelChangeResult       struct{ model llm.Model[llm.Api] }
	sessionSwitchResult     struct{ path string }
	thinkingChangeResult    struct{ level string }
	showImagesResult        struct{ show bool }
	settingsChangeResult    struct{ id, value string }
	warningsChangeResult    struct{ enabled bool }
	scopedModelsResult      struct{ enabled []string }
	forkMessageResult       struct{ entryID string }
	oauthProviderResult     struct {
		providerID string
		mode       components.OAuthSelectorMode
	}
)

// themePreviewMsg applies a live theme preview WITHOUT closing the overlay.
type themePreviewMsg struct{ name string }

// makeCancelCmd is the standard overlay cancel command.
func makeCancelCmd() tea.Cmd {
	return func() tea.Msg { return selectorCancelledMsg{} }
}

// doneCmd wraps a result in a selectorDoneMsg-producing command.
func doneCmd(result any) tea.Cmd {
	return func() tea.Msg { return selectorDoneMsg{result: result} }
}

// ============================================================================
// Open / close / forward
// ============================================================================

// openOverlay installs a component as the active overlay and returns its Init
// command (e.g. spinner/countdown ticks). width is applied immediately.
func (a *App) openOverlay(state OverlayState, model overlayComponent) tea.Cmd {
	a.overlay = state
	a.overlayModel = model
	if model != nil {
		model.SetWidth(a.overlayWidth())
	}
	return model.Init()
}

// closeOverlay tears down the active overlay.
func (a *App) closeOverlay() {
	a.overlay = OverlayNone
	a.overlayModel = nil
}

// overlayWidth is the content width handed to an overlay (clamped).
func (a App) overlayWidth() int {
	w := a.width - 4
	if w < 50 {
		w = 50
	}
	return w
}

// forwardOverlayMsg routes a message to the active overlay.
// Returns ok=false when no overlay is active.
func (a *App) forwardOverlayMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	if a.overlay == OverlayNone || a.overlayModel == nil {
		return nil, nil, false
	}
	updated, cmd := a.overlayModel.Update(msg)
	if oc, ok := updated.(overlayComponent); ok {
		a.overlayModel = oc
	}
	return *a, cmd, true
}

// ============================================================================
// Result handlers
// ============================================================================

// handleSelectorDone applies a selection result and closes the overlay.
func (a App) handleSelectorDone(result any) (tea.Model, tea.Cmd) {
	switch r := result.(type) {
	case themeChangeResult:
		if ok, _ := theme.SetTheme(r.name); ok {
			a.theme = theme.GetTheme()
		}
		a.closeOverlay()

	case modelChangeResult:
		if a.session != nil {
			if err := a.session.SetModel(r.model); err != nil {
				a.addStaticEntry(a.theme.Fg("error", "Failed to set model: "+err.Error()))
			}
		}
		a.closeOverlay()

	case sessionSwitchResult:
		a.closeOverlay()
		if a.runtimeHost != nil && r.path != "" {
			if _, err := a.runtimeHost.SwitchSession(r.path, nil); err != nil {
				a.addStaticEntry(a.theme.Fg("error", "Failed to switch session: "+err.Error()))
			}
		}

	case thinkingChangeResult:
		if a.settingsManager != nil {
			a.settingsManager.SetDefaultThinkingLevel(r.level)
		}
		a.closeOverlay()

	case showImagesResult:
		// Show-images persistence requires a settings setter not yet ported.
		a.addStaticEntry(a.theme.Fg("muted", "(show images = "+boolStr(r.show)+" — persistence: Phase 19+)"))
		a.closeOverlay()

	case settingsChangeResult:
		a.applySettingsChange(r.id, r.value)
		// Settings overlay stays open; the component reports each cycle.

	case warningsChangeResult:
		// Warnings persistence not yet ported.
		a.closeOverlay()

	case scopedModelsResult:
		// Enabled-model persistence requires a settings setter not yet ported.
		a.addStaticEntry(a.theme.Fg("muted", "(scoped models saved — persistence: Phase 19+)"))
		a.closeOverlay()

	case forkMessageResult:
		a.closeOverlay()
		if a.runtimeHost != nil && r.entryID != "" {
			if _, err := a.runtimeHost.Fork(r.entryID, &core.ForkOptions{Position: "at"}); err != nil {
				a.addStaticEntry(a.theme.Fg("error", "Failed to fork: "+err.Error()))
			}
		}

	case oauthProviderResult:
		// Full login/logout flow (dialogs) lands in a later phase.
		a.addStaticEntry(a.theme.Fg("muted", "(auth flow for "+r.providerID+" — Phase 19+)"))
		a.closeOverlay()

	default:
		a.closeOverlay()
	}
	return a, nil
}

// handleThemePreview applies a live theme preview without closing the overlay.
func (a App) handleThemePreview(name string) (tea.Model, tea.Cmd) {
	if ok, _ := theme.SetTheme(name); ok {
		a.theme = theme.GetTheme()
	}
	return a, nil
}

// applySettingsChange maps a settings-selector id+value to a SettingsManager
// setter where one exists; others are noted as pending.
func (a App) applySettingsChange(id, value string) {
	sm := a.settingsManager
	if sm == nil {
		return
	}
	switch id {
	case "steeringMode":
		sm.SetSteeringMode(value)
	case "followUpMode":
		sm.SetFollowUpMode(value)
	case "theme":
		if ok, _ := theme.SetTheme(value); ok {
			a.theme = theme.GetTheme()
		}
	case "thinking":
		sm.SetDefaultThinkingLevel(value)
	case "transport":
		sm.SetTransport(llm.Transport(value))
	case "autoCompact":
		sm.SetCompactionEnabled(value == "true")
	default:
		// Remaining cycling settings have no SettingsManager setter yet.
	}
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ============================================================================
// Overlay rendering
// ============================================================================

// renderSelectorOverlay renders the active overlay, boxed with a themed border.
func renderSelectorOverlay(a App) string {
	if a.overlay == OverlayNone || a.overlayModel == nil {
		return ""
	}
	body := a.overlayModel.View()
	return boxOverlay(a, overlayTitle(a.overlay), body)
}

// overlayTitle returns the title shown in the overlay's top border.
func overlayTitle(state OverlayState) string {
	switch state {
	case OverlayModelSelector:
		return " Model "
	case OverlaySessionSelector:
		return " Sessions "
	case OverlaySessionSearch:
		return " Search Sessions "
	case OverlayThemeSelector:
		return " Theme "
	case OverlayThinkingSelector:
		return " Thinking Level "
	case OverlaySettingsSelector:
		return " Settings "
	case OverlayScopedModelsSelector:
		return " Models "
	case OverlayConfigSelector:
		return " Resources "
	case OverlayShowImagesSelector:
		return " Show Images "
	case OverlayUserMessageSelector:
		return " Fork from Message "
	case OverlayExtensionSelector:
		return " Extensions "
	case OverlayLoginDialog:
		return " Login "
	case OverlayOAuthSelector:
		return " Select Provider "
	case OverlayTreeSelector:
		return " Session Tree "
	}
	return ""
}

// boxOverlay frames content with a themed border + title, centered within the
// available width.
func boxOverlay(a App, title, content string) string {
	width := a.overlayWidth()
	if width < 40 {
		width = 40
	}
	border := a.theme.Fg("border", "")

	style := lipgloss.NewStyle().
		BorderForeground(lipgloss.Color("")).
		BorderStyle(lipgloss.NormalBorder()).
		Width(width).
		Padding(0, 1)
	_ = border // (border color is applied via theme below when colors resolve)

	// Title line + content, separated.
	var b strings.Builder
	if title != "" {
		b.WriteString(a.theme.Fg("accent", a.theme.Bold(title)) + "\n")
	}
	b.WriteString(strings.TrimRight(content, "\n"))
	return style.Render(b.String())
}

// ============================================================================
// Overlay openers — construct each component with result callbacks.
// (Key bindings that invoke these land in 16.19.)
// ============================================================================

// openThemeSelector opens the theme picker. Live preview is applied on
// highlight; cancel restores the original theme.
func (a App) openThemeSelector() (tea.Model, tea.Cmd) {
	original := theme.GetCurrentThemeName()
	comp := components.NewThemeSelectorComponent(
		a.theme,
		original,
		components.ThemeSelectorCallbacks{
			OnSelect: func(name string) tea.Cmd { return doneCmd(themeChangeResult{name: name}) },
			OnCancel: func() tea.Cmd {
				return func() tea.Msg {
					_, _ = theme.SetTheme(original) // restore on cancel
					return selectorCancelledMsg{}
				}
			},
			OnPreview: func(name string) {
				// Void callback — emit a preview msg through the program loop.
				if globalProgram != nil {
					globalProgram.Send(themePreviewMsg{name: name})
				}
			},
		},
	)
	cmd := a.openOverlay(OverlayThemeSelector, comp)
	return a, cmd
}

// openThinkingSelector opens the thinking-level picker.
func (a App) openThinkingSelector() (tea.Model, tea.Cmd) {
	var levels []theme.ThinkingLevel
	if a.session != nil {
		for _, l := range a.session.GetAvailableThinkingLevels() {
			levels = append(levels, theme.ThinkingLevel(string(l)))
		}
	}
	current := theme.ThinkingLevel("")
	if a.session != nil {
		current = theme.ThinkingLevel(string(a.session.GetThinkingLevel()))
	}
	comp := components.NewThinkingSelectorComponent(
		a.theme, current, levels,
		components.ThinkingSelectorCallbacks{
			OnSelect: func(level theme.ThinkingLevel) tea.Cmd {
				return doneCmd(thinkingChangeResult{level: string(level)})
			},
			OnCancel: makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayThinkingSelector, comp)
	return a, cmd
}

// openModelSelector opens the model picker. (The component itself persists the
// default model/provider; the App applies the session model on confirm.)
func (a App) openModelSelector() (tea.Model, tea.Cmd) {
	var currentPtr *llm.Model[llm.Api]
	if a.session != nil {
		if c := a.session.GetModel(); c.ID != "" {
			current := c
			currentPtr = &current
		}
	}
	var registry *core.ModelRegistry
	var scoped []core.ScopedModelEntry
	if a.session != nil {
		registry = a.session.GetModelRegistry()
		scoped = a.session.GetScopedModels()
	}
	comp := components.NewModelSelectorComponent(
		a.theme, currentPtr, a.settingsManager, registry, scoped,
		components.ModelSelectorCallbacks{
			OnSelect: func(m llm.Model[llm.Api]) tea.Cmd {
				return doneCmd(modelChangeResult{model: m})
			},
			OnCancel: makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayModelSelector, comp)
	return a, cmd
}

// openSessionSelector opens the session picker across current + all folders.
func (a App) openSessionSelector() (tea.Model, tea.Cmd) {
	var current, all []*core.SessionInfo
	var currentPath string
	if sm := a.sessionManager; sm != nil {
		current = core.ListSessions(sm.GetCwd(), sm.GetSessionDir())
		all = core.ListAllSessions()
		currentPath = sm.GetSessionFile()
	}
	comp := components.NewSessionSelectorComponent(
		a.theme, current, all, currentPath,
		components.SessionSelectorCallbacks{
			OnSelect: func(path string) tea.Cmd {
				return doneCmd(sessionSwitchResult{path: path})
			},
			OnCancel: makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlaySessionSelector, comp)
	return a, cmd
}

// openScopedModelsSelector opens the Ctrl+P model enable/reorder picker.
// Changes are session-only; persistence is pending a settings setter.
func (a App) openScopedModelsSelector() (tea.Model, tea.Cmd) {
	var allModels []*llm.Model[llm.Api]
	var enabled []string
	if a.session != nil {
		if reg := a.session.GetModelRegistry(); reg != nil {
			allModels = reg.GetAll()
		}
	}
	if sm := a.settingsManager; sm != nil {
		if e := sm.GetEnabledModels(); len(e) > 0 {
			enabled = e
		}
	}
	comp := components.NewScopedModelsSelectorComponent(
		a.theme,
		components.ScopedModelsConfig{AllModels: allModels, EnabledModelIDs: enabled},
		components.ScopedModelsCallbacks{
			OnChange:  func(_ []string) tea.Cmd { return nil }, // session-only
			OnPersist: func(ids []string) tea.Cmd { return doneCmd(scopedModelsResult{enabled: ids}) },
			OnCancel:  makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayScopedModelsSelector, comp)
	return a, cmd
}

// openSettingsSelector opens the cycling-settings list with nested submenus.
func (a App) openSettingsSelector() (tea.Model, tea.Cmd) {
	cfg := a.buildSettingsConfig()
	comp := components.NewSettingsSelectorComponent(
		a.theme, cfg,
		components.SettingsCallbacks{
			OnChange:        func(id, value string) tea.Cmd { return doneCmd(settingsChangeResult{id: id, value: value}) },
			OnWarningsChange: func(enabled bool) tea.Cmd { return doneCmd(warningsChangeResult{enabled: enabled}) },
			OnThemePreview:   func(name string) tea.Cmd { return func() tea.Msg { return themePreviewMsg{name: name} } },
			OnCancel:         makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlaySettingsSelector, comp)
	return a, cmd
}

// buildSettingsConfig snapshots the current settings into a SettingsConfig.
func (a App) buildSettingsConfig() components.SettingsConfig {
	cfg := components.SettingsConfig{
		AvailableThemes: theme.GetAvailableThemes(),
		CurrentTheme:    theme.GetCurrentThemeName(),
	}
	if sm := a.settingsManager; sm != nil {
		cfg.AutoCompact = sm.GetCompactionEnabled()
		cfg.ShowImages = sm.GetShowImages()
		cfg.BlockImages = sm.GetBlockImages()
		cfg.EnableSkillCommands = sm.GetEnableSkillCommands()
		cfg.SteeringMode = sm.GetSteeringMode()
		cfg.FollowUpMode = sm.GetFollowUpMode()
		cfg.Transport = sm.GetTransport()
		cfg.ThinkingLevel = theme.ThinkingLevel(sm.GetDefaultThinkingLevel())
		cfg.HideThinkingBlock = sm.GetHideThinkingBlock()
		cfg.QuietStartup = sm.GetQuietStartup()
	}
	if a.session != nil {
		for _, l := range a.session.GetAvailableThinkingLevels() {
			cfg.AvailableThinkingLevels = append(cfg.AvailableThinkingLevels, theme.ThinkingLevel(string(l)))
		}
	}
	return cfg
}

// openShowImagesSelector opens the Yes/No images picker.
func (a App) openShowImagesSelector() (tea.Model, tea.Cmd) {
	current := false
	if sm := a.settingsManager; sm != nil {
		current = sm.GetShowImages()
	}
	comp := components.NewShowImagesSelectorComponent(
		a.theme, current,
		components.ShowImagesSelectorCallbacks{
			OnSelect: func(show bool) tea.Cmd { return doneCmd(showImagesResult{show: show}) },
			OnCancel: makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayShowImagesSelector, comp)
	return a, cmd
}

// openConfigSelector opens the resource configuration list. Real resolved
// paths require the PackageManager port (pending); opens empty until then.
func (a App) openConfigSelector() (tea.Model, tea.Cmd) {
	comp := components.NewConfigSelectorComponent(
		a.theme, &core.ResolvedPaths{},
		components.ConfigSelectorCallbacks{
			OnToggle: func(_ components.ConfigToggleReport) tea.Cmd { return nil },
			OnClose:  makeCancelCmd,
			OnExit:   makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayConfigSelector, comp)
	return a, cmd
}

// openUserMessageSelector opens the "fork from message" picker. Session message
// history sourcing is pending; opens empty until then.
func (a App) openUserMessageSelector() (tea.Model, tea.Cmd) {
	comp := components.NewUserMessageSelectorComponent(
		a.theme, nil, "",
		components.UserMessageSelectorCallbacks{
			OnSelect: func(entryID string) tea.Cmd { return doneCmd(forkMessageResult{entryID: entryID}) },
			OnCancel: makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayUserMessageSelector, comp)
	return a, cmd
}

// openOAuthSelector opens the auth provider picker. The provider-options list
// and full login/logout flow are pending; opens empty until then.
func (a App) openOAuthSelector(mode components.OAuthSelectorMode) (tea.Model, tea.Cmd) {
	var lookup components.OAuthAuthLookup
	if a.session != nil {
		if reg := a.session.GetModelRegistry(); reg != nil {
			lookup = reg.GetAuthStorage()
		}
	}
	comp := components.NewOAuthSelectorComponent(
		a.theme, mode, lookup, nil,
		components.OAuthSelectorCallbacks{
			OnSelect: func(providerID string) tea.Cmd {
				return doneCmd(oauthProviderResult{providerID: providerID, mode: mode})
			},
			OnCancel: makeCancelCmd,
		},
	)
	cmd := a.openOverlay(OverlayOAuthSelector, comp)
	return a, cmd
}

