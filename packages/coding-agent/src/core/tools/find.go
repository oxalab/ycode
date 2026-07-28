package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

const defaultFindLimit = 1000

type FindToolInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
}

type FindToolDetails struct {
	Truncation         *TruncationResult `json:"truncation,omitempty"`
	ResultLimitReached int               `json:"resultLimitReached,omitempty"`
}

// FindOperations: Defines pluggable operations for the find tool.
type FindOperations interface {
	Exists(absolutePath string) bool
	Glob(pattern string, cwd string, ignore []string, limit int) []string
}

type defaultFindOps struct{}

func (d defaultFindOps) Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (d defaultFindOps) Glob(pattern, searchPath string, ignore []string, limit int) []string {
	var results []string
	filepath.WalkDir(searchPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			// Skip ignored Directories
			if shouldSkipDir(entry.Name(), ignore) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(searchPath, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel) {
			results = append(results, rel)
		}
		if len(results) >= limit {
			return fmt.Errorf("limit reached")
		}
		return nil
	})
	return results
}

func shouldSkipDir(name string, ignore []string) bool {
	for _, pattern := range ignore {
		// Extract Directory name for patterns like "**/node_modules/**"
		parts := strings.Split(pattern, "/")
		for _, part := range parts {
			if part == "**" || part == "*" {
				continue
			}
			matched, _ := filepath.Match(part, name)
			if matched {
				return true
			}
		}
	}
	return false
}

/*
globMatch: Matches a glob pattern against a relative path.
 - Supports *, ? and ** for recursive matching.
*/
func globMatch(pattern, relPath string) bool {
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	// No slashes and no ** = match basename only
	if !strings.Contains(pattern, "/") && !strings.Contains(pattern, "**") {
		matched, _ := filepath.Match(pattern, filepath.Base(relPath))
		return matched
	}

	// Full path matching with ** support
	return doublestarMatch(pattern, relPath)
}

func doublestarMatch(pattern, path string) bool {
	starIdx := strings.Index(pattern, "**")
	if starIdx == -1 {
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	prefix := pattern[:starIdx]
	suffix := pattern[starIdx+2:]
	suffix = strings.TrimPrefix(suffix, "/")

	// Match prefix
	if prefix != "" {
		prefix = strings.TrimSuffix(prefix, "/")
		if !strings.HasPrefix(path, prefix+"/") && path != prefix {
			return false
		}
		if strings.HasPrefix(path, prefix+"/") {
			path = path[len(prefix)+1:]
		} else {
			path = ""
		}
	}

	// Try matching suffix at every path depth
	if suffix == "" {
		return true
	}
	
	for {
		if doublestarMatch(suffix, path) {
			return true
		}
		idx := strings.Index(path, "/")
		if idx == -1 {
			break
		}
		path = path[idx+1:]
	}
	return false
}

type FindToolOptions struct {
	Operations 	FindOperations
}

func toPosixPath(value string) string {
	return filepath.ToSlash(value)
}

func CreateFindToolDefinition(cwd string, opts *FindToolOptions) *extensions.ToolDefinition {
	customOps := (*FindOperations)(nil)
	if opts != nil {
		customOps = &opts.Operations
	}

	return &extensions.ToolDefinition{
		Name: 		"find",
		Label: 		"find",
		Description:   fmt.Sprintf("Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to %d results or %dKB (whichever is hit first).", defaultFindLimit, DefaultMaxBytes/1024),
		PromptSnippet: "Find files by glob pattern (respects .gitignore)",
		Parameters: objectSchema(map[string]prop{
			"pattern": {typ: propString, description: "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"},
			"path":    {typ: propString, description: "Directory to search in (default: current directory)"},
			"limit":   {typ: propInteger, description: "Maximum number of results (default: 1000)"},
		}, []string{"pattern"}),
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			var input FindToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			searchPath := ResolveToCwd(".", cwd)
			if input.Path != "" && &input.Path != nil {
				searchPath = ResolveToCwd(*&input.Path, cwd)
			}
			effectiveLimit := defaultFindLimit
			if input.Limit != nil {
				effectiveLimit = *input.Limit
			}
			// Use custom operations or default
			var results []string
			ignore := []string{"**/node_modules/**", "**/.git/**"}

			if customOps != nil && *customOps != nil {
				ops := *customOps
				if !ops.Exists(searchPath) {
					return nil, fmt.Errorf("path not found: %s", searchPath)
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				results = ops.Glob(input.Pattern, searchPath, ignore, effectiveLimit)
			} else {
				// Try fd command first, fall back to WalkDir
				results = runFdOrFallback(input.Pattern, searchPath, ignore, effectiveLimit, ctx)
			}

			select{
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			
			if len(results) == 0 {
				return &ToolExecuteResult{
					Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "No files found matching pattern"}},
				}, nil
			}

			// Relativize paths
			relativized := make([]string, 0, len(results))
			for _, p := range results {
				p = strings.TrimRight(p, "/r")
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				if filepath.IsAbs(p) && strings.HasPrefix(p, searchPath) {
					p = p[len(searchPath)+1:]
				} else if filepath.IsAbs(p) {
					rel, _ := filepath.Rel(searchPath, p)
					p = rel
				}
				relativized = append(relativized, toPosixPath(p))
			}
			
			resultLimitReached := len(relativized) >= effectiveLimit
			rawOutput := strings.Join(relativized, "\n")
			truncation :=  TruncateHead(rawOutput, TruncationOptions{})
			resultOutput := truncation.Content

			details := &FindToolDetails{}
			var notices []string
			if resultLimitReached {
				notices = append(notices, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit*2))
				details.ResultLimitReached = effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				details.Truncation = &truncation
			}
			if len(notices) > 0 {
				resultOutput += "\n\n[" + strings.Join(notices, ". ") + "]" 
			}

			toolDetails := any(nil)
			if details.ResultLimitReached > 0 || details.Truncation != nil {
				toolDetails = details
			}

			return &ToolExecuteResult{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: resultOutput}},
				Details: toolDetails,
			}, nil
		},
		RenderCall: func(args any) any {
			argsMap, _ := args.(map[string]any)
			pattern := "?"
			if p, ok := argsMap["pattern"].(string); ok {
				pattern = p
			}
			searchDir := "."
			if p, ok := argsMap["path"].(string); ok && p != "" {
				searchDir = ShortenPath(p)
			}
			text := fmt.Sprintf("find %s in %s", pattern, searchDir)
			if limit, ok := argsMap["limit"].(float64); ok {
				text += fmt.Sprintf(" (limit %d)", int(limit))
			}
			return text
		},
		RenderResult: func(args any, result any, options any) any {
			res, ok := result.(*ToolExecuteResult)
			if !ok || len(res.Content) == 0 {
				return ""
			}
			tc, ok := res.Content[0].(*llm.TextContent)
			if !ok {
				return ""
			}
			return tc.Text
		},
	}
}

