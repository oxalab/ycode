package tui

// This file is a Go port of reference/tui/src/stdin-buffer.ts (based on OpenTUI,
// MIT, Copyright (c) 2025 opentui). It buffers stdin and emits complete escape
// sequences, handling partial sequences, bracketed paste, the WezTerm
// ESC+ESC+CSI regression, and Kitty-printable raw-duplicate dedup.
//
// Concurrency: Process is not re-entrant and is meant to be driven by a single
// reader (like the reference). A mutex serializes state access against the
// timeout-flush timer goroutine. User callbacks (OnData/OnPaste) are invoked
// outside the lock.

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultStdinTimeout = 10 * time.Millisecond
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// StdinBuffer buffers input and emits complete escape sequences via OnData, and
// bracketed-paste content via OnPaste.
type StdinBuffer struct {
	mu         sync.Mutex
	timeout    time.Duration
	buffer     string
	timer      *time.Timer
	pasteMode  bool
	pasteBuffer string
	pendingKitty int // codepoint of the last Kitty printable seq, or -1

	onData  func(string)
	onPaste func(string)
}

// NewStdinBuffer returns a buffer that flushes incomplete sequences after timeout.
func NewStdinBuffer(timeout time.Duration) *StdinBuffer {
	if timeout <= 0 {
		timeout = defaultStdinTimeout
	}
	return &StdinBuffer{timeout: timeout, pendingKitty: -1}
}

// OnData registers a callback fired for each complete (non-paste) sequence.
func (b *StdinBuffer) OnData(fn func(string)) { b.onData = fn }

// OnPaste registers a callback fired with the content of a complete bracketed paste.
func (b *StdinBuffer) OnPaste(fn func(string)) { b.onPaste = fn }

// Process feeds input data into the buffer.
func (b *StdinBuffer) Process(data string) {
	var emitted []string
	var pasteContent string
	var firePaste bool
	var recurseData string
	var doRecurse bool

	// emit assumes b.mu is held; it updates pendingKitty and defers the onData call.
	emit := func(seq string) {
		rawCp := -1
		if r := []rune(seq); len(r) == 1 {
			rawCp = int(r[0])
		}
		if rawCp != -1 && rawCp == b.pendingKitty {
			b.pendingKitty = -1
			return // drop raw duplicate of a Kitty printable sequence
		}
		b.pendingKitty = parseUnmodifiedKittyPrintableCodepoint(seq)
		emitted = append(emitted, seq)
	}

	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}

	if len(data) == 0 && len(b.buffer) == 0 {
		emit("")
		b.mu.Unlock()
		b.dispatch(emitted, firePaste, pasteContent, doRecurse, recurseData)
		return
	}

	b.buffer += data

	// Already in paste mode: accumulate until the end marker.
	if b.pasteMode {
		b.pasteBuffer += b.buffer
		b.buffer = ""
		if idx := strings.Index(b.pasteBuffer, bracketedPasteEnd); idx >= 0 {
			pasteContent = b.pasteBuffer[:idx]
			remaining := b.pasteBuffer[idx+len(bracketedPasteEnd):]
			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKitty = -1
			firePaste = true
			if len(remaining) > 0 {
				doRecurse = true
				recurseData = remaining
			}
		}
		b.mu.Unlock()
		b.dispatch(emitted, firePaste, pasteContent, doRecurse, recurseData)
		return
	}

	// Entering paste mode?
	if idx := strings.Index(b.buffer, bracketedPasteStart); idx >= 0 {
		if idx > 0 {
			seqs, _ := extractCompleteSequences(b.buffer[:idx])
			for _, s := range seqs {
				emit(s)
			}
		}
		b.pendingKitty = -1
		b.buffer = b.buffer[idx+len(bracketedPasteStart):]
		b.pasteMode = true
		b.pasteBuffer = b.buffer
		b.buffer = ""
		if idx2 := strings.Index(b.pasteBuffer, bracketedPasteEnd); idx2 >= 0 {
			pasteContent = b.pasteBuffer[:idx2]
			remaining := b.pasteBuffer[idx2+len(bracketedPasteEnd):]
			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKitty = -1
			firePaste = true
			if len(remaining) > 0 {
				doRecurse = true
				recurseData = remaining
			}
		}
		b.mu.Unlock()
		b.dispatch(emitted, firePaste, pasteContent, doRecurse, recurseData)
		return
	}

	// Normal: extract complete sequences, keep the remainder buffered.
	seqs, remainder := extractCompleteSequences(b.buffer)
	b.buffer = remainder
	for _, s := range seqs {
		emit(s)
	}
	if len(b.buffer) > 0 {
		b.timer = time.AfterFunc(b.timeout, b.flushAndEmit)
	}
	b.mu.Unlock()

	b.dispatch(emitted, firePaste, pasteContent, doRecurse, recurseData)
}

