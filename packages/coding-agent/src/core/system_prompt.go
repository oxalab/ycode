package ycode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================================
// System Prompt Types
// ============================================================================

// ContextFile: Holds a pre-loaded file for inclusion in the system prompt.
type ContextFile struct {
	Path 	string
	Content string
}

// BuildSystemPromptOptions: Configures system prompt construction.
type BuildSystemPromptOptions struct {
	// Custom system prompt (replaces default).
	CustomPrompt string
	// Tools to include in prompt. Default [read, bash, edit, write]
	SelectedTools []string
	// Optional one-line tool snippets keyed by tool name.
	ToolSnippets map[string]string
	// Additional guidelines bullets appended to the default guidelines.
	PromptGuidelines []string
	// Text to append to system prompt.
	AppendSystemPrompt string
	// Working directory.
	Cwd string
	// Pre-loaded context files.
	ContextFiles []ContextFile
	// Pre-loaded skills.
	Skills []Skill
}

// ============================================================================
// Path Helpers
// ============================================================================

/* getReadmePath: returns the path to the examples directory.
TODO: Consider moving to configuration.go alongside GetDocsPath. */
func getReadmePath() string {
	exe, _ := filepath.Abs(os.Args[0])
	if exe != "" {
		return filepath.Join(filepath.Dir(exe), "README.md")
	}
	return filepath.Join(GetAgentDir(), "README.md")
}

/* getExamplesPath: Returns the path to the examples directory.
TODO: Consider moving to configuration.go alongside GetDocsPath. */
func getExamplesPath() string {
	exe, _ := filepath.Abs(os.Args[0])
	if exe != "" {
		return filepath.Join(filepath.Dir(exe), "examples")
	}
	return filepath.Join(GetAgentDir(), "examples")
}

// ============================================================================
// System Prompt Builder
// ============================================================================


