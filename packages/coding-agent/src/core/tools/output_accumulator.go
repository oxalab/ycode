package tools

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type OutputAccumulatorOptions struct {
	MaxLines       *int
	MaxBytes       *int
	TempFilePrefix string
}

type OutputSnapshot struct {
	Content        string
	Truncation     TruncationResult
	FullOutputPath string
}

func defaultTempFilePath(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	return filepath.Join(os.TempDir(), prefix+"-"+id+".log")
}

/*
OutputAccumulator: Incrementally tracks streaming output with bounded memory.
 - Appends decoded chunks, keeps a rolling tail for display snapshots,
 and opens a temp file when the full output needs to be preserved
*/
type OutputAccumulator struct {
	maxLines 	int
	maxBytes 	int
	maxRollingBytes int
	tempFilePrefix string

	mu 			sync.Mutex
	rawChunks 	[][]byte
	pendingTail []byte
	tailText 	string
	tailBytes 	int
	tailStartsAtLineBound bool
	totalRawBytes int
	totalDecodedBytes int
	totalLines 	int
	currentLineBytes int
	finished 	bool

	tempFilePath string
	tempFile 	*os.File
}

func NewOutputAccumulator(opts OutputAccumulatorOptions) *OutputAccumulator {
	maxLines := DefaultMaxLines
	if opts.MaxLines != nil {
		maxLines = *opts.MaxLines
	}
	maxBytes := DefaultMaxBytes
	if opts.MaxBytes != nil {
		maxBytes = *opts.MaxBytes
	}
	prefix := opts.TempFilePrefix
	if prefix == "" {
		prefix = "ycode-output"
	}
	return &OutputAccumulator{
		maxLines: maxLines,
		maxBytes: maxBytes,
		maxRollingBytes: max(maxBytes*2, 1),
		tempFilePrefix: prefix,
		rawChunks: make([][]byte, 0),
		totalLines: 1,
		tailStartsAtLineBound: true,
	}
}

func (a *OutputAccumulator) Append(data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	if a.finished {
		panic("cannot append to a finished output accumulator")
	}

	a.totalRawBytes += len(data)
	text := a.decodeStreamed(data)
	a.appendDecodedText(text)

	if a.tempFile != nil || a.shouldUseTempFile() {
		a.ensureTempFile()
		if a.tempFile != nil {
			a.tempFile.Write(data)
		}
	} else if len(data) > 0 {
		a.rawChunks = append(a.rawChunks, data)
	}
}

// decodeStreamed converts a chunk to text the way the reference's
// TextDecoder({stream:true}) does: an incomplete multi-byte sequence at the
// end is held back (pendingTail) and prepended to the next chunk instead of
// decoding to U+FFFD. Pipe reads are fixed 4096-byte buffers and can split a
// rune across chunks; the old per-chunk string(data) corrupted every such rune.
func (a *OutputAccumulator) decodeStreamed(data []byte) string {
	buf := append(a.pendingTail, data...)
	n := completeUTF8PrefixLen(buf)
	a.pendingTail = append(a.pendingTail[:0], buf[n:]...)
	if len(a.pendingTail) == 0 {
		a.pendingTail = nil
	}
	return string(buf[:n])
}

// completeUTF8PrefixLen returns the length of the prefix of buf that ends at a
// rune boundary, holding back only a genuinely incomplete trailing sequence.
// A dangling initiator or short continuation run is held; a continuation run
// with no valid initiator is invalid and flushed as-is (string() renders it as
// U+FFFD, the closest Go equivalent of the TextDecoder's replacement char).
func completeUTF8PrefixLen(buf []byte) int {
	n := len(buf)
	if n == 0 {
		return 0
	}
	last := buf[n-1]
	if last&0xc0 != 0x80 {
		// ASCII ends a rune; a multi-byte initiator alone is incomplete.
		switch {
		case last&0xe0 == 0xc0, last&0xf0 == 0xe0, last&0xf8 == 0xf0:
			return n - 1
		default:
			return n
		}
	}
	i := n
	for i > 0 && n-i < 3 && buf[i-1]&0xc0 == 0x80 {
		i--
	}
	if i == 0 {
		return n // continuation run with no initiator — invalid, flush
	}
	c := buf[i-1]
	var want int
	switch {
	case c&0xe0 == 0xc0:
		want = 2
	case c&0xf0 == 0xe0:
		want = 3
	case c&0xf8 == 0xf0:
		want = 4
	default:
		return n
	}
	if n-(i-1) < want {
		return i - 1 // incomplete trailing rune: hold it back
	}
	return n
}

