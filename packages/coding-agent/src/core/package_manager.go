package ycode

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ============================================================================
// DefaultPackageManager
// ============================================================================

type DefaultPackageManager struct {
	cwd              string
	agentDir         string
	settingsManager  *SettingsManager
	globalNpmRoot    string
	globalNpmRootKey string
	progressCallback ProgressCallback
}

type PackageManagerOptions struct {
	Cwd             string
	AgentDir        string
	SettingsManager *SettingsManager
}

func NewDefaultPackageManager(opts PackageManagerOptions) *DefaultPackageManager {
	return &DefaultPackageManager{
		cwd:             opts.Cwd,
		agentDir:        opts.AgentDir,
		settingsManager: opts.SettingsManager,
	}
}

func (pm *DefaultPackageManager) SetProgressCallback(cb ProgressCallback) {
	pm.progressCallback = cb
}

func (pm *DefaultPackageManager) emitProgress(event ProgressEvent) {
	if pm.progressCallback != nil {
		pm.progressCallback(event)
	}
}

func (pm *DefaultPackageManager) withProgress(
	action ProgressAction,
	source string,
	message string,
	operation func() error,
) error {
	pm.emitProgress(ProgressEvent{Type: ProgressStart, Action: action, Source: source, Message: message})
	err := operation()
	if err != nil {
		pm.emitProgress(ProgressEvent{Type: ProgressError, Action: action, Source: source, Message: err.Error()})
		return err
	}
	pm.emitProgress(ProgressEvent{Type: ProgressComplete, Action: action, Source: source})
	return nil
}

// ============================================================================
// Settings Management
// ============================================================================

func (pm *DefaultPackageManager) AddSourceToSettings(source string, local bool) bool {
	scope := ScopeUser
	if local {
		scope = ScopeProject
	}
	var currentSettings Settings
	if scope == ScopeProject {
		currentSettings = pm.settingsManager.GetProjectSettings()
	} else {
		currentSettings = pm.settingsManager.GetGlobalSettings()
	}
	currentPackages := currentSettings.Packages
	if currentPackages == nil {
		currentPackages = []PackageSource{}
	}
	normalized := pm.normalizePackageSourceForSettings(source, scope)
	for _, existing := range currentPackages {
		if pm.packageSourcesMatch(existing, source, scope) {
			return false
		}
	}
	nextPackages := append(currentPackages, normalized)
	if scope == ScopeProject {
		pm.settingsManager.SetProjectPackages(nextPackages)
	} else {
		pm.settingsManager.SetPackages(nextPackages)
	}
	return true
}

func (pm *DefaultPackageManager) RemoveSourceFromSettings(source string, local bool) bool {
	scope := ScopeUser
	if local {
		scope = ScopeProject
	}
	var currentSettings Settings
	if scope == ScopeProject {
		currentSettings = pm.settingsManager.GetProjectSettings()
	} else {
		currentSettings = pm.settingsManager.GetGlobalSettings()
	}
	currentPackages := currentSettings.Packages
	if currentPackages == nil {
		currentPackages = []PackageSource{}
	}
	var nextPackages []PackageSource
	for _, existing := range currentPackages {
		if !pm.packageSourcesMatch(existing, source, scope) {
			nextPackages = append(nextPackages, existing)
		}
	}
	if len(nextPackages) == len(currentPackages) {
		return false
	}
	if scope == ScopeProject {
		pm.settingsManager.SetProjectPackages(nextPackages)
	} else {
		pm.settingsManager.SetPackages(nextPackages)
	}
	return true
}

func (pm *DefaultPackageManager) GetInstalledPath(source string, scope string) string {
	parsed := pm.parseSource(source)
	scopeTyped := SourceScope(scope)
	if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
		path := pm.getNpmInstallPath(*parsed.Npm, scopeTyped)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return ""
	}
	if parsed.Type == ParsedSourceGit && parsed.Git != nil {
		path := pm.getGitInstallPath(*parsed.Git, scopeTyped)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return ""
	}
	if parsed.Type == ParsedSourceLocal && parsed.Local != nil {
		baseDir := pm.getBaseDirForScope(scopeTyped)
		path := pm.resolvePathFromBase(parsed.Local.Path, baseDir)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return ""
	}
	return ""
}

// ============================================================================
// Resolve
// ============================================================================

func (pm *DefaultPackageManager) Resolve(onMissing func(source string) (MissingSourceAction, error)) (*ResolvedPaths, error) {
	accumulator := pm.createAccumulator()
	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()

	// Collect all packages (project first so it wins collisions)
	var allPackages []pkgScoped
	for _, pkg := range orEmpty(projectSettings.Packages) {
		allPackages = append(allPackages, pkgScoped{pkg, ScopeProject})
	}
	for _, pkg := range orEmpty(globalSettings.Packages) {
		allPackages = append(allPackages, pkgScoped{pkg, ScopeUser})
	}

	// Dedupe (project wins over user for same identity)
	packageSources := pm.dedupePackages(allPackages)
	if err := pm.resolvePackageSources(packageSources, accumulator, onMissing); err != nil {
		return nil, err
	}

	globalBaseDir := pm.agentDir
	projectBaseDir := filepath.Join(pm.cwd, ConfigDirName)

	for _, resourceType := range resourceTypes {
		target := pm.getTargetMap(accumulator, resourceType)
		globalEntries := getStringSlice(globalSettings, resourceType)
		projectEntries := getStringSlice(projectSettings, resourceType)
		pm.resolveLocalEntries(projectEntries, resourceType, target, PathMetadata{
			Source: "local", Scope: "project", Origin: "top-level",
		}, projectBaseDir)
		pm.resolveLocalEntries(globalEntries, resourceType, target, PathMetadata{
			Source: "local", Scope: "user", Origin: "top-level",
		}, globalBaseDir)
	}

	pm.addAutoDiscoveredResources(accumulator, globalSettings, projectSettings, globalBaseDir, projectBaseDir)

	return pm.toResolvedPaths(accumulator), nil
}

