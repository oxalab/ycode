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
			if p, ok := argsMap["path"].(string); ok && p != "" {
				path = ShortenPath(p)
			} else if p, ok := argsMap["file_path"].(string); ok && p != "" {
				path = ShortenPath(p)
			}
			text := fmt.Sprintf("write %s", path)

			content, ok := argsMap["content"].(string)
			if !ok {
				return text + "\n\n[invalid content arg - expected string]"
			}
			if content == "" {
				return text
			}

			display := ReplaceTabs(NormalizeDisplayText(content))
			lines := trimTrailingEmptyLines(strings.Split(display, "\n"))
			total := len(lines)
			maxLines := total
			if maxLines > 10 {
				maxLines = 10
			}
			text += "\n\n" + strings.Join(lines[:maxLines], "\n")
			if remaining := total - maxLines; remaining > 0 {
				text += fmt.Sprintf("\n... (%d more lines, %d total, ctrl+o to expand)", remaining, total)
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

// trimTrailingEmptyLines drops trailing empty lines only — interior blank
// lines are part of the content and stay (reference trimTrailingEmptyLines).
func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
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

 2. Syntax highlighting lives in the interactive TUI (highlightWritePreview —
 chroma via the file path); this RenderCall stays plain text because the BT
 renderer that consumes it applies its own styling and passes no expanded flag.

 3. file_path alias: the render side accepts both path and file_path (the
 input structs still only declare path — A12 tracks the model-facing alias).
*/