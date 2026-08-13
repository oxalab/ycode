package exporthtml

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ============================================================================
// Types
// ============================================================================

// ExportOptions configures the HTML export.
// Mirrors TS ExportOptions (export-html/index.ts:34-39).
type ExportOptions struct {
	OutputPath  string
	ThemeName   string
	ToolRenderer ToolRenderer
}

// SessionData is the JSON structure embedded in the exported HTML.
// Mirrors TS SessionData (export-html/index.ts:129-137).
type SessionData struct {
	Header        json.RawMessage      `json:"header"`
	Entries       []json.RawMessage    `json:"entries"`
	LeafID        string               `json:"leafId"`
	SystemPrompt  string               `json:"systemPrompt,omitempty"`
	Tools         []ToolExportDef      `json:"tools,omitempty"`
	RenderedTools map[string]RenderedToolHtml `json:"renderedTools,omitempty"`
}

// ToolExportDef is a minimal tool definition for export.
type ToolExportDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// RenderedToolHtml holds pre-rendered HTML for custom tool call/result pairs.
type RenderedToolHtml struct {
	CallHtml            string `json:"callHtml,omitempty"`
	ResultHtmlCollapsed string `json:"resultHtmlCollapsed,omitempty"`
	ResultHtmlExpanded  string `json:"resultHtmlExpanded,omitempty"`
}

// SessionManagerInterface defines the methods we need from a session manager.
type SessionManagerInterface interface {
	GetSessionFile() string
	GetHeader() json.RawMessage
	GetEntries() []json.RawMessage
	GetLeafID() string
}

// ============================================================================
// Color helpers — mirrors TS index.ts:42-77
// ============================================================================

var hexRegex = regexp.MustCompile(`^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$`)
var rgbRegex = regexp.MustCompile(`^rgb\s*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$`)

type rgbColor struct{ r, g, b float64 }

func parseColor(color string) *rgbColor {
	if m := hexRegex.FindStringSubmatch(color); m != nil {
		r, _ := strconv.ParseInt(m[1], 16, 32)
		g, _ := strconv.ParseInt(m[2], 16, 32)
		b, _ := strconv.ParseInt(m[3], 16, 32)
		return &rgbColor{float64(r), float64(g), float64(b)}
	}
	if m := rgbRegex.FindStringSubmatch(color); m != nil {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return &rgbColor{float64(r), float64(g), float64(b)}
	}
	return nil
}

func getLuminance(r, g, b float64) float64 {
	toLinear := func(c float64) float64 {
		s := c / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*toLinear(r) + 0.7152*toLinear(g) + 0.0722*toLinear(b)
}

func adjustBrightness(color string, factor float64) string {
	parsed := parseColor(color)
	if parsed == nil {
		return color
	}
	adjust := func(c float64) int {
		return int(math.Min(255, math.Max(0, math.Round(c*factor))))
	}
	return fmt.Sprintf("rgb(%d, %d, %d)", adjust(parsed.r), adjust(parsed.g), adjust(parsed.b))
}

func deriveExportColors(baseColor string) exportColors {
	parsed := parseColor(baseColor)
	if parsed == nil {
		return exportColors{
			pageBg: "rgb(24, 24, 30)",
			cardBg: "rgb(30, 30, 36)",
			infoBg: "rgb(60, 55, 40)",
		}
	}
	luminance := getLuminance(parsed.r, parsed.g, parsed.b)
	isLight := luminance > 0.5

	if isLight {
		return exportColors{
			pageBg: adjustBrightness(baseColor, 0.96),
			cardBg: baseColor,
			infoBg: fmt.Sprintf("rgb(%d, %d, %d)",
				int(math.Min(255, parsed.r+10)),
				int(math.Min(255, parsed.g+5)),
				int(math.Max(0, parsed.b-20))),
		}
	}
	return exportColors{
		pageBg: adjustBrightness(baseColor, 0.7),
		cardBg: adjustBrightness(baseColor, 0.85),
		infoBg: fmt.Sprintf("rgb(%d, %d, %d)",
			int(math.Min(255, parsed.r+20)),
			int(math.Min(255, parsed.g+15)),
			int(parsed.b)),
	}
}

type exportColors struct {
	pageBg, cardBg, infoBg string
}

// ============================================================================
// HTML generation — mirrors TS generateHtml (index.ts:142-174)
// ============================================================================

func generateHtml(sessionData SessionData, themeName string) (string, error) {
	// Build theme vars
	themeVars := generateDefaultThemeVars()
	userMsgBg := "#343541"
	derived := deriveExportColors(userMsgBg)

	bodyBg := derived.pageBg
	containerBg := derived.cardBg
	infoBg := derived.infoBg

	// Build CSS with theme vars injected
	css := strings.Replace(templateCSS, "{{THEME_VARS}}", themeVars, 1)
	css = strings.Replace(css, "{{BODY_BG}}", bodyBg, 1)
	css = strings.Replace(css, "{{CONTAINER_BG}}", containerBg, 1)
	css = strings.Replace(css, "{{INFO_BG}}", infoBg, 1)

	// Base64 encode session data to avoid escaping issues (matches TS behavior)
	sessionJSON, err := json.Marshal(sessionData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session data: %w", err)
	}
	sessionDataBase64 := base64.StdEncoding.EncodeToString(sessionJSON)

	html := templateHTML
	html = strings.Replace(html, "{{CSS}}", css, 1)
	html = strings.Replace(html, "{{JS}}", templateJS, 1)
	html = strings.Replace(html, "{{SESSION_DATA}}", sessionDataBase64, 1)
	html = strings.Replace(html, "{{MARKED_JS}}", markedJS, 1)
	html = strings.Replace(html, "{{HIGHLIGHT_JS}}", highlightJS, 1)

	return html, nil
}

// generateDefaultThemeVars produces sensible defaults when no theme system is available.
func generateDefaultThemeVars() string {
	defaults := map[string]string{
		"text": "#e0e0e0", "background": "#1e1e2e",
		"userMessageBg": "#343541", "assistantMessageBg": "#2d2d3d",
		"border": "#444", "accent": "#7c6fda",
		"codeBackground": "#1a1a2e", "codeBorder": "#333",
		"success": "#4caf50", "error": "#f44336",
		"warning": "#ff9800",
	}
	var lines []string
	for k, v := range defaults {
		lines = append(lines, fmt.Sprintf("--%s: %s;", k, v))
	}
	return strings.Join(lines, "\n      ")
}

// ============================================================================
// Export functions — mirrors TS exportSessionToHtml / exportFromFile
// ============================================================================

// Tools rendered directly by the HTML template (not pre-rendered).
var templateRenderedTools = map[string]bool{
	"bash": true, "read": true, "write": true, "edit": true, "ls": true,
}

// ExportSessionToHtml exports a session to an HTML file.
// Mirrors TS exportSessionToHtml (export-html/index.ts:235-281).
func ExportSessionToHtml(sm SessionManagerInterface, systemPrompt string, tools []ToolExportDef, opts *ExportOptions) (string, error) {
	if opts == nil {
		opts = &ExportOptions{}
	}

	sessionFile := sm.GetSessionFile()
	if sessionFile == "" {
		return "", fmt.Errorf("cannot export in-memory session to HTML")
	}
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		return "", fmt.Errorf("nothing to export yet — start a conversation first")
	}

	entries := sm.GetEntries()

	// Pre-render custom tools if a tool renderer is provided
	var renderedTools map[string]RenderedToolHtml
	if opts.ToolRenderer != nil {
		renderedTools = preRenderCustomTools(entries, opts.ToolRenderer)
		if len(renderedTools) == 0 {
			renderedTools = nil
		}
	}

	sessionData := SessionData{
		Header:        sm.GetHeader(),
		Entries:       entries,
		LeafID:        sm.GetLeafID(),
		SystemPrompt:  systemPrompt,
		Tools:         tools,
		RenderedTools: renderedTools,
	}

	html, err := generateHtml(sessionData, opts.ThemeName)
	if err != nil {
		return "", err
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		sessionBasename := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl")
		outputPath = fmt.Sprintf("ycode-session-%s.html", sessionBasename)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0644); err != nil {
		return "", fmt.Errorf("failed to write export file: %w", err)
	}

	return outputPath, nil
}