func (pm *DefaultPackageManager) ResolveExtensionSources(sources []string, local bool, temporary bool) (*ResolvedPaths, error) {
	accumulator := pm.createAccumulator()
	scope := ScopeUser
	if temporary {
		scope = ScopeTemporary
	} else if local {
		scope = ScopeProject
	}
	var packageSources []pkgScoped
	for _, src := range sources {
		packageSources = append(packageSources, pkgScoped{pkg: src, scope: scope})
	}
	if err := pm.resolvePackageSources(packageSources, accumulator, nil); err != nil {
		return nil, err
	}
	return pm.toResolvedPaths(accumulator), nil
}

// ============================================================================
// List / Install / Remove / Update
// ============================================================================

func (pm *DefaultPackageManager) ListConfiguredPackages() []ConfiguredPackage {
	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()
	var result []ConfiguredPackage

	for _, pkg := range orEmpty(globalSettings.Packages) {
		source := packageSourceString(pkg)
		result = append(result, ConfiguredPackage{
			Source:       source,
			Scope:        "user",
			Filtered:     isPackageSourceFilter(pkg),
			InstalledPath: pm.GetInstalledPath(source, "user"),
		})
	}
	for _, pkg := range orEmpty(projectSettings.Packages) {
		source := packageSourceString(pkg)
		result = append(result, ConfiguredPackage{
			Source:       source,
			Scope:        "project",
			Filtered:     isPackageSourceFilter(pkg),
			InstalledPath: pm.GetInstalledPath(source, "project"),
		})
	}
	return result
}

func (pm *DefaultPackageManager) Install(source string, local bool) error {
	parsed := pm.parseSource(source)
	scope := ScopeUser
	if local {
		scope = ScopeProject
	}
	return pm.withProgress(ActionInstall, source, fmt.Sprintf("Installing %s...", source), func() error {
		if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
			return pm.installNpm(*parsed.Npm, scope, false)
		}
		if parsed.Type == ParsedSourceGit && parsed.Git != nil {
			return pm.installGit(*parsed.Git, scope)
		}
		if parsed.Type == ParsedSourceLocal && parsed.Local != nil {
			resolved := pm.resolvePath(parsed.Local.Path)
			if _, err := os.Stat(resolved); os.IsNotExist(err) {
				return fmt.Errorf("path does not exist: %s", resolved)
			}
			return nil
		}
		return fmt.Errorf("unsupported install source: %s", source)
	})
}

func (pm *DefaultPackageManager) InstallAndPersist(source string, local bool) error {
	if err := pm.Install(source, local); err != nil {
		return err
	}
	pm.AddSourceToSettings(source, local)
	return nil
}

func (pm *DefaultPackageManager) Remove(source string, local bool) error {
	parsed := pm.parseSource(source)
	scope := ScopeUser
	if local {
		scope = ScopeProject
	}
	return pm.withProgress(ActionRemove, source, fmt.Sprintf("Removing %s...", source), func() error {
		if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
			return pm.uninstallNpm(*parsed.Npm, scope)
		}
		if parsed.Type == ParsedSourceGit && parsed.Git != nil {
			return pm.removeGit(*parsed.Git, scope)
		}
		if parsed.Type == ParsedSourceLocal {
			return nil
		}
		return fmt.Errorf("unsupported remove source: %s", source)
	})
}

func (pm *DefaultPackageManager) RemoveAndPersist(source string, local bool) (bool, error) {
	if err := pm.Remove(source, local); err != nil {
		return false, err
	}
	return pm.RemoveSourceFromSettings(source, local), nil
}

func (pm *DefaultPackageManager) Update(source string) error {
	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()
	var identity string
	if source != "" {
		identity = pm.getPackageIdentity(source, "")
	}
	matched := false
	type updateSource struct {
		source string
		scope  SourceScope
	}
	var updateSources []updateSource

	for _, pkg := range orEmpty(globalSettings.Packages) {
		srcStr := packageSourceString(pkg)
		if identity != "" && pm.getPackageIdentity(srcStr, ScopeUser) != identity {
			continue
		}
		matched = true
		updateSources = append(updateSources, updateSource{srcStr, ScopeUser})
	}
	for _, pkg := range orEmpty(projectSettings.Packages) {
		srcStr := packageSourceString(pkg)
		if identity != "" && pm.getPackageIdentity(srcStr, ScopeProject) != identity {
			continue
		}
		matched = true
		updateSources = append(updateSources, updateSource{srcStr, ScopeProject})
	}

	if source != "" && !matched {
		all := append(orEmpty(globalSettings.Packages), orEmpty(projectSettings.Packages)...)
		return fmt.Errorf("%s", pm.buildNoMatchingPackageMessage(source, all))
	}

	configured := make([]configuredUpdateSource, len(updateSources))
	for i, us := range updateSources {
		configured[i] = configuredUpdateSource{source: us.source, scope: us.scope}
	}
	return pm.updateConfiguredSources(configured)
}

// ============================================================================
// Update internals
// ============================================================================

type configuredUpdateSource struct {
	source string
	scope  SourceScope
}

type npmUpdateTarget struct {
	configuredUpdateSource
	parsed NpmSource
}

type gitCandidate struct {
	configuredUpdateSource
	parsed GitSource
}

