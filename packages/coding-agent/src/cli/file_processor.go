package cli

// This file processes @file CLI arguments into text content and image
// attachments. It is the Go port of reference/coding-agent/src/cli/file-processor.ts.
//
// Each @file argument (already stripped of its leading "@" by ParseArgs) is
// resolved to an absolute path, existence-checked, and then either:
//   - inlined as wrapped text:  <file name="...">\n<contents>\n</file>
//   - or, when it is a recognised image, attached as an llm.ImageContent plus a
//     <file name="..."></file> text reference.
//
// Empty files are skipped, matching the reference. A missing file is a fatal
// error (the reference calls process.exit(1)); here it is returned as an error
// for the entry point (cmd/ycode/main.go, Phase 19.8) to surface and exit on.
//
// ============================================================================
// Reuse + honest deferrals
// ============================================================================
//
// Image handling reuses the existing core/tools helpers so @file processing and
// the read tool classify and resize images identically:
//   - tools.ResolveReadPath  — path resolution (~ expansion, macOS variants)
//   - tools.DetectImageMimeType — extension-based image classification
//   - tools.ResizeImage      — auto-resize to 2000x2000 (STUB, see below)
//
// Two image features are NOT portable with the current Go helpers and are
// deferred (documented, not faked):
//
//  1. Actual resizing: tools.ResizeImage is a stub — it returns the original
//     bytes when the image is within 2000x2000 and (nil, err) when it exceeds
//     the limit (real resizing needs golang.org/x/image/draw). When the stub
//     cannot resize, we emit the reference's "Image omitted" note and skip the
//     attachment, exactly as the TS does when resizeImage returns null.
//
//  2. Dimension notes: the TS formatDimensionNote annotates the text reference
//     with original/displayed dimensions and a coordinate scale factor.
//     tools.ResizedImage does not carry dimensions or a wasResized flag, so the
//     note cannot be produced. We emit the no-note form
//     (<file name="..."></file>), which is the TS else-branch.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	llm "ycode/packages/llm/src"

	tools "ycode/packages/coding-agent/src/core/tools"
)

// ProcessFileOptions mirrors the TS ProcessFileOptions.
type ProcessFileOptions struct {
	// AutoResizeImages controls whether images are auto-resized to a 2000x2000
	// maximum before attachment. Defaults to true when options is nil (matching
	// the TS `autoResizeImages ?? true`).
	AutoResizeImages bool
}

// ProcessFileArguments resolves and reads each @file argument, returning the
// concatenated wrapped text and the collected image attachments (TS
// processFileArguments).
//
// cwd is the working directory used to resolve relative paths (the TS reads
// process.cwd() internally; Go passes it in for testability and to match the
// codebase convention of threading cwd from the entry point).
//
// Deviation from the execution-plan sketch: the plan listed `images []any`;
// the faithful and type-safe return is `[]*llm.ImageContent` (the TS
// ImageContent[]), which BuildInitialMessage (Phase 19.4) consumes directly.
func ProcessFileArguments(fileArgs []string, cwd string, options *ProcessFileOptions) (string, []*llm.ImageContent, error) {
	autoResize := true
	if options != nil {
		autoResize = options.AutoResizeImages
	}

	var text string
	var images []*llm.ImageContent

	for _, fileArg := range fileArgs {
		// Resolve and absolutise (TS: resolve(resolveReadPath(fileArg, cwd))).
		resolved := tools.ResolveReadPath(fileArg, cwd)
		absolutePath, err := filepath.Abs(resolved)
		if err != nil {
			return "", nil, fmt.Errorf("resolve file path %q: %w", fileArg, err)
		}

		// Existence check (TS: access; not found is fatal).
		info, err := os.Stat(absolutePath)
		if err != nil {
			return "", nil, fmt.Errorf("file not found: %s", absolutePath)
		}

		// Skip empty files (TS: stats.size === 0 → continue).
		if info.Size() == 0 {
			continue
		}

		mimeType := tools.DetectImageMimeType(absolutePath)
		if mimeType != "" {
			// --- Image file ---
			data, err := os.ReadFile(absolutePath)
			if err != nil {
				return "", nil, fmt.Errorf("could not read file %s: %w", absolutePath, err)
			}
			base64Data := base64.StdEncoding.EncodeToString(data)

			attachData := base64Data
			attachMime := mimeType

			if autoResize {
				resized, resizeErr := tools.ResizeImage(base64Data, mimeType, 2000, 2000)
				if resizeErr != nil || resized == nil {
					// Resize unavailable (stub cannot resize an oversized image,
					// or dimensions could not be determined): emit the reference's
					// omission note and skip the attachment.
					_ = resizeErr
					text += fmt.Sprintf("<file name=\"%s\">[Image omitted: could not be resized below the inline image size limit.]</file>\n", absolutePath)
					continue
				}
				attachData = resized.Data
				attachMime = resized.MimeType
			}

			images = append(images, &llm.ImageContent{
				Type:     "image",
				Data:     attachData,
				MimeType: attachMime,
			})

			// Text reference. The TS appends a dimension note when the image was
			// resized; tools.ResizedImage carries no dimensions, so we always use
			// the no-note form (the TS else-branch).
			text += fmt.Sprintf("<file name=\"%s\"></file>\n", absolutePath)
		} else {
			// --- Text file ---
			content, err := os.ReadFile(absolutePath)
			if err != nil {
				return "", nil, fmt.Errorf("could not read file %s: %w", absolutePath, err)
			}
			text += fmt.Sprintf("<file name=\"%s\">\n%s\n</file>\n", absolutePath, string(content))
		}
	}

	return text, images, nil
}
