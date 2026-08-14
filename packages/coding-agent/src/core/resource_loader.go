package ycode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ycode/packages/coding-agent/src/core/extensions"
)

// ============================================================================
// Types
// ============================================================================



// ResourceExtensionPaths holds paths contributed by extensions.
type ResourceExtensionPaths struct {
	SkillPaths []PathEntry
	PromptPaths []PathEntry
	ThemePaths  []PathEntry
}

// PathEntry pairs a path with its metadata.
type PathEntry struct {
	Path     string
	Metadata PathMetadata
}

// ResourceLoader is the interface for loading all session resources.
type ResourceLoader interface {
	GetExtensions() *extensions.LoadExtensionsResult
	GetSkills() *LoadSkillsResult
	GetPrompts() *PromptLoadResult
	GetThemes() *ThemeLoadResult
	GetAgentsFiles() *AgentsFilesResult
	GetSystemPrompt() string
	GetAppendSystemPrompt() []string
	ExtendResources(paths ResourceExtensionPaths)
	Reload() error
}

// PromptLoadResult holds loaded prompts with diagnostics.
type PromptLoadResult struct {
	Prompts     []PromptTemplate
	Diagnostics []ResourceDiagnostic
}

// ThemeLoadResult holds loaded themes with diagnostics.
// Minimal stub until TUI theme system is ported.
type ThemeLoadResult struct {
	Themes      []Theme
	Diagnostics []ResourceDiagnostic
}

// Theme is a minimal stub for theme support. Full type is in TUI theme package.
type Theme struct {
	Name       string
	SourcePath string
}

// AgentsFilesResult holds loaded context files.
type AgentsFilesResult struct {
	AgentsFiles []ContextFile
}

// ============================================================================
// PackageManager Interface (stub until package-manager.go is ported)
// ============================================================================

// ResolvedPathEntry represents a discovered resource path with metadata.
type ResolvedPathEntry struct {
	Path     string
	Enabled  bool
	Metadata PathMetadata
}

// ResolvedPaths holds all resolved resource paths from the package manager.
type ResolvedPaths struct {
	Extensions []ResolvedPathEntry
	Skills     []ResolvedPathEntry
	Prompts    []ResolvedPathEntry
	Themes     []ResolvedPathEntry
}

// PackageManager resolves resource paths from packages and config.
type PackageManager interface {
	Resolve() *ResolvedPaths
	ResolveExtensionSources(paths []string, temporary bool) *ResolvedPaths
}

// ============================================================================
// DefaultResourceLoaderOptions
// ============================================================================

// DefaultResourceLoaderOptions configures resource loading.
type DefaultResourceLoaderOptions struct {
	Cwd                        string
	AgentDir                   string
	SettingsManager            *SettingsManager
	EventBus                   EventBus
	AdditionalExtensionPaths   []string
	AdditionalSkillPaths       []string
	AdditionalPromptPaths      []string
	AdditionalThemePaths       []string
	ExtensionFactories         []extensions.ExtensionFactory
	NoExtensions               bool
	NoSkills                   bool
	NoPromptTemplates          bool
	NoThemes                   bool
	NoContextFiles             bool
	SystemPrompt               string
	AppendSystemPrompt         []string
	ExtensionsOverride         func(base *extensions.LoadExtensionsResult) *extensions.LoadExtensionsResult
	SkillsOverride             func(base *LoadSkillsResult) *LoadSkillsResult
	PromptsOverride            func(base *PromptLoadResult) *PromptLoadResult
	ThemesOverride             func(base *ThemeLoadResult) *ThemeLoadResult
	AgentsFilesOverride        func(base *AgentsFilesResult) *AgentsFilesResult
	SystemPromptOverride       func(base string) string
	AppendSystemPromptOverride func(base []string) []string
	PackageManager             PackageManager
}

// ============================================================================
// DefaultResourceLoader
// ============================================================================

// DefaultResourceLoader discovers and loads all session resources:
// extensions, skills, prompts, themes, context files, and system prompts.
type DefaultResourceLoader struct {
	cwd                        string
	agentDir                   string
	settingsManager            *SettingsManager
	eventBus                   EventBus
	packageManager             PackageManager
	additionalExtensionPaths   []string
	additionalSkillPaths       []string
	additionalPromptPaths      []string
	additionalThemePaths       []string
	extensionFactories         []extensions.ExtensionFactory
	noExtensions               bool
	noSkills                   bool
	noPromptTemplates          bool
	noThemes                   bool
	noContextFiles             bool
	systemPromptSource         string
	appendSystemPromptSource   []string
	extensionsOverride         func(base *extensions.LoadExtensionsResult) *extensions.LoadExtensionsResult
	skillsOverride             func(base *LoadSkillsResult) *LoadSkillsResult
	promptsOverride            func(base *PromptLoadResult) *PromptLoadResult
	themesOverride             func(base *ThemeLoadResult) *ThemeLoadResult
	agentsFilesOverride        func(base *AgentsFilesResult) *AgentsFilesResult
	systemPromptOverride       func(base string) string
	appendSystemPromptOverride func(base []string) []string

	// Loaded state
	extensionsResult          *extensions.LoadExtensionsResult
	skillsResult              *LoadSkillsResult
	promptsResult             *PromptLoadResult
	themesResult              *ThemeLoadResult
	agentsFilesResult         *AgentsFilesResult
	systemPrompt              string
	appendSystemPrompt        []string

	// Path tracking for extension resources
	lastSkillPaths            []string
	lastPromptPaths           []string
	lastThemePaths            []string
	extensionSkillSourceInfos map[string]SourceInfo
	extensionPromptSourceInfos map[string]SourceInfo
	extensionThemeSourceInfos map[string]SourceInfo
}