func (pm *DefaultPackageManager) updateConfiguredSources(sources []configuredUpdateSource) error {
	if isOfflineModeEnabled() || len(sources) == 0 {
		return nil
	}

	var npmCandidates []npmUpdateTarget
	var gitCandidates []gitCandidate

	for _, entry := range sources {
		parsed := pm.parseSource(entry.source)
		if parsed.Type == ParsedSourceLocal || parsed.Pinned {
			continue
		}
		if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
			npmCandidates = append(npmCandidates, npmUpdateTarget{entry, *parsed.Npm})
			continue
		}
		if parsed.Type == ParsedSourceGit && parsed.Git != nil {
			gitCandidates = append(gitCandidates, gitCandidate{entry, *parsed.Git})
		}
	}

	// NPM update checks
	type npmCheckResult struct {
		entry       npmUpdateTarget
		shouldUpdate bool
	}
	checkTasks := make([]func() (interface{}, error), len(npmCandidates))
	for i, entry := range npmCandidates {
		entry := entry
		checkTasks[i] = func() (interface{}, error) {
			shouldUpdate, _ := pm.shouldUpdateNpmSource(entry.parsed, entry.scope)
			return npmCheckResult{entry, shouldUpdate}, nil
		}
	}
	results, _ := runWithConcurrency(checkTasks, updateCheckConcurrency)

	var userNpmUpdates, projectNpmUpdates []npmUpdateTarget
	for _, r := range results {
		res := r.(npmCheckResult)
		if !res.shouldUpdate {
			continue
		}
		if res.entry.scope == ScopeUser {
			userNpmUpdates = append(userNpmUpdates, res.entry)
		} else {
			projectNpmUpdates = append(projectNpmUpdates, res.entry)
		}
	}

	var tasks []func() error
	if len(userNpmUpdates) > 0 {
		tasks = append(tasks, func() error {
			return pm.updateNpmBatch(userNpmUpdates, ScopeUser)
		})
	}
	if len(projectNpmUpdates) > 0 {
		tasks = append(tasks, func() error {
			return pm.updateNpmBatch(projectNpmUpdates, ScopeProject)
		})
	}
	if len(gitCandidates) > 0 {
		gitTasks := make([]func() (interface{}, error), len(gitCandidates))
		for i, entry := range gitCandidates {
			entry := entry
			gitTasks[i] = func() (interface{}, error) {
				pm.withProgress(ActionUpdate, entry.source, fmt.Sprintf("Updating %s...", entry.source), func() error {
					return pm.updateGit(entry.parsed, entry.scope)
				})
				return nil, nil
			}
		}
		tasks = append(tasks, func() error {
			runWithConcurrency(gitTasks, gitUpdateConcurrency)
			return nil
		})
	}

	errs := make(chan error, len(tasks))
	for _, t := range tasks {
		t := t
		go func() {
			errs <- t()
		}()
	}
	for range tasks {
		if err := <-errs; err != nil {
			return err
		}
	}
	return nil
}

func (pm *DefaultPackageManager) shouldUpdateNpmSource(source NpmSource, scope SourceScope) (bool, error) {
	installedPath := pm.getNpmInstallPath(source, scope)
	installedVersion := ""
	if _, err := os.Stat(installedPath); err == nil {
		installedVersion = pm.getInstalledNpmVersion(installedPath)
	}
	if installedVersion == "" {
		return true, nil
	}
	latestVersion, err := pm.getLatestNpmVersion(source.Name)
	if err != nil {
		return true, nil
	}
	return latestVersion != installedVersion, nil
}

func (pm *DefaultPackageManager) updateNpmBatch(sources []npmUpdateTarget, scope SourceScope) error {
	if len(sources) == 0 {
		return nil
	}

	var sourceLabel, message string
	var specs []string
	if len(sources) == 1 {
		sourceLabel = sources[0].source
		message = fmt.Sprintf("Updating %s...", sources[0].source)
	} else {
		sourceLabel = fmt.Sprintf("%s npm packages", scope)
		message = fmt.Sprintf("Updating %s npm packages...", scope)
	}
	for _, entry := range sources {
		specs = append(specs, entry.parsed.Name+"@latest")
	}

	return pm.withProgress(ActionUpdate, sourceLabel, message, func() error {
		return pm.installNpmBatch(specs, scope)
	})
}

func (pm *DefaultPackageManager) installNpmBatch(specs []string, scope SourceScope) error {
	if scope == ScopeUser {
		args := append([]string{"install", "-g"}, specs...)
		return pm.runNpmCommand(args, "")
	}
	installRoot := pm.getNpmInstallRoot(scope, false)
	pm.ensureNpmProject(installRoot)
	args := append([]string{"install"}, specs...)
	args = append(args, "--prefix", installRoot)
	return pm.runNpmCommand(args, "")
}

