package tui

import (
	"testing"
)

func TestFuzzyMatch(t *testing.T) {
	t.Run("empty query matches everything with score 0", func(t *testing.T) {
		r := FuzzyMatch("", "anything")
		if !r.Matches || r.Score != 0 {
			t.Fatalf("got %+v, want {Matches:true Score:0}", r)
		}
	})
	t.Run("query longer than text does not match", func(t *testing.T) {
		if FuzzyMatch("longquery", "short").Matches {
			t.Fatal("should not match")
		}
	})
	t.Run("exact match has negative score", func(t *testing.T) {
		r := FuzzyMatch("test", "test")
		if !r.Matches || r.Score >= 0 {
			t.Fatalf("got %+v, want negative score", r)
		}
	})
	t.Run("characters must appear in order", func(t *testing.T) {
		if !FuzzyMatch("abc", "aXbXc").Matches {
			t.Fatal("in-order should match")
		}
		if FuzzyMatch("abc", "cba").Matches {
			t.Fatal("out-of-order should not match")
		}
	})
	t.Run("case insensitive", func(t *testing.T) {
		if !FuzzyMatch("ABC", "abc").Matches || !FuzzyMatch("abc", "ABC").Matches {
			t.Fatal("should be case insensitive")
		}
	})
	t.Run("consecutive beats scattered", func(t *testing.T) {
		consecutive := FuzzyMatch("foo", "foobar")
		scattered := FuzzyMatch("foo", "f_o_o_bar")
		if !(consecutive.Matches && scattered.Matches && consecutive.Score < scattered.Score) {
			t.Fatalf("consecutive %+v should score better than scattered %+v", consecutive, scattered)
		}
	})
	t.Run("word boundary beats non-boundary", func(t *testing.T) {
		atBoundary := FuzzyMatch("fb", "foo-bar")
		notAtBoundary := FuzzyMatch("fb", "afbx")
		if !(atBoundary.Score < notAtBoundary.Score) {
			t.Fatalf("boundary %+v should beat non-boundary %+v", atBoundary, notAtBoundary)
		}
	})
	t.Run("swapped alpha-numeric tokens", func(t *testing.T) {
		if !FuzzyMatch("codex52", "gpt-5.2-codex").Matches {
			t.Fatal("swap fallback should match")
		}
	})
}

func TestFuzzyFilter(t *testing.T) {
	getText := func(s string) string { return s }

	t.Run("empty query returns all unchanged", func(t *testing.T) {
		items := []string{"apple", "banana", "cherry"}
		got := FuzzyFilter(items, "", getText)
		if len(got) != 3 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("filters non-matching", func(t *testing.T) {
		got := FuzzyFilter([]string{"apple", "banana", "cherry"}, "an", getText)
		hasBanana := false
		for _, s := range got {
			if s == "banana" {
				hasBanana = true
			}
			if s == "apple" || s == "cherry" {
				t.Fatal("non-match should be filtered")
			}
		}
		if !hasBanana {
			t.Fatal("banana should remain")
		}
	})
	t.Run("sorts by quality", func(t *testing.T) {
		got := FuzzyFilter([]string{"a_p_p", "app", "application"}, "app", getText)
		if got[0] != "app" {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("prioritizes exact over prefix", func(t *testing.T) {
		got := FuzzyFilter([]string{"clone", "cl"}, "cl", getText)
		if len(got) != 2 || got[0] != "cl" || got[1] != "clone" {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("custom getText with structs", func(t *testing.T) {
		type item struct {
			name string
			id   int
		}
		items := []item{{"foo", 1}, {"bar", 2}, {"foobar", 3}}
		got := FuzzyFilter(items, "foo", func(i item) string { return i.name })
		if len(got) != 2 {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("slash-separated reordered tokens", func(t *testing.T) {
		type model struct {
			id, provider string
		}
		item := model{"gpt-5.5", "openai-codex"}
		got := FuzzyFilter([]model{item}, "openai-codex/gpt-5.5", func(m model) string {
			return m.id + " " + m.provider
		})
		if len(got) != 1 {
			t.Fatalf("got %v", got)
		}
	})
}
