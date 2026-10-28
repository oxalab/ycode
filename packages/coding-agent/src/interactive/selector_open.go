package interactive

// selector_open.go holds the editor-swap selector framework + the P5a selector
// open methods. Selectors swap INTO editorContainer (reference showSelector,
// interactive-mode.ts:3745-3756): clear the editor, add the selector, focus it;
// on select/cancel, restore the editor. All of this runs under the engine
// render lock (from HandleInput/OnSubmit), so it uses the lock-assumed
// SetFocusFromInput + direct tree mutation — never SetFocus/Mutate/RequestRender
// (those re-lock and self-deadlock). The engine re-renders after HandleInput
// returns, so no explicit RequestRender is needed.

import (
	"ycode/packages/tui"
	"ycode/packages/tui/components"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
	oauth "ycode/packages/llm/src/utils/oauth"
)

// swapInSelector replaces the editor with a selector and focuses it. Called from
// an input callback (under the render lock) — uses SetFocusFromInput.
func (o *Orchestrator) swapInSelector(c tui.Component) {
	o.editorContainer.Clear()
	o.editorContainer.AddChild(c)
	o.engine.SetFocusFromInput(c)
}

// closeSelector restores the editor (called from a selector's select/cancel).
func (o *Orchestrator) closeSelector() {
	o.editorContainer.Clear()
	o.editorContainer.AddChild(o.editor)
	o.engine.SetFocusFromInput(o.editor)
}

// refreshTheme re-reads the global theme and refreshes the components whose
// colors are read each render (footer, editor border) and clears the chat's
// cached render output so transcript components (markdown cards) re-resolve
// through the live adapter closures (TS onThemeChange → ui.invalidate()).
// Called under the render lock from selector callbacks — direct mutation only
// (no Mutate/RequestRender). Tool/bash box chrome still captures th at
// construction (P8 hardening refinement, as before).
func (o *Orchestrator) refreshTheme() {
	o.th = theme.GetTheme()
	if o.footer != nil {
		o.footer.th = o.th
	}
	if o.chat != nil {
		o.chat.Invalidate()
	}
	o.updateEditorBorder(o.editor.GetText())
}

// openThinkingSelector swaps in the thinking-level selector (thinking-selector.ts).
func (o *Orchestrator) openThinkingSelector() {
	current := o.session.GetThinkingLevel()
	available := o.session.GetAvailableThinkingLevels()
	sel := newThinkingSelector(current, available, o.th,
		func(level sdk.ThinkingLevel) { // onSelect — apply + close
			o.session.SetThinkingLevel(level)
			o.updateEditorBorder(o.editor.GetText())
			o.closeSelector()
		},
		o.closeSelector, // onCancel
	)
	o.swapInSelector(sel)
}

// openThemeSelector swaps in the theme selector (theme-selector.ts) with a live
// preview: navigating re-themes the UI; selecting keeps it; canceling restores
// the original theme.
func (o *Orchestrator) openThemeSelector() {
	original := theme.GetCurrentThemeName()
	sel := newThemeSelector(original, o.th,
		func(name string) { // onSelect — apply + close
			theme.SetTheme(name)
			o.refreshTheme()
			o.closeSelector()
		},
		func() { // onCancel — restore original + close
			if theme.GetCurrentThemeName() != original {
				theme.SetTheme(original)
				o.refreshTheme()
			}
			o.closeSelector()
		},
		func(name string) { // onPreview — live preview (no close)
			theme.SetTheme(name)
			o.refreshTheme()
		},
	)
	o.swapInSelector(sel)
}

// openModelSelector swaps in the searchable model selector (model-selector.ts).
// SetModel runs off the input-callback goroutine (it may do client init); the
// selector closes immediately and the footer re-renders when the model applies.
func (o *Orchestrator) openModelSelector() {
	current := o.session.GetModel()
	sel := NewModelSelector(current, current.ID != "", o.settings, o.session.GetModelRegistry(),
		o.session.GetScopedModels(), o.th,
		func(model llm.Model[llm.Api]) { // onSelect
			o.updateEditorBorder(o.editor.GetText())
			o.closeSelector()
			go func() {
				_ = o.session.SetModel(model) // not under the render lock
				o.engine.RequestRender()      // reflect the new model in footer/editor
			}()
		},
		o.closeSelector, // onCancel
	)
	o.swapInSelector(sel)
}