/*
runFdOrFallback: Tries to use the fd command for glob matching.
 - Falls back to WalkDir-based implementation if fd is not available.
*/
func runFdOrFallback(pattern, searchPath string, ignore []string, limit int, ctx context.Context) []string {
	// Check if fd is available
	fdPath, err := exec.LookPath("fd")
	if err != nil || fdPath == "" {
		return defaultFindOps{}.Glob(pattern, searchPath, ignore, limit)
	}

	// Build fd arguments (mirrors TS implementations)
	args := []string{
		"--glob", "--color=never", "--hidden",
		"--no-require-git", "--max-results", fmt.Sprintf("%d", limit),
	}

	effectivePattern := pattern
	if strings.Contains(pattern, "/") {
		args = append(args, "--full-path")
		if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "**/") && pattern != "**" {
			effectivePattern = "**/*" + pattern
		}
	}
	args = append(args, "--", effectivePattern, searchPath)

	cmd := exec.CommandContext(ctx, fdPath, args...)
	output, err := cmd.Output()
	if err != nil {
		// Falll back to WalkDir on fd failure
		return defaultFindOps{}.Glob(pattern, searchPath, ignore, limit)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func CreateFindTool(cwd string, opts *FindToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateFindToolDefinition(cwd, opts), nil)
}



/*
FOOTNOTE:
 1. Default glob implementation doesn't respect .gitignore — TS uses fd 
 --no-require-git which applies hierarchical .gitignore semantics. Our WalkDir 
 fallback skips node_modules and .git but does NOT parse .gitignore files. If fd 
 is on PATH, it's used preferentially (same as TS). Without fd, .gitignore is not 
 respected.

 2. ensureTool("fd") not ported — TS auto-downloads fd if missing. Our version uses 
 exec.LookPath("fd") and falls back to WalkDir if not found. The auto-download 
 functionality from tools-manager.ts would need porting separately.

 3. ** glob matching is simplified — The doublestarMatch function handles the 
 first ** in a pattern. Patterns with multiple ** segments (e.g., .test.ts) won't 
 match correctly. The fd path handles these correctly.

 4. TS truncateHead uses maxLines: Number.MAX_SAFE_INTEGER (byte-only truncation) 
 — Same issue as ls.go. Our version applies a 2000-line default limit where TS 
 doesn't.
*/