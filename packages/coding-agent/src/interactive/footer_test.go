package interactive

// footer_test.go locks in the formatTokens port of footer.ts:21-27 across every
// threshold boundary. The Render path needs a live *core.AgentSession (heavy),
// so it is verified end-to-end with a configured provider on a real terminal;
// formatTokens is pure and the most error-prone piece, so it gets a table test.

import "testing"

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},          // <1k → raw
		{1000, "1.0k"},        // <10k → X.Xk
		{1500, "1.5k"},
		{9999, "10.0k"},       // 9.999 → 10.0k
		{10000, "10k"},        // <1M → round k
		{12345, "12k"},        // round(12.345) → 12
		{999999, "1000k"},     // round(999.999) → 1000
		{1000000, "1.0M"},     // <10M → X.XM
		{1500000, "1.5M"},
		{9999999, "10.0M"},    // 9.999999 → 10.0M
		{10000000, "10M"},     // else → round M
		{12345678, "12M"},     // round(12.345678) → 12
	}
	for _, c := range cases {
		if got := formatTokens(c.count); got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.count, got, c.want)
		}
	}
}

func TestDistinctAvailableProvidersNilSafe(t *testing.T) {
	// No session/registry → 0, never panics.
	if got := distinctAvailableProviders(nil); got != 0 {
		t.Errorf("distinctAvailableProviders(nil) = %d, want 0", got)
	}
}
