package theme

import (
	"strings"
	"testing"
)

func TestHexToRGB(t *testing.T) {
	tests := []struct {
		hex string
		r, g, b int
		wantErr bool
	}{
		{"#ff0000", 255, 0, 0, false},
		{"#00ff00", 0, 255, 0, false},
		{"#0000ff", 0, 0, 255, false},
		{"#8abeb7", 138, 190, 183, false},
		{"#000000", 0, 0, 0, false},
		{"#ffffff", 255, 255, 255, false},
		{"invalid", 0, 0, 0, true},
		{"#fff", 0, 0, 0, true}, // too short
	}

	for _, tt := range tests {
		t.Run(tt.hex, func(t *testing.T) {
			r, g, b, err := hexToRGB(tt.hex)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r != tt.r || g != tt.g || b != tt.b {
				t.Errorf("hexToRGB(%q) = (%d, %d, %d), want (%d, %d, %d)",
					tt.hex, r, g, b, tt.r, tt.g, tt.b)
			}
		})
	}
}

func TestRGBTo256(t *testing.T) {
	tests := []struct {
		name     string
		r, g, b  int
		expected int
	}{
		{"black", 0, 0, 0, 16},
		{"white", 255, 255, 255, 231},
		{"red", 255, 0, 0, 196},
		{"green", 0, 255, 0, 46},
		{"blue", 0, 0, 255, 21},
		{"pure gray", 128, 128, 128, 244},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rgbTo256(tt.r, tt.g, tt.b)
			if got != tt.expected {
				t.Errorf("rgbTo256(%d, %d, %d) = %d, want %d",
					tt.r, tt.g, tt.b, got, tt.expected)
			}
		})
	}
}

func TestRGBTo256_GrayscaleFallback(t *testing.T) {
	// Nearly neutral gray should use grayscale ramp for better accuracy
	got := rgbTo256(100, 100, 100)
	if got < 232 || got > 255 {
		t.Errorf("rgbTo256(100,100,100) = %d, expected grayscale index (232-255)", got)
	}
}

func TestFgAnsi_TrueColor(t *testing.T) {
	// Hex color in truecolor mode
	val := ColorValue{Hex: "#ff0000"}
	result := fgAnsi(val, ColorModeTrueColor)
	if !strings.Contains(result, "\x1b[38;2;255;0;0m") {
		t.Errorf("fgAnsi(#ff0000, truecolor) = %q, expected truecolor escape", result)
	}

	// Empty color = reset
	empty := ColorValue{Hex: ""}
	result = fgAnsi(empty, ColorModeTrueColor)
	if result != "\x1b[39m" {
		t.Errorf("fgAnsi(empty) = %q, expected reset", result)
	}
}

func TestFgAnsi_256Color(t *testing.T) {
	val := ColorValue{Hex: "#ff0000"}
	result := fgAnsi(val, ColorMode256Color)
	if !strings.HasPrefix(result, "\x1b[38;5;") {
		t.Errorf("fgAnsi(#ff0000, 256color) = %q, expected 256-color escape", result)
	}
}

func TestBgAnsi_TrueColor(t *testing.T) {
	val := ColorValue{Hex: "#00ff00"}
	result := bgAnsi(val, ColorModeTrueColor)
	if !strings.Contains(result, "\x1b[48;2;0;255;0m") {
		t.Errorf("bgAnsi(#00ff00, truecolor) = %q, expected truecolor bg escape", result)
	}
}

func TestBgAnsi_Empty(t *testing.T) {
	empty := ColorValue{Hex: ""}
	result := bgAnsi(empty, ColorModeTrueColor)
	if result != "\x1b[49m" {
		t.Errorf("bgAnsi(empty) = %q, expected bg reset", result)
	}
}

func TestColorValueTypes(t *testing.T) {
	// Hex
	cv := ColorValue{Hex: "#ff0000"}
	if !cv.IsHex() {
		t.Error("expected IsHex() = true")
	}
	if cv.IsVariable() {
		t.Error("expected IsVariable() = false")
	}

	// Variable ref
	cv = ColorValue{Hex: "accent"}
	if cv.IsHex() {
		t.Error("expected IsHex() = false")
	}
	if !cv.IsVariable() {
		t.Error("expected IsVariable() = true")
	}

	// Empty
	cv = ColorValue{Hex: ""}
	if !cv.IsEmpty() {
		t.Error("expected IsEmpty() = true")
	}
}

