package interactive

import "testing"

// Tools re-audit B8: ansiStripRe is the runtime stripper for the interactive
// bash preview; it must handle the full npm strip-ansi pattern, not just plain
// SGR/CSI — OSC sequences (the app emits OSC 133 itself) and private modes
// used to leak into the TUI.
func TestAnsiStripReHandlesOSCAndPrivateModes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"osc bel keeps text", "\x1b]133;A\x07visible", "visible"},
		{"osc st keeps text", "\x1b]8;;http://e\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"private mode csi", "\x1b[?25hcursor", "cursor"},
		{"plain sgr still stripped", "\x1b[31mred\x1b[0m", "red"},
	}
	for _, tc := range cases {
		if got := ansiStripRe.ReplaceAllString(tc.in, ""); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
