package theme

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed dark.json
var darkJSON []byte

//go:embed light.json
var lightJSON []byte

// ThemeJson represents the raw JSON structure of a theme file.
type ThemeJson struct {
	Schema string            `json:"$schema,omitempty"`
	Name   string            `json:"name"`
	Vars   map[string]string `json:"vars,omitempty"`
	Colors ThemeColors       `json:"colors"`
	Export *ThemeExport      `json:"export,omitempty"`
}

// ThemeColors contains all 50 color tokens required by the theme schema.
type ThemeColors struct {
	// Core UI (10 colors)
	Accent      string `json:"accent"`
	Border      string `json:"border"`
	BorderAccent string `json:"borderAccent"`
	BorderMuted string `json:"borderMuted"`
	Success     string `json:"success"`
	Error       string `json:"error"`
	Warning     string `json:"warning"`
	Muted       string `json:"muted"`
	Dim         string `json:"dim"`
	Text        string `json:"text"`
	ThinkingText string `json:"thinkingText"`

	// Backgrounds & Content Text (11 colors)
	SelectedBg       string `json:"selectedBg"`
	UserMessageBg    string `json:"userMessageBg"`
	UserMessageText  string `json:"userMessageText"`
	CustomMessageBg  string `json:"customMessageBg"`
	CustomMessageText string `json:"customMessageText"`
	CustomMessageLabel string `json:"customMessageLabel"`
	ToolPendingBg    string `json:"toolPendingBg"`
	ToolSuccessBg    string `json:"toolSuccessBg"`
	ToolErrorBg      string `json:"toolErrorBg"`
	ToolTitle        string `json:"toolTitle"`
	ToolOutput       string `json:"toolOutput"`

	// Markdown (10 colors)
	MdHeading       string `json:"mdHeading"`
	MdLink          string `json:"mdLink"`
	MdLinkUrl       string `json:"mdLinkUrl"`
	MdCode          string `json:"mdCode"`
	MdCodeBlock     string `json:"mdCodeBlock"`
	MdCodeBlockBorder string `json:"mdCodeBlockBorder"`
	MdQuote         string `json:"mdQuote"`
	MdQuoteBorder   string `json:"mdQuoteBorder"`
	MdHr            string `json:"mdHr"`
	MdListBullet    string `json:"mdListBullet"`

	// Tool Diffs (3 colors)
	ToolDiffAdded   string `json:"toolDiffAdded"`
	ToolDiffRemoved string `json:"toolDiffRemoved"`
	ToolDiffContext string `json:"toolDiffContext"`

	// Syntax Highlighting (9 colors)
	SyntaxComment     string `json:"syntaxComment"`
	SyntaxKeyword     string `json:"syntaxKeyword"`
	SyntaxFunction    string `json:"syntaxFunction"`
	SyntaxVariable    string `json:"syntaxVariable"`
	SyntaxString      string `json:"syntaxString"`
	SyntaxNumber      string `json:"syntaxNumber"`
	SyntaxType        string `json:"syntaxType"`
	SyntaxOperator    string `json:"syntaxOperator"`
	SyntaxPunctuation string `json:"syntaxPunctuation"`

	// Thinking Level Borders (6 colors)
	ThinkingOff     string `json:"thinkingOff"`
	ThinkingMinimal string `json:"thinkingMinimal"`
	ThinkingLow     string `json:"thinkingLow"`
	ThinkingMedium  string `json:"thinkingMedium"`
	ThinkingHigh    string `json:"thinkingHigh"`
	ThinkingXhigh   string `json:"thinkingXhigh"`

	// Bash Mode (1 color)
	BashMode string `json:"bashMode"`
}

// ThemeExport contains export-related color overrides.
type ThemeExport struct {
	PageBg string `json:"pageBg,omitempty"`
	CardBg string `json:"cardBg,omitempty"`
	InfoBg string `json:"infoBg,omitempty"`
}

// allColorFieldNames returns the list of all required color field names for validation.
func allColorFieldNames() []string {
	return []string{
		"accent", "border", "borderAccent", "borderMuted", "success", "error",
		"warning", "muted", "dim", "text", "thinkingText",
		"selectedBg", "userMessageBg", "userMessageText", "customMessageBg",
		"customMessageText", "customMessageLabel", "toolPendingBg", "toolSuccessBg",
		"toolErrorBg", "toolTitle", "toolOutput",
		"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock",
		"mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
		"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
		"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable",
		"syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
		"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium",
		"thinkingHigh", "thinkingXhigh",
		"bashMode",
	}
}