// NewDefaultResourceLoader creates a new resource loader with the given options.
func NewDefaultResourceLoader(options DefaultResourceLoaderOptions) *DefaultResourceLoader {
	sm := options.SettingsManager
	if sm == nil {
		sm = NewSettingsManager(NewFileSettingsStorage(options.Cwd, options.AgentDir))
	}

	eb := options.EventBus
	if eb == nil {
		eb = CreateEventBus()
	}

	return &DefaultResourceLoader{
		cwd:                       options.Cwd,
		agentDir:                  options.AgentDir,
		settingsManager:           sm,
		eventBus:                  eb,
		packageManager:            options.PackageManager,
		additionalExtensionPaths:  options.AdditionalExtensionPaths,
		additionalSkillPaths:      options.AdditionalSkillPaths,
		additionalPromptPaths:     options.AdditionalPromptPaths,
		additionalThemePaths:      options.AdditionalThemePaths,
		extensionFactories:        options.ExtensionFactories,
		noExtensions:              options.NoExtensions,
		noSkills:                  options.NoSkills,
		noPromptTemplates:         options.NoPromptTemplates,
		noThemes:                  options.NoThemes,
		noContextFiles:            options.NoContextFiles,
		systemPromptSource:        options.SystemPrompt,
		appendSystemPromptSource:  options.AppendSystemPrompt,
		extensionsOverride:        options.ExtensionsOverride,
		skillsOverride:            options.SkillsOverride,
		promptsOverride:           options.PromptsOverride,
		themesOverride:            options.ThemesOverride,
		agentsFilesOverride:       options.AgentsFilesOverride,
		systemPromptOverride:      options.SystemPromptOverride,
		appendSystemPromptOverride: options.AppendSystemPromptOverride,
		extensionsResult: &extensions.LoadExtensionsResult{
			Runtime: extensions.CreateExtensionRuntime(),
		},
		skillsResult:              &LoadSkillsResult{},
		promptsResult:             &PromptLoadResult{},
		themesResult:              &ThemeLoadResult{},
		agentsFilesResult:         &AgentsFilesResult{},
		extensionSkillSourceInfos: make(map[string]SourceInfo),
		extensionPromptSourceInfos: make(map[string]SourceInfo),
		extensionThemeSourceInfos: make(map[string]SourceInfo),
	}
}

// ============================================================================
// Getter Methods
// ============================================================================

func (l *DefaultResourceLoader) GetExtensions() *extensions.LoadExtensionsResult {
	return l.extensionsResult
}

func (l *DefaultResourceLoader) GetSkills() *LoadSkillsResult {
	return l.skillsResult
}

func (l *DefaultResourceLoader) GetPrompts() *PromptLoadResult {
	return l.promptsResult
}

func (l *DefaultResourceLoader) GetThemes() *ThemeLoadResult {
	return l.themesResult
}

func (l *DefaultResourceLoader) GetAgentsFiles() *AgentsFilesResult {
	return l.agentsFilesResult
}

func (l *DefaultResourceLoader) GetSystemPrompt() string {
	return l.systemPrompt
}

func (l *DefaultResourceLoader) GetAppendSystemPrompt() []string {
	return l.appendSystemPrompt
}

// ============================================================================
// ExtendResources
// ============================================================================

