package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// FindOperations: Defines pluggable operations for the find tool (reference
// find.ts options.operations hook, used for remote backends). The default
// local path does NOT use a fallback glob — it runs fd via EnsureTool and
// hard-fails when fd is unavailable, like the reference.
type FindOperations interface {
	Exists(absolutePath string) bool
	Glob(pattern string, cwd string, ignore []string, limit int) []string
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
				// Default path: fd via EnsureTool (downloads if missing).
				// No fallback — reference find.ts hard-fails without fd.
				fdResults, err := runFd(input.Pattern, searchPath, effectiveLimit, ctx)
				if err != nil {
					return nil, err
				}
				results = fdResults
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
			relativized := relativizeFindResults(results, searchPath)
			
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
// buildFdPattern rewrites a user glob for fd's --full-path mode: a relative
// pattern containing "/" must be anchored so it matches at any depth
// (TS find.ts:242-248 uses "**/" + pattern). Returns whether --full-path is
// needed.
func buildFdPattern(pattern string) (string, bool) {
	if strings.Contains(pattern, "/") {
		if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "**/") && pattern != "**" {
			return "**/" + pattern, true
		}
		return pattern, true
	}
	return pattern, false
}

// relativizeFindResults turns fd/WalkDir output lines into searchPath-relative
// POSIX paths. Mirrors TS find.ts:303-316: strip a trailing CR, trim
// whitespace, preserve a trailing directory slash (recorded before
// relativizing, re-appended after), prefix-strip searchPath or Rel() it.
// (The old Go code TrimRight'd the cutset "/r", mangling names like
// "driver" → "drive" and destroying dir markers.)
func relativizeFindResults(results []string, searchPath string) []string {
	relativized := make([]string, 0, len(results))
	for _, raw := range results {
		line := strings.TrimSuffix(raw, "\r")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hadTrailingSlash := strings.HasSuffix(line, "/") || strings.HasSuffix(line, "\\")
		var relativePath string
		if strings.HasPrefix(line, searchPath) && len(line) > len(searchPath) {
			relativePath = line[len(searchPath)+1:]
		} else {
			rel, err := filepath.Rel(searchPath, line)
			if err != nil {
				relativePath = line
			} else {
				relativePath = rel
			}
		}
		if hadTrailingSlash && !strings.HasSuffix(relativePath, "/") {
			relativePath += "/"
		}
		relativized = append(relativized, toPosixPath(relativePath))
	}
	return relativized
}

// runFd resolves fd via EnsureTool (downloading it if missing — reference
// ensureTool) and runs it with the reference argument set. There is no
// WalkDir fallback: the tool description promises .gitignore respect, which
// only fd provides, so an unavailable fd is a hard error (reference find.ts).
func runFd(pattern, searchPath string, limit int, ctx context.Context) ([]string, error) {
	fdPath := EnsureTool("fd", true)
	if fdPath == "" {
		return nil, fmt.Errorf("fd is not available and could not be downloaded")
	}

	// Build fd arguments (mirrors TS find.ts)
	args := []string{
		"--glob", "--color=never", "--hidden",
		"--no-require-git", "--max-results", fmt.Sprintf("%d", limit),
	}

	effectivePattern, needsFullPath := buildFdPattern(pattern)
	if needsFullPath {
		args = append(args, "--full-path")
	}
	args = append(args, "--", effectivePattern, searchPath)

	cmd := exec.CommandContext(ctx, fdPath, args...)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimSpace(string(exitErr.Stderr))
			if msg == "" {
				msg = fmt.Sprintf("fd exited with code %d", exitErr.ExitCode())
			}
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, fmt.Errorf("Failed to run fd: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}

func CreateFindTool(cwd string, opts *FindToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateFindToolDefinition(cwd, opts), nil)
}



/*
FOOTNOTE:
 1. .gitignore respect comes from fd (--no-require-git applies hierarchical
 .gitignore semantics). The old gitignore-blind WalkDir fallback was removed
 with story 20 — fd is resolved via EnsureTool (tools_manager.go, downloads
 from GitHub releases when missing) and an unavailable fd is a hard error,
 exactly like the reference find.ts. Bug-for-bug parity decision: no
 checksums, no version pinning (HTTPS is the only integrity).

 2. TS truncateHead uses maxLines: Number.MAX_SAFE_INTEGER (byte-only truncation)
 — Same issue as ls.go. Our version applies a 2000-line default limit where TS
 doesn't.
*/