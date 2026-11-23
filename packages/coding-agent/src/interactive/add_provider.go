package interactive

// add_provider.go implements the in-TUI "Add custom provider" flow — a beyond-
// reference convenience (the reference only lets you pick already-defined
// providers; custom ones are added by editing models.json or via the extension
// pi.registerProvider API). Reachable from /login → "Use an API key" →
// "Add custom provider..." and from /add-provider.
//
// Design (per the user's spec):
//   - Inputs: provider name + base URL + API key + model id (stepped single-line
//     inputs) + API type (chooser).
//   - The provider key is slug(name); the model id is a SEPARATE field, because
//     the two usually differ (provider "zai" → model "glm-4.7"; "openai" →
//     "gpt-4.1"). The model id is what gets sent in the request payload.
//   - API type: chosen from the real llm.Api constants ("from existing types"),
//     defaulting to openai-completions (the most broadly compatible for arbitrary
//     base URLs — proxies, vLLM, OpenRouter, etc.).
//   - Persistence: ModelRegistry.SaveCustomProvider does a read-modify-write of
//     models.json (NOT the in-memory RegisterProvider), so it survives restarts.
//     The loader requires baseUrl + apiKey in models.json for custom providers
//     with models, so the key lives there (not auth.json).

import (
	"strings"

	"ycode/packages/tui"
	"ycode/packages/tui/components"
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

// providerAPIChoices is the set of real llm.Api constants offered for the API
// type. Ordered most-likely-first for a custom base URL.
var providerAPIChoices = []llm.Api{
	llm.OpenAICompletions,
	llm.OpenAIResponses,
	llm.AnthropicMessages,
	llm.MistralConversations,
	llm.OpenAICodexResponses,
	llm.AzureOpenAIResponses,
	llm.GoogleGenerativeAI,
	llm.GoogleVertexAI,
	llm.BedrockConverseStream,
}

const (
	pfName = iota
	pfBaseURL
	pfAPIKey
	pfModelID
	pfAPIType
)

// providerForm is a stepped editor-swap form: name → base URL → API key → model
// id → API-type chooser. It owns a single Input (re-labeled + reset per text
// step) and a SelectList for the final API-type step.
type providerForm struct {
	*tui.Container
	th      *theme.Theme
	step    int
	label   *components.Text
	body    *tui.Container
	input   *components.Input
	apiList *components.SelectList

	name, baseURL, apiKey, modelID string
	apiType                        llm.Api

	onFinish func(name, baseURL, apiKey, modelID string, api llm.Api)
	onCancel func()
	focused  bool
}

func newProviderForm(th *theme.Theme, onFinish func(string, string, string, string, llm.Api), onCancel func()) *providerForm {
	f := &providerForm{th: th, onFinish: onFinish, onCancel: onCancel, apiType: llm.OpenAICompletions}

	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewText(th.Bold(th.Fg("accent", "Add custom provider")), 1, 0, nil))
	c.AddChild(components.NewSpacer(1))
	f.label = components.NewText("", 0, 0, nil)
	c.AddChild(f.label)
	f.body = &tui.Container{}
	c.AddChild(f.body)
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText("("+keyHint(th, "tui.select.cancel", "to cancel")+")", 0, 0, nil))
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	f.Container = c

	f.input = components.NewInput()
	f.input.OnSubmit = func(s string) { f.advance(s) }
	f.buildAPIList()
	f.renderStep()
	return f
}

// buildAPIList creates the API-type chooser (step pfAPIType), preselecting the
// current apiType.
func (f *providerForm) buildAPIList() {
	items := make([]components.SelectItem, len(providerAPIChoices))
	curIdx := 0
	for i, a := range providerAPIChoices {
		items[i] = components.SelectItem{Value: string(a), Label: string(a)}
		if a == f.apiType {
			curIdx = i
		}
	}
	f.apiList = components.NewSelectList(items, len(items), buildSelectListTheme(f.th))
	f.apiList.SetSelectedIndex(curIdx)
	f.apiList.OnSelect = func(item components.SelectItem) {
		f.apiType = llm.Api(item.Value)
		f.finish()
	}
	f.apiList.OnCancel = func() {
		if f.onCancel != nil {
			f.onCancel()
		}
	}
}