// bgFieldNames returns the set of color keys that are background colors.
var bgFieldNames = map[string]bool{
	"selectedBg":      true,
	"userMessageBg":   true,
	"customMessageBg": true,
	"toolPendingBg":   true,
	"toolSuccessBg":   true,
	"toolErrorBg":     true,
}

// ValidateTheme checks that a ThemeJson has all required color fields.
func ValidateTheme(t *ThemeJson) error {
	if t == nil {
		return fmt.Errorf("theme is nil")
	}
	if t.Name == "" {
		return fmt.Errorf("theme missing name")
	}
	colorsMap, err := structToMap(&t.Colors)
	if err != nil {
		return fmt.Errorf("theme %q: failed to inspect colors: %w", t.Name, err)
	}
	var missing []string
	for _, field := range allColorFieldNames() {
		if _, ok := colorsMap[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("theme %q: missing required color tokens: %s", t.Name, strings.Join(missing, ", "))
	}
	return nil
}

// parseThemeJSON parses raw JSON bytes into a ThemeJson and validates it.
func parseThemeJSON(label string, data []byte) (*ThemeJson, error) {
	var tj ThemeJson
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("failed to parse theme %s: %w", label, err)
	}
	if err := ValidateTheme(&tj); err != nil {
		return nil, err
	}
	return &tj, nil
}

// GetDefaultDarkTheme returns the built-in dark theme.
func GetDefaultDarkTheme() *ThemeJson {
	tj, err := parseThemeJSON("dark", darkJSON)
	if err != nil {
		panic(fmt.Sprintf("built-in dark theme is invalid: %s", err))
	}
	return tj
}

// GetDefaultLightTheme returns the built-in light theme.
func GetDefaultLightTheme() *ThemeJson {
	tj, err := parseThemeJSON("light", lightJSON)
	if err != nil {
		panic(fmt.Sprintf("built-in light theme is invalid: %s", err))
	}
	return tj
}

// LoadThemeJSON loads and validates a theme from a file path.
func LoadThemeJSON(path string) (*ThemeJson, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read theme %s: %w", path, err)
	}
	return parseThemeJSON(filepath.Base(path), data)
}

// ResolveVarRefs resolves variable references in color values using the vars map.
// Variable references are strings that don't start with "#" and are not empty.
func ResolveVarRefs(value string, vars map[string]string) (string, error) {
	return resolveVarRefs(value, vars, nil)
}

func resolveVarRefs(value string, vars map[string]string, visited []string) (string, error) {
	if value == "" || strings.HasPrefix(value, "#") {
		return value, nil
	}
	// Check for circular references
	for _, v := range visited {
		if v == value {
			return "", fmt.Errorf("circular variable reference detected: %s", value)
		}
	}
	resolved, ok := vars[value]
	if !ok {
		return "", fmt.Errorf("variable reference not found: %s", value)
	}
	visited = append(visited, value)
	return resolveVarRefs(resolved, vars, visited)
}

// resolveThemeColors resolves all variable references in the theme's color values.
func resolveThemeColors(colors *ThemeColors, vars map[string]string) (*ThemeColors, error) {
	resolved := *colors // copy
	val, err := resolveStructFields(&resolved, vars)
	if err != nil {
		return nil, err
	}
	_ = val
	return &resolved, nil
}