func (pm *DefaultPackageManager) CheckForAvailableUpdates() ([]PackageUpdate, error) {
	if isOfflineModeEnabled() {
		return nil, nil
	}

	globalSettings := pm.settingsManager.GetGlobalSettings()
	projectSettings := pm.settingsManager.GetProjectSettings()

	var allPackages []pkgScoped
	for _, pkg := range orEmpty(projectSettings.Packages) {
		allPackages = append(allPackages, pkgScoped{pkg, ScopeProject})
	}
	for _, pkg := range orEmpty(globalSettings.Packages) {
		allPackages = append(allPackages, pkgScoped{pkg, ScopeUser})
	}

	// Dedupe
	deduped := pm.dedupePackages(allPackages)

	checks := make([]func() (interface{}, error), 0, len(deduped))
	for _, entry := range deduped {
		if entry.scope == ScopeTemporary {
			continue
		}
		entry := entry
		checks = append(checks, func() (interface{}, error) {
			source := packageSourceString(entry.pkg)
			parsed := pm.parseSource(source)
			if parsed.Type == ParsedSourceLocal || parsed.Pinned {
				return nil, nil
			}

			if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
				installedPath := pm.getNpmInstallPath(*parsed.Npm, entry.scope)
				if _, err := os.Stat(installedPath); os.IsNotExist(err) {
					return nil, nil
				}
				hasUpdate, _ := pm.npmHasAvailableUpdate(*parsed.Npm, installedPath)
				if !hasUpdate {
					return nil, nil
				}
				return PackageUpdate{
					Source: source, DisplayName: parsed.Npm.Name,
					Type: PackageUpdateNpm, Scope: entry.scope,
				}, nil
			}

			if parsed.Type == ParsedSourceGit && parsed.Git != nil {
				installedPath := pm.getGitInstallPath(*parsed.Git, entry.scope)
				if _, err := os.Stat(installedPath); os.IsNotExist(err) {
					return nil, nil
				}
				hasUpdate, _ := pm.gitHasAvailableUpdate(installedPath)
				if !hasUpdate {
					return nil, nil
				}
				return PackageUpdate{
					Source: source, DisplayName: fmt.Sprintf("%s/%s", parsed.Git.Host, parsed.Git.Path),
					Type: PackageUpdateGit, Scope: entry.scope,
				}, nil
			}
			return nil, nil
		})
	}

	results, _ := runWithConcurrency(checks, updateCheckConcurrency)
	var updates []PackageUpdate
	for _, r := range results {
		if r == nil {
			continue
		}
		if update, ok := r.(PackageUpdate); ok {
			updates = append(updates, update)
		}
	}
	return updates, nil
}

// ============================================================================
// Package source resolution
// ============================================================================

type pkgScoped struct {
	pkg   PackageSource
	scope SourceScope
}

func (pm *DefaultPackageManager) resolvePackageSources(
	sources []pkgScoped,
	accumulator *ResourceAccumulator,
	onMissing func(source string) (MissingSourceAction, error),
) error {
	for _, entry := range sources {
		sourceStr := packageSourceString(entry.pkg)
		var filter *PackageFilter
		if isPackageSourceFilter(entry.pkg) {
			f := packageSourceToFilter(entry.pkg)
			filter = &f
		}
		parsed := pm.parseSource(sourceStr)
		metadata := PathMetadata{Source: sourceStr, Scope: entry.scope, Origin: "package"}

		if parsed.Type == ParsedSourceLocal && parsed.Local != nil {
			baseDir := pm.getBaseDirForScope(entry.scope)
			pm.resolveLocalExtensionSource(*parsed.Local, accumulator, filter, metadata, baseDir)
			continue
		}

		installMissing := func() (bool, error) {
			if isOfflineModeEnabled() {
				return false, nil
			}
			if onMissing == nil {
				if err := pm.installParsedSource(parsed, entry.scope); err != nil {
					return false, err
				}
				return true, nil
			}
			action, err := onMissing(sourceStr)
			if err != nil {
				return false, err
			}
			if action == MissingSourceSkip {
				return false, nil
			}
			if action == MissingSourceError {
				return false, fmt.Errorf("missing source: %s", sourceStr)
			}
			if err := pm.installParsedSource(parsed, entry.scope); err != nil {
				return false, err
			}
			return true, nil
		}

		if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
			installedPath := pm.getNpmInstallPath(*parsed.Npm, entry.scope)
			needsInstall := false
			if _, err := os.Stat(installedPath); os.IsNotExist(err) {
				needsInstall = true
			} else if parsed.Pinned {
				matches, _ := pm.installedNpmMatchesPinnedVersion(*parsed.Npm, installedPath)
				if !matches {
					needsInstall = true
				}
			}
			if needsInstall {
				installed, err := installMissing()
				if err != nil {
					return err
				}
				if !installed {
					continue
				}
			}
			metadata.BaseDir = installedPath
			pm.collectPackageResources(installedPath, accumulator, filter, metadata)
			continue
		}

		if parsed.Type == ParsedSourceGit && parsed.Git != nil {
			installedPath := pm.getGitInstallPath(*parsed.Git, entry.scope)
			if _, err := os.Stat(installedPath); os.IsNotExist(err) {
				installed, err := installMissing()
				if err != nil {
					return err
				}
				if !installed {
					continue
				}
			} else if entry.scope == ScopeTemporary && !parsed.Pinned && !isOfflineModeEnabled() {
				pm.refreshTemporaryGitSource(*parsed.Git, sourceStr)
			}
			metadata.BaseDir = installedPath
			pm.collectPackageResources(installedPath, accumulator, filter, metadata)
		}
	}
	return nil
}

func (pm *DefaultPackageManager) resolveLocalExtensionSource(
	source LocalSource,
	accumulator *ResourceAccumulator,
	filter *PackageFilter,
	metadata PathMetadata,
	baseDir string,
) {
	resolved := pm.resolvePathFromBase(source.Path, baseDir)
	if _, err := os.Stat(resolved); os.IsNotExist(err) {
		return
	}
	stat, err := os.Stat(resolved)
	if err != nil {
		return
	}
	if !stat.IsDir() {
		metadata.BaseDir = filepath.Dir(resolved)
		pm.addResource(accumulator.Extensions, resolved, metadata, true)
		return
	}
	metadata.BaseDir = resolved
	resources := pm.collectPackageResources(resolved, accumulator, filter, metadata)
	if !resources {
		pm.addResource(accumulator.Extensions, resolved, metadata, true)
	}
}

func (pm *DefaultPackageManager) installParsedSource(parsed *ParsedSource, scope SourceScope) error {
	if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
		return pm.installNpm(*parsed.Npm, scope, scope == ScopeTemporary)
	}
	if parsed.Type == ParsedSourceGit && parsed.Git != nil {
		return pm.installGit(*parsed.Git, scope)
	}
	return nil
}

