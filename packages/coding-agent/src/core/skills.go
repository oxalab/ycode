package ycode

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxNameLength = 64
const maxDescriptionLength = 1024

type ResourceDiagnostic struct {
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Path      string         `json:"path,omitempty"`
	Collision *CollisionInfo `json:"collision,omitempty"`
}

type CollisionInfo struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
}

type SkillFrontmatter struct {
	Name                   string
	Description            string
	DisableModelInvocation bool
}

type Skill struct {
	Name                   string
	Description            string
	FilePath               string
	BaseDir                string
	SourceInfo             SourceInfo
	DisableModelInvocation bool
}

type LoadSkillsResult struct {
	Skills      []Skill
	Diagnostics []ResourceDiagnostic
}

var validNameRe = regexp.MustCompile(`^[a-z0-9]+$`)

func validateSkillName(name, parentDirName string) []string {
	var errors []string

	if name != parentDirName {
		errors = append(errors, fmt.Sprintf("name %q does not match parent directory %q", name, parentDirName))
	}
	if len(name) > maxNameLength {
		errors = append(errors, fmt.Sprintf("name exceeds %d characters (%d)", maxNameLength, len(name)))
	}
	if !validNameRe.MatchString(name) {
		errors = append(errors, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errors = append(errors, "name must not start or end with a hypen")
	}
	if strings.Contains(name, "--") {
		errors = append(errors, "name must not contain consecutive hyphens")
	}
	return errors
}

func validateSkillDescription(description string) []string {
	var errors []string

	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		errors = append(errors, "description is required")
	} else if len(description) > maxDescriptionLength {
		errors = append(errors, fmt.Sprintf("description exceeds %d characters (%d)", maxDescriptionLength, len(description)))
	}
	return errors
}

func createSkillSourceInfo(filePath, baseDir, source string) SourceInfo {
	switch source {
	case "user":
		return CreateSyntheticSourceInfo(filePath, "local", ScopeUser, "", baseDir)
	case "project":
		return CreateSyntheticSourceInfo(filePath, "local", ScopeProject, "", baseDir)
	case "path":
		return CreateSyntheticSourceInfo(filePath, "local", ScopeTemporary, "", baseDir)
	default:
		return CreateSyntheticSourceInfo(filePath, source, ScopeTemporary, "", baseDir)
	}
}

func loadSkillFromFile(filePath, source string) loadSkillResult {
	var diagnostics []ResourceDiagnostic

	data, err := os.ReadFile(filePath)
	if err != nil {
		msg := err.Error()
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: msg, Path: filePath})
		return loadSkillResult{Skill: nil, Diagnostics: diagnostics}
	}

	rawContent := string(data)
	fm, _ := ParseFrontmatter(rawContent)

	skillDir := filepath.Dir(filePath)
	parentDirName := filepath.Base(skillDir)

	// Extract frontmatter fields
	description, _ := fm["description"].(string)
	name, _ := fm["name"].(string)
	disableInv := false

	if val, ok := fm["disable-model-invocation"].(string); ok && val == "true" {
		disableInv = true
	}
	// Validate description
	descErrors := validateSkillDescription(description)
	for _, e := range descErrors {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: e, Path: filePath})
	}

	// Use name from frontmatter, or fall back to parent directory name
	if name == "" {
		name = parentDirName
	}

	// Validate name
	nameErrors := validateSkillName(name, parentDirName)
	for _, e := range nameErrors {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: e, Path: filePath})
	}

	// Dont load if description is missing
	if strings.TrimSpace(description) == "" {
		return loadSkillResult{Skill: nil, Diagnostics: diagnostics}
	}

	return loadSkillResult{
		Skill: &Skill{
			Name: 				name,
			Description: 		description,
			FilePath: 			filePath,
			BaseDir: 			skillDir,
			SourceInfo: 		createSkillSourceInfo(filePath, skillDir, source),
			DisableModelInvocation: disableInv,
		},
		Diagnostics: diagnostics,
	}
}

type loadSkillResult struct {
	Skill *Skill
	Diagnostics []ResourceDiagnostic
}


/*
loadSkillFromDirInterna: Discovers skills from a directory
Discovery Rules:
 • if a directory contains SKILL.md, treat it as a skill root and do not recurse further
 • otherwise, load direct .md children in the root
 • recurse into subdirectories to find SKILL.md 
*/
func loadSkillsFromDirInternal(dir, source string, includeRootFiles bool) LoadSkillsResult {
	var skills []Skill
	var diagnostics []ResourceDiagnostic

	entries, err := os.ReadDir(dir)
	if err != nil {
		return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
	}

	// First Pass: look for SKILL.md in this directory
	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
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

		result := loadSkillFromFile(fullPath, source)
		if result.Skill != nil {
			skills = append(skills, *result.Skill)
		}
		diagnostics = append(diagnostics, result.Diagnostics...)
		return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
	}

	// Second Pass: Recurse into subdirectories and load root .md files
	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden files/dirs
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Skip node_modules
		if name == "node_modules" {
			continue
		}
		fullPath := filepath.Join(dir, name)

		isDir := entry.IsDir()
		isFile := !entry.IsDir()
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stat.IsDir()
			isFile = !stat.IsDir()
		}
		if isDir {
			subResult := loadSkillsFromDirInternal(fullPath, source, false)
			skills = append(skills, subResult.Skills...)
			diagnostics = append(diagnostics, subResult.Diagnostics...)
			continue
		}

		if !isFile || !includeRootFiles || !strings.HasSuffix(name, ".md") {
			continue
		}
		result := loadSkillFromFile(fullPath, source)
		if result.Skill != nil {
			skills = append(skills, *result.Skill)
		}
		diagnostics = append(diagnostics, result.Diagnostics...)
	}
	return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
}

