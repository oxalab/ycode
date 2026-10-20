package interactive

import (
	"strings"
	"testing"
)

// Resize-responsiveness R2: ExpandableText baked the closure output at
// construction (and on ctrl+o), but the closures read live terminal width for
// header centering — after a resize the header kept old-width centering until
// the next toggle. Render must re-evaluate the closure every call.
func TestExpandableTextReEvaluatesWidthOnRender(t *testing.T) {
	width := 100
	center := func(s string) string {
		pad := (width - len(s)) / 2
		if pad < 0 {
			pad = 0
		}
		return strings.Repeat(" ", pad) + s
	}
	e := NewExpandableText(
		func() string { return center("LOGO") },
		func() string { return center("LOGO EXPANDED") },
		false, 0, 0,
	)

	// Render at the construction width: centered for 100.
	got := strings.TrimRight(strings.Join(e.Render(100), "\n"), " ")
	if want := strings.Repeat(" ", 48) + "LOGO"; got != want {
		t.Fatalf("width 100: got %q, want %q", got, want)
	}

	// Simulate a resize: the closure's width source moves to 40.
	width = 40
	got = strings.TrimRight(strings.Join(e.Render(40), "\n"), " ")
	if want := strings.Repeat(" ", 18) + "LOGO"; got != want {
		t.Fatalf("width 40 after resize: got %q, want %q (stale centering?)", got, want)
	}

	// Toggle: expanded closure becomes the active one.
	e.SetExpanded(true)
	got = strings.TrimRight(strings.Join(e.Render(40), "\n"), " ")
	if want := strings.Repeat(" ", 13) + "LOGO EXPANDED"; got != want {
		t.Fatalf("expanded at width 40: got %q, want %q", got, want)
	}
	if !e.IsExpanded() {
		t.Fatal("IsExpanded false after SetExpanded(true)")
	}
}