// ============================================================================
// Source matching and parsing
// ============================================================================

func packageSourceString(pkg PackageSource) string {
	if s, ok := pkg.(string); ok {
		return s
	}
	if f, ok := pkg.(PackageSourceFilter); ok {
		return f.Source
	}
	return ""
}

func isPackageSourceFilter(pkg PackageSource) bool {
	_, ok := pkg.(PackageSourceFilter)
	return ok
}

func packageSourceToFilter(pkg PackageSource) PackageFilter {
	if f, ok := pkg.(PackageSourceFilter); ok {
		return PackageFilter{
			Extensions: f.Extensions,
			Skills:     f.Skills,
			Prompts:    f.Prompts,
			Themes:     f.Themes,
		}
	}
	return PackageFilter{}
}

func (pm *DefaultPackageManager) getSourceMatchKeyForInput(source string) string {
	parsed := pm.parseSource(source)
	if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
		return "npm:" + parsed.Npm.Name
	}
	if parsed.Type == ParsedSourceGit && parsed.Git != nil {
		return fmt.Sprintf("git:%s/%s", parsed.Git.Host, parsed.Git.Path)
	}
	return "local:" + pm.resolvePath(parsed.Local.Path)
}

func (pm *DefaultPackageManager) getSourceMatchKeyForSettings(source string, scope SourceScope) string {
	parsed := pm.parseSource(source)
	if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
		return "npm:" + parsed.Npm.Name
	}
	if parsed.Type == ParsedSourceGit && parsed.Git != nil {
		return fmt.Sprintf("git:%s/%s", parsed.Git.Host, parsed.Git.Path)
	}
	if parsed.Local != nil {
		baseDir := pm.getBaseDirForScope(scope)
		return "local:" + pm.resolvePathFromBase(parsed.Local.Path, baseDir)
	}
	return source
}

func (pm *DefaultPackageManager) buildNoMatchingPackageMessage(source string, configuredPackages []PackageSource) string {
	suggestion := pm.findSuggestedConfiguredSource(source, configuredPackages)
	if suggestion == "" {
		return fmt.Sprintf("No matching package found for %s", source)
	}
	return fmt.Sprintf("No matching package found for %s. Did you mean %s?", source, suggestion)
}

func (pm *DefaultPackageManager) findSuggestedConfiguredSource(source string, configuredPackages []PackageSource) string {
	trimmed := strings.TrimSpace(source)
	for _, pkg := range configuredPackages {
		srcStr := packageSourceString(pkg)
		parsed := pm.parseSource(srcStr)
		if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
			if trimmed == parsed.Npm.Name || trimmed == parsed.Npm.Spec {
				return srcStr
			}
			continue
		}
		if parsed.Type == ParsedSourceGit && parsed.Git != nil {
			shorthand := fmt.Sprintf("%s/%s", parsed.Git.Host, parsed.Git.Path)
			if trimmed == shorthand {
				return srcStr
			}
			if parsed.Git.Ref != "" {
				withRef := fmt.Sprintf("%s@%s", shorthand, parsed.Git.Ref)
				if trimmed == withRef {
					return srcStr
				}
			}
		}
	}
	return ""
}

func (pm *DefaultPackageManager) packageSourcesMatch(existing PackageSource, inputSource string, scope SourceScope) bool {
	left := pm.getSourceMatchKeyForSettings(packageSourceString(existing), scope)
	right := pm.getSourceMatchKeyForInput(inputSource)
	return left == right
}

func (pm *DefaultPackageManager) normalizePackageSourceForSettings(source string, scope SourceScope) string {
	parsed := pm.parseSource(source)
	if parsed.Type != ParsedSourceLocal {
		return source
	}
	if parsed.Local == nil {
		return source
	}
	baseDir := pm.getBaseDirForScope(scope)
	resolved := pm.resolvePath(parsed.Local.Path)
	rel, _ := filepath.Rel(baseDir, resolved)
	if rel == "" {
		return "."
	}
	return rel
}

func (pm *DefaultPackageManager) parseSource(source string) *ParsedSource {
	if strings.HasPrefix(source, "npm:") {
		spec := strings.TrimSpace(source[4:])
		name, version := pm.parseNpmSpec(spec)
		return &ParsedSource{
			Type:   ParsedSourceNpm,
			Pinned: version != "",
			Npm: &NpmSource{
				Spec:   spec,
				Name:   name,
				Pinned: version != "",
			},
		}
	}
	if isLocalPath(source) {
		return &ParsedSource{
			Type:  ParsedSourceLocal,
			Local: &LocalSource{Path: source},
		}
	}
	if gitParsed := ParseGitUrl(source); gitParsed != nil {
		return gitParsed
	}
	return &ParsedSource{
		Type:  ParsedSourceLocal,
		Local: &LocalSource{Path: source},
	}
}

var npmSpecRegex = regexp.MustCompile(`^(@?[^@]+(?:\/[^@]+)?)(?:@(.+))?$`)

func (pm *DefaultPackageManager) parseNpmSpec(spec string) (name string, version string) {
	m := npmSpecRegex.FindStringSubmatch(spec)
	if m == nil {
		return spec, ""
	}
	name = m[1]
	if name == "" {
		name = spec
	}
	version = m[2]
	return
}

