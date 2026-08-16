package autocomplete

import (
	core "ycode/packages/coding-agent/src/core"
	llm "ycode/packages/llm/src"
)

// SlashCommandProvider is a standalone AutocompleteProvider over builtin slash
// commands, with optional per-command argument completers (e.g. /model).
//
// It mirrors the builtin-commands portion of CombinedProvider so callers that
// want slash suggestions without the template/extension/skill merge can use it
// directly. The two share the completeSlash routing helper; the substantive new
// logic in 17.3 is the /model argument completion (ModelArgumentCompleter).
//
// Port of the slash-command autocomplete in interactive-mode.ts (the
// BUILTIN_SLASH_COMMANDS → AutocompleteItem conversion and the /model argument
// completion attached at interactive-mode.ts:451-477).
type SlashCommandProvider struct {
	commands []commandSource
}

// NewSlashCommandProvider builds a provider from builtin commands.
func NewSlashCommandProvider(commands []core.BuiltinSlashCommand) SlashCommandProvider {
	p := SlashCommandProvider{}
	for _, c := range commands {
		p.commands = append(p.commands, commandSource{name: c.Name, description: c.Description})
	}
	return p
}

// WithArgumentCompleter attaches an argument completer to the named command and
// returns the updated provider. Unknown names are ignored.
func (p SlashCommandProvider) WithArgumentCompleter(name string, completer func(prefix string) []AutocompleteItem) SlashCommandProvider {
	for i := range p.commands {
		if p.commands[i].name == name {
			p.commands[i].argCompleter = completer
			break
		}
	}
	return p
}

// GetItems implements AutocompleteProvider.
func (p SlashCommandProvider) GetItems(prefix string) []AutocompleteItem {
	return completeSlash(prefix, p.commands)
}

// ----------------------------------------------------------------------------
// /model argument completion
// ----------------------------------------------------------------------------

// ModelArgumentCompleter builds the /model argument completer from a model
// getter. The getter returns the models to suggest — scoped models when any are
// configured, otherwise the registry's available set. The caller (Phase 17.6
// wiring) makes that choice, faithfully matching the TS
// "scopedModels.length > 0 ? scopedModels.map(s => s.model) : getAvailable()".
//
// The returned completer fuzzy-filters models by "id provider" (so "opus
// anthropic" matches), listing each as label=model id, description=provider.
//
// Value note: the TS source sets `value: item.label` where the filtered item has
// no `label` field (only id+provider), so its inserted value is effectively
// undefined — a latent reference bug. The Go port sets value to the canonical
// "provider/id" reference so the selection resolves unambiguously via the model
// resolver.
func ModelArgumentCompleter(modelsFn func() []*llm.Model[llm.Api]) func(prefix string) []AutocompleteItem {
	return func(prefix string) []AutocompleteItem {
		models := modelsFn()
		if len(models) == 0 {
			return nil
		}
		var items []AutocompleteItem
		for _, m := range models {
			if m == nil {
				continue
			}
			if fuzzyMatch(m.ID+" "+string(m.Provider), prefix) {
				items = append(items, NewItem(
					string(m.Provider)+"/"+m.ID, // value: canonical reference
					m.ID,                         // label
					string(m.Provider),           // description
				))
			}
		}
		return items
	}
}