// resolveStructFields uses reflection-free field resolution for ThemeColors.
func resolveStructFields(colors *ThemeColors, vars map[string]string) (map[string]string, error) {
	// Build a map of field name -> value, resolve, and set back.
	// Since ThemeColors is a known struct, we do this manually for performance.
	result := make(map[string]string, 50)

	fields := []struct {
		name string
		get  func() string
		set  func(string)
	}{
		{"accent", func() string { return colors.Accent }, func(v string) { colors.Accent = v }},
		{"border", func() string { return colors.Border }, func(v string) { colors.Border = v }},
		{"borderAccent", func() string { return colors.BorderAccent }, func(v string) { colors.BorderAccent = v }},
		{"borderMuted", func() string { return colors.BorderMuted }, func(v string) { colors.BorderMuted = v }},
		{"success", func() string { return colors.Success }, func(v string) { colors.Success = v }},
		{"error", func() string { return colors.Error }, func(v string) { colors.Error = v }},
		{"warning", func() string { return colors.Warning }, func(v string) { colors.Warning = v }},
		{"muted", func() string { return colors.Muted }, func(v string) { colors.Muted = v }},
		{"dim", func() string { return colors.Dim }, func(v string) { colors.Dim = v }},
		{"text", func() string { return colors.Text }, func(v string) { colors.Text = v }},
		{"thinkingText", func() string { return colors.ThinkingText }, func(v string) { colors.ThinkingText = v }},
		{"selectedBg", func() string { return colors.SelectedBg }, func(v string) { colors.SelectedBg = v }},
		{"userMessageBg", func() string { return colors.UserMessageBg }, func(v string) { colors.UserMessageBg = v }},
		{"userMessageText", func() string { return colors.UserMessageText }, func(v string) { colors.UserMessageText = v }},
		{"customMessageBg", func() string { return colors.CustomMessageBg }, func(v string) { colors.CustomMessageBg = v }},
		{"customMessageText", func() string { return colors.CustomMessageText }, func(v string) { colors.CustomMessageText = v }},
		{"customMessageLabel", func() string { return colors.CustomMessageLabel }, func(v string) { colors.CustomMessageLabel = v }},
		{"toolPendingBg", func() string { return colors.ToolPendingBg }, func(v string) { colors.ToolPendingBg = v }},
		{"toolSuccessBg", func() string { return colors.ToolSuccessBg }, func(v string) { colors.ToolSuccessBg = v }},
		{"toolErrorBg", func() string { return colors.ToolErrorBg }, func(v string) { colors.ToolErrorBg = v }},
		{"toolTitle", func() string { return colors.ToolTitle }, func(v string) { colors.ToolTitle = v }},
		{"toolOutput", func() string { return colors.ToolOutput }, func(v string) { colors.ToolOutput = v }},
		{"mdHeading", func() string { return colors.MdHeading }, func(v string) { colors.MdHeading = v }},
		{"mdLink", func() string { return colors.MdLink }, func(v string) { colors.MdLink = v }},
		{"mdLinkUrl", func() string { return colors.MdLinkUrl }, func(v string) { colors.MdLinkUrl = v }},
		{"mdCode", func() string { return colors.MdCode }, func(v string) { colors.MdCode = v }},
		{"mdCodeBlock", func() string { return colors.MdCodeBlock }, func(v string) { colors.MdCodeBlock = v }},
		{"mdCodeBlockBorder", func() string { return colors.MdCodeBlockBorder }, func(v string) { colors.MdCodeBlockBorder = v }},
		{"mdQuote", func() string { return colors.MdQuote }, func(v string) { colors.MdQuote = v }},
		{"mdQuoteBorder", func() string { return colors.MdQuoteBorder }, func(v string) { colors.MdQuoteBorder = v }},
		{"mdHr", func() string { return colors.MdHr }, func(v string) { colors.MdHr = v }},
		{"mdListBullet", func() string { return colors.MdListBullet }, func(v string) { colors.MdListBullet = v }},
		{"toolDiffAdded", func() string { return colors.ToolDiffAdded }, func(v string) { colors.ToolDiffAdded = v }},
		{"toolDiffRemoved", func() string { return colors.ToolDiffRemoved }, func(v string) { colors.ToolDiffRemoved = v }},
		{"toolDiffContext", func() string { return colors.ToolDiffContext }, func(v string) { colors.ToolDiffContext = v }},
		{"syntaxComment", func() string { return colors.SyntaxComment }, func(v string) { colors.SyntaxComment = v }},
		{"syntaxKeyword", func() string { return colors.SyntaxKeyword }, func(v string) { colors.SyntaxKeyword = v }},
		{"syntaxFunction", func() string { return colors.SyntaxFunction }, func(v string) { colors.SyntaxFunction = v }},
		{"syntaxVariable", func() string { return colors.SyntaxVariable }, func(v string) { colors.SyntaxVariable = v }},
		{"syntaxString", func() string { return colors.SyntaxString }, func(v string) { colors.SyntaxString = v }},
		{"syntaxNumber", func() string { return colors.SyntaxNumber }, func(v string) { colors.SyntaxNumber = v }},
		{"syntaxType", func() string { return colors.SyntaxType }, func(v string) { colors.SyntaxType = v }},
		{"syntaxOperator", func() string { return colors.SyntaxOperator }, func(v string) { colors.SyntaxOperator = v }},
		{"syntaxPunctuation", func() string { return colors.SyntaxPunctuation }, func(v string) { colors.SyntaxPunctuation = v }},
		{"thinkingOff", func() string { return colors.ThinkingOff }, func(v string) { colors.ThinkingOff = v }},
		{"thinkingMinimal", func() string { return colors.ThinkingMinimal }, func(v string) { colors.ThinkingMinimal = v }},
		{"thinkingLow", func() string { return colors.ThinkingLow }, func(v string) { colors.ThinkingLow = v }},
		{"thinkingMedium", func() string { return colors.ThinkingMedium }, func(v string) { colors.ThinkingMedium = v }},
		{"thinkingHigh", func() string { return colors.ThinkingHigh }, func(v string) { colors.ThinkingHigh = v }},
		{"thinkingXhigh", func() string { return colors.ThinkingXhigh }, func(v string) { colors.ThinkingXhigh = v }},
		{"bashMode", func() string { return colors.BashMode }, func(v string) { colors.BashMode = v }},
	}

	for _, f := range fields {
		resolved, err := ResolveVarRefs(f.get(), vars)
		if err != nil {
			return nil, fmt.Errorf("resolving color %q: %w", f.name, err)
		}
		f.set(resolved)
		result[f.name] = resolved
	}

	return result, nil
}

