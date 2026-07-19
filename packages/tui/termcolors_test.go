package tui

import (
	"testing"
)

func TestParseOsc11BackgroundColor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want RgbColor
		ok   bool
	}{
		{"16-bit rgb", "\x1b]11;rgb:0000/8000/ffff\x07", RgbColor{0, 128, 255}, true},
		{"hex ffffff ST", "\x1b]11;#ffffff\x1b\\", RgbColor{255, 255, 255}, true},
		{"hex 000000 BEL", "\x1b]11;#000000\x07", RgbColor{0, 0, 0}, true},
		{"leading junk", "x\x1b]11;#ffffff\x07", RgbColor{}, false},
		{"wrong OSC", "\x1b]10;#ffffff\x07", RgbColor{}, false},
		{"trailing junk", "\x1b]11;#ffffff\x07x", RgbColor{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseOsc11BackgroundColor(c.in)
			if ok != c.ok {
				t.Fatalf("ok=%v want %v for %q", ok, c.ok, c.in)
			}
			if ok && got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
}

func TestIsOsc11BackgroundColorResponse(t *testing.T) {
	if !IsOsc11BackgroundColorResponse("\x1b]11;#ffffff\x07") {
		t.Fatal("should match")
	}
	if IsOsc11BackgroundColorResponse("\x1b]10;#ffffff\x07") {
		t.Fatal("should not match")
	}
}

func TestParseTerminalColorSchemeReport(t *testing.T) {
	cases := []struct {
		in   string
		want TerminalColorScheme
		ok   bool
	}{
		{"\x1b[?997;1n", ColorSchemeDark, true},
		{"\x1b[?997;2n", ColorSchemeLight, true},
		{"\x1b[?997;3n", "", false},
		{"\x1b[?996n", "", false},
		{"x\x1b[?997;1n", "", false},
	}
	for _, c := range cases {
		got, ok := ParseTerminalColorSchemeReport(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("ParseTerminalColorSchemeReport(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