func (l *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) {
	skillPaths := l.normalizeExtensionPaths(paths.SkillPaths)
	promptPaths := l.normalizeExtensionPaths(paths.PromptPaths)
	themePaths := l.normalizeExtensionPaths(paths.ThemePaths)

	for _, entry := range skillPaths {
		l.extensionSkillSourceInfos[entry.Path] = CreateSourceInfo(entry.Path, entry.Metadata)
	}
	for _, entry := range promptPaths {
		l.extensionPromptSourceInfos[entry.Path] = CreateSourceInfo(entry.Path, entry.Metadata)
	}
	for _, entry := range themePaths {
		l.extensionThemeSourceInfos[entry.Path] = CreateSourceInfo(entry.Path, entry.Metadata)
	}

	if len(skillPaths) > 0 {
		l.lastSkillPaths = l.mergePaths(l.lastSkillPaths, pathsOf(skillPaths))
		l.updateSkillsFromPaths(l.lastSkillPaths, nil)
	}

	if len(promptPaths) > 0 {
		l.lastPromptPaths = l.mergePaths(l.lastPromptPaths, pathsOf(promptPaths))
		l.updatePromptsFromPaths(l.lastPromptPaths, nil)
	}

	if len(themePaths) > 0 {
		l.lastThemePaths = l.mergePaths(l.lastThemePaths, pathsOf(themePaths))
		l.updateThemesFromPaths(l.lastThemePaths, nil)
	}
}

// ============================================================================
// Reload
// ============================================================================