// renderStep refreshes the label + body for the current step.
func (f *providerForm) renderStep() {
	f.body.Clear()
	switch f.step {
	case pfName:
		f.label.SetText(f.th.Fg("text", "Provider name:"))
		f.input.SetValue("")
		f.body.AddChild(f.input)
	case pfBaseURL:
		f.label.SetText(f.th.Fg("text", "Base URL (e.g. https://api.example.com/v1):"))
		f.input.SetValue("")
		f.body.AddChild(f.input)
	case pfAPIKey:
		f.label.SetText(f.th.Fg("text", "API key:"))
		f.input.SetValue("")
		f.body.AddChild(f.input)
	case pfModelID:
		f.label.SetText(f.th.Fg("text", "Model id (e.g. glm-4.7):"))
		f.input.SetValue("")
		f.body.AddChild(f.input)
	case pfAPIType:
		f.label.SetText(f.th.Fg("text", "API type:"))
		f.body.AddChild(f.apiList)
	}
}

// advance handles a submitted text value, validating per step before moving on.
// Invalid input keeps the user on the same step.
func (f *providerForm) advance(value string) {
	value = strings.TrimSpace(value)
	switch f.step {
	case pfName:
		if value == "" {
			return
		}
		f.name = value
		f.step = pfBaseURL
	case pfBaseURL:
		if value == "" || !strings.HasPrefix(value, "http") {
			return
		}
		f.baseURL = value
		f.step = pfAPIKey
	case pfAPIKey:
		if value == "" { // loader requires apiKey for custom providers with models
			return
		}
		f.apiKey = value
		f.step = pfModelID
	case pfModelID:
		if value == "" { // the model id is what the provider receives (e.g. glm-4.7)
			return
		}
		f.modelID = value
		f.step = pfAPIType
	}
	f.renderStep()
}

// finish fires onFinish with the collected values.
func (f *providerForm) finish() {
	if f.onFinish != nil {
		f.onFinish(f.name, f.baseURL, f.apiKey, f.modelID, f.apiType)
	}
}

// HandleInput: Esc cancels; otherwise the text steps route to the Input and the
// API-type step routes to the SelectList.
func (f *providerForm) HandleInput(data string) {
	if tui.GetKeybindings().Matches(data, "tui.select.cancel") {
		if f.onCancel != nil {
			f.onCancel()
		}
		return
	}
	if f.step == pfAPIType {
		f.apiList.HandleInput(data)
		return
	}
	f.input.HandleInput(data)
}

func (f *providerForm) Focused() bool { return f.focused }
func (f *providerForm) SetFocused(on bool) {
	f.focused = on
	if f.step != pfAPIType {
		f.input.SetFocused(on)
	}
}

// openAddProviderForm swaps in the provider form. onFinish persists the provider
// to models.json on a goroutine (file I/O + registry refresh), then restores the
// editor with a success/error notice.
func (o *Orchestrator) openAddProviderForm() {
	form := newProviderForm(o.th,
		func(name, baseURL, apiKey, modelID string, api llm.Api) { // onFinish (render lock)
			o.closeSelector()
			go func() {
				key := slugify(name) // provider key from the name; model id is separate
				registry := o.session.GetModelRegistry()
				if err := registry.SaveCustomProvider(key, name, baseURL, apiKey, string(api), modelID); err != nil {
					o.engine.Mutate(func() {
						o.addNotice(o.th.Fg("error", "Failed to add provider: "+err.Error()))
					})
					o.engine.RequestRender()
					return
				}
				registry.Refresh()
				o.engine.Mutate(func() {
					o.addNotice(o.th.Fg("dim", "Added provider \""+name+"\" (model "+modelID+"). Use /model to select it, /add-provider-model to add more models."))
				})
				o.engine.RequestRender()
			}()
		},
		o.closeSelector, // onCancel
	)
		o.swapInSelector(form)
}

