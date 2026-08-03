package ycode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ============================================================================
// Constants
// ============================================================================

var ignoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// ============================================================================
// Posix path helper
// ============================================================================

func toPosixPath(p string) string {
	return strings.ReplaceAll(p, string(filepath.Separator), "/")
}

func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("HOME")
	}
	return home
}

// ============================================================================
// Ignore file handling (basic .gitignore support)
// ============================================================================

type ignoreMatcher struct {
	patterns []string
}

func newIgnoreMatcher() *ignoreMatcher {
	return &ignoreMatcher{}
}

func (ig *ignoreMatcher) add(patterns []string) {
	ig.patterns = append(ig.patterns, patterns...)
}

func (ig *ignoreMatcher) ignores(relPath string) bool {
	for _, pattern := range ig.patterns {
		matched := matchIgnorePattern(relPath, pattern)
		if matched {
			return true
		}
	}
	return false
}

func matchIgnorePattern(path string, pattern string) bool {
	if strings.HasPrefix(pattern, "!") {
		return false // negation not handled in basic impl
	}
	// Simple glob matching
	matched, _ := filepath.Match(pattern, path)
	return matched
}

func prefixIgnorePattern(line string, prefix string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "\\#") {
		return ""
	}

	pattern := line
	negated := false

	if strings.HasPrefix(pattern, "!") {
		negated = true
		pattern = pattern[1:]
	} else if strings.HasPrefix(pattern, "\\!") {
		pattern = pattern[1:]
	}

	pattern = strings.TrimPrefix(pattern, "/")

	var prefixed string
	if prefix != "" {
		prefixed = prefix + pattern
	} else {
		prefixed = pattern
	}

	if negated {
		return "!" + prefixed
	}
	return prefixed
}

func addIgnoreRules(ig *ignoreMatcher, dir string, rootDir string) {
	relDir, _ := filepath.Rel(rootDir, dir)
	relDir = toPosixPath(relDir)
	prefix := ""
	if relDir != "" && relDir != "." {
		prefix = relDir + "/"
	}

	for _, filename := range ignoreFileNames {
		ignorePath := filepath.Join(dir, filename)
		data, err := os.ReadFile(ignorePath)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		var patterns []string
		for _, line := range lines {
			p := prefixIgnorePattern(line, prefix)
			if p != "" {
				patterns = append(patterns, p)
			}
		}
		if len(patterns) > 0 {
			ig.add(patterns)
		}
	}
}

// ============================================================================
// Pattern helpers
// ============================================================================

func isPattern(s string) bool {
	return strings.HasPrefix(s, "!") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") ||
		strings.Contains(s, "*") || strings.Contains(s, "?")
}

func isOverridePattern(s string) bool {
	return strings.HasPrefix(s, "!") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-")
}

func hasGlobPattern(s string) bool {
	return strings.Contains(s, "*") || strings.Contains(s, "?")
}

func splitPatterns(entries []string) (plain []string, patterns []string) {
	for _, entry := range entries {
		if isPattern(entry) {
			patterns = append(patterns, entry)
		} else {
			plain = append(plain, entry)
		}
	}
	return
}

// ============================================================================
// File collection
// ============================================================================

func collectFiles(dir string, filePattern *regexp.Regexp, skipNodeModules bool, ig *ignoreMatcher, rootDir string) []string {
	var files []string
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return files
	}

	root := rootDir
	if root == "" {
		root = dir
	}
	if ig == nil {
		ig = newIgnoreMatcher()
	}
	addIgnoreRules(ig, dir, root)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if skipNodeModules && entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		isFile := !entry.IsDir()

		if entry.Type()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stat.IsDir()
			isFile = !stat.IsDir()
		}

		relPath := toPosixPath(relPathFrom(root, fullPath))
		ignoreCheck := relPath
		if isDir {
			ignoreCheck = relPath + "/"
		}
		if ig.ignores(ignoreCheck) {
			continue
		}

		if isDir {
			files = append(files, collectFiles(fullPath, filePattern, skipNodeModules, ig, root)...)
		} else if isFile && filePattern.MatchString(entry.Name()) {
			files = append(files, fullPath)
		}
	}

	return files
}

// ============================================================================
// Skill collection
// ============================================================================

type skillDiscoveryMode string

const (
	skillModePi     skillDiscoveryMode = "pi"
	skillModeAgents skillDiscoveryMode = "agents"
)