func (l *DefaultResourceLoader) Reload() error {
	// Reload settings
	l.settingsManager.Reload()

	// Resolve paths from package manager
	var resolvedPaths *ResolvedPaths
	if l.packageManager != nil {
		resolvedPaths = l.packageManager.Resolve()
	} else {
		resolvedPaths = &ResolvedPaths{}
	}

	cliExtensionPaths := &ResolvedPaths{}
	if l.packageManager != nil {
		cliExtensionPaths = l.packageManager.ResolveExtensionSources(l.additionalExtensionPaths, true)
	}

	metadataByPath := make(map[string]PathMetadata)
	l.extensionSkillSourceInfos = make(map[string]SourceInfo)
	l.extensionPromptSourceInfos = make(map[string]SourceInfo)
	l.extensionThemeSourceInfos = make(map[string]SourceInfo)

	// Helpers to extract enabled paths and store metadata
	getEnabledResources := func(resources []ResolvedPathEntry) []ResolvedPathEntry {
		for _, r := range resources {
			if _, ok := metadataByPath[r.Path]; !ok {
				metadataByPath[r.Path] = r.Metadata
			}
		}
		var enabled []ResolvedPathEntry
		for _, r := range resources {
			if r.Enabled {
				enabled = append(enabled, r)
			}
		}
		return enabled
	}

	getEnabledPaths := func(resources []ResolvedPathEntry) []string {
		enabled := getEnabledResources(resources)
		paths := make([]string, len(enabled))
		for i, r := range enabled {
			paths[i] = r.Path
		}
		return paths
	}

	enabledExtensions := getEnabledPaths(resolvedPaths.Extensions)
	enabledSkillResources := getEnabledResources(resolvedPaths.Skills)
	enabledPrompts := getEnabledPaths(resolvedPaths.Prompts)
	enabledThemes := getEnabledPaths(resolvedPaths.Themes)

	// Map skill directories to SKILL.md files when applicable
	enabledSkills := make([]string, len(enabledSkillResources))
	for i, r := range enabledSkillResources {
		enabledSkills[i] = l.mapSkillPath(r, metadataByPath)
	}

	// Add CLI paths metadata
	for _, r := range cliExtensionPaths.Extensions {
		if _, ok := metadataByPath[r.Path]; !ok {
			metadataByPath[r.Path] = PathMetadata{Source: "cli", Scope: "temporary", Origin: "top-level"}
		}
	}
	for _, r := range cliExtensionPaths.Skills {
		if _, ok := metadataByPath[r.Path]; !ok {
			metadataByPath[r.Path] = PathMetadata{Source: "cli", Scope: "temporary", Origin: "top-level"}
		}
	}

	cliEnabledExtensions := getEnabledPaths(cliExtensionPaths.Extensions)
	cliEnabledSkills := getEnabledPaths(cliExtensionPaths.Skills)
	cliEnabledPrompts := getEnabledPaths(cliExtensionPaths.Prompts)
	cliEnabledThemes := getEnabledPaths(cliExtensionPaths.Themes)

	// ---- Load extensions ----
	var extensionPaths []string
	if l.noExtensions {
		extensionPaths = cliEnabledExtensions
	} else {
		extensionPaths = l.mergePaths(cliEnabledExtensions, enabledExtensions)
	}

	extResult := extensions.LoadExtensions(extensionPaths, l.cwd, l.eventBus)
	inlineResult := l.loadExtensionFactories(extResult.Runtime)
	extResult.Extensions = append(extResult.Extensions, inlineResult.Extensions...)
	extResult.Errors = append(extResult.Errors, inlineResult.Errors...)

	// Detect extension conflicts
	conflicts := l.detectExtensionConflicts(extResult.Extensions)
	for _, conflict := range conflicts {
		extResult.Errors = append(extResult.Errors, extensions.ExtensionLoadError{
			Path:  conflict.Path,
			Error: conflict.Message,
		})
	}

	for _, p := range l.additionalExtensionPaths {
		if isLocalPath(p) {
			if _, err := os.Stat(p); os.IsNotExist(err) {
				extResult.Errors = append(extResult.Errors, extensions.ExtensionLoadError{
					Path: p, Error: fmt.Sprintf("Extension path does not exist: %s", p),
				})
			}
		}
	}

	if l.extensionsOverride != nil {
		l.extensionsResult = l.extensionsOverride(extResult)
	} else {
		l.extensionsResult = extResult
	}
	l.applyExtensionSourceInfo(l.extensionsResult.Extensions, metadataByPath)

	// ---- Load skills ----
	var skillPaths []string
	if l.noSkills {
		skillPaths = l.mergePaths(cliEnabledSkills, l.additionalSkillPaths)
	} else {
		var allSkills []string
		allSkills = append(allSkills, cliEnabledSkills...)
		allSkills = append(allSkills, enabledSkills...)
		skillPaths = l.mergePaths(allSkills, l.additionalSkillPaths)
	}

	l.lastSkillPaths = skillPaths
	l.updateSkillsFromPaths(skillPaths, metadataByPath)

	for _, p := range l.additionalSkillPaths {
		if isLocalPath(p) {
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if !hasDiagnosticForPath(l.skillsResult.Diagnostics, p) {
					l.skillsResult.Diagnostics = append(l.skillsResult.Diagnostics, ResourceDiagnostic{
						Type:    "error",
						Message: "Skill path does not exist",
						Path:    p,
					})
				}
			}
		}
	}

	// ---- Load prompts ----
	var promptPaths []string
	if l.noPromptTemplates {
		promptPaths = l.mergePaths(cliEnabledPrompts, l.additionalPromptPaths)
	} else {
		var allPrompts []string
		allPrompts = append(allPrompts, cliEnabledPrompts...)
		allPrompts = append(allPrompts, enabledPrompts...)
		promptPaths = l.mergePaths(allPrompts, l.additionalPromptPaths)
	}

	l.lastPromptPaths = promptPaths
	l.updatePromptsFromPaths(promptPaths, metadataByPath)

	for _, p := range l.additionalPromptPaths {
		if isLocalPath(p) {
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if !hasDiagnosticForPath(l.promptsResult.Diagnostics, p) {
					l.promptsResult.Diagnostics = append(l.promptsResult.Diagnostics, ResourceDiagnostic{
						Type:    "error",
						Message: "Prompt template path does not exist",
						Path:    p,
					})
				}
			}
		}
	}

	// ---- Load themes ----
	var themePaths []string
	if l.noThemes {
		themePaths = l.mergePaths(cliEnabledThemes, l.additionalThemePaths)
	} else {
		var allThemes []string
		allThemes = append(allThemes, cliEnabledThemes...)
		allThemes = append(allThemes, enabledThemes...)
		themePaths = l.mergePaths(allThemes, l.additionalThemePaths)
	}

	l.lastThemePaths = themePaths
	l.updateThemesFromPaths(themePaths, metadataByPath)

	for _, p := range l.additionalThemePaths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if !hasDiagnosticForPath(l.themesResult.Diagnostics, p) {
				l.themesResult.Diagnostics = append(l.themesResult.Diagnostics, ResourceDiagnostic{
					Type:    "error",
					Message: "Theme path does not exist",
					Path:    p,
				})
			}
		}
	}

	// ---- Load context files ----
	agentsResult := &AgentsFilesResult{}
	if !l.noContextFiles {
		agentsResult.AgentsFiles = loadProjectContextFiles(l.cwd, l.agentDir)
	}
	if l.agentsFilesOverride != nil {
		agentsResult = l.agentsFilesOverride(agentsResult)
	}
	l.agentsFilesResult = agentsResult

	// ---- System prompt ----
	baseSystemPrompt := resolvePromptInput(
		firstNonEmpty(l.systemPromptSource, l.discoverSystemPromptFile()),
		"system prompt",
	)
	if l.systemPromptOverride != nil {
		l.systemPrompt = l.systemPromptOverride(baseSystemPrompt)
	} else {
		l.systemPrompt = baseSystemPrompt
	}

	// ---- Append system prompt ----
	appendSources := l.appendSystemPromptSource
	if len(appendSources) == 0 {
		if discovered := l.discoverAppendSystemPromptFile(); discovered != "" {
			appendSources = []string{discovered}
		}
	}
	var baseAppend []string
	for _, s := range appendSources {
		if resolved := resolvePromptInput(s, "append system prompt"); resolved != "" {
			baseAppend = append(baseAppend, resolved)
		}
	}
	if l.appendSystemPromptOverride != nil {
		l.appendSystemPrompt = l.appendSystemPromptOverride(baseAppend)
	} else {
		l.appendSystemPrompt = baseAppend
	}

	return nil
}

