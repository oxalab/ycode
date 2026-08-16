package theme

import (
	"fmt"
	"os"
	"strings"
)

// ColorMode represents the terminal's color capability.
type ColorMode string

const (
	ColorModeTrueColor ColorMode = "truecolor"
	ColorMode256Color  ColorMode = "256color"
)

// ColorValue represents a theme color: either a hex string ("#ff0000"),
// a 256-color index (numeric), a variable reference ("primary"), or empty ("").
type ColorValue struct {
	Hex   string // "#ff0000" or "" or a variable name
	Index *int   // nil for hex/variable refs, non-nil for 256-color index
}

// IsHex returns true if this is a hex color value.
func (c ColorValue) IsHex() bool {
	return c.Index == nil && strings.HasPrefix(c.Hex, "#")
}

// IsVariable returns true if this looks like a variable reference (not hex, not empty, not numeric).
func (c ColorValue) IsVariable() bool {
	return c.Index == nil && c.Hex != "" && !strings.HasPrefix(c.Hex, "#")
}

// IsEmpty returns true if the color value is empty (reset to default).
func (c ColorValue) IsEmpty() bool {
	return c.Index == nil && c.Hex == ""
}

// DetectColorMode determines the terminal's color capability.
func DetectColorMode() ColorMode {
	colorterm := os.Getenv("COLORTERM")
	if colorterm == "truecolor" || colorterm == "24bit" {
		return ColorModeTrueColor
	}
	// Windows Terminal supports truecolor
	if os.Getenv("WT_SESSION") != "" {
		return ColorModeTrueColor
	}
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" || term == "linux" {
		return ColorMode256Color
	}
	// Terminal.app doesn't support truecolor
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return ColorMode256Color
	}
	// GNU screen doesn't support truecolor unless explicitly opted in
	if term == "screen" || strings.HasPrefix(term, "screen-") || strings.HasPrefix(term, "screen.") {
		return ColorMode256Color
	}
	// Assume truecolor for everything else
	return ColorModeTrueColor
}

// The 6x6x6 color cube channel values (indices 0-5).
var cubeValues = [6]int{0, 95, 135, 175, 215, 255}

// Grayscale ramp values (indices 232-255, 24 grays from 8 to 238).
var grayValues [24]int

func init() {
	for i := 0; i < 24; i++ {
		grayValues[i] = 8 + i*10
	}
}

// hexToRGB converts a hex color string to r, g, b components.
func hexToRGB(hex string) (r, g, b int, err error) {
	cleaned := strings.TrimPrefix(hex, "#")
	if len(cleaned) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex color: %s", hex)
	}
	var ri, gi, bi int
	_, err = fmt.Sscanf(cleaned, "%02x%02x%02x", &ri, &gi, &bi)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid hex color: %s", hex)
	}
	return ri, gi, bi, nil
}