// providerOption is one entry of the add-model form's provider picker.
type providerOption struct {
	key string
	api llm.Api // api of the provider's first known model (default for the new one)
}

// Steps of the add-model form.
const (
	amProvider = iota
	amModelID
	amAPIType
)

// addModelForm is the "add a model to an existing provider" flow
// (/add-provider-model): provider picker → model id → API-type chooser. The
// API type defaults to the picked provider's existing api. Persistence goes
// through ModelRegistry.AddCustomModel, which appends to (or creates) the
// provider's models.json entry without touching its other fields.
type addModelForm struct {
	*tui.Container
	th        *theme.Theme
	step      int
	label     *components.Text
	body      *tui.Container
	input     *components.Input
	pickList  *components.SelectList
	apiList   *components.SelectList
	providers []providerOption

	provider string
	modelID  string
	apiType  llm.Api

	onFinish func(providerKey string, api llm.Api, modelID string)
	onCancel func()
	focused  bool
}

// providerOptionsFromModels dedups registry models into provider pick entries
// (insertion order preserved; api = the provider's first known model's api,
// defaulting to openai-completions).
func providerOptionsFromModels(models []*llm.Model[llm.Api]) []providerOption {
	var out []providerOption
	seen := map[string]bool{}
	for _, m := range models {
		key := string(m.Provider)
		if seen[key] {
			continue
		}
		seen[key] = true
		api := m.Api
		if api == "" {
			api = llm.OpenAICompletions
		}
		out = append(out, providerOption{key: key, api: api})
	}
	return out
}

// persistAddModel appends the model to the provider's models.json entry via
// AddCustomModel and refreshes the registry so it becomes selectable. Package-
// level so tests can pin the persistence method + args.
func persistAddModel(registry *core.ModelRegistry, providerKey string, api llm.Api, modelID string) error {
	if err := registry.AddCustomModel(providerKey, string(api), modelID); err != nil {
		return err
	}
	registry.Refresh()
	return nil
}

// openAddModelForm swaps in the add-model form over the registry's providers.
// onFinish appends the model to the provider's models.json entry on a goroutine
// (file I/O + registry refresh), then restores the editor with a notice.
func (o *Orchestrator) openAddModelForm() {
	providers := providerOptionsFromModels(o.session.GetModelRegistry().GetAll())
	if len(providers) == 0 {
		o.addNotice(o.th.Fg("muted", "No providers available. Use /login to add a provider."))
		return
	}
	form := newAddModelForm(o.th, providers,
		func(providerKey string, api llm.Api, modelID string) { // onFinish (render lock)
			o.closeSelector()
			go func() {
				if err := persistAddModel(o.session.GetModelRegistry(), providerKey, api, modelID); err != nil {
					o.engine.Mutate(func() {
						o.addNotice(o.th.Fg("error", "Failed to add model: "+err.Error()))
					})
					o.engine.RequestRender()
					return
				}
				o.engine.Mutate(func() {
					o.addNotice(o.th.Fg("dim", "Added model "+modelID+" to "+providerKey+". Use /model to select it."))
				})
				o.engine.RequestRender()
			}()
		},
		o.closeSelector, // onCancel
	)
	o.swapInSelector(form)
}

func newAddModelForm(th *theme.Theme, providers []providerOption, onFinish func(string, llm.Api, string), onCancel func()) *addModelForm {
	f := &addModelForm{th: th, providers: providers, onFinish: onFinish, onCancel: onCancel, apiType: llm.OpenAICompletions}

	c := &tui.Container{}
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	c.AddChild(components.NewText(th.Bold(th.Fg("accent", "Add model to provider")), 1, 0, nil))
	c.AddChild(components.NewSpacer(1))
	f.label = components.NewText("", 0, 0, nil)
	c.AddChild(f.label)
	f.body = &tui.Container{}
	c.AddChild(f.body)
	c.AddChild(components.NewSpacer(1))
	c.AddChild(components.NewText("("+keyHint(th, "tui.select.cancel", "to cancel")+")", 0, 0, nil))
	c.AddChild(newDynamicBorder(fgStyle(th, "border")))
	f.Container = c

	f.input = components.NewInput()
	f.input.OnSubmit = func(s string) { f.advance(s) }
	f.buildPickList()
	f.buildAPIList()
	f.renderStep()
	return f
}