// ============================================================================
// Private: Update Methods
// ============================================================================

func (l *DefaultResourceLoader) updateSkillsFromPaths(skillPaths []string, metadataByPath map[string]PathMetadata) {
	var skillsData LoadSkillsResult
	if l.noSkills && len(skillPaths) == 0 {
		skillsData = LoadSkillsResult{}
	} else {
		skillsData = LoadSkills(LoadSkillsOptions{
        Cwd:            l.cwd,
        AgentDir:       l.agentDir,
        SkillPaths:     skillPaths,
        IncludeDefaults: false,
    	})
	}

	result := &skillsData
	if l.skillsOverride != nil {
		result = l.skillsOverride(result)
	}

	// Attach source info to each skill
	resolved := *result
	for i := range resolved.Skills {
		resolved.Skills[i].SourceInfo = l.findSourceInfoForPath(
			resolved.Skills[i].FilePath, l.extensionSkillSourceInfos, metadataByPath,
		)
		if resolved.Skills[i].SourceInfo == (SourceInfo{}) {
			resolved.Skills[i].SourceInfo = l.getDefaultSourceInfoForPath(resolved.Skills[i].FilePath)
		}
	}
	l.skillsResult = &resolved
}

func (l *DefaultResourceLoader) updatePromptsFromPaths(promptPaths []string, metadataByPath map[string]PathMetadata) {
	var result *PromptLoadResult
	if l.noPromptTemplates && len(promptPaths) == 0 {
		result = &PromptLoadResult{}
	} else {
		loaded := LoadPromptTemplates(LoadPromptTemplateOptions{
			Cwd:            l.cwd,
			AgentDir:       l.agentDir,
			PromptPaths:    promptPaths,
			IncludeDefaults: false,
		})
		result = l.dedupePrompts(loaded)
	}

	if l.promptsOverride != nil {
		result = l.promptsOverride(result)
	}

	for i := range result.Prompts {
		result.Prompts[i].SourceInfo = l.findSourceInfoForPath(
			result.Prompts[i].FilePath, l.extensionPromptSourceInfos, metadataByPath,
		)
		if result.Prompts[i].SourceInfo == (SourceInfo{}) {
			result.Prompts[i].SourceInfo = l.getDefaultSourceInfoForPath(result.Prompts[i].FilePath)
		}
	}
	l.promptsResult = result
}

func (l *DefaultResourceLoader) updateThemesFromPaths(themePaths []string, metadataByPath map[string]PathMetadata) {
	var result *ThemeLoadResult
	if l.noThemes && len(themePaths) == 0 {
		result = &ThemeLoadResult{}
	} else {
		loaded := l.loadThemes(themePaths, false)
		deduped := l.dedupeThemes(loaded.Themes)
		var diags []ResourceDiagnostic
		diags = append(diags, loaded.Diagnostics...)
		diags = append(diags, deduped.Diagnostics...)
		result = &ThemeLoadResult{Themes: deduped.Themes, Diagnostics: diags}
	}

	if l.themesOverride != nil {
		result = l.themesOverride(result)
	}

	for i := range result.Themes {
		if result.Themes[i].SourcePath != "" {
			si := l.findSourceInfoForPath(result.Themes[i].SourcePath, l.extensionThemeSourceInfos, metadataByPath)
			if si == (SourceInfo{}) {
				si = l.getDefaultSourceInfoForPath(result.Themes[i].SourcePath)
			}
			// Theme doesn't have SourceInfo field in stub; store via caller context
			_ = si
		}
	}
	l.themesResult = result
}

// ============================================================================
// Private: Extension Helpers
// ============================================================================

func (l *DefaultResourceLoader) loadExtensionFactories(runtime *extensions.ExtensionRuntime) *extensions.LoadExtensionsResult {
	result := &extensions.LoadExtensionsResult{Runtime: runtime}

	for i, factory := range l.extensionFactories {
		extensionPath := fmt.Sprintf("<inline:%d>", i+1)
		ext, err := extensions.LoadExtensionFromFactory(factory, l.cwd, l.eventBus, runtime)
		if err != nil {
			result.Errors = append(result.Errors, extensions.ExtensionLoadError{
				Path: extensionPath, Error: err.Error(),
			})
		} else {
			result.Extensions = append(result.Extensions, ext)
		}
	}

	return result
}

func (l *DefaultResourceLoader) applyExtensionSourceInfo(exts []*extensions.Extension, metadataByPath map[string]PathMetadata) {
	for _, ext := range exts {
		si := l.findSourceInfoForPath(ext.Path, nil, metadataByPath)
		if si == (SourceInfo{}) {
			si = l.getDefaultSourceInfoForPath(ext.Path)
		}
		ext.SourceInfo = si
		// Propagate to commands and tools
		for _, cmd := range ext.Commands {
			cmd.SourceInfo = si
		}
			for _, tool := range ext.Tools {
				tool.SourceInfo = si
			}
	}
}

