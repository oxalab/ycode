package tools

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024 // 50KB
	GrepMaxLineLength = 500
)

type TruncationResult struct {
	Content               string  `json:"content"`
	Truncated             bool    `json:"truncated"`
	TruncatedBy           *string `json:"truncatedBy"` // "lines", "bytes" or nil when not truncated (TS: null)
	TotalLines            int     `json:"totalLines"`
	TotalBytes            int     `json:"totalBytes"`
	OutputLines           int     `json:"outputLines"`
	OutputBytes           int     `json:"outputBytes"`
	LastLinePartial       bool    `json:"lastLinePartial"`
	FirstLineExceedsLimit bool    `json:"firstLineExceedsLimit"`
	MaxLines              int     `json:"maxLines"`
	MaxBytes              int     `json:"maxBytes"`
}

type TruncationOptions struct {
	MaxLines *int
	MaxBytes *int
}

func (o TruncationOptions) maxLines() int {
	if o.MaxLines != nil {
		return *o.MaxLines
	}
	return DefaultMaxLines
}

func (o TruncationOptions) maxBytes() int {
	if o.MaxBytes != nil {
		return *o.MaxBytes
	}
	return DefaultMaxBytes
}

// byteOnlyTruncation returns the reference's truncation options for find/ls/
// grep result output: bytes only, no line cap (TS passes
// `maxLines: Number.MAX_SAFE_INTEGER` — the result/match limit has already
// capped the row count, so the 2000-line default would wrongly truncate).
func byteOnlyTruncation() TruncationOptions {
	maxLines := math.MaxInt32
	return TruncationOptions{MaxLines: &maxLines}
}

// FormatSize: Formats bytes as a human-readable size.
func FormatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

/*
TruncateHead: truncates content from the head (keeps first N lines/bytes).
 - Never returns partial lines.
 - If first line exceeds byte limit, returns empty content with FirstLineExceedsLimit=true
*/
func TruncateHead(content string, opts TruncationOptions) TruncationResult {
	maxLines := opts.maxLines()
	maxBytes := opts.maxBytes()

	totalBytes := len([]byte(content))
	lines := splitLines(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content,
			Truncated: false,
			TruncatedBy: nil,
			TotalLines: totalLines,
			TotalBytes: totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines: maxLines,
			MaxBytes: maxBytes,
		}
	}
	// Check if first line alone exceeds byte limit
	firstLineBytes := len([]byte(lines[0]))
	if firstLineBytes > maxBytes {
		truncatedBy := "bytes"
		return TruncationResult{
			Content:               "",
			Truncated:             true,
			TruncatedBy:           &truncatedBy,
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           0,
			OutputBytes:           0,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	// Collect complete lines that fit
	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"

	for i := 0; i < len(lines) && i < maxLines; i++ {
		line := lines[i]
		lineBytes := len([]byte(line))
		if i > 0 {
			lineBytes++ // +1 for new line
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, line)
		outputBytesCount += lineBytes
	}

	// If we exited due to line limit
	if len(outputLines) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := joinLines(outputLines)
	finalOutputBytes := len([]byte(outputContent))

	return TruncationResult{
		Content: outputContent,
		Truncated: true,
		TruncatedBy: &truncatedBy,
		TotalLines: totalLines,
		TotalBytes: totalBytes,
		OutputLines: len(outputLines),
		OutputBytes: finalOutputBytes,
		MaxLines: maxLines,
		MaxBytes: maxBytes,
	}
}

/*
TruncateTail: Truncates content from the tail (keeps the lat N lines/bytes).
 - May return partial first line if the last line exceeds byte limit.
*/
func TruncateTail(content string, opts TruncationOptions) TruncationResult {
	maxLines := opts.maxLines()
	maxBytes := opts.maxBytes()

	totalBytes := len([]byte(content))
	lines := splitLines(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content,
			Truncated: false,
			TruncatedBy: nil,
			TotalLines: totalLines,
			TotalBytes: totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines: maxLines,
			MaxBytes: maxBytes,
		}
	}

	// Work backwards from the end
	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(outputLines) < maxLines; i-- {
		line := lines[i]
		lineBytes := len([]byte(line))
		if len(outputLines) > 0 {
			lineBytes++ // +1 for new line
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			// Edge case: if no lines added yet and this line exceeds maxBytes,
			// take the end of the line (partial)
			if len(outputLines) == 0 {
				truncatedLine := truncateStringToBytesFromEnd(line, maxBytes)
				outputLines = append([]string{truncatedLine}, outputLines...)
				outputBytesCount = len([]byte(truncatedLine))
				lastLinePartial = true
			}
			break
		}
		outputLines = append([]string{line}, outputLines...)
		outputBytesCount += lineBytes
	}
	// If we exited due to line limit
	if len(outputLines) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := joinLines(outputLines)
	finalOutputBytes := len([]byte(outputContent))

	return TruncationResult{
		Content: outputContent,
		Truncated: true,
		TruncatedBy: &truncatedBy,
		TotalLines: totalLines,
		TotalBytes: totalBytes,
		OutputLines: len(outputLines),
		OutputBytes: finalOutputBytes,
		LastLinePartial: lastLinePartial,
		MaxLines: maxLines,
		MaxBytes: maxBytes,
	}
}

/*
truncateStringToBytesFromEnd: Truncates a string to fit withing a byte limit (from the end).
 - Handles multi-byte UTF-8 characters correctly
*/
func truncateStringToBytesFromEnd(str string, maxBytes int) string {
	buf := []byte(str)
	if len(buf) <= maxBytes {
		return str
	}

	// Start from the end, skip maxBytes back
	start := len(buf) - maxBytes

	// Find a valid UTF-8 boundary(start of a character)
	for start < len(buf) && (buf[start]&0xc0) == 0x80 {
		start++
	}
	return string(buf[start:])
}

// TruncateLine: Truncates a single line to max characters, adding [truncated] suffix.
func TruncateLine(line string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(line) <= maxChars {
		return line, false
	}
	runes := []rune(line)
	return string(runes[:maxChars]) + "... [truncated]", true
}

// splitLines: Splits content by newlines, preserving the same semantics as TS string.split("\n")
func splitLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(content, "\n")
}

// joinLines: Joins lines with newlines, matching the semantics of the TS array.join("\n").
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}



/*
Footnote:
 - TruncationResult is json-tagged camelCase like the reference interface, and
 TruncatedBy is a *string so a non-truncated result marshals "truncatedBy":null
 (TS union `"lines" | "bytes" | null`), not "".
 - TruncateLine counts Unicode code points (utf8.RuneCountInString) where TS
 counts UTF-16 code units (line.length + slice): identical for BMP text; for
 astral characters (emoji) Go counts 1 and always cuts at a rune boundary while
 TS counts 2 and can split a surrogate pair. ACCEPTED DIVERGENCE — Go's cut
 points are strictly more correct; matching TS bug-for-bug would emit invalid
 lone surrogates.
*/