// structToMap converts ThemeColors to a map[string]string.
func structToMap(colors *ThemeColors) (map[string]string, error) {
	m := make(map[string]string, 50)
	m["accent"] = colors.Accent
	m["border"] = colors.Border
	m["borderAccent"] = colors.BorderAccent
	m["borderMuted"] = colors.BorderMuted
	m["success"] = colors.Success
	m["error"] = colors.Error
	m["warning"] = colors.Warning
	m["muted"] = colors.Muted
	m["dim"] = colors.Dim
	m["text"] = colors.Text
	m["thinkingText"] = colors.ThinkingText
	m["selectedBg"] = colors.SelectedBg
	m["userMessageBg"] = colors.UserMessageBg
	m["userMessageText"] = colors.UserMessageText
	m["customMessageBg"] = colors.CustomMessageBg
	m["customMessageText"] = colors.CustomMessageText
	m["customMessageLabel"] = colors.CustomMessageLabel
	m["toolPendingBg"] = colors.ToolPendingBg
	m["toolSuccessBg"] = colors.ToolSuccessBg
	m["toolErrorBg"] = colors.ToolErrorBg
	m["toolTitle"] = colors.ToolTitle
	m["toolOutput"] = colors.ToolOutput
	m["mdHeading"] = colors.MdHeading
	m["mdLink"] = colors.MdLink
	m["mdLinkUrl"] = colors.MdLinkUrl
	m["mdCode"] = colors.MdCode
	m["mdCodeBlock"] = colors.MdCodeBlock
	m["mdCodeBlockBorder"] = colors.MdCodeBlockBorder
	m["mdQuote"] = colors.MdQuote
	m["mdQuoteBorder"] = colors.MdQuoteBorder
	m["mdHr"] = colors.MdHr
	m["mdListBullet"] = colors.MdListBullet
	m["toolDiffAdded"] = colors.ToolDiffAdded
	m["toolDiffRemoved"] = colors.ToolDiffRemoved
	m["toolDiffContext"] = colors.ToolDiffContext
	m["syntaxComment"] = colors.SyntaxComment
	m["syntaxKeyword"] = colors.SyntaxKeyword
	m["syntaxFunction"] = colors.SyntaxFunction
	m["syntaxVariable"] = colors.SyntaxVariable
	m["syntaxString"] = colors.SyntaxString
	m["syntaxNumber"] = colors.SyntaxNumber
	m["syntaxType"] = colors.SyntaxType
	m["syntaxOperator"] = colors.SyntaxOperator
	m["syntaxPunctuation"] = colors.SyntaxPunctuation
	m["thinkingOff"] = colors.ThinkingOff
	m["thinkingMinimal"] = colors.ThinkingMinimal
	m["thinkingLow"] = colors.ThinkingLow
	m["thinkingMedium"] = colors.ThinkingMedium
	m["thinkingHigh"] = colors.ThinkingHigh
	m["thinkingXhigh"] = colors.ThinkingXhigh
	m["bashMode"] = colors.BashMode
	return m, nil
}