// ============================================================================
// Private: Source Info
// ============================================================================

func (l *DefaultResourceLoader) findSourceInfoForPath(
	resourcePath string,
	extraSourceInfos map[string]SourceInfo,
	metadataByPath map[string]PathMetadata,
) SourceInfo {
	if resourcePath == "" {
		return SourceInfo{}
	}

	// Handle synthetic paths like <inline:1>
	if strings.HasPrefix(resourcePath, "<") {
		return l.getDefaultSourceInfoForPath(resourcePath)
	}

	normalizedResourcePath, _ := filepath.Abs(resourcePath)

	if extraSourceInfos != nil {
		for sourcePath, sourceInfo := range extraSourceInfos {
			normalizedSourcePath, _ := filepath.Abs(sourcePath)
			if normalizedResourcePath == normalizedSourcePath ||
				strings.HasPrefix(normalizedResourcePath, normalizedSourcePath+string(filepath.Separator)) {
				si := sourceInfo
				si.Path = resourcePath
				return si
			}
		}
	}

	if metadataByPath != nil {
		if meta, ok := metadataByPath[normalizedResourcePath]; ok {
			return CreateSourceInfo(resourcePath, meta)
		}
		if meta, ok := metadataByPath[resourcePath]; ok {
			return CreateSourceInfo(resourcePath, meta)
		}

		for sourcePath, meta := range metadataByPath {
			normalizedSourcePath, _ := filepath.Abs(sourcePath)
			if normalizedResourcePath == normalizedSourcePath ||
				strings.HasPrefix(normalizedResourcePath, normalizedSourcePath+string(filepath.Separator)) {
				return CreateSourceInfo(resourcePath, meta)
			}
		}
	}

	return SourceInfo{}
}

func (l *DefaultResourceLoader) getDefaultSourceInfoForPath(filePath string) SourceInfo {
	// Handle synthetic paths like <inline:1>
	if strings.HasPrefix(filePath, "<") && strings.HasSuffix(filePath, ">") {
		inner := filePath[1 : len(filePath)-1]
		parts := strings.SplitN(inner, ":", 2)
		source := "temporary"
		if len(parts) > 0 && parts[0] != "" {
			source = parts[0]
		}
		return SourceInfo{
			Path:   filePath,
			Source: source,
			Scope:  "temporary",
			Origin: "top-level",
		}
	}

	normalizedPath, _ := filepath.Abs(filePath)

	agentRoots := []string{
		filepath.Join(l.agentDir, "skills"),
		filepath.Join(l.agentDir, "prompts"),
		filepath.Join(l.agentDir, "themes"),
		filepath.Join(l.agentDir, "extensions"),
	}
	projectRoots := []string{
		filepath.Join(l.cwd, ConfigDirName, "skills"),
		filepath.Join(l.cwd, ConfigDirName, "prompts"),
		filepath.Join(l.cwd, ConfigDirName, "themes"),
		filepath.Join(l.cwd, ConfigDirName, "extensions"),
	}

	for _, root := range agentRoots {
		if isUnderPath(normalizedPath, root) {
			return SourceInfo{Path: filePath, Source: "local", Scope: "user", Origin: "top-level", BaseDir: root}
		}
	}

	for _, root := range projectRoots {
		if isUnderPath(normalizedPath, root) {
			return SourceInfo{Path: filePath, Source: "local", Scope: "project", Origin: "top-level", BaseDir: root}
		}
	}

	baseDir := normalizedPath
	stat, err := os.Stat(normalizedPath)
	if err == nil && !stat.IsDir() {
		baseDir = filepath.Dir(normalizedPath)
	}

	return SourceInfo{
		Path:    filePath,
		Source:  "local",
		Scope:   "temporary",
		Origin:  "top-level",
		BaseDir: baseDir,
	}
}

// ============================================================================
// Private: Path Helpers
// ============================================================================

func (l *DefaultResourceLoader) normalizeExtensionPaths(entries []PathEntry) []PathEntry {
	result := make([]PathEntry, len(entries))
	for i, entry := range entries {
		result[i] = PathEntry{
			Path:     l.resolveResourcePath(entry.Path),
			Metadata: entry.Metadata,
		}
	}
	return result
}

func (l *DefaultResourceLoader) mergePaths(primary []string, additional []string) []string {
	var merged []string
	seen := make(map[string]bool)

	for _, p := range append(primary, additional...) {
		resolved := l.resolveResourcePath(p)
		canon := canonicalizePath(resolved)
		if seen[canon] {
			continue
		}
		seen[canon] = true
		merged = append(merged, resolved)
	}

	return merged
}

