package tui

// autocomplete.go ports reference/tui/src/autocomplete.ts — the
// CombinedAutocompleteProvider handling slash commands, @file fuzzy search (via
// fd), and readdir path completion. See mapping/11-autocomplete.md.
//
// Divergence from the reference: when `fd` is not installed, `@file` falls back
// to an in-Go walker (getFuzzyFileSuggestionsViaWalk) so the feature works out
// of the box for new users. The reference disables `@` entirely when `fd` is
// missing. The walker is slower than `fd` on huge trees and uses a hardcoded
// ignore list rather than parsing .gitignore, but it produces the same
// AutocompleteItem shape and ranking.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AutocompleteItem is one suggestion.
type AutocompleteItem struct {
	Value       string
	Label       string
	Description string
}

// SlashCommand is a command with optional argument completions.
type SlashCommand struct {
	Name              string
	Description       string
	ArgumentHint      string
	GetArgumentCompletions func(prefix string) ([]AutocompleteItem, bool)
}

// AutocompleteSuggestions is the result of a GetSuggestions call.
type AutocompleteSuggestions struct {
	Items  []AutocompleteItem
	Prefix string
}

// AutocompleteProvider supplies suggestions and applies completions.
type AutocompleteProvider interface {
	TriggerCharacters() []string
	GetSuggestions(lines []string, cursorLine, cursorCol int, ctx context.Context, force bool) (*AutocompleteSuggestions, bool)
	ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int)
	ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool
}

// CommandSource is either a SlashCommand or a plain AutocompleteItem.
type CommandSource struct {
	Slash *SlashCommand
	Item  *AutocompleteItem
}

var pathDelimiters = map[byte]bool{' ': true, '\t': true, '"': true, '\'': true, '=': true}

// CombinedAutocompleteProvider handles slash commands and file paths.
type CombinedAutocompleteProvider struct {
	commands []CommandSource
	basePath string
	fdPath   string
}

// NewCombinedAutocompleteProvider returns a provider. If fdPath is "" it auto-detects.
func NewCombinedAutocompleteProvider(commands []CommandSource, basePath, fdPath string) *CombinedAutocompleteProvider {
	if fdPath == "" {
		if p, err := exec.LookPath("fd"); err == nil {
			fdPath = p
		}
	}
	return &CombinedAutocompleteProvider{commands: commands, basePath: basePath, fdPath: fdPath}
}

// TriggerCharacters returns the characters that naturally trigger this provider.
func (p *CombinedAutocompleteProvider) TriggerCharacters() []string {
	return []string{"@", "#"}
}

// ShouldTriggerFileCompletion reports whether Tab should trigger file completion.
func (p *CombinedAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool {
	currentLine := ""
	if cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	beforeCursor := currentLine[:minIntAC(cursorCol, len(currentLine))]
	trimmed := strings.TrimSpace(beforeCursor)
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		return false
	}
	return true
}

// GetSuggestions produces suggestions for the current cursor position.
func (p *CombinedAutocompleteProvider) GetSuggestions(lines []string, cursorLine, cursorCol int, ctx context.Context, force bool) (*AutocompleteSuggestions, bool) {
	currentLine := ""
	if cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	beforeCursor := currentLine[:minIntAC(cursorCol, len(currentLine))]

	// @ prefix → fuzzy file search.
	if atPrefix := extractAtPrefix(beforeCursor); atPrefix != "" {
		rawPrefix, _, isQuoted := parsePathPrefix(atPrefix)
		suggestions := p.getFuzzyFileSuggestions(rawPrefix, isQuoted, ctx)
		if len(suggestions) == 0 {
			return nil, false
		}
		return &AutocompleteSuggestions{Items: suggestions, Prefix: atPrefix}, true
	}

	// /command at line start (no space).
	if !force && strings.HasPrefix(beforeCursor, "/") {
		spaceIdx := strings.Index(beforeCursor, " ")
		if spaceIdx == -1 {
			prefix := beforeCursor[1:]
			items := p.commandItems()
			filtered := FuzzyFilter(items, prefix, func(item AutocompleteItem) string { return item.Value })
			var result []AutocompleteItem
			for _, item := range filtered {
				result = append(result, AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description})
			}
			if len(result) == 0 {
				return nil, false
			}
			return &AutocompleteSuggestions{Items: result, Prefix: beforeCursor}, true
		}
		// /command <arg>
		commandName := beforeCursor[1:spaceIdx]
		argumentText := beforeCursor[spaceIdx+1:]
		cmd := p.findCommand(commandName)
		if cmd == nil || cmd.Slash == nil || cmd.Slash.GetArgumentCompletions == nil {
			return nil, false
		}
		argSuggestions, ok := cmd.Slash.GetArgumentCompletions(argumentText)
		if !ok || len(argSuggestions) == 0 {
			return nil, false
		}
		return &AutocompleteSuggestions{Items: argSuggestions, Prefix: argumentText}, true
	}

	// Path prefix.
	pathMatch := extractPathPrefix(beforeCursor, force)
	if pathMatch == "" && !force {
		return nil, false
	}
	if pathMatch == "" && force {
		pathMatch = lastToken(beforeCursor)
	}
	suggestions := p.getFileSuggestions(pathMatch)
	if len(suggestions) == 0 {
		return nil, false
	}
	return &AutocompleteSuggestions{Items: suggestions, Prefix: pathMatch}, true
}

