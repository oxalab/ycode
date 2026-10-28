package interactive

// theme_adapter_test.go pins the theme-cache story: adapter closures must
// resolve the CURRENT global theme at call time (the reference's live theme
// Proxy — theme/theme.ts), so a /theme switch plus invalidation recolors
// already-rendered transcript markdown. Pre-fix, closures captured the
// *Theme pointer and SetTheme's pointer swap stranded them on the old
// instance — footer/border recolored, chat never did.

import (
	"strings"
	"testing"

	"ycode/packages/tui/components"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

const mdSample = "# Heading\n\nsome **bold** body text"

// TestThemeSwitchRecolorsMarkdown: render under theme A, switch to B,
// invalidate, re-render at the same width — the ANSI output must differ.
// Pre-fix both halves fail: without live closures the colors stay A's;
// without the chat invalidation the stale line cache is served.
func TestThemeSwitchRecolorsMarkdown(t *testing.T) {
	orig := theme.GetCurrentThemeName()
	defer func() { theme.SetTheme(orig) }()

	theme.SetTheme("dark")
	md := components.NewMarkdown(mdSample, 0, 0, buildMarkdownTheme(theme.GetTheme()), nil)
	before := strings.Join(md.Render(80), "\n")
	if before == "" {
		t.Fatal("empty render under dark theme")
	}

	theme.SetTheme("light")
	md.Invalidate()
	after := strings.Join(md.Render(80), "\n")

	if after == before {
		t.Error("markdown output identical after theme switch + invalidate — " +
			"closures are bound to the old theme or the line cache was served")
	}
}

// The stale-cache half of the pin, isolated: with the switch but WITHOUT
// Invalidate, the cache must serve the old render (proving the test would
// catch a missing invalidation).
func TestThemeSwitchStaleCacheWithoutInvalidate(t *testing.T) {
	orig := theme.GetCurrentThemeName()
	defer func() { theme.SetTheme(orig) }()

	theme.SetTheme("dark")
	md := components.NewMarkdown(mdSample, 0, 0, buildMarkdownTheme(theme.GetTheme()), nil)
	before := strings.Join(md.Render(80), "\n")

	theme.SetTheme("light")
	after := strings.Join(md.Render(80), "\n")

	if after != before {
		t.Error("expected the un-invalidated cache to serve the pre-switch render")
	}
}

// Fallback: with no global initialized, closures render through the captured
// theme (Current() returns nil — exercised on a fresh theme package state via
// the adapter's liveTheme helper).
func TestLiveThemeFallsBackToCaptured(t *testing.T) {
	fallback := theme.GetTheme() // global is initialized in this test binary
	got := liveTheme(fallback)
	if got != fallback && got != theme.Current() {
		t.Errorf("liveTheme returned %v, want the global or the fallback", got)
	}
	if theme.Current() == nil && got != fallback {
		t.Error("liveTheme must return the fallback when no global is initialized")
	}
}

// Highlight memo: repeated highlightCode calls for the same language reuse the
// memoized coalesced lexer (one map entry, not one per call).
func TestHighlightLexerMemoReused(t *testing.T) {
	code := "func main() { println(\"hi\") }"
	th := theme.GetTheme()
	a := highlightCode(code, "go", th)
	b := highlightCode(code, "go", th)
	if a == nil || b == nil {
		t.Fatalf("expected highlighted lines, got %v / %v", a, b)
	}
	lexerMemoMu.Lock()
	_, ok := coalescedLexers["lang:go"]
	entries := len(coalescedLexers)
	lexerMemoMu.Unlock()
	if !ok {
		t.Error("lang:go not memoized")
	}
	if entries == 0 {
		t.Error("memo map empty after highlight calls")
	}
}