// LoadSkillFromDir: Loads skills from a directory (public entry point).
func LoadSkillsFromDir(dir, source string) LoadSkillsResult {
	return loadSkillsFromDirInternal(dir, source, true)
}

type LoadSkillsOptions struct {
	Cwd 		string
	AgentDir 	string
	SkillPaths  []string
	IncludeDefaults bool
}

// LoadSkills: Loads skills from all configured locations.
// Returns skills and any validation diagnostics

func LoadSkills(options LoadSkillsOptions) LoadSkillsResult {
	resolvedAgentDir := options.AgentDir
	if resolvedAgentDir == "" {
		resolvedAgentDir = GetAgentDir()
	}

	skillMap := make(map[string]*Skill)
	realPathSet := make(map[string]bool)
	var allDiagnostics []ResourceDiagnostic
	var collisionDiagnostics []ResourceDiagnostic

	addSkills := func(result LoadSkillsResult) {
		allDiagnostics = append(allDiagnostics, result.Diagnostics...)
		for i := range result.Skills {
			skill := &result.Skills[i]

			// Resolve symlinks to detect duplicate files
			realPath := canonicalizePath(skill.FilePath)

			// Skip silently if we've already loaded this exact file (via symlink)
			if realPathSet[realPath] {
				continue
			}

			existing, exists := skillMap[skill.Name]
			if exists {
				collisionDiagnostics = append(collisionDiagnostics, ResourceDiagnostic{
					Type: 	"collision",
					Message: fmt.Sprintf("name %q collision", skill.Name),
					Path: 	 skill.FilePath,
					Collision: &CollisionInfo{
						ResourceType: "skill",
						Name: 		  skill.Name,
						WinnerPath:   existing.FilePath,
						LoserPath:    skill.FilePath,
					},
				})
			} else {
				skillMap[skill.Name] = skill
				realPathSet[realPath] = true
			}
		}
	}

	userSkillsDir := filepath.Join(resolvedAgentDir, "skills")
	projectSkillsDir := filepath.Join(options.Cwd, ConfigDirName, "skills")

	if options.IncludeDefaults {
		addSkills(loadSkillsFromDirInternal(userSkillsDir, "user", true))
		addSkills(loadSkillsFromDirInternal(projectSkillsDir, "project", true))
	}

	getSource := func(resolvedPath string) string {
		if !options.IncludeDefaults {
			if isUnderPath(resolvedPath, userSkillsDir) {
				return "user"
			}
			if isUnderPath(resolvedPath, projectSkillsDir) {
				return "project"
			}
		}
		return "path"
	}

	for _, rawPath := range options.SkillPaths {
		resolvedPath := resolveSkillPath(rawPath, options.Cwd)
		if _, err := os.Stat(resolvedPath); err != nil {
			allDiagnostics = append(allDiagnostics, ResourceDiagnostic{
				Type: "warning", Message: "skill path does not exist", Path: resolvedPath,
			})
			continue
		}

		stat, err := os.Stat(resolvedPath)
		if err != nil {
			allDiagnostics = append(allDiagnostics, ResourceDiagnostic{
				Type: "warning", Message: err.Error(), Path: resolvedPath,
			})
			continue
		}

		source := getSource(resolvedPath)
		if stat.IsDir() {
			addSkills(loadSkillsFromDirInternal(resolvedPath, source, true))
		} else if strings.HasSuffix(resolvedPath, ".md") {
			result := loadSkillFromFile(resolvedPath, source)
			if result.Skill != nil {
				addSkills(LoadSkillsResult{Skills: []Skill{*result.Skill}, Diagnostics: result.Diagnostics})
			} else {
				allDiagnostics = append(allDiagnostics, result.Diagnostics...)
			}
		} else {
			allDiagnostics = append(allDiagnostics, ResourceDiagnostic{
				Type: "warning", Message: "skll path is not a markdown file", Path: resolvedPath,
			})
		}
	}

	// Collect skills from map
	var skills []Skill
	for _, skill := range skillMap {
		skills = append(skills, *skill)
	}

	allDiagnostics = append(allDiagnostics, collisionDiagnostics...)
	return LoadSkillsResult{Skills: skills, Diagnostics: allDiagnostics}
}

// FormatSkillsForPrompt: Formats skills for inclusion in a system prompt using XML format.
func FormatSkillForPrompt(skills []Skill) string {
	var visible []Skill
	for _, s := range skills {
		if !s.DisableModelInvocation {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines,
			"",
			"",
			"The following skills provide specialized instructions for specific tasks.",
			"Use the read tool to load a skill's file when the task matches its description",
			"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
			"",
			"<available_skills>",
	)

	for _, skill := range visible {
		lines = append(lines,
			"	<skill>",
			fmt.Sprintf("	<name>%s</name>", escapeXml(skill.Name)),
			fmt.Sprintf("	<description>%s</description>", escapeXml(skill.Description)),
			fmt.Sprintf("	<location>%s</location>", escapeXml(skill.FilePath)),
			"  </skill>",
		)
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func escapeXml(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, ".", "&apos;")
	return s
}

func canonicalizePath(path string) string {
	eval, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(eval)
	if err != nil {
		return eval
	}
	return abs
}

func resolveSkillPath(p, cwd string) string {
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