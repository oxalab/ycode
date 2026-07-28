package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	llm "ycode/packages/llm/src"
)

type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteOperations: Defines pluggable operations for the write tool.
type WriteOperations interface {
	WriteFile(absolutePath string, content string) error
	Mkdir(dir string) error
}

type defaultWriteOps struct{}

func (d defaultWriteOps) WriteFile(p string, content string) error {
	return os.WriteFile(p, []byte(content), 0644)
}

func (d defaultWriteOps) Mkdir(dir string) error {
	return os.MkdirAll(dir, 0755)
}


type WriteToolOptions struct {
	Operations 		WriteOperations
}

func CreateWriteToolDefinition(cwd string, opts *WriteToolOptions) *extensions.ToolDefinition {
	var ops WriteOperations = defaultWriteOps{}
	if opts != nil && opts.Operations != nil {
		ops = opts.Operations
	}
	return &extensions.ToolDefinition{
		Name: 		"write",
		Label: 		"write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		PromptSnippet: "Create or overwrite files",
		PromptGuidelines: 	[]string{"Use write only for new files or complete rewrites."},
		Parameters: objectSchema(map[string]prop{
			"path":    {typ: propString, description: "Path to the file to write (relative or absolute)"},
			"content": {typ: propString, description: "Content to write to the file"},
		}, []string{"path", "content"}),
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			var input WriteToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if input.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			absolutePath := ResolveToCwd(input.Path, cwd)
			dir := filepath.Dir(absolutePath)

			result, err := WithFileMutationQueue(absolutePath, func() (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}

				if err := ops.Mkdir(dir); err != nil {
					return nil, fmt.Errorf("failed to create directory: %w", err)
				}

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}

				if err := ops.WriteFile(absolutePath, input.Content); err != nil {
					return nil, fmt.Errorf("failed to write file: %w", err)
				}

				return &ToolExecuteResult{
					Content: []llm.ContentBlock{&llm.TextContent{
						Type: "text",
						Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(input.Content), input.Path),
					}},
				}, nil
			})
			return result, err
		},
		RenderCall: func(args any) any {
			argsMap, _ := args.(map[string]any)
			path := "?"
			if p, ok := argsMap["path"].(string); ok {
				path = ShortenPath(p)
			}
			content, _ := argsMap["content"].(string)
			display := NormalizeDisplayText(content)
			display = ReplaceTabs(display)

			var lines []string
			for _, line := range strings.Split(display, "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				lines = append(lines, line)
				if len(lines) >= 10 {
					break
				}
			}
			text := fmt.Sprintf("write %s", path)
			if len(lines) > 0 {
				text += "\n\n" + strings.Join(lines, "\n" )
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

func CreateWriteTool(cwd string, opts *WriteToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateWriteToolDefinition(cwd, opts), nil)
}



/*
FOOTNOTE:
 1. TS content.length counts characters, Go len(input.Content) counts bytes — For 
 ASCII content these are the same. For multi-byte UTF-8, Go reports bytes while TS 
 reports characters. The success message will show "wrote 6 bytes" for "hello!" (same) 
 but "wrote 8 bytes" for "héllo!" where TS would show "wrote 6 characters". Minor display 
 difference.

 2. Syntax highlighting deferred — TS has incremental highlight caching 
 (WriteHighlightCache, refreshWriteHighlightPrefix) using highlightCode from the TUI 
 theme. Our RenderCall does plain text. Will be added when TUI theme is ported.

 3. file_path alias not ported — The TS renderCall accepts both path and file_path (alias). 
 Our Go version only uses path. This alias appears to be for backward compatibility. 
 If needed, add FilePath string \json:"file_path,omitempty"`` to the input struct.
*/