func TestResolveVarRefs(t *testing.T) {
	vars := map[string]string{
		"accent":    "#8abeb7",
		"blue":      "#5f87ff",
		"darkGray":  "#505050",
		"reference": "blue",
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"hex passthrough", "#ff0000", "#ff0000", false},
		{"empty passthrough", "", "", false},
		{"simple var", "accent", "#8abeb7", false},
		{"chained var", "reference", "#5f87ff", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveVarRefs(tt.input, vars)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveVarRefs(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveVarRefs_Circular(t *testing.T) {
	vars := map[string]string{
		"a": "b",
		"b": "a",
	}
	_, err := ResolveVarRefs("a", vars)
	if err == nil {
		t.Error("expected circular reference error")
	}
}

func TestResolveVarRefs_Missing(t *testing.T) {
	vars := map[string]string{
		"accent": "#8abeb7",
	}
	_, err := ResolveVarRefs("nonexistent", vars)
	if err == nil {
		t.Error("expected missing variable error")
	}
}

func TestLoadDefaultDarkTheme(t *testing.T) {
	tj := GetDefaultDarkTheme()
	if tj.Name != "dark" {
		t.Errorf("dark theme name = %q, want %q", tj.Name, "dark")
	}
	// Verify some known color values are present
	if tj.Colors.Accent != "accent" {
		t.Errorf("dark accent = %q, want %q", tj.Colors.Accent, "accent")
	}
	if tj.Colors.Error != "red" {
		t.Errorf("dark error = %q, want %q", tj.Colors.Error, "red")
	}
}

func TestLoadDefaultLightTheme(t *testing.T) {
	tj := GetDefaultLightTheme()
	if tj.Name != "light" {
		t.Errorf("light theme name = %q, want %q", tj.Name, "light")
	}
	if tj.Colors.Accent != "teal" {
		t.Errorf("light accent = %q, want %q", tj.Colors.Accent, "teal")
	}
}

func TestNewThemeFromJSON(t *testing.T) {
	tj := GetDefaultDarkTheme()
	theme, err := NewThemeFromJSON(tj, nil)
	if err != nil {
		t.Fatalf("NewThemeFromJSON(dark) error: %v", err)
	}
	if theme.Name != "dark" {
		t.Errorf("theme name = %q, want %q", theme.Name, "dark")
	}

	// Fg should produce colored output
	colored := theme.Fg("error", "test")
	if !strings.Contains(colored, "test") {
		t.Error("Fg() output doesn't contain input text")
	}
	// Should have ANSI codes (not raw text)
	if !strings.Contains(colored, "\x1b[") {
		t.Error("Fg() output doesn't contain ANSI escape codes")
	}

	// Bold should wrap in bold ANSI
	bold := theme.Bold("hello")
	if !strings.Contains(bold, "\x1b[1m") {
		t.Errorf("Bold() = %q, expected bold escape", bold)
	}

	// Italic
	italic := theme.Italic("hello")
	if !strings.Contains(italic, "\x1b[3m") {
		t.Errorf("Italic() = %q, expected italic escape", italic)
	}
}

func TestNewThemeFromJSON_VarResolution(t *testing.T) {
	tj := GetDefaultDarkTheme()
	theme, err := NewThemeFromJSON(tj, nil)
	if err != nil {
		t.Fatalf("NewThemeFromJSON(dark) error: %v", err)
	}
	// "accent" in dark theme resolves via vars: accent -> #8abeb7
	// The fgMap should have a resolved ANSI code for ThemeColor("accent")
	ansi := theme.GetFgAnsi("accent")
	if ansi == "" || ansi == "\x1b[39m" {
		t.Errorf("accent fg ansi = %q, expected resolved color", ansi)
	}
}

func TestTheme_ThinkingBorderColor(t *testing.T) {
	tj := GetDefaultDarkTheme()
	theme, err := NewThemeFromJSON(tj, nil)
	if err != nil {
		t.Fatalf("NewThemeFromJSON error: %v", err)
	}

	levels := []ThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXhigh}
	for _, level := range levels {
		fn := theme.GetThinkingBorderColor(level)
		result := fn("test")
		if !strings.Contains(result, "test") {
			t.Errorf("GetThinkingBorderColor(%s) didn't preserve text", level)
		}
		if !strings.Contains(result, "\x1b[") {
			t.Errorf("GetThinkingBorderColor(%s) = %q, no ANSI codes", level, result)
		}
	}
}

func TestTheme_BgColors(t *testing.T) {
	tj := GetDefaultDarkTheme()
	theme, err := NewThemeFromJSON(tj, nil)
	if err != nil {
		t.Fatalf("NewThemeFromJSON error: %v", err)
	}

	// Bg should work for background color keys
	bg := theme.Bg("userMessageBg", "test")
	if !strings.Contains(bg, "test") {
		t.Error("Bg() didn't preserve text")
	}
	if !strings.Contains(bg, "\x1b[") {
		t.Errorf("Bg() = %q, expected ANSI codes", bg)
	}
}

func TestTheme_EmptyColor(t *testing.T) {
	tj := GetDefaultDarkTheme()
	theme, err := NewThemeFromJSON(tj, nil)
	if err != nil {
		t.Fatalf("NewThemeFromJSON error: %v", err)
	}

	// "text" in dark theme is "" (empty = reset to default)
	colored := theme.Fg("text", "hello")
	if colored != "hello\x1b[39m" && colored != "\x1b[39mhello\x1b[39m" {
		// Either the reset comes before or after — both acceptable
		if !strings.Contains(colored, "hello") || !strings.Contains(colored, "\x1b[39m") {
			t.Errorf("Fg(text, hello) = %q, unexpected", colored)
		}
	}
}

func TestValidateTheme(t *testing.T) {
	// Valid theme
	tj := GetDefaultDarkTheme()
	if err := ValidateTheme(tj); err != nil {
		t.Errorf("ValidateTheme(dark) error: %v", err)
	}

	// Nil theme
	if err := ValidateTheme(nil); err == nil {
		t.Error("ValidateTheme(nil) expected error")
	}

	// Missing name
	tj2 := &ThemeJson{Name: "", Colors: tj.Colors}
	if err := ValidateTheme(tj2); err == nil {
		t.Error("ValidateTheme(missing name) expected error")
	}
}

func TestDetectColorMode(t *testing.T) {
	mode := DetectColorMode()
	if mode != ColorModeTrueColor && mode != ColorMode256Color {
		t.Errorf("DetectColorMode() = %q, expected truecolor or 256color", mode)
	}
}