func collectSkillEntries(dir string, mode skillDiscoveryMode, ig *ignoreMatcher, rootDir string) []string {
	var entries []string
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries
	}

	root := rootDir
	if root == "" {
		root = dir
	}
	if ig == nil {
		ig = newIgnoreMatcher()
	}
	addIgnoreRules(ig, dir, root)

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	// Check for SKILL.md in current dir
	for _, entry := range dirEntries {
		if entry.Name() != "SKILL.md" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		isFile := !entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = !stat.IsDir()
		}
		relPath := toPosixPath(relPathFrom(root, fullPath))
		if isFile && !ig.ignores(relPath) {
			entries = append(entries, fullPath)
			return entries
		}
	}

	// Recurse into subdirectories
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		isFile := !entry.IsDir()

		if entry.Type()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stat.IsDir()
			isFile = !stat.IsDir()
		}

		relPath := toPosixPath(relPathFrom(root, fullPath))
		if mode == skillModePi && dir == root && isFile && strings.HasSuffix(entry.Name(), ".md") && !ig.ignores(relPath) {
			entries = append(entries, fullPath)
			continue
		}

		if !isDir {
			continue
		}
		if ig.ignores(relPath + "/") {
			continue
		}

		entries = append(entries, collectSkillEntries(fullPath, mode, ig, root)...)
	}

	return entries
}

func collectAutoSkillEntries(dir string, mode skillDiscoveryMode) []string {
	return collectSkillEntries(dir, mode, nil, "")
}

// ============================================================================
// Auto-discovery helpers
// ============================================================================

func findGitRepoRoot(startDir string) string {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func collectAncestorAgentsSkillDirs(startDir string) []string {
	var skillDirs []string
	resolvedStartDir, _ := filepath.Abs(startDir)
	gitRepoRoot := findGitRepoRoot(resolvedStartDir)

	dir := resolvedStartDir
	for {
		skillDirs = append(skillDirs, filepath.Join(dir, ".agents", "skills"))
		if gitRepoRoot != "" && dir == gitRepoRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return skillDirs
}

func collectAutoPromptEntries(dir string) []string {
	var entries []string
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries
	}

	ig := newIgnoreMatcher()
	addIgnoreRules(ig, dir, dir)

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isFile := !entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = !stat.IsDir()
		}

		relPath := toPosixPath(relPathFrom(dir, fullPath))
		if ig.ignores(relPath) {
			continue
		}

		if isFile && strings.HasSuffix(entry.Name(), ".md") {
			entries = append(entries, fullPath)
		}
	}

	return entries
}

func collectAutoThemeEntries(dir string) []string {
	var entries []string
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries
	}

	ig := newIgnoreMatcher()
	addIgnoreRules(ig, dir, dir)

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isFile := !entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = !stat.IsDir()
		}

		relPath := toPosixPath(relPathFrom(dir, fullPath))
		if ig.ignores(relPath) {
			continue
		}

		if isFile && strings.HasSuffix(entry.Name(), ".json") {
			entries = append(entries, fullPath)
		}
	}

	return entries
}

// ============================================================================
// Extension resolution
// ============================================================================