func (pm *DefaultPackageManager) getPackageIdentity(source string, scope SourceScope) string {
	parsed := pm.parseSource(source)
	if parsed.Type == ParsedSourceNpm && parsed.Npm != nil {
		return "npm:" + parsed.Npm.Name
	}
	if parsed.Type == ParsedSourceGit && parsed.Git != nil {
		return fmt.Sprintf("git:%s/%s", parsed.Git.Host, parsed.Git.Path)
	}
	if scope != "" && parsed.Local != nil {
		baseDir := pm.getBaseDirForScope(scope)
		return "local:" + pm.resolvePathFromBase(parsed.Local.Path, baseDir)
	}
	if parsed.Local != nil {
		return "local:" + pm.resolvePath(parsed.Local.Path)
	}
	return source
}

func (pm *DefaultPackageManager) dedupePackages(packages []pkgScoped) []pkgScoped {
	seen := make(map[string]pkgScoped)
	for _, entry := range packages {
		identity := pm.getPackageIdentity(packageSourceString(entry.pkg), entry.scope)
		existing, ok := seen[identity]
		if !ok {
			seen[identity] = entry
		} else if entry.scope == ScopeProject && existing.scope == ScopeUser {
			seen[identity] = entry
		}
	}
	result := make([]pkgScoped, 0, len(seen))
	for _, v := range seen {
		result = append(result, v)
	}
	return result
}

// ============================================================================
// Path helpers
// ============================================================================

func (pm *DefaultPackageManager) getBaseDirForScope(scope SourceScope) string {
	if scope == ScopeProject {
		return filepath.Join(pm.cwd, ConfigDirName)
	}
	if scope == ScopeUser {
		return pm.agentDir
	}
	return pm.cwd
}

func (pm *DefaultPackageManager) resolvePath(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "~" {
		return getHomeDir()
	}
	if strings.HasPrefix(trimmed, "~/") {
		return filepath.Join(getHomeDir(), trimmed[2:])
	}
	if strings.HasPrefix(trimmed, "~") {
		return filepath.Join(getHomeDir(), trimmed[1:])
	}
	abs, _ := filepath.Abs(filepath.Join(pm.cwd, trimmed))
	return abs
}

func (pm *DefaultPackageManager) resolvePathFromBase(input string, baseDir string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "~" {
		return getHomeDir()
	}
	if strings.HasPrefix(trimmed, "~/") {
		return filepath.Join(getHomeDir(), trimmed[2:])
	}
	if strings.HasPrefix(trimmed, "~") {
		return filepath.Join(getHomeDir(), trimmed[1:])
	}
	abs, _ := filepath.Abs(filepath.Join(baseDir, trimmed))
	return abs
}

// ============================================================================
// Resource collection
// ============================================================================

func (pm *DefaultPackageManager) collectPackageResources(
	packageRoot string,
	accumulator *ResourceAccumulator,
	filter *PackageFilter,
	metadata PathMetadata,
) bool {
	if filter != nil {
		for _, resourceType := range resourceTypes {
			patterns := getFilterPatterns(filter, resourceType)
			target := pm.getTargetMap(accumulator, resourceType)
			if patterns != nil {
				pm.applyPackageFilter(packageRoot, patterns, resourceType, target, metadata)
			} else {
				pm.collectDefaultResources(packageRoot, resourceType, target, metadata)
			}
		}
		return true
	}

	manifest := pm.readPiManifest(packageRoot)
	if manifest != nil {
		for _, resourceType := range resourceTypes {
			entries := getManifestEntries(manifest, resourceType)
			pm.addManifestEntries(entries, packageRoot, resourceType, pm.getTargetMap(accumulator, resourceType), metadata)
		}
		return true
	}

	hasAnyDir := false
	for _, resourceType := range resourceTypes {
		dir := filepath.Join(packageRoot, string(resourceType))
		if _, err := os.Stat(dir); err == nil {
			files := collectResourceFiles(dir, resourceType)
			for _, f := range files {
				pm.addResource(pm.getTargetMap(accumulator, resourceType), f, metadata, true)
			}
			hasAnyDir = len(files) > 0 || hasAnyDir
		}
	}
	return hasAnyDir
}

func (pm *DefaultPackageManager) collectDefaultResources(
	packageRoot string,
	resourceType ResourceType,
	target map[string]resourceEntry,
	metadata PathMetadata,
) {
	manifest := pm.readPiManifest(packageRoot)
	entries := getManifestEntries(manifest, resourceType)
	if len(entries) > 0 {
		pm.addManifestEntries(entries, packageRoot, resourceType, target, metadata)
		return
	}
	dir := filepath.Join(packageRoot, string(resourceType))
	if _, err := os.Stat(dir); err == nil {
		files := collectResourceFiles(dir, resourceType)
		for _, f := range files {
			pm.addResource(target, f, metadata, true)
		}
	}
}

func (pm *DefaultPackageManager) applyPackageFilter(
	packageRoot string,
	userPatterns []string,
	resourceType ResourceType,
	target map[string]resourceEntry,
	metadata PathMetadata,
) {
	allFiles, _ := pm.collectManifestFiles(packageRoot, resourceType)

	if len(userPatterns) == 0 {
		for _, f := range allFiles {
			pm.addResource(target, f, metadata, false)
		}
		return
	}

	enabledByUser := applyPatterns(allFiles, userPatterns, packageRoot)
	for _, f := range allFiles {
		pm.addResource(target, f, metadata, enabledByUser[f])
	}
}