// ApplyCompletion inserts item at the prefix position.
func (p *CombinedAutocompleteProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	currentLine := ""
	if cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	beforePrefix := ""
	if cursorCol >= len(prefix) {
		beforePrefix = currentLine[:cursorCol-len(prefix)]
	}
	afterCursor := currentLine[cursorCol:]
	isQuotedPrefix := strings.HasPrefix(prefix, "\"") || strings.HasPrefix(prefix, "@\"")
	hasTrailingQuoteInItem := strings.HasSuffix(item.Value, "\"")
	adjustedAfterCursor := afterCursor
	if isQuotedPrefix && hasTrailingQuoteInItem && strings.HasPrefix(afterCursor, "\"") {
		adjustedAfterCursor = afterCursor[1:]
	}

	isSlashCommand := strings.HasPrefix(prefix, "/") && strings.TrimSpace(beforePrefix) == "" && !strings.Contains(prefix[1:], "/")
	if isSlashCommand {
		newLine := beforePrefix + "/" + item.Value + " " + adjustedAfterCursor
		newLines := replaceLine(lines, cursorLine, newLine)
		return newLines, cursorLine, len(beforePrefix) + len(item.Value) + 2
	}

	if strings.HasPrefix(prefix, "@") {
		isDir := strings.HasSuffix(item.Label, "/")
		suffix := " "
		if isDir {
			suffix = ""
		}
		newLine := beforePrefix + item.Value + suffix + adjustedAfterCursor
		newLines := replaceLine(lines, cursorLine, newLine)
		cursorOffset := len(item.Value)
		if isDir && hasTrailingQuoteInItem {
			cursorOffset--
		}
		return newLines, cursorLine, len(beforePrefix) + cursorOffset + len(suffix)
	}

	// Command argument or plain path.
	newLine := beforePrefix + item.Value + adjustedAfterCursor
	newLines := replaceLine(lines, cursorLine, newLine)
	isDir := strings.HasSuffix(item.Label, "/")
	hasTrailingQuote := strings.HasSuffix(item.Value, "\"")
	cursorOffset := len(item.Value)
	if isDir && hasTrailingQuote {
		cursorOffset--
	}
	return newLines, cursorLine, len(beforePrefix) + cursorOffset
}

// --- Path helpers ---

func toDisplayPath(p string) string { return strings.ReplaceAll(p, "\\", "/") }

func findLastDelimiter(text string) int {
	for i := len(text) - 1; i >= 0; i-- {
		if pathDelimiters[text[i]] {
			return i
		}
	}
	return -1
}

func findUnclosedQuoteStart(text string) int {
	inQuote := false
	quoteStart := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			inQuote = !inQuote
			if inQuote {
				quoteStart = i
			}
		}
	}
	if inQuote {
		return quoteStart
	}
	return -1
}

func isTokenStart(text string, index int) bool {
	return index == 0 || pathDelimiters[text[index-1]]
}

func extractQuotedPrefix(text string) string {
	quoteStart := findUnclosedQuoteStart(text)
	if quoteStart < 0 {
		return ""
	}
	if quoteStart > 0 && text[quoteStart-1] == '@' {
		if !isTokenStart(text, quoteStart-1) {
			return ""
		}
		return text[quoteStart-1:]
	}
	if !isTokenStart(text, quoteStart) {
		return ""
	}
	return text[quoteStart:]
}

