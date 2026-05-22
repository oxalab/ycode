package tools

import (
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"regexp"
	"strings"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripAnsi: Removes ANSI escape sequences from a string.
func StripAnsi(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

/*
SanitizeBinaryOutput: Filters out problematic Unicode characters from output.
 - Ports sanitizeBinaryOutput from utils/shell
*/
func SanitizeBinaryOutput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Allow tab, newline, carriage return
		if r == '\t' || r == '\n' || r == '\r' {
			b.WriteRune(r)
			continue
		}
		// Filter out control characters (0x00-0x1F)
		if r <= 0x1f {
			continue
		}
		// Filter out Unicode format characters
		if r >= 0xfff9 && r <= 0xfffb {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ShortenPath: Replaces the gome directory prefix with ~.
func ShortenPath(path string) string {
	if path == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

/*
Str: Returns the string value if it's a string, "" if nil, or false for other types.
 - Equivalent to TS str() which returns string | null.
*/
func Str(value any) (string, bool) {
	if s, ok := value.(string); ok {
		return s, true
	}
	if value == nil {
		return "", true
	}
	return "", false
}

// ReplaceTabs: Replaces tabs with 3 spaces
func ReplaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

// NormalizeDisplayText: Removes carriage returns.
func NormalizeDisplayText(text string) string {
	return strings.ReplaceAll(text, "\r", "")
}

// --- Terminal Capabilities (ports pi-tui getCapabilities)
type TerminalCapabilities struct {
	Images 	bool
}

// GetCapabilities: Detecs terminal image support by checking env vars.
func GetCapabilities() TerminalCapabilities {
	termProgram := os.Getenv("TERM_PROGRAM")
	supportsImages := termProgram == "iTerm.app" || termProgram == "kitty" ||
		os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("TERMKIT") != ""
	return TerminalCapabilities{Images: supportsImages}
}

// --- Image utilities (ports pi-tui getImageDimensions, imageFallback) ---

// ImageDimensions: Holds width and height of an image
type ImageDimensions struct {
	Width 	int
	Height  int
}

/*
GetImageDimensions: Extracts image dimensions from base64-encoded data.
 - Returns nil if dimensions cannot be determined
*/
func GetImageDimensions(data string, mimeType string) *ImageDimensions {
	reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))
	config, _ , err := image.DecodeConfig(reader)
	if err != nil {
		return nil
	}
	return &ImageDimensions{Width: config.Width, Height: config.Height}
}

// ImageFallback: Returns a text representation of an image when it cannot be displayed
func ImageFallback(mimeType string, dims *ImageDimensions) string {
	if dims != nil && dims.Width > 0 && dims.Height > 0 {
		return fmt.Sprintf("[Image: %s %dx%d]", mimeType, dims.Width, dims.Height)
	}
	return fmt.Sprintf("[Image: %s]", mimeType)
}

// TextContentBlock: Represents a text content block in a tool result
type TextContentBlock struct {
	Type string `json:"types"`
	Text string `json:"text,omitempty"`
}

// ImageContentBlock: Represents an image content block in a tool result.
type ImageContentBlock struct {
	Type 	string 	`json:"type"`
	Data 	string 	`json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ToolResult: Represents the common shape of a tool execution result.
type ToolResult struct {
	Content []any 	`json:"content"`
}

/*GetTextOutput: Extracts text output from a tool result.
 - NOTE: When images are present but cannot be displayed, 
 generates fallback text indicators.
*/
func GetTextOutput(result *ToolResult, showImages bool) string {
	if result == nil {
		return ""
	}

	var textBlocks []map[string]string
	var imageBlocks []map[string]string
	for _, c := range result.Content {
		switch block := c.(type) {
		case *TextContentBlock:
			textBlocks = append(textBlocks, map[string]string{
				"type": block.Type,
				"text": block.Text,
			})
		case TextContentBlock:
			textBlocks = append(textBlocks, map[string]string{
				"type": block.Type,
				"text": block.Text,
			})
		case *ImageContentBlock:
			imageBlocks = append(imageBlocks, map[string]string{
				"type":     block.Type,
				"data":     block.Data,
				"mimeType": block.MimeType,
			})
		case ImageContentBlock:
			imageBlocks = append(imageBlocks, map[string]string{
				"type":     block.Type,
				"data":     block.Data,
				"mimeType": block.MimeType,
			})
		case map[string]any:
			t, _ := block["type"].(string)
			if t == "text" {
				text, _ := block["text"].(string)
				textBlocks = append(textBlocks, map[string]string{
					"type": t,
					"text": text,
				})
			} else if t == "image" {
				data, _ := block["data"].(string)
				mimeType, _ := block["mimeType"].(string)
				imageBlocks = append(imageBlocks, map[string]string{
					"type":     t,
					"data":     data,
					"mimeType": mimeType,
				})
			}
		}
	}
	// Build text output
	var textParts []string
	for _, block := range textBlocks {
		cleaned := SanitizeBinaryOutput(StripAnsi(block["text"]))
		cleaned = NormalizeDisplayText(cleaned)
		textParts = append(textParts, cleaned)
	}
	output := strings.Join(textParts, "\n")

	// Handle image fallbacks
	caps := GetCapabilities()
	if len(imageBlocks) > 0 && (!caps.Images || !showImages) {
		var indicators []string
		for _, img := range imageBlocks {
			mimeType := img["mimeType"]
			if mimeType == "" {
				mimeType = "image/unknown"
			}
			var dims *ImageDimensions
			if img["data"] != "" && img["mimeType"] != "" {
				dims = GetImageDimensions(img["data"], img["mimeType"])
			}
			indicators = append(indicators, ImageFallback(mimeType, dims))
		}
		imageText := strings.Join(indicators, "\n")
		if output != "" {
			output = output + "\n" + imageText
		} else {
			output = imageText
		}
	}

	return output
}


//  --- Theme (minimal interface for InvalidArgText) ---
type Theme interface {
	Fg(name string, text string) string
}

// PlainTheme is a no-op theme that returns text unchanged.
type PlainTheme struct {}

func (t PlainTheme) Fg(name string, text string) string {
	return text
}

// InvalidArgText returns a formatted invalid argument indicator.
// NOTE: Theme support depends on TUI (deferred). Returns plain text for now.
func InvalidArgText() string {
	return "[invalid arg]"
}


/*
FOOTNOTE:
 - 1. GetCapabilities uses env var heuristics — 
 The TS pi-tui likely has more sophisticated terminal detection 
 (e.g., Kitty graphics protocol query, Sixel support check). 
 Our version checks common env vars (TERM_PROGRAM, KITTY_WINDOW_ID, TERMKIT). 
 This covers iTerm2, Kitty, and Termkit but may miss other terminals. 
 Can be enhanced when pi-tui is ported.

 - 2. GetImageDimensions only supports PNG/JPEG/GIF — 
 Go's image package supports these via stdlib imports. The TS pi-tui may 
 support more formats (WebP, etc.). Additional formats would need 
 golang.org/x/image/webp or similar.
 
 - 3.PlainTheme is a placeholder — When the TUI theming system is ported, 
 the real theme implementation will replace this. InvalidArgText already accepts 
 the Theme interface so it's ready for the swap
*/