func (pm *DefaultPackageManager) collectManifestFiles(packageRoot string, resourceType ResourceType) ([]string, map[string]bool) {
	manifest := pm.readPiManifest(packageRoot)
	entries := getManifestEntries(manifest, resourceType)
	if len(entries) > 0 {
		allFiles := pm.collectFilesFromManifestEntries(entries, packageRoot, resourceType)
		var manifestPatterns []string
		for _, e := range entries {
			if isOverridePattern(e) {
				manifestPatterns = append(manifestPatterns, e)
			}
		}
		var enabledByManifest map[string]bool
		if len(manifestPatterns) > 0 {
			enabledByManifest = applyPatterns(allFiles, manifestPatterns, packageRoot)
		} else {
			enabledByManifest = make(map[string]bool, len(allFiles))
			for _, f := range allFiles {
				enabledByManifest[f] = true
			}
		}
		var allEnabled []string
		for f := range enabledByManifest {
			allEnabled = append(allEnabled, f)
		}
		return allEnabled, enabledByManifest
	}

	conventionDir := filepath.Join(packageRoot, string(resourceType))
	if _, err := os.Stat(conventionDir); os.IsNotExist(err) {
		return nil, make(map[string]bool)
	}
	allFiles := collectResourceFiles(conventionDir, resourceType)
	enabledByManifest := make(map[string]bool, len(allFiles))
	for _, f := range allFiles {
		enabledByManifest[f] = true
	}
	return allFiles, enabledByManifest
}

func (pm *DefaultPackageManager) readPiManifest(packageRoot string) *YcodeManifest {
	packageJsonPath := filepath.Join(packageRoot, "package.json")
	if _, err := os.Stat(packageJsonPath); os.IsNotExist(err) {
		return nil
	}
	return readPiManifestFile(packageJsonPath)
}

func (pm *DefaultPackageManager) addManifestEntries(
	entries []string,
	root string,
	resourceType ResourceType,
	target map[string]resourceEntry,
	metadata PathMetadata,
) {
	if len(entries) == 0 {
		return
	}
	allFiles := pm.collectFilesFromManifestEntries(entries, root, resourceType)
	var patterns []string
	for _, e := range entries {
		if isOverridePattern(e) {
			patterns = append(patterns, e)
		}
	}
	enabledPaths := applyPatterns(allFiles, patterns, root)
	for _, f := range allFiles {
		if enabledPaths[f] {
			pm.addResource(target, f, metadata, true)
		}
	}
}

func (pm *DefaultPackageManager) collectFilesFromManifestEntries(entries []string, root string, resourceType ResourceType) []string {
	var sourceEntries []string
	for _, e := range entries {
		if !isOverridePattern(e) {
			sourceEntries = append(sourceEntries, e)
		}
	}
	var resolved []string
	for _, entry := range sourceEntries {
		if !hasGlobPattern(entry) {
			resolved = append(resolved, filepath.Join(root, entry))
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, entry))
		resolved = append(resolved, matches...)
	}
	return pm.collectFilesFromPaths(resolved, resourceType)
}

func (pm *DefaultPackageManager) resolveLocalEntries(
	entries []string,
	resourceType ResourceType,
	target map[string]resourceEntry,
	metadata PathMetadata,
	baseDir string,
) {
	if len(entries) == 0 {
		return
	}
	plain, patterns := splitPatterns(entries)
	var resolvedPlain []string
	for _, p := range plain {
		resolvedPlain = append(resolvedPlain, pm.resolvePathFromBase(p, baseDir))
	}
	allFiles := pm.collectFilesFromPaths(resolvedPlain, resourceType)
	enabledPaths := applyPatterns(allFiles, patterns, baseDir)
	for _, f := range allFiles {
		pm.addResource(target, f, metadata, enabledPaths[f])
	}
}

func (pm *DefaultPackageManager) addAutoDiscoveredResources(
	accumulator *ResourceAccumulator,
	globalSettings Settings,
	projectSettings Settings,
	globalBaseDir string,
	projectBaseDir string,
) {
	projectMetadata := PathMetadata{Source: "auto", Scope: "project", Origin: "top-level", BaseDir: projectBaseDir}
	userMetadata := PathMetadata{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: globalBaseDir}

	projectOverrides := overridesFromSettings(projectSettings)
	userOverrides := overridesFromSettings(globalSettings)

	projectDirs := dirsForBase(projectBaseDir)
	userDirs := dirsForBase(globalBaseDir)

	addResources := func(resourceType ResourceType, paths []string, metadata PathMetadata, overrides []string, baseDir string) {
		target := pm.getTargetMap(accumulator, resourceType)
		for _, path := range paths {
			enabled := isEnabledByOverrides(path, overrides, baseDir)
			pm.addResource(target, path, metadata, enabled)
		}
	}

	// Project auto-discovered
	addResources(ResourceExtensions, collectAutoExtensionEntries(projectDirs["extensions"]), projectMetadata, projectOverrides["extensions"], projectBaseDir)
	addResources(ResourceSkills, collectAutoSkillEntries(projectDirs["skills"], skillModePi), projectMetadata, projectOverrides["skills"], projectBaseDir)

	// Project .agents/ skills
	userAgentsSkillsDir := filepath.Join(getHomeDir(), ".agents", "skills")
	projectAgentsSkillDirs := collectAncestorAgentsSkillDirs(pm.cwd)
	for _, dir := range projectAgentsSkillDirs {
		abs1, _ := filepath.Abs(dir)
		abs2, _ := filepath.Abs(userAgentsSkillsDir)
		if abs1 == abs2 {
			continue
		}
		agentsBaseDir := filepath.Dir(dir)
		agentsMetadata := projectMetadata
		agentsMetadata.BaseDir = agentsBaseDir
		addResources(ResourceSkills, collectAutoSkillEntries(dir, skillModeAgents), agentsMetadata, projectOverrides["skills"], agentsBaseDir)
	}

	addResources(ResourcePrompts, collectAutoPromptEntries(projectDirs["prompts"]), projectMetadata, projectOverrides["prompts"], projectBaseDir)
	addResources(ResourceThemes, collectAutoThemeEntries(projectDirs["themes"]), projectMetadata, projectOverrides["themes"], projectBaseDir)

	// User auto-discovered
	addResources(ResourceExtensions, collectAutoExtensionEntries(userDirs["extensions"]), userMetadata, userOverrides["extensions"], globalBaseDir)
	addResources(ResourceSkills, collectAutoSkillEntries(userDirs["skills"], skillModePi), userMetadata, userOverrides["skills"], globalBaseDir)

	userAgentsBaseDir := filepath.Dir(userAgentsSkillsDir)
	userAgentsMetadata := userMetadata
	userAgentsMetadata.BaseDir = userAgentsBaseDir
	addResources(ResourceSkills, collectAutoSkillEntries(userAgentsSkillsDir, skillModeAgents), userAgentsMetadata, userOverrides["skills"], userAgentsBaseDir)

	addResources(ResourcePrompts, collectAutoPromptEntries(userDirs["prompts"]), userMetadata, userOverrides["prompts"], globalBaseDir)
	addResources(ResourceThemes, collectAutoThemeEntries(userDirs["themes"]), userMetadata, userOverrides["themes"], globalBaseDir)
}

