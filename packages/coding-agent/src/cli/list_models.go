package cli

// This file lists available models, optionally filtered by a fuzzy search
// pattern, as a fixed-width table. It is the Go port of
// reference/coding-agent/src/cli/list-models.ts.
//
// ============================================================================
// Reuse + honest deferrals
// ============================================================================
//
// Reuse (no duplication within the cli package):
//   - padRight / bold (defined in args.go) for column padding and styling.
//
// fuzzyMatch is a LOCAL copy of components.FuzzyMatch (a case-folded
// subsequence matcher approximating pi-tui's fuzzyFilter). The cli package keeps
// its own copy rather than importing core/tui/components, so a stdout-only
// --list-models path does not pull the entire TUI component graph (bubbletea,
// lipgloss, theme) into the CLI. This mirrors the precedent already set by
// core/tui/autocomplete/fuzzy.go. (The codebase currently has three near-identical
// copies; consolidating into a shared fuzzy package is a later cleanup.)
//
// auth-guidance is NOT ported as a module; getProviderLoginHelp and
// formatNoModelsAvailableMessage are reproduced here (the only consumers in this
// phase) using config.GetDocsPath(). The rest of auth-guidance (no-model-selected
// / no-api-key messages, the OAuth login flow) is deferred to Phase 19+.

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"ycode/packages/coding-agent/src/config"
	core "ycode/packages/coding-agent/src/core"
	llm "ycode/packages/llm/src"
)

// ListModels lists available models, optionally filtered by a fuzzy search
// pattern, writing a fixed-width table to out (TS listModels). A non-empty model
// registry load error is warned to stderr first (TS console.error).
//
// searchPattern matches the TS second parameter (the plan sketch omitted it).
// out is taken as an io.Writer for testability (TS writes to stdout via
// console.log); the load-error warning still goes to stderr.
func ListModels(registry *core.ModelRegistry, searchPattern string, out io.Writer) error {
	if loadErr := registry.GetError(); loadErr != "" {
		fmt.Fprintln(os.Stderr, yellow("Warning: errors loading models.json:\n"+loadErr))
	}

	models, err := registry.GetAvailable()
	if err != nil {
		return err
	}

	if len(models) == 0 {
		fmt.Fprintln(out, formatNoModelsAvailableMessage())
		return nil
	}

	// Apply the fuzzy filter against "provider id" when a pattern is given.
	filtered := models
	if searchPattern != "" {
		filtered = make([]*llm.Model[llm.Api], 0, len(models))
		for _, m := range models {
			if fuzzyMatch(string(m.Provider)+" "+m.ID, searchPattern) {
				filtered = append(filtered, m)
			}
		}
	}

	if len(filtered) == 0 {
		fmt.Fprintf(out, "No models matching %q\n", searchPattern)
		return nil
	}

	renderModelTable(filtered, out)
	return nil
}

// renderModelTable sorts the models (provider, then id) and writes the
// fixed-width table. Extracted from ListModels so the column/padding logic is
// unit-testable without constructing a ModelRegistry.
func renderModelTable(models []*llm.Model[llm.Api], out io.Writer) {
	// Sort by provider, then by model id (TS localeCompare; ASCII identifiers
	// make byte comparison equivalent here).
	sort.Slice(models, func(i, j int) bool {
		if c := strings.Compare(string(models[i].Provider), string(models[j].Provider)); c != 0 {
			return c < 0
		}
		return strings.Compare(models[i].ID, models[j].ID) < 0
	})

	// Column definitions: header + cell accessor.
	cols := []struct {
		header string
		get    func(m *llm.Model[llm.Api]) string
	}{
		{"provider", func(m *llm.Model[llm.Api]) string { return string(m.Provider) }},
		{"model", func(m *llm.Model[llm.Api]) string { return m.ID }},
		{"context", func(m *llm.Model[llm.Api]) string { return formatTokenCount(m.ContextWindow) }},
		{"max-out", func(m *llm.Model[llm.Api]) string { return formatTokenCount(m.MaxTokens) }},
		{"thinking", func(m *llm.Model[llm.Api]) string { return yesNo(m.Reasoning) }},
		{"images", func(m *llm.Model[llm.Api]) string { return yesNo(hasImageInput(m.Input)) }},
	}

	// Column widths: max(header, widest cell), in runes (matching padRight).
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c.header)
		for _, m := range models {
			if w := utf8.RuneCountInString(c.get(m)); w > widths[i] {
				widths[i] = w
			}
		}
	}

	// Header row.
	hcells := make([]string, len(cols))
	for i, c := range cols {
		hcells[i] = padRight(c.header, widths[i])
	}
	fmt.Fprintln(out, strings.Join(hcells, "  "))

	// Data rows.
	for _, m := range models {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = padRight(c.get(m), widths[i])
		}
		fmt.Fprintln(out, strings.Join(cells, "  "))
	}
}

// formatTokenCount formats a token count as human-readable, e.g. 200000 → "200K",
// 1500000 → "1.5M" (TS formatTokenCount).
func formatTokenCount(count int) string {
	if count >= 1_000_000 {
		m := float64(count) / 1_000_000
		if m == math.Trunc(m) {
			return strconv.FormatFloat(m, 'f', -1, 64) + "M"
		}
		return strconv.FormatFloat(m, 'f', 1, 64) + "M"
	}
	if count >= 1_000 {
		k := float64(count) / 1_000
		if k == math.Trunc(k) {
			return strconv.FormatFloat(k, 'f', -1, 64) + "K"
		}
		return strconv.FormatFloat(k, 'f', 1, 64) + "K"
	}
	return strconv.Itoa(count)
}

// yesNo renders a bool as "yes"/"no" (TS `x ? "yes" : "no"`).
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// hasImageInput reports whether the model accepts image input (TS
// `m.input.includes("image")`).
func hasImageInput(inputs []llm.InputType) bool {
	for _, in := range inputs {
		if string(in) == "image" {
			return true
		}
	}
	return false
}

// fuzzyMatch reports whether query matches text as a case-folded subsequence (an
// empty query matches everything). It is a verbatim copy of
// components.FuzzyMatch, kept local so cli avoids importing the TUI graph.
func fuzzyMatch(text, query string) bool {
	if query == "" {
		return true
	}
	t := strings.ToLower(text)
	q := strings.ToLower(query)
	ti := 0
	for qi := 0; qi < len(q); qi++ {
		found := false
		for ; ti < len(t); ti++ {
			if t[ti] == q[qi] {
				found = true
				ti++
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// yellow wraps s in ANSI yellow SGR codes (chalk.yellow equivalent; the warning
// is printed regardless of theme, before any TUI starts).
func yellow(s string) string {
	return "\x1b[33m" + s + "\x1b[0m"
}

// formatNoModelsAvailableMessage reproduces the TS auth-guidance message (the
// only consumer in this phase).
func formatNoModelsAvailableMessage() string {
	return "No models available. " + getProviderLoginHelp()
}

// getProviderLoginHelp reproduces the TS auth-guidance login help text using the
// ported config.GetDocsPath.
func getProviderLoginHelp() string {
	return strings.Join([]string{
		"Use /login to log into a provider via OAuth or API key. See:",
		"  " + filepath.Join(config.GetDocsPath(), "providers.md"),
		"  " + filepath.Join(config.GetDocsPath(), "models.md"),
	}, "\n")
}