// dispatch sends deferred callbacks outside the lock, in order.
func (b *StdinBuffer) dispatch(emitted []string, firePaste bool, pasteContent string, doRecurse bool, recurseData string) {
	for _, s := range emitted {
		if b.onData != nil {
			b.onData(s)
		}
	}
	if firePaste && b.onPaste != nil {
		b.onPaste(pasteContent)
	}
	if doRecurse {
		b.Process(recurseData)
	}
}

func (b *StdinBuffer) flushAndEmit() {
	for _, s := range b.Flush() {
		b.emitDataSequence(s)
	}
}

// emitDataSequence is the locked, standalone emitter used by the timer path.
func (b *StdinBuffer) emitDataSequence(seq string) {
	b.mu.Lock()
	rawCp := -1
	if r := []rune(seq); len(r) == 1 {
		rawCp = int(r[0])
	}
	drop := false
	if rawCp != -1 && rawCp == b.pendingKitty {
		b.pendingKitty = -1
		drop = true
	} else {
		b.pendingKitty = parseUnmodifiedKittyPrintableCodepoint(seq)
	}
	cb := b.onData
	b.mu.Unlock()
	if !drop && cb != nil {
		cb(seq)
	}
}

// Flush flushes any buffered (incomplete) data as a single sequence.
func (b *StdinBuffer) Flush() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if len(b.buffer) == 0 {
		return nil
	}
	seqs := []string{b.buffer}
	b.buffer = ""
	b.pendingKitty = -1
	return seqs
}

// Clear discards buffered content without emitting.
func (b *StdinBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.buffer = ""
	b.pasteMode = false
	b.pasteBuffer = ""
	b.pendingKitty = -1
}

// Destroy is an alias for Clear.
func (b *StdinBuffer) Destroy() { b.Clear() }

// Buffered returns the currently buffered (incomplete) content.
func (b *StdinBuffer) Buffered() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer
}

// ---------------------------------------------------------------------------
// Sequence completeness + extraction
// ---------------------------------------------------------------------------

const (
	seqNotEscape = iota
	seqIncomplete
	seqComplete
)

var (
	sgrMouseRe       = regexp.MustCompile(`^<\d+;\d+;\d+[Mm]$`)
	unmodifiedKittyRe = regexp.MustCompile(`^\x1b\[(\d+)(?::\d*)?(?::\d+)?u$`)
)

func parseUnmodifiedKittyPrintableCodepoint(seq string) int {
	m := unmodifiedKittyRe.FindStringSubmatch(seq)
	if m == nil {
		return -1
	}
	cp, err := strconv.Atoi(m[1])
	if err != nil || cp < 32 {
		return -1
	}
	return cp
}

func endsWithSTorBEL(s string) bool {
	return strings.HasSuffix(s, "\x1b\\") || strings.HasSuffix(s, "\x07")
}

