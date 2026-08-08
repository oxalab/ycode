package interactive

// login_apikey.go adds the API-key branch of /login that the reference has but the
// port was missing (reference interactive-mode.ts:4416-4648). /login now opens an
// auth-type chooser — "Use a subscription" (OAuth) or "Use an API key" — matching
// showLoginAuthTypeSelector (:4416-4435). The API-key branch lists registry
// providers that are NOT OAuth providers (isApiKeyLoginProvider, :199-211),
// prompts for the key, and persists via AuthStorage.Set({type:"api_key", key})
// (:4637) — exactly showApiKeyLoginDialog (:4607-4648).
//
// This fixes the "/login does nothing" symptom: the port listed ONLY OAuth
// providers and silently returned when the user had none (the common API-key
// case), because providers are registered as OAuth only when their config has an
// `oauth` block (model_registry.go:825-826). Now /login always offers the API-key
// path, which is how most users authenticate.

import (
	"sort"
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	oauth "ycode/packages/llm/src/utils/oauth"
)

// openLoginAuthTypeSelector shows the OAuth-vs-API-key chooser (reference
// showLoginAuthTypeSelector, :4416-4435). The labels match the reference verbatim.
func (o *Orchestrator) openLoginAuthTypeSelector() {
	const (
		subscription = "Use a subscription"
		apiKeyLabel  = "Use an API key"
	)
	items := []components.SelectItem{
		{Value: "oauth", Label: subscription},
		{Value: "api_key", Label: apiKeyLabel},
	}
	list := components.NewSelectList(items, len(items), buildSelectListTheme(o.th))
	list.OnSelect = func(item components.SelectItem) {
		switch item.Value {
		case "oauth":
			o.openOAuthProviderSelector()
		case "api_key":
			o.openApiKeyProviderSelector()
		}
	}
	list.OnCancel = o.closeSelector
	o.swapInSelector(newSimpleSelector(list, o.th))
}

// openApiKeyProviderSelector lists API-key-capable providers and opens the API-key
// prompt on select (reference showLoginProviderSelector("api_key"), :4437-4466). An
// "Add custom provider..." entry is always present (even with zero providers) so the
// user can create one in-TUI rather than editing models.json by hand.
func (o *Orchestrator) openApiKeyProviderSelector() {
	const addCustom = "__add_custom_provider__"
	providers := apiKeyProviders(o.session.GetModelRegistry())
	items := []components.SelectItem{{Value: addCustom, Label: "Add custom provider..."}}
	for _, p := range providers {
		items = append(items, components.SelectItem{Value: p, Label: p})
	}
	list := components.NewSelectList(items, 10, buildSelectListTheme(o.th))
	list.OnSelect = func(item components.SelectItem) {
		if item.Value == addCustom {
			o.openAddProviderForm()
			return
		}
		o.openApiKeyPrompt(item.Value, item.Label)
	}
	list.OnCancel = o.closeSelector
	o.swapInSelector(newSimpleSelector(list, o.th))
}

// openApiKeyPrompt swaps in the API-key input. On submit it persists the key
// (AuthStorage.Set, :4637) + refreshes the registry on a goroutine, then restores
// the editor with a success notice. Esc cancels.
func (o *Orchestrator) openApiKeyPrompt(providerId, name string) {
	prompt := newApiKeyPrompt(o.th, name,
		func(key string) { // onSubmit — runs under the render lock
			key = strings.TrimSpace(key)
			o.closeSelector()
			if key == "" {
				o.addNotice(o.th.Fg("warning", "API key cannot be empty."))
				return
			}
			go func() {
				auth := o.session.GetModelRegistry().GetAuthStorage()
				auth.Set(providerId, core.AuthCredential{Type: "api_key", Key: key})
				o.session.GetModelRegistry().Refresh()
				o.engine.Mutate(func() {
					o.addNotice(o.th.Fg("dim", "API key saved for "+name+". Use /model to select a model."))
				})
				o.engine.RequestRender()
			}()
		},
		o.closeSelector, // onCancel
	)
	o.swapInSelector(prompt)
}

// apiKeyProviders returns distinct registry providers that are NOT OAuth providers
// (isApiKeyLoginProvider, :199-211), sorted for stable display.
func apiKeyProviders(registry *core.ModelRegistry) []string {
	oauthIDs := make(map[string]bool)
	for _, p := range oauth.GetOAuthProviders() {
		oauthIDs[p.ID()] = true
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range registry.GetAll() {
		pid := string(m.Provider)
		if pid == "" || oauthIDs[pid] || seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
	}
	sort.Strings(out)
	return out
}

// apiKeyPrompt is a minimal editor-swap input for the API key (the prompt half of
// reference showApiKeyLoginDialog, :4607-4648). Esc cancels; Enter submits.
type apiKeyPrompt struct {
	*tui.Container
	th       *theme.Theme
	input    *components.Input
	onSubmit func(string)
	onCancel func()
	focused  bool
}

func newApiKeyPrompt(th *theme.Theme, providerName string, onSubmit func(string), onCancel func()) *apiKeyPrompt {
	p := &apiKeyPrompt{th: th, onSubmit: onSubmit, onCancel: onCancel}
	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewText(th.Bold(th.Fg("accent", "Login to "+providerName)), 1, 0, nil))
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText(th.Fg("text", "Enter API key:"), 1, 0, nil))
	p.input = components.NewInput()
	p.input.OnSubmit = func(v string) {
		if p.onSubmit != nil {
			p.onSubmit(v)
		}
	}
	c.AddChild(p.input)
	c.AddChild(components.NewSpacer(1))
	hint := "(" + keyHint(th, "tui.select.cancel", "to cancel,") + " " +
		keyHint(th, "tui.select.confirm", "to submit") + ")"
	c.AddChild(components.NewText(hint, 1, 0, nil))
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	p.Container = c
	return p
}

func (p *apiKeyPrompt) HandleInput(data string) {
	if tui.GetKeybindings().Matches(data, "tui.select.cancel") {
		if p.onCancel != nil {
			p.onCancel()
		}
		return
	}
	p.input.HandleInput(data)
}

func (p *apiKeyPrompt) Focused() bool     { return p.focused }
func (p *apiKeyPrompt) SetFocused(f bool) { p.focused = f; p.input.SetFocused(f) }