func (pm *DefaultPackageManager) collectFilesFromPaths(paths []string, resourceType ResourceType) []string {
	var files []string
	for _, p := range paths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			continue
		}
		stat, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !stat.IsDir() {
			files = append(files, p)
		} else {
			files = append(files, collectResourceFiles(p, resourceType)...)
		}
	}
	return files
}

// ============================================================================
// Accumulator management
// ============================================================================

func (pm *DefaultPackageManager) getTargetMap(accumulator *ResourceAccumulator, resourceType ResourceType) map[string]resourceEntry {
	switch resourceType {
	case ResourceExtensions:
		return accumulator.Extensions
	case ResourceSkills:
		return accumulator.Skills
	case ResourcePrompts:
		return accumulator.Prompts
	case ResourceThemes:
		return accumulator.Themes
	}
	return nil
}

func (pm *DefaultPackageManager) addResource(target map[string]resourceEntry, path string, metadata PathMetadata, enabled bool) {
	if path == "" {
		return
	}
	if _, ok := target[path]; !ok {
		target[path] = resourceEntry{Metadata: metadata, Enabled: enabled}
	}
}

func (pm *DefaultPackageManager) createAccumulator() *ResourceAccumulator {
	return &ResourceAccumulator{
		Extensions: make(map[string]resourceEntry),
		Skills:     make(map[string]resourceEntry),
		Prompts:    make(map[string]resourceEntry),
		Themes:     make(map[string]resourceEntry),
	}
}

func (pm *DefaultPackageManager) toResolvedPaths(accumulator *ResourceAccumulator) *ResolvedPaths {
	mapToResolved := func(entries map[string]resourceEntry) []ResolvedPathEntry {
		var resolved []ResolvedPathEntry
		for path, e := range entries {
			resolved = append(resolved, ResolvedPathEntry{
				Path: path, Enabled: e.Enabled, Metadata: e.Metadata,
			})
		}
		sort.SliceStable(resolved, func(i, j int) bool {
			return resourcePrecedenceRank(resolved[i].Metadata) < resourcePrecedenceRank(resolved[j].Metadata)
		})

		seen := make(map[string]bool)
		var deduped []ResolvedPathEntry
		for _, entry := range resolved {
			canon := canonicalizePath(entry.Path)
			if seen[canon] {
				continue
			}
			seen[canon] = true
			deduped = append(deduped, entry)
		}
		return deduped
	}

	return &ResolvedPaths{
		Extensions: mapToResolved(accumulator.Extensions),
		Skills:     mapToResolved(accumulator.Skills),
		Prompts:    mapToResolved(accumulator.Prompts),
		Themes:     mapToResolved(accumulator.Themes),
	}
}

// ============================================================================
// Helpers (free functions)
// ============================================================================

func orEmpty(pkg []PackageSource) []PackageSource {
	if pkg == nil {
		return []PackageSource{}
	}
	return pkg
}

func getStringSlice(settings Settings, resourceType ResourceType) []string {
	switch resourceType {
	case ResourceExtensions:
		return settings.Extensions
	case ResourceSkills:
		return settings.Skills
	case ResourcePrompts:
		return settings.Prompts
	case ResourceThemes:
		return settings.Themes
	}
	return nil
}

func getFilterPatterns(filter *PackageFilter, resourceType ResourceType) []string {
	if filter == nil {
		return nil
	}
	switch resourceType {
	case ResourceExtensions:
		return filter.Extensions
	case ResourceSkills:
		return filter.Skills
	case ResourcePrompts:
		return filter.Prompts
	case ResourceThemes:
		return filter.Themes
	}
	return nil
}

func getManifestEntries(manifest *YcodeManifest, resourceType ResourceType) []string {
	if manifest == nil {
		return nil
	}
	switch resourceType {
	case ResourceExtensions:
		return manifest.Extensions
	case ResourceSkills:
		return manifest.Skills
	case ResourcePrompts:
		return manifest.Prompts
	case ResourceThemes:
		return manifest.Themes
	}
	return nil
}

func overridesFromSettings(settings Settings) map[string][]string {
	return map[string][]string{
		"extensions": settings.Extensions,
		"skills":     settings.Skills,
		"prompts":    settings.Prompts,
		"themes":     settings.Themes,
	}
}

func dirsForBase(base string) map[string]string {
	return map[string]string{
		"extensions": filepath.Join(base, "extensions"),
		"skills":     filepath.Join(base, "skills"),
		"prompts":    filepath.Join(base, "prompts"),
		"themes":     filepath.Join(base, "themes"),
	}
}