func isCompleteSequence(data string) int {
	if !strings.HasPrefix(data, "\x1b") {
		return seqNotEscape
	}
	if len(data) == 1 {
		return seqIncomplete
	}
	afterEsc := data[1:]
	switch {
	case strings.HasPrefix(afterEsc, "["):
		if strings.HasPrefix(afterEsc, "[M") {
			if len(data) >= 6 {
				return seqComplete
			}
			return seqIncomplete
		}
		return isCompleteCsi(data)
	case strings.HasPrefix(afterEsc, "]"):
		return isCompleteOsc(data)
	case strings.HasPrefix(afterEsc, "P"):
		return isCompleteDcs(data)
	case strings.HasPrefix(afterEsc, "_"):
		return isCompleteApc(data)
	case strings.HasPrefix(afterEsc, "O"):
		if len(afterEsc) >= 2 {
			return seqComplete
		}
		return seqIncomplete
	case len(afterEsc) == 1:
		return seqComplete
	}
	return seqComplete
}

func isCompleteCsi(data string) int {
	if !strings.HasPrefix(data, "\x1b[") {
		return seqComplete
	}
	if len(data) < 3 {
		return seqIncomplete
	}
	payload := data[2:]
	last := payload[len(payload)-1]
	if last >= 0x40 && last <= 0x7e {
		if strings.HasPrefix(payload, "<") {
			if sgrMouseRe.MatchString(payload) {
				return seqComplete
			}
			if last == 'M' || last == 'm' {
				parts := strings.Split(payload[1:len(payload)-1], ";")
				if len(parts) == 3 && isAllDigits(parts[0]) && isAllDigits(parts[1]) && isAllDigits(parts[2]) {
					return seqComplete
				}
			}
			return seqIncomplete
		}
		return seqComplete
	}
	return seqIncomplete
}

func isCompleteOsc(data string) int {
	if !strings.HasPrefix(data, "\x1b]") {
		return seqComplete
	}
	if endsWithSTorBEL(data) {
		return seqComplete
	}
	return seqIncomplete
}

func isCompleteDcs(data string) int {
	if !strings.HasPrefix(data, "\x1bP") {
		return seqComplete
	}
	if endsWithSTorBEL(data) {
		return seqComplete
	}
	return seqIncomplete
}

func isCompleteApc(data string) int {
	if !strings.HasPrefix(data, "\x1b_") {
		return seqComplete
	}
	if endsWithSTorBEL(data) {
		return seqComplete
	}
	return seqIncomplete
}

func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// extractCompleteSequences splits a buffer into complete sequences plus a remainder.
func extractCompleteSequences(buffer string) (seqs []string, remainder string) {
	pos := 0
	for pos < len(buffer) {
		remaining := buffer[pos:]

		if remaining[0] == '\x1b' {
			seqEnd := 1
			consumed := false
			for seqEnd <= len(remaining) {
				candidate := remaining[:seqEnd]
				status := isCompleteSequence(candidate)
				if status == seqComplete {
					// WezTerm: Escape press as raw ESC + release as CSI-u,
					// concatenated as "\x1b\x1b[27;...u". Emit only the first ESC
					// when the next byte starts a new escape sequence.
					if candidate == "\x1b\x1b" && seqEnd < len(remaining) {
						next := remaining[seqEnd]
						if next == '[' || next == ']' || next == 'O' || next == 'P' || next == '_' {
							seqs = append(seqs, "\x1b")
							pos += 1
							consumed = true
							break
						}
					}
					seqs = append(seqs, candidate)
					pos += seqEnd
					consumed = true
					break
				} else if status == seqIncomplete {
					seqEnd++
				} else {
					seqs = append(seqs, candidate)
					pos += seqEnd
					consumed = true
					break
				}
			}
			if !consumed {
				return seqs, remaining
			}
			continue
		}

		// Not an escape: take one rune.
		_, size := utf8.DecodeRuneInString(remaining)
		seqs = append(seqs, remaining[:size])
		pos += size
	}
	return seqs, ""
}
