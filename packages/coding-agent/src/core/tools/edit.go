package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

type ReplaceEdit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type EditToolInput struct {
	Path  string        `json:"path"`
	Edits []ReplaceEdit `json:"edits"`

	// Legacy fields (some models send oldText/newText at top level)
	OldText *string `json:"oldText,omitempty"`
	NewText *string `json:"newText,omitempty"`
}

type EditToolDetails struct {
	Diff             string `json:"diff"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

// EditOperations: Defines pluggable operations for the edit tool.
type EditOperations interface {
	ReadFile(absolutePath string) ([]byte, error)
	WriteFile(absolutePath string, content string) error
	Access(absolutePath string) error
}

type defaultEditOps struct{}

func (d defaultEditOps) ReadFile(p string) ([]byte, error){ 
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (d defaultEditOps) WriteFile(p string, content string) error {
	return os.WriteFile(p, []byte(content), 0644)
}

func (d defaultEditOps) Access(p string) error {
	_, err := os.Stat(p)
	return err
}

type EditToolOptions struct {
	Operations EditOperations
}

/*
prepareEditArguments: Normalized edit input, handling legact oldText/newText at top level
and models that send edits as a JSON instead of an array. Wired as the tool
definition's PrepareArguments shim (TS edit.ts:308); the agent loop applies it
before Execute.
*/
func prepareEditArguments(input any) any {
	argsMap, ok := input.(map[string]any)
	if !ok {
		return input
	}

	// Some models send edits as a JSON string instead of an array. Parsed as
	// []any (TS JSON.parse + Array.isArray) so objects with extra keys survive.
	if editStr, ok := argsMap["edits"].(string); ok {
		var parsed []any
		if err := json.Unmarshal([]byte(editStr), &parsed); err == nil {
			argsMap["edits"] = parsed
		}
	}

	// Convert legacy oldText/newText at top level into edits array
	oldText, hasOld := argsMap["oldText"].(string)
	newText, hasNew := argsMap["newText"].(string)
	if !hasOld || !hasNew {
		return argsMap
	}

	edits, _ := argsMap["edits"].([]any)
	if edits == nil {
		edits = []any{}
	}
	edits = append(edits, map[string]any{
		"oldText": oldText,
		"newText": newText,
	})
	argsMap["edits"] = edits
	delete(argsMap, "oldText")
	delete(argsMap, "newText")

	return argsMap
}

func validateEditInput(input *EditToolInput) ([]Edit, error) {
	if len(input.Edits) == 0 {
		if input.OldText != nil && input.NewText != nil {
			return []Edit{{OldText: *input.OldText, NewText: *input.NewText}}, nil
		}
		return nil, fmt.Errorf("edit tools input in invalid. edits must contain atleast one replacement")
	}
	edits := make([]Edit, len(input.Edits))
	for i, e := range input.Edits {
		edits[i] = Edit{OldText: e.OldText, NewText: e.NewText}
	}
	return edits, nil
}

func CreateEditToolDefinition(cwd string, opts *EditToolOptions) *extensions.ToolDefinition {
	var ops EditOperations = defaultEditOps{}
	if opts != nil && opts.Operations != nil {
		ops = opts.Operations
	}

	return &extensions.ToolDefinition{
		Name:          "edit",
		Label:         "edit",
		Description:   "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		PromptSnippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		PromptGuidelines: []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		},
		Parameters: objectSchema(map[string]prop{
			"path": {typ: propString, description: "Path to the file to edit (relative or absolute)"},
			"edits": {
				typ:         propArray,
				description: "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead.",
				items: arrayItems(map[string]prop{
					"oldText": {typ: propString, description: "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
					"newText": {typ: propString, description: "Replacement text for this targeted edit."},
				}, []string{"oldText", "newText"}),
			},
		}, []string{"path", "edits"}),
		PrepareArguments: func(args any) (any, error) {
			return prepareEditArguments(args), nil
		},
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			var input EditToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			edits, err := validateEditInput(&input)
			if err != nil {
				return nil, err
			}
			absolutePath := ResolveToCwd(input.Path, cwd)
			result, err := WithFileMutationQueue(absolutePath, func() (any, error) {
				// Check if file exits and is accesible
				if err := ops.Access(absolutePath); err != nil {
					return nil, fmt.Errorf("could not edit file: %s. %s", input.Path, err.Error())
				}

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}

				// Read the file
				data, err := ops.ReadFile(absolutePath)
				if err != nil {
					return nil, fmt.Errorf("failed to read file: %w", err)
				}
				rawContent := string(data)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				
				// Strip BOM before matching (model won't include invisible BOM in oldText)
				bom, content := StripBom(rawContent)
				originalEnding := DetectLineEnding(content)
				normalizedContent := NormalizeToLF(content)

				// Apply Edits
				editResult, err := ApplyEditsToNormalizedContent(normalizedContent, edits, input.Path)
				if err != nil {
					return nil, err
				}

				select {
				case <- ctx.Done():
					return nil, ctx.Err()
				default:
				}

				// Write back with restored line endings and BOM
				finalContent := bom + RestoreLineEndings(editResult.NewContent, originalEnding)
				if err := ops.WriteFile(absolutePath, finalContent); err != nil {
					return nil, fmt.Errorf("failed to write file: %w", err)
				}

				// Generate diff for details 
				diff, firstChangedLine := GenerateDiffString(editResult.BaseContent, editResult.NewContent, 4)

				return &ToolExecuteResult{
					Content: []llm.ContentBlock{&llm.TextContent{
						Type: "text",
						Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), input.Path),
					}},
					Details: &EditToolDetails{
						Diff:             diff,
						FirstChangedLine: firstChangedLine,
					},
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
			if p, ok := argsMap["file_path"].(string); ok {
				path = ShortenPath(p)
			}
			return fmt.Sprintf("edit %s", path)
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
			// Include diff in ender if available
			if res.Details != nil {
				if details, ok := res.Details.(*EditToolDetails); ok && details.Diff != "" {
					return tc.Text + "\n\n" + details.Diff
				}
			}
			return tc.Text
		},
	}
}

func CreateEditTool(cwd string, opts *EditToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateEditToolDefinition(cwd, opts), nil)
}

/*
FOOTNOTE:
 1. prepareEditArguments wired 2026-10-05 (audit A8) — ToolDefinition now has a
 PrepareArguments shim field, copied to the AgentTool by WrapToolDefinition and
 applied by the SDK agent loop before Execute (mirroring TS prepareArguments →
 agent-loop). The OldText/NewText optional fields + validateEditInput fallback
 remain as a second net.

 2. Diff preview in renderCall deferred — TS has a sophisticated preview system
 (EditCallRenderComponent) that computes diffs on-the-fly as the user types.
 This requires async rendering and component state management from the TUI.
 Simplified to just showing the filename.

 3. renderShell: "self" not ported — TS marks the edit tool with renderShell: "self" 
 which likely indicates it renders its own shell output. Not relevant for the 
 Go ToolDefinition structure.

 4. No file_path alias in input struct — Same as write/read. Add 
 FilePath string \json:"file_path,omitempty"`` if needed for backward compat.
*/