func readPiManifestFile(packageJsonPath string) *YcodeManifest {
	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return nil
	}
	var pkg struct {
		Ycode *YcodeManifest `json:"ycode"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Ycode
}

func resolveExtensionEntries(dir string) []string {
	packageJsonPath := filepath.Join(dir, "package.json")
	if _, err := os.Stat(packageJsonPath); err == nil {
		manifest := readPiManifestFile(packageJsonPath)
		if manifest != nil && len(manifest.Extensions) > 0 {
			var entries []string
			for _, extPath := range manifest.Extensions {
				resolvedExtPath := filepath.Join(dir, extPath)
				if _, err := os.Stat(resolvedExtPath); err == nil {
					entries = append(entries, resolvedExtPath)
				}
			}
			if len(entries) > 0 {
				return entries
			}
		}
	}

	indexTs := filepath.Join(dir, "index.ts")
	indexJs := filepath.Join(dir, "index.js")
	if _, err := os.Stat(indexTs); err == nil {
		return []string{indexTs}
	}
	if _, err := os.Stat(indexJs); err == nil {
		return []string{indexJs}
	}

	return nil
}

func collectAutoExtensionEntries(dir string) []string {
	var entries []string
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries
	}

	// Check if this directory itself has explicit extension entries
	rootEntries := resolveExtensionEntries(dir)
	if rootEntries != nil {
		return rootEntries
	}

	ig := newIgnoreMatcher()
	addIgnoreRules(ig, dir, dir)

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		isFile := !entry.IsDir()

		if entry.Type()&os.ModeSymlink != 0 {
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stat.IsDir()
			isFile = !stat.IsDir()
		}

		relPath := toPosixPath(relPathFrom(dir, fullPath))
		ignoreCheck := relPath
		if isDir {
			ignoreCheck = relPath + "/"
		}
		if ig.ignores(ignoreCheck) {
			continue
		}

		if isFile && (strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".js")) {
			entries = append(entries, fullPath)
		} else if isDir {
			resolvedEntries := resolveExtensionEntries(fullPath)
			if resolvedEntries != nil {
				entries = append(entries, resolvedEntries...)
			}
		}
	}

	return entries
}

// ============================================================================
// Resource file collection dispatcher
// ============================================================================

var filePatterns = map[ResourceType]*regexp.Regexp{
	ResourceExtensions: regexp.MustCompile(`\.(ts|js)$`),
	ResourceSkills:     regexp.MustCompile(`\.md$`),
	ResourcePrompts:    regexp.MustCompile(`\.md$`),
	ResourceThemes:     regexp.MustCompile(`\.json$`),
}

func collectResourceFiles(dir string, resourceType ResourceType) []string {
	if resourceType == ResourceSkills {
		return collectSkillEntries(dir, skillModePi, nil, "")
	}
	if resourceType == ResourceExtensions {
		return collectAutoExtensionEntries(dir)
	}
	return collectFiles(dir, filePatterns[resourceType], true, nil, "")
}

// ============================================================================
// Pattern matching
// ============================================================================

func matchesAnyPattern(filePath string, patterns []string, baseDir string) bool {
	rel := toPosixPath(relPathFrom(baseDir, filePath))
	name := filepath.Base(filePath)
	filePathPosix := toPosixPath(filePath)
	isSkillFile := name == "SKILL.md"

	var parentRel, parentDirPosix string
	if isSkillFile {
		parentDir := filepath.Dir(filePath)
		parentRel = toPosixPath(relPathFrom(baseDir, parentDir))
		parentDirPosix = toPosixPath(parentDir)
	}

	for _, pattern := range patterns {
		normalized := toPosixPath(pattern)
		if simpleMatch(rel, normalized) || simpleMatch(name, normalized) || simpleMatch(filePathPosix, normalized) {
			return true
		}
		if !isSkillFile {
			continue
		}
		if simpleMatch(parentRel, normalized) || simpleMatch(filepath.Base(filepath.Dir(filePath)), normalized) || simpleMatch(parentDirPosix, normalized) {
			return true
		}
	}
	return false
}

func normalizeExactPattern(pattern string) string {
	normalized := pattern
	if strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, ".\\") {
		normalized = normalized[2:]
	}
	return toPosixPath(normalized)
}

func matchesAnyExactPattern(filePath string, patterns []string, baseDir string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel := toPosixPath(relPathFrom(baseDir, filePath))
	filePathPosix := toPosixPath(filePath)
	name := filepath.Base(filePath)
	isSkillFile := name == "SKILL.md"

	var parentRel, parentDirPosix string
	if isSkillFile {
		parentDir := filepath.Dir(filePath)
		parentRel = toPosixPath(relPathFrom(baseDir, parentDir))
		parentDirPosix = toPosixPath(parentDir)
	}

	for _, pattern := range patterns {
		normalized := normalizeExactPattern(pattern)
		if normalized == rel || normalized == filePathPosix {
			return true
		}
		if !isSkillFile {
			continue
		}
		if normalized == parentRel || normalized == parentDirPosix {
			return true
		}
	}
	return false
}

func getOverridePatterns(entries []string) []string {
	var overrides []string
	for _, p := range entries {
		if isOverridePattern(p) {
			overrides = append(overrides, p)
		}
	}
	return overrides
}

func isEnabledByOverrides(filePath string, patterns []string, baseDir string) bool {
	overrides := getOverridePatterns(patterns)
	var excludes, forceIncludes, forceExcludes []string
	for _, p := range overrides {
		if strings.HasPrefix(p, "!") {
			excludes = append(excludes, p[1:])
		} else if strings.HasPrefix(p, "+") {
			forceIncludes = append(forceIncludes, p[1:])
		} else if strings.HasPrefix(p, "-") {
			forceExcludes = append(forceExcludes, p[1:])
		}
	}

	enabled := true
	if len(excludes) > 0 && matchesAnyPattern(filePath, excludes, baseDir) {
		enabled = false
	}
	if len(forceIncludes) > 0 && matchesAnyExactPattern(filePath, forceIncludes, baseDir) {
		enabled = true
	}
	if len(forceExcludes) > 0 && matchesAnyExactPattern(filePath, forceExcludes, baseDir) {
		enabled = false
	}
	return enabled
}

func applyPatterns(allPaths []string, patterns []string, baseDir string) map[string]bool {
	var includes, excludes, forceIncludes, forceExcludes []string

	for _, p := range patterns {
		if strings.HasPrefix(p, "+") {
			forceIncludes = append(forceIncludes, p[1:])
		} else if strings.HasPrefix(p, "-") {
			forceExcludes = append(forceExcludes, p[1:])
		} else if strings.HasPrefix(p, "!") {
			excludes = append(excludes, p[1:])
		} else {
			includes = append(includes, p)
		}
	}

	// Step 1: Apply includes
	var result []string
	if len(includes) == 0 {
		result = make([]string, len(allPaths))
		copy(result, allPaths)
	} else {
		for _, fp := range allPaths {
			if matchesAnyPattern(fp, includes, baseDir) {
				result = append(result, fp)
			}
		}
	}

	// Step 2: Apply excludes
	if len(excludes) > 0 {
		filtered := result[:0]
		for _, fp := range result {
			if !matchesAnyPattern(fp, excludes, baseDir) {
				filtered = append(filtered, fp)
			}
		}
		result = filtered
	}

	// Step 3: Force-include
	if len(forceIncludes) > 0 {
		for _, fp := range allPaths {
			found := false
			for _, r := range result {
				if r == fp {
					found = true
					break
				}
			}
			if !found && matchesAnyExactPattern(fp, forceIncludes, baseDir) {
				result = append(result, fp)
			}
		}
	}

	// Step 4: Force-exclude
	if len(forceExcludes) > 0 {
		filtered := result[:0]
		for _, fp := range result {
			if !matchesAnyExactPattern(fp, forceExcludes, baseDir) {
				filtered = append(filtered, fp)
			}
		}
		result = filtered
	}

	set := make(map[string]bool, len(result))
	for _, fp := range result {
		set[fp] = true
	}
	return set
}

// ============================================================================
// Precedence
// ============================================================================

func resourcePrecedenceRank(m PathMetadata) int {
	if m.Origin == "package" {
		return 4
	}
	scopeBase := 2
	if m.Scope == "project" {
		scopeBase = 0
	}
	sourceOffset := 1
	if m.Source == "local" {
		sourceOffset = 0
	}
	return scopeBase + sourceOffset
}

// ============================================================================
// Git URL parsing (basic, no hosted-git-info)
// ============================================================================

func splitGitRef(url string) (repo string, ref string) {
	// SCP-like: git@host:path@ref
	scpMatch := regexp.MustCompile(`^git@([^:]+):(.+)$`).FindStringSubmatch(url)
	if len(scpMatch) > 0 {
		pathWithMaybeRef := scpMatch[2]
		refSep := strings.Index(pathWithMaybeRef, "@")
		if refSep < 0 {
			return url, ""
		}
		repoPath := pathWithMaybeRef[:refSep]
		ref = pathWithMaybeRef[refSep+1:]
		if repoPath == "" || ref == "" {
			return url, ""
		}
		return fmt.Sprintf("git@%s:%s", scpMatch[1], repoPath), ref
	}

	// URL-like: https://...@ref
	if strings.Contains(url, "://") {
		parts := strings.SplitN(url, "://", 2)
		if len(parts) == 2 {
			slashIdx := strings.Index(parts[1], "/")
			if slashIdx >= 0 {
				pathPart := parts[1][slashIdx+1:]
				refSep := strings.Index(pathPart, "@")
				if refSep >= 0 {
					repoPath := pathPart[:refSep]
					ref = pathPart[refSep+1:]
					if repoPath != "" && ref != "" {
						return fmt.Sprintf("%s://%s/%s", parts[0], parts[1][:slashIdx], repoPath), ref
					}
				}
			}
		}
		return url, ""
	}

	// Shorthand: host/path@ref
	slashIdx := strings.Index(url, "/")
	if slashIdx < 0 {
		return url, ""
	}
	pathWithMaybeRef := url[slashIdx+1:]
	refSep := strings.Index(pathWithMaybeRef, "@")
	if refSep < 0 {
		return url, ""
	}
	repoPath := pathWithMaybeRef[:refSep]
	ref = pathWithMaybeRef[refSep+1:]
	if repoPath == "" || ref == "" {
		return url, ""
	}
	return url[:slashIdx+1] + repoPath, ref
}

// ParseGitUrl parses an extension source string as a git URL (SSH/HTTPS/git
// protocol/shorthand). Returns nil when source is not a git source. Exported so
// the autocomplete source-tag formatter can classify git-installed resources.
func ParseGitUrl(source string) *ParsedSource {
	trimmed := strings.TrimSpace(source)
	hasGitPrefix := strings.HasPrefix(trimmed, "git:")
	url := trimmed
	if hasGitPrefix {
		url = strings.TrimSpace(trimmed[4:])
	}

	if !hasGitPrefix && !regexp.MustCompile(`^(https?|ssh|git)://`).MatchString(url) {
		return nil
	}

	repoWithoutRef, ref := splitGitRef(url)
	repo := repoWithoutRef
	host := ""
	path := ""

	// SCP-like
	scpMatch := regexp.MustCompile(`^git@([^:]+):(.+)$`).FindStringSubmatch(repoWithoutRef)
	if len(scpMatch) > 0 {
		host = scpMatch[1]
		path = scpMatch[2]
	} else if strings.HasPrefix(repoWithoutRef, "https://") || strings.HasPrefix(repoWithoutRef, "http://") ||
		strings.HasPrefix(repoWithoutRef, "ssh://") || strings.HasPrefix(repoWithoutRef, "git://") {
		// Parse as URL
		urlStr := repoWithoutRef
		protoEnd := strings.Index(urlStr, "://")
		if protoEnd >= 0 {
			rest := urlStr[protoEnd+3:]
			slashIdx := strings.Index(rest, "/")
			if slashIdx >= 0 {
				host = rest[:slashIdx]
				path = rest[slashIdx+1:]
			}
		}
	} else {
		// Shorthand: host/path
		slashIdx := strings.Index(repoWithoutRef, "/")
		if slashIdx < 0 {
			return nil
		}
		host = repoWithoutRef[:slashIdx]
		path = repoWithoutRef[slashIdx+1:]
		if !strings.Contains(host, ".") && host != "localhost" {
			return nil
		}
		repo = "https://" + repoWithoutRef
	}

	normalizedPath := strings.TrimSuffix(strings.TrimPrefix(path, "/"), ".git")
	if host == "" || normalizedPath == "" || len(strings.Split(normalizedPath, "/")) < 2 {
		return nil
	}

	return &ParsedSource{
		Type:   ParsedSourceGit,
		Pinned: ref != "",
		Git: &GitSource{
			Host: host,
			Path: normalizedPath,
			Repo: repo,
			Ref:  ref,
		},
	}
}

// ============================================================================
// Helpers
// ============================================================================

func relPathFrom(base string, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

// simpleMatch is a basic glob matcher (* and ? support only).
// For full minimatch compatibility, use github.com/bmatcuk/doublestar.
func simpleMatch(s string, pattern string) bool {
	matched, err := filepath.Match(pattern, s)
	if err != nil {
		return false
	}
	return matched
}

/* KEYFLAGS:
 1. ignoreMatcher is basic: Only handles simple patterns, not full .gitignore spec. 
 For production, swap with github.com/sabhiram/go-gitignore. First iteration is fine though.

 2. simpleMatch uses filepath.Match: Only supports * and ?, not ** (doublestar). TS uses 
 minimatch which supports **. Swap with doublestar library if extensions use ** patterns.

 3. parseGitUrl skips hosted-git-info: Handles SSH, HTTPS, git protocol, and shorthand formats. 
 May miss some edge cases that the npm hosted-git-info catches (Bitbucket URLs with # instead of 
 @ for refs, etc.).

 4. No external dependencies: Everything is self-contained. No third-party Go libs needed for first 
 iteration.
*/