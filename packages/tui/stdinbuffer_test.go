package tui

import (
	"reflect"
	"testing"
	"time"
)

type sbCollector struct {
	data  []string
	paste []string
}

func (c *sbCollector) dataFn(s string)  { c.data = append(c.data, s) }
func (c *sbCollector) pasteFn(s string) { c.paste = append(c.paste, s) }

func newBuffer(t *testing.T, timeout time.Duration) (*StdinBuffer, *sbCollector) {
	t.Helper()
	c := &sbCollector{}
	b := NewStdinBuffer(timeout)
	b.OnData(c.dataFn)
	b.OnPaste(c.pasteFn)
	return b, c
}

func TestStdinRegularChars(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	b.Process("a")
	if !reflect.DeepEqual(c.data, []string{"a"}) {
		t.Errorf("got %v", c.data)
	}
	b.Process("abc")
	if !reflect.DeepEqual(c.data, []string{"a", "a", "b", "c"}) {
		t.Errorf("got %v", c.data)
	}
}

func TestStdinUnicode(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	b.Process("hello 世界")
	want := []string{"h", "e", "l", "l", "o", " ", "世", "界"}
	if !reflect.DeepEqual(c.data, want) {
		t.Errorf("got %v want %v", c.data, want)
	}
}

func TestStdinCompleteSequences(t *testing.T) {
	cases := []string{
		"\x1b[<35;20;5m", "\x1b[A", "\x1b[11~", "\x1ba", "\x1bOA",
	}
	for _, in := range cases {
		b, c := newBuffer(t, time.Second)
		b.Process(in)
		if !reflect.DeepEqual(c.data, []string{in}) {
			t.Errorf("Process(%q)=%v want [%q]", in, c.data, in)
		}
	}
}

func TestStdinPartialMouseAcrossChunks(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	b.Process("\x1b")
	if len(c.data) != 0 || b.Buffered() != "\x1b" {
		t.Fatalf("after ESC: data=%v buffer=%q", c.data, b.Buffered())
	}
	b.Process("[<35")
	if len(c.data) != 0 || b.Buffered() != "\x1b[<35" {
		t.Fatalf("after [\\<35: data=%v buffer=%q", c.data, b.Buffered())
	}
	b.Process(";20;5m")
	if !reflect.DeepEqual(c.data, []string{"\x1b[<35;20;5m"}) {
		t.Errorf("got %v", c.data)
	}
	if b.Buffered() != "" {
		t.Errorf("buffer should be empty, got %q", b.Buffered())
	}
}

func TestStdinCSISplit(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	b.Process("\x1b[")
	b.Process("1;")
	b.Process("5H")
	if !reflect.DeepEqual(c.data, []string{"\x1b[1;5H"}) {
		t.Errorf("got %v", c.data)
	}
}

func TestStdinSplitManyChunks(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	for _, ch := range []string{"\x1b", "[", "<", "3", "5", ";", "2", "0", ";", "5", "m"} {
		b.Process(ch)
	}
	if !reflect.DeepEqual(c.data, []string{"\x1b[<35;20;5m"}) {
		t.Errorf("got %v", c.data)
	}
}

func TestStdinFlushAfterTimeout(t *testing.T) {
	b, c := newBuffer(t, 10*time.Millisecond)
	b.Process("\x1b[<35")
	if len(c.data) != 0 {
		t.Fatalf("should buffer, got %v", c.data)
	}
	time.Sleep(60 * time.Millisecond)
	if !reflect.DeepEqual(c.data, []string{"\x1b[<35"}) {
		t.Errorf("after timeout got %v", c.data)
	}
}

func TestStdinMixedContent(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	b.Process("abc\x1b[A")
	if !reflect.DeepEqual(c.data, []string{"a", "b", "c", "\x1b[A"}) {
		t.Errorf("got %v", c.data)
	}

	b2, c2 := newBuffer(t, time.Second)
	b2.Process("\x1b[Aabc")
	if !reflect.DeepEqual(c2.data, []string{"\x1b[A", "a", "b", "c"}) {
		t.Errorf("got %v", c2.data)
	}

	b3, c3 := newBuffer(t, time.Second)
	b3.Process("\x1b[A\x1b[B\x1b[C")
	if !reflect.DeepEqual(c3.data, []string{"\x1b[A", "\x1b[B", "\x1b[C"}) {
		t.Errorf("got %v", c3.data)
	}
}

func TestStdinKitty(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"\x1b[97u", []string{"\x1b[97u"}},
		{"\x1b[97;1:3u", []string{"\x1b[97;1:3u"}},
		{"\x1b[97u\x1b[97;1:3u", []string{"\x1b[97u", "\x1b[97;1:3u"}},
		{"\x1b[97u\x1b[97;1:3u\x1b[98u\x1b[98;1:3u", []string{"\x1b[97u", "\x1b[97;1:3u", "\x1b[98u", "\x1b[98;1:3u"}},
		{"\x1b[1;1:1A", []string{"\x1b[1;1:1A"}},
		{"\x1b[3;1:3~", []string{"\x1b[3;1:3~"}},
		{"a\x1b[97;1:3u", []string{"a", "\x1b[97;1:3u"}},
	}
	for _, tc := range cases {
		b, c := newBuffer(t, time.Second)
		b.Process(tc.in)
		if !reflect.DeepEqual(c.data, tc.want) {
			t.Errorf("Process(%q)=%v want %v", tc.in, c.data, tc.want)
		}
	}
}

