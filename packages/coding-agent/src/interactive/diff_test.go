package interactive

// diff_test.go locks in the word-diff port (diffmatchpatch token-encoding) and
// the renderDiff pipeline: single removed→added pairs get intra-line inverse
// highlighting, multi-line hunks render whole-line, tabs expand to 3 spaces.

import (
	"strings"
	"testing"

	"ycode/packages/coding-agent/src/core/tui/theme"
)

func diffTheme(t *testing.T) *theme.Theme {
	t.Helper()
	ensureTheme()
	return theme.GetTheme()
}

// visibleText strips ANSI SGR sequences so assertions see only the text.
func visibleText(s string) string {
	return ansiStripRe.ReplaceAllString(s, "")
}

func TestDiffWordsSingleWordChange(t *testing.T) {
	parts := diffWords("the quick brown fox", "the quick red fox")
	// Expect: equal("the quick "), removed("brown"), added("red"), equal(" fox")
	if len(parts) < 3 {
		t.Fatalf("expected >=3 parts, got %d: %+v", len(parts), parts)
	}
	var removed, added string
	for _, p := range parts {
		if p.removed {
			removed += p.value
		}
		if p.added {
			added += p.value
		}
	}
	if strings.TrimSpace(removed) != "brown" {
		t.Errorf("removed = %q, want \"brown\"", removed)
	}
	if strings.TrimSpace(added) != "red" {
		t.Errorf("added = %q, want \"red\"", added)
	}
}

func TestDiffWordsIdentical(t *testing.T) {
	parts := diffWords("same words here", "same words here")
	for _, p := range parts {
		if p.removed || p.added {
			t.Errorf("identical input produced removed/added part: %+v", p)
		}
	}
	joined := ""
	for _, p := range parts {
		joined += p.value
	}
	if joined != "same words here" {
		t.Errorf("round-trip = %q", joined)
	}
}

func TestDiffWordsEmptyInputs(t *testing.T) {
	parts := diffWords("", "")
	if len(parts) == 0 {
		return // empty diff is fine
	}
	for _, p := range parts {
		if p.value != "" {
			t.Errorf("empty inputs produced non-empty part: %+v", p)
		}
	}
}

func TestDiffWordsPureAddition(t *testing.T) {
	parts := diffWords("", "new words")
	var added string
	for _, p := range parts {
		if p.removed {
			t.Errorf("pure addition produced removed part: %+v", p)
		}
		if p.added {
			added += p.value
		}
	}
	if strings.TrimSpace(added) != "new words" {
		t.Errorf("added = %q, want \"new words\"", added)
	}
}

func TestDiffWordsUnicode(t *testing.T) {
	parts := diffWords("héllo wörld", "héllo wårld")
	var removed, added string
	for _, p := range parts {
		if p.removed {
			removed += p.value
		}
		if p.added {
			added += p.value
		}
	}
	if strings.TrimSpace(removed) != "wörld" {
		t.Errorf("removed = %q, want \"wörld\"", removed)
	}
	if strings.TrimSpace(added) != "wårld" {
		t.Errorf("added = %q, want \"wårld\"", added)
	}
}

func TestDiffWordsRoundTripPreservesText(t *testing.T) {
	// The token encoding must not lose or duplicate content: concatenating
	// non-added parts reproduces the old text; non-removed reproduces the new.
	oldText := "alpha beta gamma delta epsilon"
	newText := "alpha BETA gamma delta omega"
	parts := diffWords(oldText, newText)
	var fromRemoved, fromAdded string
	for _, p := range parts {
		if !p.added {
			fromRemoved += p.value
		}
		if !p.removed {
			fromAdded += p.value
		}
	}
	if fromRemoved != oldText {
		t.Errorf("old round-trip = %q, want %q", fromRemoved, oldText)
	}
	if fromAdded != newText {
		t.Errorf("new round-trip = %q, want %q", fromAdded, newText)
	}
}

