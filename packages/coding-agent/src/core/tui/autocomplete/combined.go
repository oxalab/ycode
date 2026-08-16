package autocomplete

import (
	"strings"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/extensions"
)

// commandSource is one slash-style entry the CombinedProvider can suggest.
// name omits the leading "/". argCompleter, when non-nil, provides argument
// completions for the "/<name> <arg>" form (e.g. /model's model list).
type commandSource struct {
	name         string
	description  string
	argCompleter func(prefix string) []AutocompleteItem
}

// WithArgumentCompleter attaches an argument completer to the named command
// (e.g. the /model list wired in 17.3) and returns the updated provider. Unknown
// names are ignored. This is how the slash-name merge (17.2) and the /model
// argument logic (17.3) compose without CombinedProvider depending on 17.3.
func (c CombinedProvider) WithArgumentCompleter(name string, completer func(prefix string) []AutocompleteItem) CombinedProvider {
	for i := range c.sources {
		if c.sources[i].name == name {
			c.sources[i].argCompleter = completer
			break
		}
	}
	return c
}

// CombinedProvider merges the four slash-command sources — builtin commands,
// prompt templates, extension commands, and skills — into a single autocomplete
// provider. Port of CombinedAutocompleteProvider together with
// createBaseAutocompleteProvider from interactive-mode.ts:442-517.
//
// Phase 17.2 scope: slash-name completion ("/<query>") plus the routing of
// argument completions. File/path and "#" completions are deferred. cwd is
// retained for that future work but unused now.
type CombinedProvider struct {
	sources []commandSource
	cwd     string
}

// NewCombinedProvider builds a provider from the four sources plus the working
// directory. The caller decides which skills to pass: skills are included only
// when enableSkillCommands is on, matching createBaseAutocompleteProvider's
// gating — CombinedProvider itself never consults that setting.
func NewCombinedProvider(
	commands []core.BuiltinSlashCommand,
	templates []core.PromptTemplate,
	extensionCmds []*extensions.ResolvedCommand,
	skills []core.Skill,
	cwd string,
) CombinedProvider {
	cp := CombinedProvider{cwd: cwd}

	// Builtins keep plain descriptions (no source tag).
	for _, c := range commands {
		cp.sources = append(cp.sources, commandSource{name: c.Name, description: c.Description})
	}

	// Names claimed by builtins — extension commands that collide are skipped,
	// matching "filter((cmd) => !builtinCommandNames.has(cmd.name))".
	builtinNames := make(map[string]struct{}, len(commands))
	for _, c := range commands {
		builtinNames[c.Name] = struct{}{}
	}

	// Prompt templates → "/<name>".
	for _, t := range templates {
		cp.sources = append(cp.sources, commandSource{
			name:        t.Name,
			description: prefixDescription(t.Description, t.SourceInfo),
		})
	}

	// Extension commands → "/<invocationName>", collision-free.
	for _, c := range extensionCmds {
		if c == nil || builtinNames[c.Name] != (struct{}{}) {
			continue
		}
		cp.sources = append(cp.sources, commandSource{
			name:        c.InvocationName,
			description: prefixDescription(c.Description, asCoreSourceInfo(c.SourceInfo)),
		})
	}

	// Skills → "/skill:<name>".
	for _, s := range skills {
		cp.sources = append(cp.sources, commandSource{
			name:        "skill:" + s.Name,
			description: prefixDescription(s.Description, s.SourceInfo),
		})
	}

	return cp
}

// GetItems implements AutocompleteProvider.
func (c CombinedProvider) GetItems(prefix string) []AutocompleteItem {
	return completeSlash(prefix, c.sources)
}

// completeSlash resolves a slash prefix against a flat list of command sources.
// It handles both command-name completion ("/<query>") and argument completion
// ("/<cmd> <arg>" → the command's argCompleter), and returns nil for non-slash
// prefixes. Shared by CombinedProvider and SlashCommandProvider.
func completeSlash(prefix string, sources []commandSource) []AutocompleteItem {
	if !strings.HasPrefix(prefix, "/") {
		// Phase 17 is slash-only; file/path and "#" completions are deferred.
		return nil
	}

	// Argument completion: "/<cmd> <arg>" → delegate to the command's completer.
	if i := strings.IndexAny(prefix, " \t"); i >= 0 {
		cmdName := strings.TrimPrefix(prefix[:i], "/")
		argPrefix := prefix[i+1:]
		for _, s := range sources {
			if s.name == cmdName && s.argCompleter != nil {
				return s.argCompleter(argPrefix)
			}
		}
		return nil
	}

	// Command-name completion: fuzzy-match names against the "/<query>" prefix.
	query := strings.TrimPrefix(prefix, "/")
	var items []AutocompleteItem
	for _, s := range sources {
		if fuzzyMatch(s.name, query) {
			items = append(items, NewItem("/"+s.name, "/"+s.name, s.description))
		}
	}
	return items
}

// ----------------------------------------------------------------------------
// Source tagging (port of getAutocompleteSourceTag / prefixAutocompleteDescription)
// ----------------------------------------------------------------------------

// prefixDescription prepends a source tag to a description. No tag → unchanged.
func prefixDescription(description string, si core.SourceInfo) string {
	tag := autocompleteSourceTag(si)
	if tag == "" {
		return description
	}
	if description == "" {
		return "[" + tag + "]"
	}
	return "[" + tag + "] " + description
}

// autocompleteSourceTag renders the short "[tag]" provenance marker shown before
// a non-builtin command's description. Port of getAutocompleteSourceTag.
func autocompleteSourceTag(si core.SourceInfo) string {
	scopePrefix := "t" // user→u, project→p, otherwise temporary
	switch si.Scope {
	case "user":
		scopePrefix = "u"
	case "project":
		scopePrefix = "p"
	}

	source := strings.TrimSpace(si.Source)
	if source == "" || source == "auto" || source == "local" || source == "cli" {
		return scopePrefix
	}
	if strings.HasPrefix(source, "npm:") {
		return scopePrefix + ":" + source
	}
	if git := core.ParseGitUrl(source); git != nil && git.Git != nil {
		ref := ""
		if git.Git.Ref != "" {
			ref = "@" + git.Git.Ref
		}
		return scopePrefix + ":git:" + git.Git.Host + "/" + git.Git.Path + ref
	}
	return scopePrefix
}

// asCoreSourceInfo coerces an opaque RegisteredCommand.SourceInfo (typed `any`
// in the extensions package) back to core.SourceInfo. Returns the zero value
// when the value isn't a core.SourceInfo, which yields no source tag.
func asCoreSourceInfo(si any) core.SourceInfo {
	if c, ok := si.(core.SourceInfo); ok {
		return c
	}
	if cp, ok := si.(*core.SourceInfo); ok && cp != nil {
		return *cp
	}
	return core.SourceInfo{}
}