func (l *DefaultResourceLoader) resolveResourcePath(p string) string {
	trimmed := strings.TrimSpace(p)
	expanded := trimmed
	home, _ := os.UserHomeDir()
	if trimmed == "~" {
		expanded = home
	} else if strings.HasPrefix(trimmed, "~/") {
		expanded = filepath.Join(home, trimmed[2:])
	} else if strings.HasPrefix(trimmed, "~") {
		expanded = filepath.Join(home, trimmed[1:])
	}
	abs, _ := filepath.Abs(filepath.Join(l.cwd, expanded))
	return abs
}

func (l *DefaultResourceLoader) mapSkillPath(resource ResolvedPathEntry, metadataByPath map[string]PathMetadata) string {
	if resource.Metadata.Source != "auto" && resource.Metadata.Origin != "package" {
		return resource.Path
	}
	stat, err := os.Stat(resource.Path)
	if err != nil || !stat.IsDir() {
		return resource.Path
	}
	skillFile := filepath.Join(resource.Path, "SKILL.md")
	if _, err := os.Stat(skillFile); err == nil {
		if _, ok := metadataByPath[skillFile]; !ok {
			metadataByPath[skillFile] = resource.Metadata
		}
		return skillFile
	}
	return resource.Path
}

// ============================================================================
// Private: Theme Loading (stub until TUI ported)
// ============================================================================

func (l *DefaultResourceLoader) loadThemes(paths []string, includeDefaults bool) *ThemeLoadResult {
	var themes []Theme
	var diagnostics []ResourceDiagnostic

	if includeDefaults {
		defaultDirs := []string{
			filepath.Join(l.agentDir, "themes"),
			filepath.Join(l.cwd, ConfigDirName, "themes"),
		}
		for _, dir := range defaultDirs {
			l.loadThemesFromDir(dir, &themes, &diagnostics)
		}
	}

	for _, p := range paths {
		resolved, _ := filepath.Abs(p)
		if _, err := os.Stat(resolved); os.IsNotExist(err) {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type: "warning", Message: "theme path does not exist", Path: resolved,
			})
			continue
		}
		stat, err := os.Stat(resolved)
		if err != nil {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type: "warning", Message: err.Error(), Path: resolved,
			})
			continue
		}
		if stat.IsDir() {
			l.loadThemesFromDir(resolved, &themes, &diagnostics)
		} else if strings.HasSuffix(resolved, ".json") {
			l.loadThemeFromFile(resolved, &themes, &diagnostics)
		} else {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type: "warning", Message: "theme path is not a json file", Path: resolved,
			})
		}
	}

	return &ThemeLoadResult{Themes: themes, Diagnostics: diagnostics}
}

func (l *DefaultResourceLoader) loadThemesFromDir(dir string, themes *[]Theme, diagnostics *[]ResourceDiagnostic) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		*diagnostics = append(*diagnostics, ResourceDiagnostic{
			Type: "warning", Message: "failed to read theme directory", Path: dir,
		})
		return
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		l.loadThemeFromFile(filepath.Join(dir, entry.Name()), themes, diagnostics)
	}
}

func (l *DefaultResourceLoader) loadThemeFromFile(filePath string, themes *[]Theme, diagnostics *[]ResourceDiagnostic) {
	// TODO: Replace with actual theme loading when TUI theme system is ported.
	// For now, record the file as a theme with a name derived from the filename.
	name := strings.TrimSuffix(filepath.Base(filePath), ".json")
	*themes = append(*themes, Theme{Name: name, SourcePath: filePath})
}

func (l *DefaultResourceLoader) dedupeThemes(themes []Theme) *ThemeLoadResult {
	seen := make(map[string]Theme)
	var diagnostics []ResourceDiagnostic

	for _, t := range themes {
		name := t.Name
		if name == "" {
			name = "unnamed"
		}
		if existing, ok := seen[name]; ok {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: fmt.Sprintf("name \"%s\" collision", name),
				Path:    t.SourcePath,
				Collision: &CollisionInfo{
					ResourceType: "theme",
					Name:         name,
					WinnerPath:   existing.SourcePath,
					LoserPath:    t.SourcePath,
				},
			})
		} else {
			seen[name] = t
		}
	}

	var result []Theme
	for _, t := range seen {
		result = append(result, t)
	}
	return &ThemeLoadResult{Themes: result, Diagnostics: diagnostics}
}

func (l *DefaultResourceLoader) dedupePrompts(prompts []PromptTemplate) *PromptLoadResult {
	seen := make(map[string]PromptTemplate)
	var diagnostics []ResourceDiagnostic

	for _, prompt := range prompts {
		if existing, ok := seen[prompt.Name]; ok {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: fmt.Sprintf("name \"/%s\" collision", prompt.Name),
				Path:    prompt.FilePath,
				Collision: &CollisionInfo{
					ResourceType: "prompt",
					Name:         prompt.Name,
					WinnerPath:   existing.FilePath,
					LoserPath:    prompt.FilePath,
				},
			})
		} else {
			seen[prompt.Name] = prompt
		}
	}

	var result []PromptTemplate
	for _, p := range seen {
		result = append(result, p)
	}
	return &PromptLoadResult{Prompts: result, Diagnostics: diagnostics}
}

