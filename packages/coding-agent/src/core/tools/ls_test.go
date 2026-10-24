package tools

import (
	"testing"
)

// C12 parity pin: every built-in tool definition must carry the reference's
// exact promptSnippet (system-prompt tool list). ls was the lone missing one —
// its read-only contexts lost the "List directory contents" line. Asserts
// through the exposure sets (coding + read-only), not just the constructors,
// so a tool left out of a set fails too.
func TestBuiltinToolsCarryReferencePromptSnippets(t *testing.T) {
	want := map[string]string{
		"read":  "Read file contents",
		"bash":  "Execute bash commands (ls, grep, find, etc.)",
		"edit":  "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		"write": "Create or overwrite files",
		"grep":  "Search file contents for patterns (respects .gitignore)",
		"find":  "Find files by glob pattern (respects .gitignore)",
		"ls":    "List directory contents",
	}

	coding := CreateCodingToolDefinitions(".", &ToolsOptions{})
	readOnly := CreateReadOnlyToolDefinitions(".", &ToolsOptions{})

	seen := map[string]string{} // name -> snippet (union of both sets)
	for _, def := range append(coding, readOnly...) {
		if def == nil {
			continue
		}
		seen[def.Name] = def.PromptSnippet
	}

	for name, snippet := range want {
		got, ok := seen[name]
		if !ok {
			t.Errorf("tool %q not exposed in coding or read-only sets", name)
			continue
		}
		if got != snippet {
			t.Errorf("tool %q PromptSnippet = %q, want reference %q", name, got, snippet)
		}
	}
}