// colorDistance computes weighted Euclidean distance between two RGB colors.
// Human eye is more sensitive to green.
func colorDistance(r1, g1, b1, r2, g2, b2 int) float64 {
	dr := float64(r1 - r2)
	dg := float64(g1 - g2)
	db := float64(b1 - b2)
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

// findClosestCubeIndex finds the nearest 6x6x6 cube channel value index.
func findClosestCubeIndex(value int) int {
	minDist := float64(1<<30)
	minIdx := 0
	for i, v := range cubeValues {
		d := float64(abs(value - v))
		if d < minDist {
			minDist = d
			minIdx = i
		}
	}
	return minIdx
}

// findClosestGrayIndex finds the nearest grayscale ramp value index.
func findClosestGrayIndex(gray int) int {
	minDist := float64(1 << 30)
	minIdx := 0
	for i, v := range grayValues {
		d := float64(abs(gray - v))
		if d < minDist {
			minDist = d
			minIdx = i
		}
	}
	return minIdx
}

// rgbTo256 maps an RGB color to the nearest 256-color palette index.
func rgbTo256(r, g, b int) int {
	// Find closest 6x6x6 cube color
	rIdx := findClosestCubeIndex(r)
	gIdx := findClosestCubeIndex(g)
	bIdx := findClosestCubeIndex(b)
	cubeR := cubeValues[rIdx]
	cubeG := cubeValues[gIdx]
	cubeB := cubeValues[bIdx]
	cubeIndex := 16 + 36*rIdx + 6*gIdx + bIdx
	cubeDist := colorDistance(r, g, b, cubeR, cubeG, cubeB)

	// Find closest grayscale
	gray := int(float64(r)*0.299 + float64(g)*0.587 + float64(b)*0.114)
	grayIdx := findClosestGrayIndex(gray)
	grayValue := grayValues[grayIdx]
	grayIndex := 232 + grayIdx
	grayDist := colorDistance(r, g, b, grayValue, grayValue, grayValue)

	// Check if color has noticeable saturation
	maxC := max(r, g, b)
	minC := min(r, g, b)
	spread := maxC - minC

	// Only consider grayscale if nearly neutral (spread < 10) AND closer
	if spread < 10 && grayDist < cubeDist {
		return grayIndex
	}

	return cubeIndex
}

// fgAnsi returns the ANSI escape sequence for a foreground color.
// val is either a hex string, an integer (256-color index), or empty (reset).
func fgAnsi(val ColorValue, mode ColorMode) string {
	if val.IsEmpty() {
		return "\x1b[39m"
	}
	if val.Index != nil {
		return fmt.Sprintf("\x1b[38;5;%dm", *val.Index)
	}
	if val.IsHex() {
		r, g, b, err := hexToRGB(val.Hex)
		if err != nil {
			return "\x1b[39m"
		}
		if mode == ColorModeTrueColor {
			return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
		}
		idx := rgbTo256(r, g, b)
		return fmt.Sprintf("\x1b[38;5;%dm", idx)
	}
	// Variable reference — return reset (should be resolved before calling)
	return "\x1b[39m"
}

// bgAnsi returns the ANSI escape sequence for a background color.
func bgAnsi(val ColorValue, mode ColorMode) string {
	if val.IsEmpty() {
		return "\x1b[49m"
	}
	if val.Index != nil {
		return fmt.Sprintf("\x1b[48;5;%dm", *val.Index)
	}
	if val.IsHex() {
		r, g, b, err := hexToRGB(val.Hex)
		if err != nil {
			return "\x1b[49m"
		}
		if mode == ColorModeTrueColor {
			return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
		}
		idx := rgbTo256(r, g, b)
		return fmt.Sprintf("\x1b[48;5;%dm", idx)
	}
	return "\x1b[49m"
}

// fgAnsiRaw returns the ANSI escape for a raw resolved color value (string hex or int index).
func fgAnsiRaw(resolved string, idx int, hasIndex bool, mode ColorMode) string {
	if hasIndex {
		return fmt.Sprintf("\x1b[38;5;%dm", idx)
	}
	if resolved == "" {
		return "\x1b[39m"
	}
	if strings.HasPrefix(resolved, "#") {
		r, g, b, err := hexToRGB(resolved)
		if err != nil {
			return "\x1b[39m"
		}
		if mode == ColorModeTrueColor {
			return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
		}
		i := rgbTo256(r, g, b)
		return fmt.Sprintf("\x1b[38;5;%dm", i)
	}
	return "\x1b[39m"
}

// bgAnsiRaw returns the ANSI escape for a raw resolved background color.
func bgAnsiRaw(resolved string, idx int, hasIndex bool, mode ColorMode) string {
	if hasIndex {
		return fmt.Sprintf("\x1b[48;5;%dm", idx)
	}
	if resolved == "" {
		return "\x1b[49m"
	}
	if strings.HasPrefix(resolved, "#") {
		r, g, b, err := hexToRGB(resolved)
		if err != nil {
			return "\x1b[49m"
		}
		if mode == ColorModeTrueColor {
			return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
		}
		i := rgbTo256(r, g, b)
		return fmt.Sprintf("\x1b[48;5;%dm", i)
	}
	return "\x1b[49m"
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b, c int) int {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

func min(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
