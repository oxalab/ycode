package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

type ReadToolInput struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

type ReadToolDetails struct {
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// ReadOperations: Defines pluggable operations for the read tool.
type ReadOperations interface {
	ReadFile(absolutePath string) ([]byte, error)
	Access(absolutePath string) error
	DetectImageMimeType(absolutePath string) string
}

type defaultReadOps struct{}

func (d defaultReadOps) ReadFile(p string) ([]byte, error) {
	return os.ReadFile(p)
}

// Access probes readability (the Go equivalent of TS fsAccess(path, R_OK)):
// open the file for reading and close it. os.Stat would only check existence,
// letting unreadable files pass the gate and fail later with a different
// message.
func (d defaultReadOps) Access(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	return f.Close()
}

// DetectImageMimeType reports the image MIME type for a path by extension, or ""
// if the path is not a recognised image (jpg/jpeg/png/gif/webp). It is the
// package-level form of the per-ops DetectImageMimeType, exposed so other
// packages (e.g. cli @file processing) classify images identically to the read
// tool — one source of truth for image classification.
//
// NOTE: like the read tool, this is extension-based only. The TS reference
// (utils/mime.ts detectSupportedImageMimeTypeFromFile) sniffs file magic bytes
// via the file-type library; that richer detection is not ported.
func DetectImageMimeType(p string) string {
	ext := strings.ToLower(p)
	if strings.HasSuffix(ext, ".jpg") || strings.HasSuffix(ext, ".jpeg") {
		return "image/jpeg"
	}
	if strings.HasSuffix(ext, ".png") {
		return "image/png"
	}
	if strings.HasSuffix(ext, ".gif") {
		return "image/gif"
	}
	if strings.HasSuffix(ext, ".webp") {
		return "image/webp"
	}
	return ""
}

func (d defaultReadOps) DetectImageMimeType(p string) string {
	return DetectImageMimeType(p)
}

// getNonVisionImageNote ports read.ts getNonVisionImageNote (read.ts:90-95): a
// warning line for models whose input list lacks "image". A zero-valued model
// (unknown) stays silent, matching TS's !model guard.
func getNonVisionImageNote(model *llm.Model[llm.Api]) string {
	if model == nil || model.ID == "" {
		return ""
	}
	for _, in := range model.Input {
		if in == llm.InputImage {
			return ""
		}
	}
	return "[Current model does not support images. The image will be omitted from this request.]"
}

type ReadToolOptions struct {
	AutoResizeImages 	bool
	Operations 			ReadOperations
}

func CreateReadToolDefinition(cwd string, opts *ReadToolOptions) *extensions.ToolDefinition {
	autoResizeImages := true
	var ops ReadOperations = defaultReadOps{}
	if opts != nil {
		autoResizeImages = opts.AutoResizeImages
		if opts.Operations != nil {
			ops = opts.Operations
		}
	}

	// execRead is the shared implementation; execModel is non-nil only on the
	// ExtensionContext-aware path, where the session's current model enables
	// the non-vision image note (reference getNonVisionImageNote(ctx.model)).
	execRead := func(execModel *llm.Model[llm.Api], ctx context.Context, args json.RawMessage) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var input ReadToolInput
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, fmt.Errorf("inavlid arguments: %w", err)
		}
		absolutePath := ResolveReadPath(input.Path, cwd)

		if err := ops.Access(absolutePath); err != nil {
			// Surface the raw fs error (reference re-throws it unmodified):
			// the model sees the real reason (not-found vs permission)
			// instead of a generic line.
			return nil, err
		}

		mimeType := ops.DetectImageMimeType(absolutePath)
		var content []llm.ContentBlock
		var details any

		if mimeType != "" {
			// Image file
			data, err := ops.ReadFile(absolutePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read image: %w", err)
			}
			base64Data := base64.StdEncoding.EncodeToString(data)
			visionNote := getNonVisionImageNote(execModel)

			// Auto-Resize if enabled. On resize failure, degrade the way TS
			// does (read.ts:255-262): a text-only "[Image omitted: …]" block
			// carrying the Read-image-file header line (read.ts:257).
			if autoResizeImages {
				resized, err := ResizeImage(base64Data, mimeType, 2000, 2000)
				if err != nil || resized == nil {
					textNote := fmt.Sprintf("Read image file [%s]\n[Image omitted: could not be resized below the inline image size limit.]", mimeType)
					if visionNote != "" {
						textNote += "\n" + visionNote
					}
					return &ToolExecuteResult{
						Content: []llm.ContentBlock{
							&llm.TextContent{Type: "text", Text: textNote},
						},
					}, nil
				}
				base64Data = resized.Data
				mimeType = resized.MimeType
			}
			textNote := fmt.Sprintf("Read image file [%s]", mimeType)
			if visionNote != "" {
				textNote += "\n" + visionNote
			}
			content = []llm.ContentBlock{
				&llm.TextContent{Type: "text", Text: textNote},
				&llm.ImageContent{Type: "image", Data: base64Data, MimeType: mimeType},
			}
		} else {
			// Text File
			data, err := ops.ReadFile(absolutePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			textContent := string(data)
			allLines := strings.Split(textContent, "\n")
			totalFileLines := len(allLines)

			startLine := 0
			if input.Offset != nil {
				startLine = max(0, *input.Offset-1)
			}
			startLineDisplay := startLine + 1
			if startLine >= len(allLines) {
				return nil, fmt.Errorf("Offset %d is beyond end of file (%d lines total)", *input.Offset, len(allLines))
			}
			var selectedContent string
			var userLimitedLines *int

			if input.Limit != nil {
				endLine := min(startLine+*input.Limit, len(allLines))
				selectedContent = strings.Join(allLines[startLine:endLine], "\n")
				count := endLine - startLine
				userLimitedLines = &count
			} else {
				selectedContent = strings.Join(allLines[startLine:], "\n")
			}
			truncation := TruncateHead(selectedContent, TruncationOptions{})
			var outputText string

			if truncation.FirstLineExceedsLimit {
				firstLineSize := FormatSize(len([]byte(allLines[startLine])))
				outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
					startLineDisplay, firstLineSize, FormatSize(DefaultMaxBytes), startLineDisplay, input.Path, DefaultMaxBytes)
				details = &ReadToolDetails{Truncation: &truncation}
			} else if truncation.Truncated {
				endLineDisplay := startLineDisplay + truncation.OutputLines - 1
				nextOffset := endLineDisplay + 1
				outputText = truncation.Content
				if truncation.TruncatedBy != nil && *truncation.TruncatedBy == "lines" {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, FormatSize(DefaultMaxBytes), nextOffset)
				}
				details = &ReadToolDetails{Truncation: &truncation}
			} else if userLimitedLines != nil && startLine+*userLimitedLines < len(allLines) {
				remaining := len(allLines) - (startLine + *userLimitedLines)
				nextOffset := startLine + *userLimitedLines + 1
				outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]",
					truncation.Content, remaining, nextOffset)
			} else {
				outputText = truncation.Content
			}

			content = []llm.ContentBlock{&llm.TextContent{Type: "text", Text: outputText}}
		}
		return &ToolExecuteResult{
			Content: content,
			Details: details,
		}, nil
	}

	return &extensions.ToolDefinition{
		Name: 	"read",
		Label: 	"read",
		Description:      fmt.Sprintf("Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.", DefaultMaxLines, DefaultMaxBytes/1024),
		PromptSnippet:    "Read file contents",
		PromptGuidelines: []string{"Use read to examine files instead of cat or sed."},
		Parameters: objectSchema(map[string]prop{
			"path":   {typ: propString, description: "Path to the file to read (relative or absolute)"},
			"offset": {typ: propInteger, description: "Line number to start reading from (1-indexed)"},
			"limit":  {typ: propInteger, description: "Maximum number of lines to read"},
		}, []string{"path"}),
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			return execRead(nil, ctx, args)
		},
		ExecuteContext: func(ctx context.Context, args json.RawMessage, extCtx extensions.ExtensionContext) (any, error) {
			return execRead(&extCtx.Model, ctx, args)
		},
		RenderCall: func(args any) any {
			argsMap, _ := args.(map[string]any)
			path := "?"
			if p, ok := argsMap["path"].(string); ok {
				path = ShortenPath(p)
			}
			text := fmt.Sprintf("read %s", path)
			if offset, ok := argsMap["offset"].(float64); ok {
				if limit, ok := argsMap["limit"].(float64); ok {
					text += fmt.Sprintf(":%d-%d", int(offset), int(offset+limit-1))
				} else {
					text += fmt.Sprintf(":%d", int(offset))
				}
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

// ResizedImage: Represents a resized image result.
type ResizedImage struct {
	Data 	string
	MimeType string
}

/*
ResizeImage: Resized an image to fit within max dimensions.
 - Returns nil if the image is already within bounds or cannot be resized.
*/
func ResizeImage(base64Data string, mimeType string, maxWidth, maxHeight int) (*ResizedImage, error) {
	// Decode Image
	_, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, err
	}
	dims := GetImageDimensions(base64Data, mimeType)
	if dims == nil {
		return nil, nil
	}
	// Check if resize is needed
	if dims.Width <= maxWidth && dims.Height <= maxHeight {
		return &ResizedImage{Data: base64Data, MimeType: mimeType}, nil
	}

	/*
	Resize using Go's image package
	NOTE: Full resize implementation requires image decoding/encoding.
	For now, return the original if withing bounds, nil otherwise.
	TODO: Implement actual resizing with image/draw
	*/
	if dims.Width > maxWidth || dims.Height > maxHeight {
		// Image exceeds limits but we can't resize with full decode/encode
		// Return nil to indicate the image should be omitted
		return nil, fmt.Errorf("image %dx%d exceeds max %dx%d and resize not yet implemented", dims.Width, dims.Height, maxWidth, maxHeight)
	}
	return &ResizedImage{Data: base64Data, MimeType: mimeType}, nil
}

func CreateReadTool(cwd string, opts *ReadToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateReadToolDefinition(cwd, opts), nil)
}


/*
FOOTNOTE: 
 1. ResizeImage is a stub — TS uses resizeImage from utils/image-resize.ts with sharp or 
 canvas. Go's image package can do basic resizing, but a proper implementation needs 
 image/draw with scaling logic. Currently returns nil (image omitted) if it exceeds limits. 
 Full resize needs golang.org/x/image/draw or similar.

 2. detectImageMimeType is extension-based only — TS uses 
 detectSupportedImageMimeTypeFromFile which reads file magic bytes. Our version checks 
 file extension only. Magic byte detection would require reading the file header.

 3. getCompactReadClassification not ported — TS has special compact rendering for skill 
 files, docs, and resource files (CLAUDE.md, AGENTS.md). This depends on getReadmePath 
 and other config that hasn't been ported. The compact rendering is purely TUI cosmetic.

 4. file_path alias not ported — Same as write.go. TS accepts both path and file_path. 
 Add FilePath string \json:"file_path,omitempty"`` if needed.

 5. llm.ImageContent — check its Go struct. If it doesn't have Data/MimeType fields, 
 you'll need to adjust. Let me verify.
*/