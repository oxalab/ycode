package theme

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// ThemeColor identifies a foreground theme color slot.
type ThemeColor string

// ThemeBg identifies a background theme color slot.
type ThemeBg string

// ThinkingLevel represents a thinking level for border coloring.
type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXhigh   ThinkingLevel = "xhigh"
)

// Theme provides themed color styling for all TUI components.
type Theme struct {
	Name       string
	SourcePath string
	Export     *ThemeExport

	mode    ColorMode
	fgMap   map[ThemeColor]string // pre-computed ANSI escape for each fg color
	bgMap   map[ThemeBg]string    // pre-computed ANSI escape for each bg color
	colors  *ThemeColors          // resolved color values
}

var (
	globalTheme     *Theme
	globalThemeMu   sync.RWMutex
	currentThemeName string
	onThemeChangeFn  func()
	registeredThemes = make(map[string]*Theme)
)

// NewThemeFromJSON creates a Theme from a parsed ThemeJson, resolving all variable references.
func NewThemeFromJSON(tj *ThemeJson, mode *ColorMode) (*Theme, error) {
	colorMode := ColorModeTrueColor
	if mode != nil {
		colorMode = *mode
	} else {
		colorMode = DetectColorMode()
	}

	// Resolve variable references
	resolved, err := resolveThemeColors(&tj.Colors, tj.Vars)
	if err != nil {
		return nil, fmt.Errorf("theme %q: %w", tj.Name, err)
	}

	// Build ANSI maps
	fgMap := make(map[ThemeColor]string, 50)
	bgMap := make(map[ThemeBg]string, 6)

	colorsMap, _ := structToMap(resolved)
	for key, value := range colorsMap {
		if value == "" {
			// Empty means "reset to default"
			if bgFieldNames[key] {
				bgMap[ThemeBg(key)] = "\x1b[49m"
			} else {
				fgMap[ThemeColor(key)] = "\x1b[39m"
			}
			continue
		}
		ansi := fgAnsiRaw(value, 0, false, colorMode)
		if bgFieldNames[key] {
			bgMap[ThemeBg(key)] = bgAnsiRaw(value, 0, false, colorMode)
		} else {
			fgMap[ThemeColor(key)] = ansi
		}
	}

	return &Theme{
		Name:       tj.Name,
		SourcePath: "",
		Export:     tj.Export,
		mode:       colorMode,
		fgMap:      fgMap,
		bgMap:      bgMap,
		colors:     resolved,
	}, nil
}

// Fg applies a foreground theme color to text.
func (t *Theme) Fg(color ThemeColor, text string) string {
	ansi, ok := t.fgMap[color]
	if !ok {
		return text
	}
	return ansi + text + "\x1b[39m"
}

// Bg applies a background theme color to text.
func (t *Theme) Bg(bg ThemeBg, text string) string {
	ansi, ok := t.bgMap[bg]
	if !ok {
		return text
	}
	return ansi + text + "\x1b[49m"
}

// Bold wraps text in bold ANSI.
func (t *Theme) Bold(text string) string {
	return "\x1b[1m" + text + "\x1b[22m"
}

// Italic wraps text in italic ANSI.
func (t *Theme) Italic(text string) string {
	return "\x1b[3m" + text + "\x1b[23m"
}

// Underline wraps text in underline ANSI.
func (t *Theme) Underline(text string) string {
	return "\x1b[4m" + text + "\x1b[24m"
}

// Dim wraps text in dim ANSI.
func (t *Theme) Dim(text string) string {
	return "\x1b[2m" + text + "\x1b[22m"
}

// Strikethrough wraps text in strikethrough ANSI.
func (t *Theme) Strikethrough(text string) string {
	return "\x1b[9m" + text + "\x1b[29m"
}

// Inverse wraps text in inverse ANSI.
func (t *Theme) Inverse(text string) string {
	return "\x1b[7m" + text + "\x1b[27m"
}

// GetFgAnsi returns the raw ANSI escape for a foreground color.
func (t *Theme) GetFgAnsi(color ThemeColor) string {
	ansi, ok := t.fgMap[color]
	if !ok {
		return "\x1b[39m"
	}
	return ansi
}

// GetBgAnsi returns the raw ANSI escape for a background color.
func (t *Theme) GetBgAnsi(bg ThemeBg) string {
	ansi, ok := t.bgMap[bg]
	if !ok {
		return "\x1b[49m"
	}
	return ansi
}

// GetThinkingBorderColor returns a color function for the given thinking level.
func (t *Theme) GetThinkingBorderColor(level ThinkingLevel) func(string) string {
	switch level {
	case ThinkingOff:
		return func(s string) string { return t.Fg("thinkingOff", s) }
	case ThinkingMinimal:
		return func(s string) string { return t.Fg("thinkingMinimal", s) }
	case ThinkingLow:
		return func(s string) string { return t.Fg("thinkingLow", s) }
	case ThinkingMedium:
		return func(s string) string { return t.Fg("thinkingMedium", s) }
	case ThinkingHigh:
		return func(s string) string { return t.Fg("thinkingHigh", s) }
	case ThinkingXhigh:
		return func(s string) string { return t.Fg("thinkingXhigh", s) }
	default:
		return func(s string) string { return t.Fg("thinkingOff", s) }
	}
}

// GetBashModeBorderColor returns a color function for bash mode borders.
func (t *Theme) GetBashModeBorderColor() func(string) string {
	return func(s string) string { return t.Fg("bashMode", s) }
}

