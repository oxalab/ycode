package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Tools re-audit B9: Append did string(data) per chunk, but pipe reads are
// fixed 4096-byte buffers that can split a multi-byte rune across chunks —
// each split decoded to U+FFFD. Append must hold an incomplete trailing
// sequence and reassemble it with the next chunk (TextDecoder stream:true
// parity).
func TestOutputAccumulatorReassemblesSplitRune(t *testing.T) {
	accum := NewOutputAccumulator(OutputAccumulatorOptions{})
	you := []byte("你") // E4 BD A0

	accum.Append([]byte("a"))
	accum.Append(you[:1]) // dangling initiator — must be held, not decoded
	mid := accum.Snapshot().Content
	if strings.ContainsRune(mid, utf8.RuneError) {
		t.Fatalf("incomplete rune decoded early: %q", mid)
	}
	accum.Append(you[1:])
	accum.Append([]byte("b"))
	accum.Finish()

	if got := accum.Snapshot().Content; got != "a你b" {
		t.Fatalf("got %q, want %q", got, "a你b")
	}
}

// The end-of-stream flush: a rune still incomplete at Finish() decodes to the
// replacement char, like the reference's final decoder.decode() call.
func TestOutputAccumulatorFlushesDanglingTail(t *testing.T) {
	accum := NewOutputAccumulator(OutputAccumulatorOptions{})
	accum.Append([]byte("ok "))
	accum.Append([]byte("你")[:2]) // E4 BD — incomplete 你
	accum.Finish()

	got := accum.Snapshot().Content
	if !strings.HasPrefix(got, "ok ") {
		t.Fatalf("prefix lost: %q", got)
	}
	if !strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("dangling tail not flushed: %q", got)
	}
}

// ASCII across multiple appends is unaffected by the streaming decoder.
func TestOutputAccumulatorAsciiMultiAppend(t *testing.T) {
	accum := NewOutputAccumulator(OutputAccumulatorOptions{})
	accum.Append([]byte("line1\n"))
	accum.Append([]byte("line2\n"))
	accum.Append([]byte("line3"))
	accum.Finish()
	if got := accum.Snapshot().Content; got != "line1\nline2\nline3" {
		t.Fatalf("got %q", got)
	}
}

// The spilled temp file receives raw process bytes verbatim (both sides of the
// reference) — including a rune that was split across Append calls.
func TestOutputAccumulatorSpillPreservesRawBytes(t *testing.T) {
	maxBytes := 8
	accum := NewOutputAccumulator(OutputAccumulatorOptions{MaxBytes: &maxBytes})
	raw := []byte("你好, world — this output spills to the temp file")
	accum.Append(raw[:3]) // splits 你 mid-rune
	accum.Append(raw[3:])
	accum.Finish()
	if err := accum.CloseTempFile(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	if accum.tempFilePath == "" {
		t.Fatal("expected a spilled temp file")
	}
	contents, err := os.ReadFile(accum.tempFilePath)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(contents) != string(raw) {
		t.Fatalf("temp file corrupted:\n got %q\nwant %q", contents, raw)
	}
}

// B9: the bash tool passes "ycode-bash" (reference bash.ts passes "pi-bash");
// the old call site passed a "ycode-base" prefix paralleled by nothing.
func TestOutputAccumulatorTempPrefix(t *testing.T) {
	mk := func(opts OutputAccumulatorOptions) string {
		maxBytes := 1
		opts.MaxBytes = &maxBytes
		a := NewOutputAccumulator(opts)
		a.Append([]byte("spill me"))
		a.Finish()
		a.CloseTempFile()
		if a.tempFilePath == "" {
			t.Fatal("expected a spilled temp file")
		}
		return filepath.Base(a.tempFilePath)
	}

	if base := mk(OutputAccumulatorOptions{}); !strings.HasPrefix(base, "ycode-output-") {
		t.Errorf("default prefix: %q", base)
	}
	if base := mk(OutputAccumulatorOptions{TempFilePrefix: "ycode-bash"}); !strings.HasPrefix(base, "ycode-bash-") {
		t.Errorf("explicit prefix: %q", base)
	}
}