func parsePathPrefix(prefix string) (rawPrefix string, isAtPrefix, isQuotedPrefix bool) {
	if strings.HasPrefix(prefix, "@\"") {
		return prefix[2:], true, true
	}
	if strings.HasPrefix(prefix, "\"") {
		return prefix[1:], false, true
	}
	if strings.HasPrefix(prefix, "@") {
		return prefix[1:], true, false
	}
	return prefix, false, false
}

func buildCompletionValue(path string, isDir, isAt, isQuoted bool) string {
	needsQuotes := isQuoted || strings.Contains(path, " ")
	prefix := ""
	if isAt {
		prefix = "@"
	}
	if !needsQuotes {
		return prefix + path
	}
	return prefix + "\"" + path + "\""
}

func extractAtPrefix(text string) string {
	if quoted := extractQuotedPrefix(text); strings.HasPrefix(quoted, "@\"") {
		return quoted
	}
	lastDelim := findLastDelimiter(text)
	tokenStart := 0
	if lastDelim >= 0 {
		tokenStart = lastDelim + 1
	}
	if tokenStart < len(text) && text[tokenStart] == '@' {
		return text[tokenStart:]
	}
	return ""
}

func extractPathPrefix(text string, force bool) string {
	if quoted := extractQuotedPrefix(text); quoted != "" {
		return quoted
	}
	lastDelim := findLastDelimiter(text)
	pathPrefix := text
	if lastDelim >= 0 {
		pathPrefix = text[lastDelim+1:]
	}
	if force {
		return pathPrefix
	}
	if strings.Contains(pathPrefix, "/") || strings.HasPrefix(pathPrefix, ".") || strings.HasPrefix(pathPrefix, "~/") {
		return pathPrefix
	}
	if pathPrefix == "" && strings.HasSuffix(text, " ") {
		return pathPrefix
	}
	return ""
}

func lastToken(text string) string {
	lastDelim := findLastDelimiter(text)
	if lastDelim >= 0 {
		return text[lastDelim+1:]
	}
	return text
}

func expandHomePath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		expanded := filepath.Join(home, p[2:])
		if strings.HasSuffix(p, "/") && !strings.HasSuffix(expanded, "/") {
			expanded += "/"
		}
		return expanded
	}
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return home
	}
	return p
}

// --- File suggestions (readdir) ---

func (p *CombinedAutocompleteProvider) getFileSuggestions(prefix string) []AutocompleteItem {
	rawPrefix, isAt, isQuoted := parsePathPrefix(prefix)
	expandedPrefix := rawPrefix
	if strings.HasPrefix(expandedPrefix, "~") {
		expandedPrefix = expandHomePath(expandedPrefix)
	}

	isRootPrefix := rawPrefix == "" || rawPrefix == "./" || rawPrefix == "../" || rawPrefix == "~" ||
		rawPrefix == "~/" || rawPrefix == "/" || (isAt && rawPrefix == "")

	var searchDir, searchPrefix string
	if isRootPrefix {
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = expandedPrefix
		} else {
			searchDir = filepath.Join(p.basePath, expandedPrefix)
		}
		searchPrefix = ""
	} else if strings.HasSuffix(rawPrefix, "/") {
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = expandedPrefix
		} else {
			searchDir = filepath.Join(p.basePath, expandedPrefix)
		}
		searchPrefix = ""
	} else {
		dir := filepath.Dir(expandedPrefix)
		file := filepath.Base(expandedPrefix)
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = dir
		} else {
			searchDir = filepath.Join(p.basePath, dir)
		}
		searchPrefix = file
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var suggestions []AutocompleteItem
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(searchPrefix)) {
			continue
		}
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(searchDir, name)); err == nil {
				isDir = info.IsDir()
			}
		}
		var relPath string
		if strings.HasSuffix(rawPrefix, "/") {
			relPath = rawPrefix + name
		} else if strings.Contains(rawPrefix, "/") || strings.Contains(rawPrefix, "\\") {
			if strings.HasPrefix(rawPrefix, "~/") {
				dirPart := filepath.Dir(rawPrefix[2:])
				if dirPart == "." {
					relPath = "~/" + name
				} else {
					relPath = "~/" + filepath.Join(dirPart, name)
				}
			} else if strings.HasPrefix(rawPrefix, "/") {
				dirPart := filepath.Dir(rawPrefix)
				if dirPart == "/" {
					relPath = "/" + name
				} else {
					relPath = filepath.Join(dirPart, name)
				}
			} else {
				relPath = filepath.Join(filepath.Dir(rawPrefix), name)
				if strings.HasPrefix(rawPrefix, "./") && !strings.HasPrefix(relPath, "./") {
					relPath = "./" + relPath
				}
			}
		} else {
			if strings.HasPrefix(rawPrefix, "~") {
				relPath = "~/" + name
			} else {
				relPath = name
			}
		}
		relPath = toDisplayPath(relPath)
		pathValue := relPath
		if isDir {
			pathValue += "/"
		}
		value := buildCompletionValue(pathValue, isDir, isAt, isQuoted)
		suggestions = append(suggestions, AutocompleteItem{
			Value: value,
			Label: name + map[bool]string{true: "/", false: ""}[isDir],
		})
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		aDir := strings.HasSuffix(suggestions[i].Value, "/")
		bDir := strings.HasSuffix(suggestions[j].Value, "/")
		if aDir != bDir {
			return aDir
		}
		return suggestions[i].Label < suggestions[j].Label
	})
	return suggestions
}