func TestStdinWezTermEscapeRegression(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"\x1b\x1b[27;129:3u", []string{"\x1b", "\x1b[27;129:3u"}},
		{"\x1b\x1b[27;1:3u", []string{"\x1b", "\x1b[27;1:3u"}},
		{"\x1b\x1b", []string{"\x1b\x1b"}}, // ctrl+alt+[
	}
	for _, tc := range cases {
		b, c := newBuffer(t, time.Second)
		b.Process(tc.in)
		if !reflect.DeepEqual(c.data, tc.want) {
			t.Errorf("Process(%q)=%v want %v", tc.in, c.data, tc.want)
		}
	}
}

func TestStdinKittyPrintableDedup(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"\x1b[224uà", []string{"\x1b[224u"}},               // raw à (U+00E0) is 224
		{"\x1b[97ub", []string{"\x1b[97u", "b"}},            // non-matching raw kept
		{"\x1b[64;3u@", []string{"\x1b[64;3u", "@"}},        // modified seq: raw kept
	}
	for _, tc := range cases {
		b, c := newBuffer(t, time.Second)
		b.Process(tc.in)
		if !reflect.DeepEqual(c.data, tc.want) {
			t.Errorf("Process(%q)=%v want %v", tc.in, c.data, tc.want)
		}
	}

	// Dedup across chunks.
	b, c := newBuffer(t, time.Second)
	b.Process("\x1b[64u")
	b.Process("@")
	if !reflect.DeepEqual(c.data, []string{"\x1b[64u"}) {
		t.Errorf("cross-chunk dedup got %v", c.data)
	}
}

func TestStdinMouse(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"\x1b[<0;10;5M", []string{"\x1b[<0;10;5M"}},
		{"\x1b[<0;10;5m", []string{"\x1b[<0;10;5m"}},
		{"\x1b[<35;20;5m", []string{"\x1b[<35;20;5m"}},
		{"\x1b[<35;1;1m\x1b[<35;2;2m\x1b[<35;3;3m", []string{"\x1b[<35;1;1m", "\x1b[<35;2;2m", "\x1b[<35;3;3m"}},
		{"\x1b[M abc", []string{"\x1b[M ab", "c"}}, // old-style: ESC[M + 3 bytes
	}
	for _, tc := range cases {
		b, c := newBuffer(t, time.Second)
		b.Process(tc.in)
		if !reflect.DeepEqual(c.data, tc.want) {
			t.Errorf("Process(%q)=%v want %v", tc.in, c.data, tc.want)
		}
	}

	// Split old-style mouse.
	b, c := newBuffer(t, time.Second)
	b.Process("\x1b[M")
	if b.Buffered() != "\x1b[M" {
		t.Fatalf("buffer=%q", b.Buffered())
	}
	b.Process(" a")
	if b.Buffered() != "\x1b[M a" {
		t.Fatalf("buffer=%q", b.Buffered())
	}
	b.Process("b")
	if !reflect.DeepEqual(c.data, []string{"\x1b[M ab"}) {
		t.Errorf("got %v", c.data)
	}
}

func TestStdinEdgeCases(t *testing.T) {
	// Empty input emits an empty data event.
	b, c := newBuffer(t, time.Second)
	b.Process("")
	if !reflect.DeepEqual(c.data, []string{""}) {
		t.Errorf("empty got %v", c.data)
	}

	// Lone escape via explicit flush.
	b2, c2 := newBuffer(t, time.Second)
	b2.Process("\x1b")
	if len(c2.data) != 0 {
		t.Fatalf("should buffer, got %v", c2.data)
	}
	flushed := b2.Flush()
	if !reflect.DeepEqual(flushed, []string{"\x1b"}) {
		t.Errorf("flush=%v", flushed)
	}
}

func TestStdinClear(t *testing.T) {
	b, c := newBuffer(t, time.Second)
	b.Process("\x1b[<35")
	if b.Buffered() != "\x1b[<35" {
		t.Fatalf("buffer=%q", b.Buffered())
	}
	b.Clear()
	if b.Buffered() != "" || len(c.data) != 0 {
		t.Errorf("after clear buffer=%q data=%v", b.Buffered(), c.data)
	}
}

func TestStdinBracketedPaste(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		b, c := newBuffer(t, time.Second)
		b.Process("\x1b[200~hello world\x1b[201~")
		if !reflect.DeepEqual(c.paste, []string{"hello world"}) || len(c.data) != 0 {
			t.Errorf("paste=%v data=%v", c.paste, c.data)
		}
	})
	t.Run("chunks", func(t *testing.T) {
		b, c := newBuffer(t, time.Second)
		b.Process("\x1b[200~")
		b.Process("hello ")
		b.Process("world\x1b[201~")
		if !reflect.DeepEqual(c.paste, []string{"hello world"}) || len(c.data) != 0 {
			t.Errorf("paste=%v data=%v", c.paste, c.data)
		}
	})
	t.Run("input before and after", func(t *testing.T) {
		b, c := newBuffer(t, time.Second)
		b.Process("a")
		b.Process("\x1b[200~pasted\x1b[201~")
		b.Process("b")
		if !reflect.DeepEqual(c.data, []string{"a", "b"}) {
			t.Errorf("data=%v", c.data)
		}
		if !reflect.DeepEqual(c.paste, []string{"pasted"}) {
			t.Errorf("paste=%v", c.paste)
		}
	})
	t.Run("unicode and newlines", func(t *testing.T) {
		b, c := newBuffer(t, time.Second)
		b.Process("\x1b[200~Hello 世界 🎉\nline2\x1b[201~")
		if !reflect.DeepEqual(c.paste, []string{"Hello 世界 🎉\nline2"}) {
			t.Errorf("paste=%v", c.paste)
		}
	})
}