func TestRenderIntraLineDiffInverseOnChangedWords(t *testing.T) {
	th := diffTheme(t)
	removed, added := renderIntraLineDiff(th, "the quick brown fox", "the quick red fox")

	// Visible text must round-trip the originals (minus the changed word swap).
	if got := visibleText(removed); got != "the quick brown fox" {
		t.Errorf("removed visible = %q", got)
	}
	if got := visibleText(added); got != "the quick red fox" {
		t.Errorf("added visible = %q", got)
	}
	// The changed words must carry inverse styling; the unchanged prefix must not.
	if !strings.Contains(removed, th.Inverse("brown")) {
		t.Errorf("removed line missing inverse on \"brown\": %q", removed)
	}
	if !strings.Contains(added, th.Inverse("red")) {
		t.Errorf("added line missing inverse on \"red\": %q", added)
	}
	if !strings.HasPrefix(removed, "the quick ") {
		t.Errorf("unchanged prefix should be unstyled, got %q", removed)
	}
}

func TestRenderIntraLineDiffPreservesLeadingIndent(t *testing.T) {
	th := diffTheme(t)
	// Indentation should stay plain (not inverse-highlighted).
	removed, _ := renderIntraLineDiff(th, "    if x {", "        if x {")
	if !strings.HasPrefix(removed, "    ") {
		t.Errorf("leading indent not preserved: %q", removed)
	}
}

func TestRenderDiffSinglePairGetsWordDiff(t *testing.T) {
	th := diffTheme(t)
	out := renderDiff("-1 the quick brown fox\n+1 the quick red fox\n", th)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], th.Inverse("brown")) {
		t.Errorf("removed line missing word-diff: %q", lines[0])
	}
	if !strings.Contains(lines[1], th.Inverse("red")) {
		t.Errorf("added line missing word-diff: %q", lines[1])
	}
}

func TestRenderDiffMultiLineHunkNoWordDiff(t *testing.T) {
	th := diffTheme(t)
	// Two removed + two added → whole-line colors only, no inverse spans.
	out := renderDiff("-1 line one old\n-2 line two old\n+1 line one new\n+2 line two new\n", th)
	if strings.Contains(out, th.Inverse("old")) {
		t.Errorf("multi-line hunk should not word-diff: %q", out)
	}
	if strings.Contains(out, th.Inverse("new")) {
		t.Errorf("multi-line hunk should not word-diff: %q", out)
	}
	// All four lines still render (input's trailing \n yields a 5th empty
	// line, same as the reference — count non-empty visible lines).
	nonEmpty := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(visibleText(l)) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 4 {
		t.Errorf("expected 4 non-empty lines, got %d", nonEmpty)
	}
}

func TestRenderDiffRemovedOnlyHunk(t *testing.T) {
	th := diffTheme(t)
	out := renderDiff("-1 gone\n-2 also gone\n", th)
	if strings.Contains(out, th.Inverse("gone")) {
		t.Errorf("removed-only hunk should not word-diff: %q", out)
	}
}

func TestRenderDiffContextLine(t *testing.T) {
	th := diffTheme(t)
	out := renderDiff(" 1 unchanged\n", th)
	if !strings.Contains(visibleText(out), "unchanged") {
		t.Errorf("context line missing: %q", out)
	}
}

func TestRenderDiffStandaloneAdded(t *testing.T) {
	th := diffTheme(t)
	out := renderDiff("+1 brand new line\n", th)
	if !strings.Contains(visibleText(out), "brand new line") {
		t.Errorf("added line missing: %q", out)
	}
}

func TestRenderDiffTabsExpanded(t *testing.T) {
	th := diffTheme(t)
	// Tabs must become 3 spaces before diffing so indentation renders flat.
	out := renderDiff("-1 \tindented\n+1 \t\tmore indented\n", th)
	if strings.Contains(out, "\t") {
		t.Errorf("tab not expanded: %q", out)
	}
}

func TestRenderDiffUnparsedLineFallsThrough(t *testing.T) {
	th := diffTheme(t)
	out := renderDiff("not a diff line at all\n", th)
	if !strings.Contains(out, "not a diff line at all") {
		t.Errorf("unparsed line dropped: %q", out)
	}
}

func TestIsDiffLike(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"+1 added\n", true},
		{"- 12 removed\n", true},
		{"plain text\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isDiffLike(c.in); got != c.want {
			t.Errorf("isDiffLike(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