// --- Fuzzy file suggestions (fd) ---

func (p *CombinedAutocompleteProvider) getFuzzyFileSuggestions(query string, isQuoted bool, ctx context.Context) []AutocompleteItem {
	if p.fdPath == "" {
		// Fallback: in-Go walker. Slower than fd but works with zero external
		// dependencies, so `@file` search works out of the box for new users.
		return p.getFuzzyFileSuggestionsViaWalk(query, isQuoted, ctx)
	}
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	fdBaseDir := p.basePath
	fdQuery := query
	displayBase := ""
	if scoped := resolveScopedFuzzyQuery(query, p.basePath); scoped != nil {
		fdBaseDir = scoped.baseDir
		fdQuery = scoped.query
		displayBase = scoped.displayBase
	}

	args := []string{
		"--base-directory", fdBaseDir,
		"--max-results", "100",
		"--type", "f", "--type", "d",
		"--follow", "--hidden",
		"--exclude", ".git", "--exclude", ".git/*", "--exclude", ".git/**",
	}
	if strings.Contains(toDisplayPath(fdQuery), "/") {
		args = append(args, "--full-path")
	}
	if fdQuery != "" {
		args = append(args, buildFdPathQuery(fdQuery))
	}

	cmd := exec.CommandContext(ctx, p.fdPath, args...)
 stdout, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	type scored struct {
		entry    struct{ path string; isDir bool }
		score    int
	}
	var scoredEntries []scored
	for _, line := range lines {
		if line == "" {
			continue
		}
		displayLine := toDisplayPath(line)
		hasTrailingSep := strings.HasSuffix(displayLine, "/")
		normalizedPath := displayLine
		if hasTrailingSep {
			normalizedPath = displayLine[:len(displayLine)-1]
		}
		if normalizedPath == ".git" || strings.HasPrefix(normalizedPath, ".git/") || strings.Contains(normalizedPath, "/.git/") {
			continue
		}
		s := 1
		if fdQuery != "" {
			s = scoreEntry(normalizedPath, fdQuery, hasTrailingSep)
		}
		if s > 0 {
			scoredEntries = append(scoredEntries, scored{struct{ path string; isDir bool }{displayLine, hasTrailingSep}, s})
		}
	}
	sort.SliceStable(scoredEntries, func(i, j int) bool { return scoredEntries[i].score > scoredEntries[j].score })
	if len(scoredEntries) > 20 {
		scoredEntries = scoredEntries[:20]
	}
	var suggestions []AutocompleteItem
	for _, se := range scoredEntries {
		pathWOslash := se.entry.path
		if se.entry.isDir {
			pathWOslash = se.entry.path[:len(se.entry.path)-1]
		}
		var displayPath string
		if displayBase != "" {
			displayPath = scopedPathForDisplay(displayBase, pathWOslash)
		} else {
			displayPath = pathWOslash
		}
		entryName := filepath.Base(pathWOslash)
		completionPath := displayPath
		if se.entry.isDir {
			completionPath += "/"
		}
		value := buildCompletionValue(completionPath, se.entry.isDir, true, isQuoted)
		suggestions = append(suggestions, AutocompleteItem{
			Value:       value,
			Label:       entryName + map[bool]string{true: "/", false: ""}[se.entry.isDir],
			Description: displayPath,
		})
	}
	return suggestions
}