// ExportFromFile exports a session file to HTML without an active AgentState.
// Mirrors TS exportFromFile (export-html/index.ts:287-314).
func ExportFromFile(inputPath string, opts *ExportOptions) (string, error) {
	if opts == nil {
		opts = &ExportOptions{}
	}

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", inputPath)
	}

	// Read and parse the session file entries
	// For standalone export, we read the file and pass raw entries
	headerJSON, entries, leafID, err := readSessionFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to read session file: %w", err)
	}

	sessionData := SessionData{
		Header:  headerJSON,
		Entries: entries,
		LeafID:  leafID,
	}

	html, err := generateHtml(sessionData, opts.ThemeName)
	if err != nil {
		return "", err
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		inputBasename := strings.TrimSuffix(filepath.Base(inputPath), ".jsonl")
		outputPath = fmt.Sprintf("ycode-session-%s.html", inputBasename)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0644); err != nil {
		return "", fmt.Errorf("failed to write export file: %w", err)
	}

	return outputPath, nil
}

// ============================================================================
// Pre-rendering helpers — mirrors TS preRenderCustomTools (index.ts:182-229)
// ============================================================================

func preRenderCustomTools(entries []json.RawMessage, renderer ToolRenderer) map[string]RenderedToolHtml {
	rendered := make(map[string]RenderedToolHtml)

	for _, raw := range entries {
		var entry struct {
			Type    string `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(raw, &entry) != nil || entry.Type != "message" {
			continue
		}

		var msg struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments any    `json:"arguments"`
			} `json:"content"`
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
			IsError    bool   `json:"isError"`
		}
		if json.Unmarshal(entry.Message, &msg) != nil {
			continue
		}

		// Find tool calls in assistant messages
		if msg.Role == "assistant" {
			for _, block := range msg.Content {
				if block.Type == "toolCall" && !templateRenderedTools[block.Name] {
					callHtml := renderer.RenderCall(block.Name, block.Arguments)
					if callHtml != "" {
						rendered[block.ID] = RenderedToolHtml{CallHtml: callHtml}
					}
				}
			}
		}

		// Find tool results
		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			toolName := msg.ToolName
			existing, hasExisting := rendered[msg.ToolCallID]
			if hasExisting || !templateRenderedTools[toolName] {
				resultHtml := renderer.RenderResult(toolName, nil, false)
				if resultHtml != "" {
					rendered[msg.ToolCallID] = RenderedToolHtml{
						CallHtml:            existing.CallHtml,
						ResultHtmlCollapsed: resultHtml,
					}
				}
			}
		}
	}

	return rendered
}

// readSessionFile reads a JSONL session file and extracts header + entries + leafID.
func readSessionFile(path string) (json.RawMessage, []json.RawMessage, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}

	var header json.RawMessage
	var entries []json.RawMessage
	leafID := ""

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if entry.Type == "session" && header == nil {
			header = json.RawMessage(line)
		} else {
			entries = append(entries, json.RawMessage(line))
		}
	}

	// Last entry is the leaf
	if len(entries) > 0 {
		var last struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(entries[len(entries)-1], &last) == nil {
			leafID = last.ID
		}
	}

	return header, entries, leafID, nil
}
