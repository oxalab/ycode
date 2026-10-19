package components

import "testing"

// Tools re-audit B8: the old ESC-run state machine terminated on any byte in
// 0x40-0x7E (so an OSC's `]` "ended" the escape) and never recognized BEL —
// OSC BEL bytes leaked and the first visible char after them was eaten.
func TestStripANSIHandlesOSCWithoutEatingText(t *testing.T) {
	if got := stripANSI("\x1b]133;A\x07ok"); got != "ok" {
		t.Errorf("osc bel: got %q, want %q", got, "ok")
	}
	if got := stripANSI("\x1b[?25hx"); got != "x" {
		t.Errorf("private mode: got %q, want %q", got, "x")
	}
	if got := stripANSI("\x1b[31mred\x1b[0m"); got != "red" {
		t.Errorf("plain sgr: got %q, want %q", got, "red")
	}
}
