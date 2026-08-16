package tui

// Phase 17.6: autocomplete wiring.
//
// createBaseAutocompleteProvider builds the CombinedProvider from the session's
// four slash-command sources (builtin commands, prompt templates, extension
// commands, skills) and attaches the /model argument completer. setupAutocomplete
// installs it on the editor and is re-run after a session replacement so the
// popup reflects the current session. Port of createBaseAutocompleteProvider +
// setupAutocompleteProvider from interactive-mode.ts:442-530.

import (
	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/autocomplete"
	llm "ycode/packages/llm/src"
)

// setupAutocomplete builds the base autocomplete provider and installs it on the
// editor. Safe to call repeatedly; each call fully replaces the provider.
func (a *App) setupAutocomplete() {
	a.editor.SetAutocompleteProvider(a.createBaseAutocompleteProvider())
}

// createBaseAutocompleteProvider assembles the slash-command autocomplete sources
// into a single provider. Skills are included only when "enable skill commands"
// is on — the same gate the TS applies inside createBaseAutocompleteProvider.
func (a App) createBaseAutocompleteProvider() autocomplete.AutocompleteProvider {
	commands := core.BuiltinSlashCommands

	if a.session == nil {
		return autocomplete.NewCombinedProvider(commands, nil, nil, nil, a.cwd())
	}

	var skills []core.Skill
	if a.settingsManager != nil && a.settingsManager.GetEnableSkillCommands() {
		skills = a.session.GetSkills()
	}

	combined := autocomplete.NewCombinedProvider(
		commands,
		a.session.GetPromptTemplates(),
		a.session.GetExtensionCommands(),
		skills,
		a.session.GetCwd(),
	)

	// Attach the /model argument completer (Phase 17.3).
	return combined.WithArgumentCompleter(
		"model",
		autocomplete.ModelArgumentCompleter(a.modelsForCompletion),
	)
}

// modelsForCompletion returns the models to suggest for "/model <prefix>":
// scoped models when any are configured, otherwise the registry's available set
// — faithfully matching the TS "scopedModels.length > 0 ? scoped : getAvailable()".
// ScopedModelEntry.Model is stored as a non-pointer llm.Model[llm.Api].
func (a App) modelsForCompletion() []*llm.Model[llm.Api] {
	if a.session == nil {
		return nil
	}
	if scoped := a.session.GetScopedModels(); len(scoped) > 0 {
		out := make([]*llm.Model[llm.Api], 0, len(scoped))
		for _, sm := range scoped {
			if m, ok := sm.Model.(llm.Model[llm.Api]); ok {
				out = append(out, &m)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if reg := a.session.GetModelRegistry(); reg != nil {
		if avail, err := reg.GetAvailable(); err == nil {
			return avail
		}
	}
	return nil
}

// cwd returns the working directory passed to the autocomplete provider. It is
// retained for future file/path completion (currently unused by CombinedProvider).
func (a App) cwd() string {
	if a.session != nil {
		return a.session.GetCwd()
	}
	if a.sessionManager != nil {
		return a.sessionManager.GetCwd()
	}
	return ""
}
