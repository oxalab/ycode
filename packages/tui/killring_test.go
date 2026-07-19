package tui

import "testing"

func TestKillRingPushPeek(t *testing.T) {
	k := NewKillRing()
	if _, ok := k.Peek(); ok {
		t.Fatal("empty ring should not peek")
	}
	k.Push("a", false, false)
	k.Push("b", false, false)
	if got, _ := k.Peek(); got != "b" || k.Len() != 2 {
		t.Fatalf("peek=%q len=%d", got, k.Len())
	}
}

func TestKillRingAccumulate(t *testing.T) {
	t.Run("prepend (backward)", func(t *testing.T) {
		k := NewKillRing()
		k.Push("three", true, false)
		k.Push("two ", true, true) // prepend -> "two three"
		k.Push("one ", true, true) // prepend -> "one two three"
		if got, _ := k.Peek(); got != "one two three" || k.Len() != 1 {
			t.Fatalf("got %q len %d", got, k.Len())
		}
	})
	t.Run("append (forward)", func(t *testing.T) {
		k := NewKillRing()
		k.Push("one", false, false)
		k.Push("two", false, true) // append -> "onetwo"
		if got, _ := k.Peek(); got != "onetwo" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("accumulate on empty just pushes", func(t *testing.T) {
		k := NewKillRing()
		k.Push("only", true, true)
		if got, _ := k.Peek(); got != "only" || k.Len() != 1 {
			t.Fatalf("got %q len %d", got, k.Len())
		}
	})
}

func TestKillRingRotate(t *testing.T) {
	k := NewKillRing()
	k.Push("a", false, false)
	k.Push("b", false, false)
	k.Push("c", false, false)
	// ["a","b","c"] -> rotate -> ["c","a","b"], peek == "b"
	k.Rotate()
	if got, _ := k.Peek(); got != "b" {
		t.Fatalf("after rotate peek=%q want b", got)
	}
	k.Rotate()
	if got, _ := k.Peek(); got != "a" {
		t.Fatalf("after 2 rotates peek=%q want a", got)
	}
	// rotate with <=1 entry is a no-op
	single := NewKillRing()
	single.Push("x", false, false)
	single.Rotate()
	if got, _ := single.Peek(); got != "x" {
		t.Fatalf("single rotate peek=%q", got)
	}
}
