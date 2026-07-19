package tui

import "testing"

func TestFindWordBackward(t *testing.T) {
	cases := []struct {
		text   string
		cursor int
		want   int
	}{
		{"hello world", 11, 6},
		{"hello world", 6, 0},
		{"foo.bar", 7, 4},
		{"foo.bar", 4, 3},
		{"foo.bar", 3, 0},
		{"foo:bar", 7, 4},
		{"foo:bar", 4, 3},
		{"foo:bar", 3, 0},
		{"path/to/file", 12, 8},
		{"path/to/file", 8, 7},
		{"path/to/file", 7, 5},
		{"path/to/file", 5, 4},
		{"path/to/file", 4, 0},
		{"  hello  ", 9, 2},
		{"  hello  ", 2, 0},
		{"foo...bar", 9, 6},
		{"foo...bar", 6, 3},
		{"foo...bar", 3, 0},
		{"hello", 0, 0},
	}
	for _, c := range cases {
		if got := FindWordBackward(c.text, c.cursor, nil); got != c.want {
			t.Errorf("FindWordBackward(%q,%d)=%d want %d", c.text, c.cursor, got, c.want)
		}
	}
}

func TestFindWordForward(t *testing.T) {
	cases := []struct {
		text   string
		cursor int
		want   int
	}{
		{"hello world", 0, 5},
		{"hello world", 5, 11},
		{"foo.bar", 0, 3},
		{"foo.bar", 3, 4},
		{"foo.bar", 4, 7},
		{"foo:bar", 0, 3},
		{"foo:bar", 3, 4},
		{"foo:bar", 4, 7},
		{"path/to/file", 0, 4},
		{"path/to/file", 4, 5},
		{"path/to/file", 5, 7},
		{"path/to/file", 7, 8},
		{"path/to/file", 8, 12},
		{"  hello  ", 0, 7},
		{"  hello  ", 7, 9},
		{"foo...bar", 0, 3},
		{"foo...bar", 3, 6},
		{"foo...bar", 6, 9},
		{"hello", 5, 5},
	}
	for _, c := range cases {
		if got := FindWordForward(c.text, c.cursor, nil); got != c.want {
			t.Errorf("FindWordForward(%q,%d)=%d want %d", c.text, c.cursor, got, c.want)
		}
	}
}

func ws(pairs ...any) []WordSegment {
	var segs []WordSegment
	for i := 0; i+1 < len(pairs); i += 2 {
		segs = append(segs, WordSegment{Text: pairs[i].(string), IsWordLike: pairs[i+1].(bool)})
	}
	return segs
}

// Atomic-segment handling (paste markers), using an injected segmenter like the
// reference word-navigation.test.ts "atomic segments" suite.
func TestWordNavAtomicSegments(t *testing.T) {
	marker := "[paste #1 +5 lines]"
	text := "hello " + marker + " world"

	seg := func(input string) []WordSegment {
		switch input {
		case text:
			return ws("hello", true, " ", false, marker, true, " ", false, "world", true)
		case text[:26]:
			return ws("hello", true, " ", false, marker, true, " ", false)
		case text[6:]:
			return ws(marker, true, " ", false, "world", true)
		}
		return nil
	}
	opts := &WordNavOptions{
		Segment:  seg,
		IsAtomic: func(s string) bool { return s == marker },
	}

	if got := FindWordBackward(text, len(text), opts); got != 26 {
		t.Errorf("backward(end)=%d want 26", got)
	}
	if got := FindWordBackward(text, 26, opts); got != 6 {
		t.Errorf("backward(26)=%d want 6", got)
	}
	if got := FindWordForward(text, 6, opts); got != 6+len(marker) {
		t.Errorf("forward(6)=%d want %d", got, 6+len(marker))
	}
}

// TestWordNavCJKDeviation documents the known CJK deviation: the reference uses
// Intl.Segmenter dictionary segmentation (你好/世界 as words); this Go port
// segments CJK per-grapheme. Latin behavior is unaffected.
func TestWordNavCJKDeviation(t *testing.T) {
	text := "你好世界 test"
	// backward through trailing "test" matches the reference.
	if got := FindWordBackward(text, len([]rune(text)), nil); got != 5 {
		t.Errorf("backward(end)=%d want 5", got)
	}
	// Per-grapheme CJK: the reference returns 2 and 0 (dictionary words 你好/世界);
	// this port returns 3 and 1 (one grapheme per step).
	if got := FindWordBackward(text, 5, nil); got != 3 {
		t.Errorf("backward(5)=%d want 3 (per-grapheme; Intl reference is 2)", got)
	}
}
