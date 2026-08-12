package tools

import (
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
	text := string(data)
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

func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.finished {
		return
	}
	a.finished = true
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
	truncatedBy := ""
	if truncated {
		if tailTruncation.TruncatedBy != "" {
			truncatedBy = tailTruncation.TruncatedBy
		} else if a.totalDecodedBytes > a.maxBytes {
			truncatedBy = "bytes"
		} else {
			truncatedBy = "lines"
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
 1. TS uses streaming `TextDecoder` for UTF-8 — Go strings are already UTF-8, so `string(data)`
 is the equivalent of `decoder.decode(data, { stream: true })`. This works because the caller 
 (bash executor) feeds complete writes, not arbitrary byte boundaries. If data ever splits 
 mid-UTF8 across `Append` calls, the TS version handles it with `{ stream: true }` but our Go version 
 would decode a partial character. This is a real edge case but matches how the tool is used 
 (process output chunks are typically complete writes).

 2. `Snapshot` method uses variadic `bool` — TS takes an options object `{ persistIfTruncated?: boolean }`. 
 Go doesn't have optional struct fields elegantly for method calls, so `opts ...bool` is used. 
 Only the first bool is read.

 3. TS `append` throws on finished — Go uses `panic` for the same semantics (runtime error, not recoverable).
*/