// walkerIgnoreDirs is the hardcoded ignore list for the in-Go walker fallback.
// More aggressive than `fd`'s default (which only excludes `.git`) because
// `filepath.WalkDir` is sequential and significantly slower than `fd` on large
// trees. Skipping universally-unwanted dep/cache dirs keeps the fallback usable
// on typical projects (especially JS/Python where node_modules/__pycache__ can
// hold tens of thousands of files).
var walkerIgnoreDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"dist":          true,
	"build":         true,
	"target":        true,
	".next":         true,
	"out":           true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	".cache":        true,
	".turbo":        true,
	".pytest_cache": true,
	"coverage":      true,
}

// walkerMaxCollect caps how many candidates the walker collects before
// sorting/trimming. Bounding this keeps memory and time predictable on huge
// trees; we sort by score and keep only walkerMaxReturn anyway.
const (
	walkerMaxCollect = 2000
	walkerMaxReturn  = 20
	walkerTimeoutMs  = 200
)

// errWalkerStop is an internal sentinel used to short-circuit WalkDir once we
// have collected enough candidates.
var errWalkerStop = errors.New("stop walk")

// getFuzzyFileSuggestionsViaWalk is the in-Go fallback for `@file` search when
// `fd` is not installed. Walks the project tree from p.basePath (or a scoped
// subdirectory when the query contains a slash), applies the same scoring as
// the fd path, and returns AutocompleteItems in the same shape.
//
// Bounds: a 200ms timeout (walkerTimeoutMs) and a 2000-candidate collection
// cap (walkerMaxCollect) prevent UI freeze on enormous trees. On timeout, the
// partial result is returned — better to show something than nothing.
func (p *CombinedAutocompleteProvider) getFuzzyFileSuggestionsViaWalk(query string, isQuoted bool, ctx context.Context) []AutocompleteItem {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	walkBase := p.basePath
	walkQuery := query
	displayBase := ""
	if scoped := resolveScopedFuzzyQuery(query, p.basePath); scoped != nil {
		walkBase = scoped.baseDir
		walkQuery = scoped.query
		displayBase = scoped.displayBase
	}
	if info, err := os.Stat(walkBase); err != nil || !info.IsDir() {
		return nil
	}

	// Bound the walk so a huge tree can't freeze the UI. The editor calls
	// GetSuggestions with context.Background (no caller-side deadline), so the
	// walker enforces its own.
	walkCtx, cancel := context.WithTimeout(ctx, walkerTimeoutMs*time.Millisecond)
	defer cancel()

	queryHasSlash := strings.Contains(toDisplayPath(walkQuery), "/")
	lowerQuery := strings.ToLower(walkQuery)

	type scored struct {
		relPath string
		isDir   bool
		score   int
	}
	entries := make([]scored, 0, 64)

	walkErr := filepath.WalkDir(walkBase, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		select {
		case <-walkCtx.Done():
			return errWalkerStop
		default:
		}
		// Don't include the walk root itself as a candidate (mirrors fd, which
		// emits base-directory contents, not the base).
		if path == walkBase {
			return nil
		}
		name := d.Name()
		if d.IsDir() && walkerIgnoreDirs[name] {
			return filepath.SkipDir
		}

		rel, relErr := filepath.Rel(walkBase, path)
		if relErr != nil {
			return nil
		}
		relDisplay := toDisplayPath(rel)
		isDir := d.IsDir()

		// Pre-filter to avoid scoring every entry. The scoreEntry call below
		// refines this with the proper ranking.
		if walkQuery != "" {
			lowerRel := strings.ToLower(relDisplay)
			if queryHasSlash {
				if !strings.Contains(lowerRel, lowerQuery) {
					return nil
				}
			} else {
				lowerName := strings.ToLower(name)
				if !strings.Contains(lowerName, lowerQuery) && !strings.Contains(lowerRel, lowerQuery) {
					return nil
				}
			}
		}

		s := 1
		if walkQuery != "" {
			s = scoreEntry(relDisplay, walkQuery, isDir)
		}
		if s > 0 {
			entries = append(entries, scored{relDisplay, isDir, s})
			if len(entries) >= walkerMaxCollect {
				return errWalkerStop
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errWalkerStop) {
		return nil
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].score > entries[j].score })
	if len(entries) > walkerMaxReturn {
		entries = entries[:walkerMaxReturn]
	}

	suggestions := make([]AutocompleteItem, 0, len(entries))
	for _, e := range entries {
		var displayPath string
		if displayBase != "" {
			displayPath = scopedPathForDisplay(displayBase, e.relPath)
		} else {
			displayPath = e.relPath
		}
		entryName := filepath.Base(e.relPath)
		completionPath := displayPath
		if e.isDir {
			completionPath += "/"
		}
		value := buildCompletionValue(completionPath, e.isDir, true, isQuoted)
		suggestions = append(suggestions, AutocompleteItem{
			Value:       value,
			Label:       entryName + map[bool]string{true: "/", false: ""}[e.isDir],
			Description: displayPath,
		})
	}
	return suggestions
}