func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.finished {
		return
	}
	a.finished = true
	if len(a.pendingTail) > 0 {
		a.appendDecodedText(string(a.pendingTail))
		a.pendingTail = nil
	}
	if a.shouldUseTempFile() {
		a.ensureTempFile()
	}
}

func (a *OutputAccumulator) Snapshot(opts ...bool) OutputSnapshot {
	var persistIfTruncated bool
	if len(opts) > 0 {
		persistIfTruncated = opts[0]
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	tailTruncation := TruncateTail(a.getSnapshotText(), TruncationOptions{
		MaxLines: &a.maxLines,
		MaxBytes: &a.maxBytes,
	})
	truncated := a.totalLines > a.maxLines || a.totalDecodedBytes > a.maxBytes
	var truncatedBy *string
	if truncated {
		if tailTruncation.TruncatedBy != nil {
			truncatedBy = tailTruncation.TruncatedBy
		} else {
			tb := "lines"
			if a.totalDecodedBytes > a.maxBytes {
				tb = "bytes"
			}
			truncatedBy = &tb
		}
	}

	truncation := tailTruncation
	truncation.Truncated = truncated
	truncation.TruncatedBy = truncatedBy
	truncation.TotalLines = a.totalLines
	truncation.TotalBytes = a.totalDecodedBytes
	truncation.MaxLines = a.maxLines
	truncation.MaxBytes = a.maxBytes

	if persistIfTruncated && truncation.Truncated {
		a.ensureTempFile()
	}

	return OutputSnapshot{
		Content:        truncation.Content,
		Truncation:     truncation,
		FullOutputPath: a.tempFilePath,
	}
}
func (a *OutputAccumulator) CloseTempFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.tempFile == nil {
		return nil
	}
	err := a.tempFile.Close()
	a.tempFile = nil 
	return err
}

func (a *OutputAccumulator) GetLastLineBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentLineBytes
}

func (a *OutputAccumulator) appendDecodedText(text string) {
	if len(text) == 0 {
		return
	}

	bytes := len([]byte(text))
	a.totalDecodedBytes += bytes
	a.tailText += text
	a.tailBytes += bytes
	if a.tailBytes > a.maxRollingBytes*2 {
		a.trimTail()
	}
	
	newlines := strings.Count(text,"\n")
	if newlines == 0 {
		a.currentLineBytes += bytes
	} else {
		a.totalLines += newlines
		lastNewline := strings.LastIndex(text, "\n")
		a.currentLineBytes = len([]byte(text[lastNewline+1:]))
	}
}

func (a *OutputAccumulator) trimTail() {
	buf := []byte(a.tailText)
	if len(buf) <= a.maxRollingBytes {
		a.tailBytes = len(buf)
		return
	}
	start := len(buf) - a.maxRollingBytes
	for start < len(buf) && (buf[start]&0xc0) == 0x80 {
		start++
	}
	if start > 0 {
		a.tailStartsAtLineBound = buf[start-1] == 0x0a
	}
	a.tailText = string(buf[start:])
	a.tailBytes = len([]byte(a.tailText))
}

func (a *OutputAccumulator) getSnapshotText() string {
	if a.tailStartsAtLineBound {
		return a.tailText
	}
	firstNewline := strings.Index(a.tailText, "\n")
	if firstNewline == -1 {
		return a.tailText
	}
	return a.tailText[firstNewline+1:]
}

func (a *OutputAccumulator) shouldUseTempFile() bool {
	return a.totalRawBytes > a.maxBytes || a.totalDecodedBytes > a.maxBytes || a.totalLines > a.maxLines
}

func (a *OutputAccumulator) ensureTempFile() {
	if a.tempFilePath != "" {
		return
	}
	a.tempFilePath = defaultTempFilePath(a.tempFilePrefix)
	f, err := os.Create(a.tempFilePath)
	if err != nil {
		return
	}
	a.tempFile = f
	for _, chunk := range a.rawChunks {
		f.Write(chunk)
	}
	a.rawChunks = nil
}


/*
FOOTNOTE:
 1. TS decodes streamed bytes with TextDecoder({stream:true}); the Go equivalent
 is decodeStreamed — an incomplete multi-byte sequence at a chunk boundary is
 held in pendingTail and prepended to the next chunk, and Finish() flushes any
 dangling tail like the reference's end-of-stream decode(). Pipe reads are
 fixed 4096-byte writes that CAN split a rune; the old per-chunk string(data)
 decoded each split as U+FFFD.

 2. `Snapshot` method uses variadic `bool` — TS takes an options object `{ persistIfTruncated?: boolean }`.
 Go doesn't have optional struct fields elegantly for method calls, so `opts ...bool` is used.
 Only the first bool is read.

 3. TS `append` throws on finished — Go uses `panic` for the same semantics (runtime error, not recoverable).
*/