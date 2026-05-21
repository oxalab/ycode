package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024 // 50KB
	GrepMaxLineLength = 500
)

type TruncationResult struct {
	Content               string
	Truncated             bool
	TruncatedBy           string // "lines", "bytes" or ""
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	LastLinePartial       bool
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int
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
			TruncatedBy: "",
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
		return TruncationResult{
			Content:               "",
			Truncated:             true,
			TruncatedBy:           "bytes",
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

	for i := 0; i < len(lines); i++ {
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
		TruncatedBy: truncatedBy,
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
			TruncatedBy: "",
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
		TruncatedBy: truncatedBy,
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
 - No functional flags — this is a straight port. len([]byte(s)) is equivalent to 
 Buffer.byteLength(s, "utf-8"), and utf8.RuneCountInString is used for character-level 
 truncation in TruncateLine (more correct than the TS version which uses string.slice 
 on code units, but line.length already counts by code units in JS). One minor note: 
 TruncateLine in TS uses line.length (JS code units) while Go uses 
 utf8.RuneCountInString (actual characters) — this is actually more correct in Go.
*/