type scopedQuery struct {
	baseDir    string
	query      string
	displayBase string
}

func resolveScopedFuzzyQuery(rawQuery, basePath string) *scopedQuery {
	normalized := toDisplayPath(rawQuery)
	slashIdx := strings.LastIndex(normalized, "/")
	if slashIdx == -1 {
		return nil
	}
	displayBase := normalized[:slashIdx+1]
	query := normalized[slashIdx+1:]
	var baseDir string
	if strings.HasPrefix(displayBase, "~/") {
		baseDir = expandHomePath(displayBase)
	} else if strings.HasPrefix(displayBase, "/") {
		baseDir = displayBase
	} else {
		baseDir = filepath.Join(basePath, displayBase)
	}
	if info, err := os.Stat(baseDir); err != nil || !info.IsDir() {
		return nil
	}
	return &scopedQuery{baseDir: baseDir, query: query, displayBase: displayBase}
}

func scopedPathForDisplay(displayBase, relativePath string) string {
	normalized := toDisplayPath(relativePath)
	if displayBase == "/" {
		return "/" + normalized
	}
	return toDisplayPath(displayBase) + normalized
}

func buildFdPathQuery(query string) string {
	normalized := toDisplayPath(query)
	if !strings.Contains(normalized, "/") {
		return normalized
	}
	hasTrailingSep := strings.HasSuffix(normalized, "/")
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return normalized
	}
	separatorPattern := "[\\\\/]"
	segments := strings.Split(trimmed, "/")
	for i, s := range segments {
		segments[i] = escapeRegexAC(s)
	}
	pattern := strings.Join(segments, separatorPattern)
	if hasTrailingSep {
		pattern += separatorPattern
	}
	return pattern
}

func escapeRegexAC(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '.', '*', '+', '?', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func scoreEntry(filePath, query string, isDir bool) int {
	fileName := filepath.Base(filePath)
	lowerName := strings.ToLower(fileName)
	lowerQuery := strings.ToLower(query)
	score := 0
	if lowerName == lowerQuery {
		score = 100
	} else if strings.HasPrefix(lowerName, lowerQuery) {
		score = 80
	} else if strings.Contains(lowerName, lowerQuery) {
		score = 50
	} else if strings.Contains(strings.ToLower(filePath), lowerQuery) {
		score = 30
	}
	if isDir && score > 0 {
		score += 10
	}
	return score
}

// --- Command helpers ---

func (p *CombinedAutocompleteProvider) commandItems() []AutocompleteItem {
	var items []AutocompleteItem
	for _, cmd := range p.commands {
		if cmd.Slash != nil {
			hint := cmd.Slash.ArgumentHint
			desc := cmd.Slash.Description
			fullDesc := desc
			if hint != "" {
				if desc != "" {
					fullDesc = hint + " — " + desc
				} else {
					fullDesc = hint
				}
			}
			items = append(items, AutocompleteItem{Value: cmd.Slash.Name, Label: cmd.Slash.Name, Description: fullDesc})
		} else if cmd.Item != nil {
			items = append(items, *cmd.Item)
		}
	}
	return items
}

func (p *CombinedAutocompleteProvider) findCommand(name string) *CommandSource {
	for i, cmd := range p.commands {
		cmdName := ""
		if cmd.Slash != nil {
			cmdName = cmd.Slash.Name
		} else if cmd.Item != nil {
			cmdName = cmd.Item.Value
		}
		if cmdName == name {
			return &p.commands[i]
		}
	}
	return nil
}

// --- misc helpers ---

func replaceLine(lines []string, idx int, newLine string) []string {
	result := make([]string, len(lines))
	copy(result, lines)
	if idx >= 0 && idx < len(result) {
		result[idx] = newLine
	}
	return result
}

func minIntAC(a, b int) int {
	if a < b {
		return a
	}
	return b
}