// GetColorMode returns the terminal color mode.
func (t *Theme) GetColorMode() ColorMode {
	return t.mode
}

// GetResolvedColors returns the resolved color values.
func (t *Theme) GetResolvedColors() *ThemeColors {
	return t.colors
}

// ToLipglossColor converts a theme fg color to a lipgloss.TerminalColor.
func (t *Theme) ToLipglossColor(color ThemeColor) lipgloss.TerminalColor {
	if t.colors == nil {
		return lipgloss.NoColor{}
	}
	colorsMap, _ := structToMap(t.colors)
	val, ok := colorsMap[string(color)]
	if !ok || val == "" {
		return lipgloss.NoColor{}
	}
	if strings.HasPrefix(val, "#") {
		return lipgloss.Color(val)
	}
	return lipgloss.Color(val)
}

// ToLipglossBgColor converts a theme bg color to a lipgloss.TerminalColor.
func (t *Theme) ToLipglossBgColor(bg ThemeBg) lipgloss.TerminalColor {
	return t.ToLipglossColor(ThemeColor(bg))
}

// --- Global Theme Functions ---

// GetTheme returns the current global theme instance.
func GetTheme() *Theme {
	globalThemeMu.RLock()
	defer globalThemeMu.RUnlock()
	if globalTheme == nil {
		panic("theme not initialized: call InitTheme first")
	}
	return globalTheme
}

// InitTheme initializes the global theme by name. Falls back to "dark" on error.
func InitTheme(themeName string) {
	if themeName == "" {
		themeName = detectTerminalBackground()
	}
	currentThemeName = themeName
	t, err := loadTheme(themeName)
	if err != nil {
		currentThemeName = "dark"
		t, _ = loadTheme("dark")
	}
	setGlobalTheme(t)
}

// SetTheme sets the global theme by name. Returns success and optional error message.
func SetTheme(name string) (bool, string) {
	currentThemeName = name
	t, err := loadTheme(name)
	if err != nil {
		currentThemeName = "dark"
		t, _ = loadTheme("dark")
		return false, err.Error()
	}
	setGlobalTheme(t)
	if onThemeChangeFn != nil {
		onThemeChangeFn()
	}
	return true, ""
}

// SetThemeInstance sets the global theme to a pre-built Theme instance.
func SetThemeInstance(t *Theme) {
	setGlobalTheme(t)
	currentThemeName = "<in-memory>"
	if onThemeChangeFn != nil {
		onThemeChangeFn()
	}
}

// OnThemeChange registers a callback that fires when the theme changes.
func OnThemeChange(fn func()) {
	onThemeChangeFn = fn
}

// SetRegisteredThemes registers themes from the resource loader.
func SetRegisteredThemes(themes []*Theme) {
	registeredThemes = make(map[string]*Theme, len(themes))
	for _, t := range themes {
		if t.Name != "" {
			registeredThemes[t.Name] = t
		}
	}
}

// GetAvailableThemes returns a sorted list of available theme names.
func GetAvailableThemes() []string {
	names := make(map[string]bool)

	// Built-in themes
	names["dark"] = true
	names["light"] = true

	// Registered themes
	for name := range registeredThemes {
		names[name] = true
	}

	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	// Sort
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i] > result[j] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// GetThemeByName loads a theme by name, returning nil if not found.
func GetThemeByName(name string) *Theme {
	t, err := loadTheme(name)
	if err != nil {
		return nil
	}
	return t
}

// GetCurrentThemeName returns the name of the current theme.
func GetCurrentThemeName() string {
	return currentThemeName
}

// StopThemeWatcher is a no-op placeholder for the TS file watcher pattern.
// File watching will be added later if needed.
func StopThemeWatcher() {}

// --- Internal helpers ---

func setGlobalTheme(t *Theme) {
	globalThemeMu.Lock()
	globalTheme = t
	globalThemeMu.Unlock()
}

func loadTheme(name string) (*Theme, error) {
	// Check registered themes first
	if t, ok := registeredThemes[name]; ok {
		return t, nil
	}

	// Built-in themes
	var tj *ThemeJson
	switch name {
	case "dark":
		tj = GetDefaultDarkTheme()
	case "light":
		tj = GetDefaultLightTheme()
	default:
		return nil, fmt.Errorf("theme not found: %s", name)
	}

	return NewThemeFromJSON(tj, nil)
}

func detectTerminalBackground() string {
	colorfgbg := ""
	if v := getColorFGBG(); v != "" {
		colorfgbg = v
	}
	if colorfgbg != "" {
		parts := strings.Split(colorfgbg, ";")
		if len(parts) >= 2 {
			bg := 0
			for _, c := range parts[len(parts)-1] {
				if c >= '0' && c <= '9' {
					bg = bg*10 + int(c-'0')
				} else {
					bg = -1
					break
				}
			}
			if bg >= 0 && bg < 8 {
				return "dark"
			}
		}
	}
	return "dark"
}

// getColorFGBG returns the COLORFGBG environment variable.
func getColorFGBG() string {
	return ""
}

// LoadThemeFromPath loads a theme from a file path.
func LoadThemeFromPath(themePath string) (*Theme, error) {
	tj, err := LoadThemeJSON(themePath)
	if err != nil {
		return nil, err
	}
	t, err := NewThemeFromJSON(tj, nil)
	if err != nil {
		return nil, err
	}
	t.SourcePath = themePath
	return t, nil
}
