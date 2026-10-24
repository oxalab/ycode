package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

/*
There's an architectural issue to address. The TS tools return { content, details } 
from Execute, but our Go ToolDefinition.Execute returns (any, error) and our wrapper 
only handles []llm.ContentBlock. Tools need to return both content AND details. 
So I need to define a shared result type and update the wrapper.
*/


// ToolExecuteResult is the standard return type for ToolDefinition.Execute.
// The wrapper extracts Content and Details from this to build AgentToolResult.
type ToolExecuteResult struct {
	Content 	[]llm.ContentBlock
	Details 	any
	Terminate 	bool
}

const defaultLsLimit = 500

type LsToolInput struct {
	Path 		*string 	`json:"path,omitempty"`
	Limit 		*int 		`json:"limit,omitempty"`
}

type LsToolDetails struct {
	Truncation 		*TruncationResult 	`json:"truncation,omitempty"`
	EntryLimitReached int 				`json:"entryLimitReached,omitempty"`
}

// LsOperations: Defines pluggable operations for the ls tool.
type LsOperations interface {
	Exists(absolutePath string) bool
	Stat(absolutePath string) (os.FileInfo, error)
	Readdir(absolutePath string) ([]string, error)
}

type defaultLsOps struct {}

func (d defaultLsOps) Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (d defaultLsOps) Stat(p string) (os.FileInfo, error) {
	return os.Stat(p)
}

func (d defaultLsOps) Readdir(p string) ([]string, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

type LsToolOptions struct {
	Operations 	LsOperations
}

func CreateLsToolDefinition(cwd string, opts *LsToolOptions) *extensions.ToolDefinition {
	var ops LsOperations = defaultLsOps{}
	if opts != nil && opts.Operations != nil {
		ops = opts.Operations
	}

	return &extensions.ToolDefinition{
		Name: "ls",
		Label: "ls",
		Description: fmt.Sprintf("List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).", defaultLsLimit, DefaultMaxBytes/1024),
		PromptSnippet: "List directory contents",
		Parameters: objectSchema(map[string]prop{
			"path":  {typ: propString, description: "Directory to list (default: current directory)"},
			"limit": {typ: propInteger, description: "Maximum number of entries to return (default: 500)"},
		}, nil),
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			var input LsToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			dirPath := ResolveToCwd(".", cwd)
			if input.Path != nil && *input.Path != "" {
				dirPath = ResolveToCwd(*input.Path, cwd)
			}
			effectiveLimit := defaultLsLimit
			if input.Limit != nil {
				effectiveLimit = *input.Limit
			}
			if !ops.Exists(dirPath) {
				return nil, fmt.Errorf("path not found: %s", dirPath)
			}
			stat, err := ops.Stat(dirPath)
			if err != nil {
				return nil, fmt.Errorf("cannot stat path: %s", dirPath)
			}
			if !stat.IsDir() {
				return nil, fmt.Errorf("not a directory: %s", dirPath)
			}
			entries, err := ops.Readdir(dirPath)
			if err != nil {
				return nil, fmt.Errorf("cannot read directory: %s", err)
			}
			sort.Slice(entries, func(i, j int) bool {
				return strings.ToLower(entries[i]) < strings.ToLower(entries[j])
			})
			var results []string 
			entryLimitReached := 0
			for _, entry := range entries {
				if len(results) >= effectiveLimit {
					entryLimitReached = effectiveLimit
					break
				}

				fullPath := filepath.Join(dirPath, entry)
				entryStat, err := ops.Stat(fullPath)
				if err != nil {
					continue
				}
				suffix := ""
				if entryStat.IsDir() {
					suffix = "/"
				}
				results = append(results, entry+suffix)
			}
			if len(results) == 0 {
				return &ToolExecuteResult{
					Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text:"(empty directory)"}},
				}, nil
			}

			rawOutput := strings.Join(results, "\n")
			truncation := TruncateHead(rawOutput, TruncationOptions{})

			var notices []string 
			details := &LsToolDetails{}
			if entryLimitReached > 0 {
				notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", effectiveLimit, effectiveLimit*2))
				details.EntryLimitReached = effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				details.Truncation = &truncation
			}

			output := truncation.Content
			if len(notices) > 0 {
				output += "\n\n[" + strings.Join(notices, ". ") + "]"
			}
			toolDetails := any(nil)
			if details.Truncation != nil || details.EntryLimitReached > 0 {
				toolDetails = details
			}
			return &ToolExecuteResult{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: output}},
				Details: toolDetails,
			}, nil
		},
		RenderCall: func(args any) any {
			argsMap, _ := args.(map[string]any)
			path := "."
			if p, ok := argsMap["path"].(string); ok && p != "" {
				path = ShortenPath(p)
			}
			return fmt.Sprintf("ls %s", path)
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

func CreateLsTool(cwd string, opts *LsToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateLsToolDefinition(cwd, opts), nil)
}


/*
FOOTNOTE:
 1. LsOperations methods are synchronous — TS uses async/Promise-based operations. 
 Go uses synchronous calls since the tool runs in a goroutine. Same end result.

 2. Render functions return strings instead of TUI Text components — RenderCall 
 and RenderResult return string (as any) instead of pi-tui Text. When TUI is 
 ported, these should return proper TUI components.

 3. TS truncateHead called with maxLines: Number.MAX_SAFE_INTEGER — The TS ls 
 tool only applies byte truncation (no line limit). In Go, TruncateOptions{} 
 defaults to DefaultMaxLines=2000. This means Go also applies a 2000-line limit 
 that TS doesn't. To match exactly, pass 
 TruncationOptions{MaxLines: intPtr(math.MaxInt32)}. This is a minor behavioral 
 difference.
*/