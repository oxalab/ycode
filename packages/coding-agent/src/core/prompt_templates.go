package ycode

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type PromptTemplate struct {
	Name         string
	Description  string
	ArgumentHint string
	Content      string
	SourceInfo   SourceInfo
	FilePath     string
}

func ParseCommandArgs(argsString string) []string {
	var args []string
	var current strings.Builder
	inQuote := rune(0)

	for _, char := range argsString {
		if inQuote != 0 {
			if char == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(char)
			}
		} else if char == '"' || char == '\'' {
			inQuote = char
		} else if char == ' ' || char == '\t' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func SubstituteArgs(content string, args []string) string {
	result := content
	// Replace $1, $2, etc with position args
	positionalRe := regexp.MustCompile(`\$(\d+)`)
	result = positionalRe.ReplaceAllStringFunc(result, func(match string) string {
		numStr := match[1:]
		num := 0
		for _, c := range numStr {
			num = num*10 + int(c-'0')
		}
		idx := num - 1
		for idx >= 0 && idx < len(args) {
			return args[idx]
		}
		return ""
	})
	
	// Replace ${@:start} or ${a:start:length}
	sliceRe := regexp.MustCompile(`\$\{@:(\d+)(?::(\d+))?\}`)
	result = sliceRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := sliceRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return ""
		}
		start := 0
		for _, c := range sub[1] {
			start = start*10 + int(c-'0')
		}
		start--  //Convert to 0-indexed
		if start < 0 {
			start = 0
		}
		if len(sub) >= 3 && sub[2] != "" {
			length := 0
			for _, c := range sub[2] {
				length = length*10 + int(c-'0')
			}
			end := start + length
			if end > len(args) {
				end = len(args)
			}
			if start >= len(args) {
				return ""
			}
			return strings.Join(args[start:end], " ")
		}
		if start >= len(args) {
			return ""
		}
		return strings.Join(args[start:], " ")
	})

	allArgs := strings.Join(args, " ")

	// Replace $ARGUMENTS
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)

	// Replace $@
	result = strings.ReplaceAll(result, "$@", allArgs)

	return result
}

func loadTemplateFromFile(filePath string, sourceInfo SourceInfo) *PromptTemplate {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	rawContent := string(data)
	frontmatter, body := ParseFrontmatter(rawContent)

	name := strings.TrimSuffix(filepath.Base(filePath), ".md")

	description, _ := frontmatter["description"].(string)
	if description == "" {
		lines := strings.Split(body, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				description = trimmed
				if len(description) > 60 {
					description = description[:60] + "..."
				}
				break
			}
		}
	}

	argumentHint, _ := frontmatter["argument-hint"].(string)
	return &PromptTemplate{
		Name: 			name,
		Description:	description,
		ArgumentHint:	argumentHint,
		Content: 		body,
		SourceInfo: 	sourceInfo,
		FilePath: 		filePath,
	}
}

func loadTemplatesFromDir(dir string, getSourceInfo func(string) SourceInfo) []PromptTemplate {
	var templates []PromptTemplate
	
	entries, err := os.ReadDir(dir)
	if err != nil {
		return templates
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil || stat.IsDir() {
				continue
			}
		}

		sourceInfo := getSourceInfo(fullPath)
		if template := loadTemplateFromFile(fullPath, sourceInfo); template != nil {
			templates = append(templates, *template)
		}
	}
	return templates
}

type LoadPromptTemplateOptions struct {
	Cwd 		string
	AgentDir 	string
	PromptPaths []string
	IncludeDefaults bool
}

func LoadPromptTemplates(options LoadPromptTemplateOptions) []PromptTemplate {
	var templates []PromptTemplate

	globalPromptsDir := filepath.Join(options.AgentDir, "prompts")
	projectPromptsDir := filepath.Join(options.Cwd, ".ycode", "prompts")

	getSourceInfo := func(resolvedPath string) SourceInfo {
		if isUnderPath(resolvedPath, globalPromptsDir) {
			return CreateSyntheticSourceInfo(resolvedPath, "local", ScopeUser, "", globalPromptsDir)
		}
		if isUnderPath(resolvedPath, projectPromptsDir) {
			return CreateSyntheticSourceInfo(resolvedPath, "local", ScopeProject, "", projectPromptsDir)
		}
		stat, err := os.Stat(resolvedPath)
		baseDir := ""
		if err == nil {
			if stat.IsDir() {
				baseDir = resolvedPath
			} else {
				baseDir = filepath.Dir(resolvedPath)
			}
		}
		return CreateSyntheticSourceInfo(resolvedPath, "local", ScopeTemporary, "", baseDir)
	}
	if options.IncludeDefaults {
		templates = append(templates, loadTemplatesFromDir(globalPromptsDir, getSourceInfo)...)
		templates = append(templates, loadTemplatesFromDir(projectPromptsDir, getSourceInfo)...)
	}

	for _, rawPath := range options.PromptPaths {
		resolvedPath := resolvePromptPath(rawPath, options.Cwd)
		if _, err := os.Stat(resolvedPath); err != nil {
			continue
		}

		stat, err := os.Stat(resolvedPath)
		if err != nil {
			continue
		}
		if stat.IsDir() {
			templates = append(templates, loadTemplatesFromDir(resolvedPath, getSourceInfo)...)
		} else if strings.HasSuffix(resolvedPath, ".md") {
			sourceInfo := getSourceInfo(resolvedPath)
			if template := loadTemplateFromFile(resolvedPath, sourceInfo); template != nil {
				templates = append(templates, *template)
			}
		}
	}
	return templates
}

func ExpandPromptTemplate(text string, templates []PromptTemplate) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}

	spaceIndex := strings.Index(text, " ")
	var templateName, argsString string
	if spaceIndex == -1 {
		templateName = text[1:]
		argsString = ""
	} else {
		templateName = text[1:spaceIndex]
		argsString = text[spaceIndex+1:]
	}
	for _, t := range templates {
		if t.Name == templateName {
			args := ParseCommandArgs(argsString)
			return SubstituteArgs(t.Content, args)
		}
	}
	return text
}

func isUnderPath(target, root string) bool {
	absRoot, _ := filepath.Abs(root)
	if target == absRoot {
		return true
	}
	if !strings.HasSuffix(absRoot, string(filepath.Separator)) {
		absRoot += string(filepath.Separator)
	}
	return strings.HasPrefix(target, absRoot)
}

func resolvePromptPath(p, cwd string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, trimmed[2:])
	}
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Join(cwd, trimmed)
}

// ParseFrontmatter extracts YAML frontmatter from markdown content.
func ParseFrontmatter(content string) (map[string]any, string) {
	if !strings.HasPrefix(content, "---") {
		return map[string]any{}, content
	}
	end := strings.Index(content[3:], "---")
	if end == -1 {
		return map[string]any{}, content
	}
	fmText := strings.TrimSpace(content[3 : end+3])
	body := strings.TrimSpace(content[end+6:])

	fm := make(map[string]any)
	lines := strings.Split(fmText, "\n")
	for _, line := range lines {
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		fm[key] = val
	}
	return fm, body
}