// buildSystemPrompt: Constructs the system prompt with toold, guidelines and context.
func BuildSystemPrompt(options BuildSystemPromptOptions) string {
	customPrompt := options.CustomPrompt
	selectedTools := options.SelectedTools
	toolSnippets := options.ToolSnippets
	promptGuidelines := options.PromptGuidelines
	appendSystemPrompt := options.AppendSystemPrompt
	cwd := options.Cwd
	contextFiles := options.ContextFiles
	skills := options.Skills

	promptCwd := strings.ReplaceAll(cwd, "\\", "/")

	now := time.Now()
	date := now.Format("2006-01-02")

	appendSection := ""
	if appendSystemPrompt != "" {
		appendSection = "\n\n" + appendSystemPrompt
	}

	// --- Custom Prompt Path ---
	if customPrompt != "" {
		prompt := customPrompt
		if appendSection != "" {
			prompt += appendSection
		}
		// Append project context files
		if len(contextFiles) > 0 {
			prompt += "\n\n# Project Context\n\n"
			prompt += "Project-specific instructions and guidelines:\n\n"
			for _, cf := range contextFiles {
				prompt += fmt.Sprintf("## %s\n\n%s\n\n", cf.Path, cf.Content)
			}
		}

		// Append skills section (only if tool is available)
		customPromptHasRead := len(selectedTools) == 0 || containsString(selectedTools, "read")
		if customPromptHasRead && len(skills) > 0 {
			prompt += FormatSkillForPrompt(skills)
		}

		// Add date and working directory last
		prompt += fmt.Sprintf("\nCurrent date: %s", date)
		prompt += fmt.Sprintf("\nCurrent working directory: %s", promptCwd)

		return prompt
	}

	// ---- Default prompt path ----

	// Get absolute paths to documentation and examples
	readmePath := getReadmePath()
	docsPath := GetDocsPath()
	examplesPath := getExamplesPath()

	// Build tools list based on selected tools.
	// A tool appears in available tools only when the called provides a one-line snippet.
	tools := selectedTools
	if len(tools) == 0 {
		tools = []string{"read", "bash", "edit", "write"}
	}
	var visibleTools []string 
	for _, name := range tools {
		if snippet, ok := toolSnippets[name]; ok && snippet != "" {
			visibleTools = append(visibleTools, name)
		}
	}

	var toolsList string
	if len(visibleTools) > 0 {
		var parts []string
		for _, name := range visibleTools {
			parts = append(parts, fmt.Sprintf("- %s: %s", name, toolSnippets[name]))
		}
		toolsList = strings.Join(parts, "\n")
	} else {
		toolsList = "(none)"
	}

	// Build guidelines base on which tools are actually available
	guidelinesSet := make(map[string]bool)
	var guidelinesList []string
	addGuideline := func(guideline string) {
		if guidelinesSet[guideline] {
			return
		}
		guidelinesSet[guideline] = true
		guidelinesList = append(guidelinesList, guideline)
	}

	hasBash := containsString(tools, "bash")
	hasGrep := containsString(tools, "grep")
	hasFind := containsString(tools, "find")
	hasLs := containsString(tools, "ls")
	hasRead := containsString(tools, "read")

	// File exploration guidelines
	if hasBash && !hasGrep && !hasFind && !hasLs {
		addGuideline("Use bash for file operations like ls, rg, find")
	} else if hasBash && (hasGrep || hasFind || hasLs) {
		addGuideline("Prefer grep/find/ls tools over bash for file exploration (faster, respects .gitignore)")
	}

	for _, guideline := range promptGuidelines {
		normalized := strings.TrimSpace(guideline)
		if normalized != "" {
			addGuideline(normalized)
		}
	}

	// Always include these
	addGuideline("Be concise in your responses")
	addGuideline("Show file paths clearly when working with files")

	var guidelineParts []string
	for _, g := range guidelinesList {
		guidelineParts = append(guidelineParts, fmt.Sprintf("- %s", g))
	}
	guidelines := strings.Join(guidelineParts, "\n")

	prompt := fmt.Sprintf(`You are an expert coding assistant operating inside ycode, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.

Available tools:
%s

In addition to the tools above, you may have access to other custom tools depending on the project.

Guidelines:
%s

Ycode documentation (read only when the user asks about ycode itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: %s
- Additional docs: %s
- Examples: %s (extensions, custom tools, SDK)
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md)
- When working on ycode topics, read the docs and examples, and follow .md cross-references before implementing
- Always read ycode .md files completely and follow links to related docs (e.g., tui.md for TUI API details)`,
		toolsList, guidelines, readmePath, docsPath, examplesPath)

	if appendSection != "" {
		prompt += appendSection
	}

	// Append project context files
	if len(contextFiles) > 0 {
		prompt += "\n\n# Project Context\n\n"
		prompt += "Project-specific instructions and guidelines:\n\n"
		for _, cf := range contextFiles {
			prompt += fmt.Sprintf("## %s\n\n%s\n\n", cf.Path, cf.Content)
		}
	}

	// Append skills section (only if read tool is available)
	if hasRead && len(skills) > 0 {
		prompt += FormatSkillForPrompt(skills)
	}

	// Add date and working directory last
	prompt += fmt.Sprintf("\nCurrent date: %s", date)
	prompt += fmt.Sprintf("\nCurrent working directory: %s", promptCwd)

	return prompt
}

// ============================================================================
// Helpers
// ============================================================================

// containsString: Checks if a string slice contains a given string.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}


/* KEYNOTES:
 1. FormatSkillForPrompt (singular) from skills.go:375 — note the Go version uses 
 singular "Skill" not "Skills". Make sure you match the exact function name.

 2. getReadmePath / getExamplesPath — defined locally since they don't exist in 
 configuration.go yet. Marked with TODO to move there alongside GetDocsPath.

 3. containsString helper — replaces TS Array.includes(). Since Go 1.21 has 
 slices.Contains, but for broader compatibility and to avoid an import, 
 I defined a simple helper.

 4. time.Now().Format("2006-01-02") — Go's reference date format for YYYY-MM-DD. 
 Equivalent to the TS getFullYear() + padStart approach.

 5. Two prompt paths preserved exactly: custom prompt (replaces everything) and 
 default prompt (with tools, guidelines, docs paths). Both append context files, skills, 
 date, and CWD at the end.

 6. ContextFile struct replaces the TS inline type { path: string; content: string }[].
*/