// openSettingsSelector swaps in the settings panel (settings-selector.ts). It
// covers the Go-supported settings (auto-compact, steering/follow-up mode,
// transport, thinking, theme); callbacks persist via SettingsManager and
// live-refresh the UI. Runs under the render lock — direct calls only.
func (o *Orchestrator) openSettingsSelector() {
	cfg := SettingsSelectorConfig{
		AutoCompact:             o.settings.GetCompactionEnabled(),
		SteeringMode:            o.settings.GetSteeringMode(),
		FollowUpMode:            o.settings.GetFollowUpMode(),
		Transport:               o.settings.GetTransport(),
		ThinkingLevel:           o.session.GetThinkingLevel(),
		AvailableThinkingLevels: o.session.GetAvailableThinkingLevels(),
		Theme:                   theme.GetCurrentThemeName(),
		AvailableThemes:         theme.GetAvailableThemes(),
	}
	cb := SettingsSelectorCallbacks{
		OnAutoCompactChange: func(enabled bool) {
			o.settings.SetCompactionEnabled(enabled)
			if o.footer != nil {
				o.footer.SetAutoCompactEnabled(enabled)
			}
		},
		OnSteeringModeChange: o.settings.SetSteeringMode,
		OnFollowUpModeChange: o.settings.SetFollowUpMode,
		OnTransportChange:    o.settings.SetTransport,
		OnThinkingLevelChange: func(level sdk.ThinkingLevel) {
			o.settings.SetDefaultThinkingLevel(string(level))
			o.session.SetThinkingLevel(level)
			o.updateEditorBorder(o.editor.GetText())
		},
		OnThemeChange: func(name string) {
			o.settings.SetTheme(name)
			theme.SetTheme(name)
			o.refreshTheme()
		},
		OnThemePreview: func(name string) {
			theme.SetTheme(name)
			o.refreshTheme()
		},
		OnCancel: o.closeSelector,
	}
	o.swapInSelector(newSettingsSelector(cfg, cb, o.th))
}

// openLoginSelector opens the /login auth-type chooser — "Use a subscription"
// (OAuth) or "Use an API key" (reference showLoginAuthTypeSelector). The two
// branches (openOAuthProviderSelector here; openApiKeyProviderSelector in
// login_apikey.go) follow. This replaced the old OAuth-only list, which silently
// no-op'd when the user had no OAuth providers (the common API-key case).
func (o *Orchestrator) openLoginSelector() {
	o.openLoginAuthTypeSelector()
}

// openOAuthProviderSelector lists OAuth providers and opens the OAuth login dialog
// on select (reference showLoginProviderSelector("oauth")). Honest notice when
// there are none — the user should pick "Use an API key" instead.
func (o *Orchestrator) openOAuthProviderSelector() {
	providers := oauth.GetOAuthProviders()
	if len(providers) == 0 {
		o.closeSelector()
		o.addNotice(o.th.Fg("muted", "No subscription (OAuth) providers available. Pick \"Use an API key\" instead."))
		return
	}
	items := make([]components.SelectItem, len(providers))
	for i, p := range providers {
		items[i] = components.SelectItem{Value: p.ID(), Label: p.Name()}
	}
	list := components.NewSelectList(items, len(items), buildSelectListTheme(o.th))
	list.OnSelect = func(item components.SelectItem) { o.openLoginDialog(item.Value, item.Label) }
	list.OnCancel = o.closeSelector
	o.swapInSelector(newSimpleSelector(list, o.th))
}

// openLoginDialog swaps in the login dialog for a provider and starts the
// AuthStorage.Login flow on a goroutine. Runs under the render lock (from the
// provider chooser's OnSelect); Start launches the Login goroutine and returns
// immediately. onComplete (Login goroutine) restores the editor via Mutate and
// refreshes the model registry on success so models become available.
func (o *Orchestrator) openLoginDialog(providerId, displayName string) {
	auth := o.session.GetModelRegistry().GetAuthStorage()
	dialog := NewLoginDialog(o.engine, providerId, displayName, o.th,
		func(success bool, _ string) {
			o.engine.Mutate(func() { o.closeSelector() })
			if success {
				o.session.GetModelRegistry().Refresh()
			}
			o.engine.RequestRender()
		})
	o.loginDialog = dialog
	o.swapInSelector(dialog)
	dialog.Start(auth)
}

// openContextPanel swaps in the /context visualization (read-only). The
// breakdown is a snapshot computed here; Esc restores the editor via the
// panel's onCancel. Runs under the render lock — direct calls only.
func (o *Orchestrator) openContextPanel() {
	breakdown := estimateContextBreakdown(o.session, o.settings)
	panel := newContextPanel(breakdown, o.th, o.closeSelector)
	o.swapInSelector(panel)
}

// (Slash-command routing — including the selector-opening commands above —
// lives in slash.go's routeCommand, called from handleSubmit.)