// buildPickList creates the provider picker (step amProvider).
func (f *addModelForm) buildPickList() {
	items := make([]components.SelectItem, len(f.providers))
	for i, p := range f.providers {
		items[i] = components.SelectItem{Value: p.key, Label: p.key}
	}
	f.pickList = components.NewSelectList(items, len(items), buildSelectListTheme(f.th))
	f.pickList.OnSelect = func(item components.SelectItem) {
		f.provider = item.Value
		// Default the API type to the provider's existing api.
		for _, p := range f.providers {
			if p.key == item.Value {
				f.apiType = p.api
				break
			}
		}
		f.buildAPIList()
		f.step = amModelID
		f.renderStep()
	}
	f.pickList.OnCancel = func() {
		if f.onCancel != nil {
			f.onCancel()
		}
	}
}

// buildAPIList creates the API-type chooser (step amAPIType), preselecting the
// current apiType.
func (f *addModelForm) buildAPIList() {
	items := make([]components.SelectItem, len(providerAPIChoices))
	curIdx := 0
	for i, a := range providerAPIChoices {
		items[i] = components.SelectItem{Value: string(a), Label: string(a)}
		if a == f.apiType {
			curIdx = i
		}
	}
	f.apiList = components.NewSelectList(items, len(items), buildSelectListTheme(f.th))
	f.apiList.SetSelectedIndex(curIdx)
	f.apiList.OnSelect = func(item components.SelectItem) {
		f.apiType = llm.Api(item.Value)
		f.finish()
	}
	f.apiList.OnCancel = func() {
		if f.onCancel != nil {
			f.onCancel()
		}
	}
}

// renderStep refreshes the label + body for the current step.
func (f *addModelForm) renderStep() {
	f.body.Clear()
	switch f.step {
	case amProvider:
		f.label.SetText(f.th.Fg("text", "Provider:"))
		f.body.AddChild(f.pickList)
	case amModelID:
		f.label.SetText(f.th.Fg("text", "Model id (e.g. glm-4.7):"))
		f.input.SetValue("")
		f.body.AddChild(f.input)
	case amAPIType:
		f.label.SetText(f.th.Fg("text", "API type:"))
		f.body.AddChild(f.apiList)
	}
}

// advance handles the submitted model id (the only text step).
func (f *addModelForm) advance(value string) {
	if f.step != amModelID {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" { // the model id is what the provider receives (e.g. glm-4.7)
		return
	}
	f.modelID = value
	f.step = amAPIType
	f.renderStep()
}

// finish fires onFinish with the collected values.
func (f *addModelForm) finish() {
	if f.onFinish != nil {
		f.onFinish(f.provider, f.apiType, f.modelID)
	}
}

// HandleInput: Esc cancels; the provider/api steps route to their SelectLists
// and the model-id step routes to the Input.
func (f *addModelForm) HandleInput(data string) {
	if tui.GetKeybindings().Matches(data, "tui.select.cancel") {
		if f.onCancel != nil {
			f.onCancel()
		}
		return
	}
	switch f.step {
	case amProvider:
		f.pickList.HandleInput(data)
	case amModelID:
		f.input.HandleInput(data)
	case amAPIType:
		f.apiList.HandleInput(data)
	}
}

func (f *addModelForm) Focused() bool { return f.focused }
func (f *addModelForm) SetFocused(on bool) {
	f.focused = on
	if f.step == amModelID {
		f.input.SetFocused(on)
	}
}

// slugify lowercases a name and reduces non-alphanumerics to single hyphens,
// trimming leading/trailing hyphens. Used for the provider key only (the model
// id is a separate user input — see providerForm step pfModelID). Falls back to
// "custom".
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := true // trims a leading hyphen
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		out = "custom"
	}
	return out
}