// ============================================================================
// Private: Conflict Detection
// ============================================================================

type extensionConflict struct {
	Path    string
	Message string
}

func (l *DefaultResourceLoader) detectExtensionConflicts(exts []*extensions.Extension) []extensionConflict {
	var conflicts []extensionConflict
	toolOwners := make(map[string]string)
	flagOwners := make(map[string]string)

	for _, ext := range exts {
		for toolName := range ext.Tools {
			existing, ok := toolOwners[toolName]
			if ok && existing != ext.Path {
				conflicts = append(conflicts, extensionConflict{
					Path:    ext.Path,
					Message: fmt.Sprintf("Tool \"%s\" conflicts with %s", toolName, existing),
				})
			} else {
				toolOwners[toolName] = ext.Path
			}
		}

		for flagName := range ext.Flags {
			existing, ok := flagOwners[flagName]
			if ok && existing != ext.Path {
				conflicts = append(conflicts, extensionConflict{
					Path:    ext.Path,
					Message: fmt.Sprintf("Flag \"--%s\" conflicts with %s", flagName, existing),
				})
			} else {
				flagOwners[flagName] = ext.Path
			}
		}
	}

	return conflicts
}

// ============================================================================
// Private: System Prompt Discovery
// ============================================================================

func (l *DefaultResourceLoader) discoverSystemPromptFile() string {
	projectPath := filepath.Join(l.cwd, ConfigDirName, "SYSTEM.md")
	if _, err := os.Stat(projectPath); err == nil {
		return projectPath
	}
	globalPath := filepath.Join(l.agentDir, "SYSTEM.md")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath
	}
	return ""
}

func (l *DefaultResourceLoader) discoverAppendSystemPromptFile() string {
	projectPath := filepath.Join(l.cwd, ConfigDirName, "APPEND_SYSTEM.md")
	if _, err := os.Stat(projectPath); err == nil {
		return projectPath
	}
	globalPath := filepath.Join(l.agentDir, "APPEND_SYSTEM.md")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath
	}
	return ""
}

// ============================================================================
// Standalone Utility Functions
// ============================================================================

// resolvePromptInput resolves a prompt: if it's a file path, reads its contents.
// Otherwise returns the input as-is.
func resolvePromptInput(input string, description string) string {
	if input == "" {
		return ""
	}

	if _, err := os.Stat(input); err == nil {
		data, err := os.ReadFile(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not read %s file %s: %v\n", description, input, err)
			return input
		}
		return string(data)
	}

	return input
}

// loadProjectContextFiles loads AGENTS.md / CLAUDE.md from agent dir and CWD ancestors.
func loadProjectContextFiles(cwd, agentDir string) []ContextFile {
	var contextFiles []ContextFile
	seenPaths := make(map[string]bool)

	globalContext := loadContextFileFromDir(agentDir)
	if globalContext != nil {
		contextFiles = append(contextFiles, *globalContext)
		seenPaths[globalContext.Path] = true
	}

	var ancestorFiles []ContextFile
	currentDir := cwd

	for {
		cf := loadContextFileFromDir(currentDir)
		if cf != nil && !seenPaths[cf.Path] {
			ancestorFiles = append([]ContextFile{*cf}, ancestorFiles...)
			seenPaths[cf.Path] = true
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}

	contextFiles = append(contextFiles, ancestorFiles...)
	return contextFiles
}

func loadContextFileFromDir(dir string) *ContextFile {
	candidates := []string{"AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}
	for _, filename := range candidates {
		filePath := filepath.Join(dir, filename)
		data, err := os.ReadFile(filePath)
		if err == nil {
			return &ContextFile{Path: filePath, Content: string(data)}
		}
	}
	return nil
}

// isLocalPath returns true if the value looks like a local file path
// (not a URL, npm/git/github reference).
func isLocalPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "npm:") ||
		strings.HasPrefix(trimmed, "git:") ||
		strings.HasPrefix(trimmed, "github:") ||
		strings.HasPrefix(trimmed, "http:") ||
		strings.HasPrefix(trimmed, "https:") ||
		strings.HasPrefix(trimmed, "ssh:") {
		return false
	}
	return true
}

// ============================================================================
// Shared Helpers
// ============================================================================


func pathsOf(entries []PathEntry) []string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return paths
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hasDiagnosticForPath(diagnostics []ResourceDiagnostic, path string) bool {
	for _, d := range diagnostics {
		if d.Path == path {
			return true
		}
	}
	return false
}

