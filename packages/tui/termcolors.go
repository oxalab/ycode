package tui

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// RgbColor is an 8-bit-per-channel RGB color.
type RgbColor struct{ R, G, B uint8 }

// TerminalColorScheme is a reported light/dark preference.
type TerminalColorScheme string

const (
	ColorSchemeDark  TerminalColorScheme = "dark"
	ColorSchemeLight TerminalColorScheme = "light"
)

var (
	osc11ResponseRe   = regexp.MustCompile(`(?i)^\x1b\]11;([^\x07\x1b]*)(?:\x07|\x1b\\)$`)
	colorSchemeReportRe = regexp.MustCompile(`^\x1b\[\?997;(1|2)n$`)
	oscRgbPrefixRe    = regexp.MustCompile(`(?i)^rgba?:`)
)

// IsOsc11BackgroundColorResponse reports whether data is a strict OSC 11 reply.
func IsOsc11BackgroundColorResponse(data string) bool {
	return osc11ResponseRe.MatchString(data)
}

// ParseOsc11BackgroundColor parses an OSC 11 background-color reply into RGB.
// Supports #RRGGBB, #RRRRGGGGBBBB, and rgb:/rgba: R/G/B (each channel hex of any
// width, scaled to 8-bit). Matches reference terminal-colors.ts.
func ParseOsc11BackgroundColor(data string) (RgbColor, bool) {
	m := osc11ResponseRe.FindStringSubmatch(data)
	if m == nil {
		return RgbColor{}, false
	}
	value := strings.TrimSpace(m[1])

	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		switch {
		case len(hex) == 6 && isAllHex(hex):
			return RgbColor{hexByte(hex[0:2]), hexByte(hex[2:4]), hexByte(hex[4:6])}, true
		case len(hex) == 12 && isAllHex(hex):
			r, ok1 := parseOscHexChannel(hex[0:4])
			g, ok2 := parseOscHexChannel(hex[4:8])
			b, ok3 := parseOscHexChannel(hex[8:12])
			if !ok1 || !ok2 || !ok3 {
				return RgbColor{}, false
			}
			return RgbColor{uint8(r), uint8(g), uint8(b)}, true
		}
		return RgbColor{}, false
	}

	rgb := oscRgbPrefixRe.ReplaceAllString(value, "")
	parts := strings.Split(rgb, "/")
	if len(parts) != 3 {
		return RgbColor{}, false
	}
	r, ok1 := parseOscHexChannel(parts[0])
	g, ok2 := parseOscHexChannel(parts[1])
	b, ok3 := parseOscHexChannel(parts[2])
	if !ok1 || !ok2 || !ok3 {
		return RgbColor{}, false
	}
	return RgbColor{uint8(r), uint8(g), uint8(b)}, true
}

// ParseTerminalColorSchemeReport parses a CSI ?997;Nn color-scheme report:
// "1" = dark, "2" = light.
func ParseTerminalColorSchemeReport(data string) (TerminalColorScheme, bool) {
	m := colorSchemeReportRe.FindStringSubmatch(data)
	if m == nil {
		return "", false
	}
	if m[1] == "2" {
		return ColorSchemeLight, true
	}
	return ColorSchemeDark, true
}

func hexByte(h string) uint8 {
	v, _ := strconv.ParseInt(h, 16, 0)
	return uint8(v)
}

func isAllHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseOscHexChannel(ch string) (int, bool) {
	if !isAllHex(ch) {
		return 0, false
	}
	v, err := strconv.ParseInt(ch, 16, 64)
	if err != nil {
		return 0, false
	}
	max := pow16(len(ch)) - 1
	if max <= 0 {
		return 0, false
	}
	return int(math.Round(float64(v) / float64(max) * 255)), true
}

func pow16(n int) int64 {
	r := int64(1)
	for i := 0; i < n; i++ {
		r *= 16
	}
	return r
}
