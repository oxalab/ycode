package tui

// This file ports reference/tui/src/terminal.ts — the Terminal abstraction and a
// ProcessTerminal backed by direct stdio (golang.org/x/term raw mode), plus the
// Kitty keyboard protocol negotiation. See mapping/03-terminal.md.
//
// Design: pi-tui feeds RAW stdin bytes to the engine (the engine matches raw
// escape sequences). That rules out a Bubble-Tea adapter (tea.KeyMsg would lose
// raw fidelity and own the event loop), so ProcessTerminal drives stdin/stdout
// directly. The protocol negotiation is encapsulated in kittyNegotiator, which is
// unit-testable via injectable callbacks; the stdio glue is exercised in
// integration (Phase 11).

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/term"
)

// Terminal abstracts the terminal the engine renders to.
type Terminal interface {
	Start(onInput func(data string), onResize func()) error
	Stop() error
	DrainInput(maxMs, idleMs int) error
	Write(data string)
	Columns() int
	Rows() int
	KittyProtocolActive() bool
	MoveBy(lines int)
	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	SetTitle(title string)
	SetProgress(active bool)
}

// Kitty keyboard protocol negotiation constants.
const (
	// KittyKeyboardProtocolQuery pushes flags 7 (1|2|4), pops the current set, then
	// sends a Device-Attributes request as a sentinel for terminals that don't know
	// the Kitty protocol (lets us fall back to modifyOtherKeys without a timeout).
	KittyKeyboardProtocolQuery = "\x1b[>7u\x1b[?u\x1b[c"
	kittyDisableSeq            = "\x1b[<u"
	enableModifyOtherKeysSeq   = "\x1b[>4;2m"
	disableModifyOtherKeysSeq  = "\x1b[>4;0m"
	progressActiveSeq          = "\x1b]9;4;3\x07"
	progressClearSeq           = "\x1b]9;4;0;\x07"
	appleTerminalShiftEnter    = "\x1b[13;2u"
	keyboardFragmentTimeout    = 150 * time.Millisecond
	progressKeepaliveInterval  = 1000 * time.Millisecond
)

// KittyNegotiationType identifies a Kitty-protocol negotiation response.
type KittyNegotiationType int

const (
	NegotiationKittyFlags KittyNegotiationType = iota
	NegotiationDeviceAttributes
)

// KeyboardProtocolNegotiationSequence is a parsed Kitty/DA negotiation response.
type KeyboardProtocolNegotiationSequence struct {
	Type  KittyNegotiationType
	Flags int // valid when Type == NegotiationKittyFlags
}

var (
	kittyFlagsRe   = regexp.MustCompile(`^\x1b\[\?(\d+)u$`)
	deviceAttrRe   = regexp.MustCompile(`^\x1b\[\?[\d;]*c$`)
	negotiationPrefixRe = regexp.MustCompile(`^\x1b\[\?[\d;]*$`)
)

// ParseKeyboardProtocolNegotiationSequence recognizes a Kitty-flags response
// (\x1b[?Nu) or a device-attributes response (\x1b[?..c).
func ParseKeyboardProtocolNegotiationSequence(seq string) (KeyboardProtocolNegotiationSequence, bool) {
	if m := kittyFlagsRe.FindStringSubmatch(seq); m != nil {
		flags, _ := strconv.Atoi(m[1])
		return KeyboardProtocolNegotiationSequence{Type: NegotiationKittyFlags, Flags: flags}, true
	}
	if deviceAttrRe.MatchString(seq) {
		return KeyboardProtocolNegotiationSequence{Type: NegotiationDeviceAttributes}, true
	}
	return KeyboardProtocolNegotiationSequence{}, false
}

func isKeyboardProtocolNegotiationSequencePrefix(seq string) bool {
	if seq == "\x1b[" {
		return true
	}
	return negotiationPrefixRe.MatchString(seq)
}

// IsAppleTerminalSession reports a macOS Terminal.app session.
func IsAppleTerminalSession() bool {
	return runtime.GOOS == "darwin" && osGetenv("TERM_PROGRAM") == "Apple_Terminal"
}

// NormalizeAppleTerminalInput rewrites a plain Return ("\r") from Apple Terminal
// into a CSI-u shift+enter when shift is held locally.
func NormalizeAppleTerminalInput(data string, isAppleTerminal, isShiftPressed bool) string {
	if isAppleTerminal && data == "\r" && isShiftPressed {
		return appleTerminalShiftEnter
	}
	return data
}

// ---------------------------------------------------------------------------
// kittyNegotiator — Kitty protocol / modifyOtherKeys negotiation (testable)
// ---------------------------------------------------------------------------

// kittyNegotiator consumes StdinBuffer-emitted sequences: it intercepts Kitty/DA
// negotiation responses and forwards everything else. Injectable callbacks make
// it unit-testable without real stdio.
type kittyNegotiator struct {
	buffer       string
	flushTimer   *time.Timer
	fragTimeout  time.Duration

	kittyActive         bool
	modifyOtherKeys     bool

	write   func(string) // write an escape to the terminal
	forward func(string) // forward an input sequence to the engine
	setKitty func(bool)  // set the global + terminal kitty-active flag
	now     func() time.Time
}

func newKittyNegotiator(write, forward func(string), setKitty func(bool)) *kittyNegotiator {
	return &kittyNegotiator{
		fragTimeout: keyboardFragmentTimeout,
		write:       write,
		forward:     forward,
		setKitty:    setKitty,
	}
}

// feed processes one complete sequence from StdinBuffer.
func (n *kittyNegotiator) feed(sequence string) {
	if n.buffer != "" {
		combined := n.buffer + sequence
		if seq, ok := ParseKeyboardProtocolNegotiationSequence(combined); ok {
			n.clearFlushTimer()
			n.buffer = ""
			n.handle(seq)
			return
		}
		if isKeyboardProtocolNegotiationSequencePrefix(combined) {
			n.buffer = combined
			n.scheduleFlush()
			return
		}
		n.flushBufferedAsInput()
	}

	if seq, ok := ParseKeyboardProtocolNegotiationSequence(sequence); ok {
		n.handle(seq)
		return
	}
	if isKeyboardProtocolNegotiationSequencePrefix(sequence) {
		n.buffer = sequence
		n.scheduleFlush()
		return
	}
	n.forward(sequence)
}

func (n *kittyNegotiator) handle(seq KeyboardProtocolNegotiationSequence) {
	if seq.Type == NegotiationKittyFlags {
		if seq.Flags != 0 {
			if n.modifyOtherKeys {
				n.write(disableModifyOtherKeysSeq)
				n.modifyOtherKeys = false
			}
			if !n.kittyActive {
				n.kittyActive = true
				if n.setKitty != nil {
					n.setKitty(true)
				}
			}
		} else {
			n.enableModifyOtherKeys()
		}
		return
	}
	// Device attributes: fall back to modifyOtherKeys unless kitty is active.
	if !n.kittyActive {
		n.enableModifyOtherKeys()
	}
}

func (n *kittyNegotiator) enableModifyOtherKeys() {
	if n.kittyActive || n.modifyOtherKeys {
		return
	}
	n.write(enableModifyOtherKeysSeq)
	n.modifyOtherKeys = true
}

func (n *kittyNegotiator) scheduleFlush() {
	if n.buffer == "" || n.flushTimer != nil {
		return
	}
	n.flushTimer = time.AfterFunc(n.fragTimeout, n.onFragmentTimeout)
}

func (n *kittyNegotiator) clearFlushTimer() {
	if n.flushTimer != nil {
		n.flushTimer.Stop()
		n.flushTimer = nil
	}
}

// onFragmentTimeout flushes a buffered (non-resolving) prefix as input.
func (n *kittyNegotiator) onFragmentTimeout() {
	if n.buffer == "" {
		return
	}
	n.flushBufferedAsInput()
}

func (n *kittyNegotiator) flushBufferedAsInput() {
	if n.buffer == "" {
		return
	}
	seq := n.buffer
	n.buffer = ""
	n.clearFlushTimer()
	n.forward(seq)
}

func (n *kittyNegotiator) stop() {
	n.clearFlushTimer()
}

// ---------------------------------------------------------------------------
// ProcessTerminal — real stdio
// ---------------------------------------------------------------------------

// ProcessTerminal is a Terminal backed by os.Stdin/os.Stdout in raw mode.
type ProcessTerminal struct {
	input        *os.File
	output       io.Writer
	rawState     *term.State
	inputHandler func(string)
	resizeHandler func()

	stdinBuffer *StdinBuffer
	negotiator  *kittyNegotiator
	pushedKitty bool
	kittyActive bool

	progressTicker *time.Ticker
	writeLog       *bytes.Buffer
	resizeCh       chan os.Signal
	resizeStop     chan struct{}

	stopMu   sync.Mutex
	stopped  bool
	stopOnce sync.Once
	readerWg sync.WaitGroup
}

// resizePollInterval is how often non-Unix (Windows) terminals re-check the
// console size for the resize poll (terminal_resize_other.go). Var, not const,
// so tests can shorten it.
var resizePollInterval = 250 * time.Millisecond

// NewProcessTerminal returns a ProcessTerminal over os.Stdin/os.Stdout.
func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{input: os.Stdin, output: os.Stdout}
}

func (t *ProcessTerminal) KittyProtocolActive() bool {
	t.stopMu.Lock()
	defer t.stopMu.Unlock()
	return t.kittyActive
}

// ModifyOtherKeysActive reports whether the xterm modifyOtherKeys fallback is
// active (set when a terminal doesn't support the Kitty keyboard protocol).
func (t *ProcessTerminal) ModifyOtherKeysActive() bool {
	t.stopMu.Lock()
	defer t.stopMu.Unlock()
	return t.negotiator != nil && t.negotiator.modifyOtherKeys
}

func (t *ProcessTerminal) Start(onInput func(string), onResize func()) error {
	t.inputHandler = onInput
	t.resizeHandler = onResize

	// Raw mode.
	fd := int(t.input.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	t.rawState = state

	// Bracketed paste.
	t.writeRaw("\x1b[?2004h")

	// Resize handling (platform-specific; no-op where unsupported).
	t.setupResize()

	// Refresh dimensions on Unix (SIGWINCH may have been missed while stopped).
	t.refreshDimensions()

	// Windows: enable ENABLE_VIRTUAL_TERMINAL_INPUT (after raw mode).
	t.enableWindowsVTInput()

	// Kitty keyboard protocol negotiation.
	t.stdinBuffer = NewStdinBuffer(defaultStdinTimeout)
	t.negotiator = newKittyNegotiator(t.writeRaw, t.forwardInputSequence, func(active bool) {
		t.stopMu.Lock()
		t.kittyActive = active
		t.stopMu.Unlock()
		SetKittyProtocolActive(active)
	})
	t.stdinBuffer.OnData(t.negotiator.feed)
	// Bracketed paste: the StdinBuffer extracts paste content and delivers it via
	// OnPaste (separately from OnData). Re-wrap it in the paste markers and forward
	// to the engine so the focused Editor/Input detects the markers and handles the
	// paste. Without this, paste content is extracted then dropped — paste was
	// broken for every input. The marker's leading ESC does NOT match
	// tui.select.cancel: MatchesKey requires data == "\x1b" exactly for escape, so
	// wrapper components' cancel checks don't eat it.
	t.stdinBuffer.OnPaste(func(content string) {
		if t.inputHandler != nil {
			t.inputHandler(bracketedPasteStart + content + bracketedPasteEnd)
		}
	})
	t.pushedKitty = true
	t.writeRaw(KittyKeyboardProtocolQuery)

	// Read stdin in a goroutine, feeding the buffer.
	t.readerWg.Add(1)
	go func() {
		defer t.readerWg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := t.input.Read(buf)
			if n > 0 {
				t.stdinBuffer.Process(string(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}()
	return nil
}

// forwardInputSequence sends a (non-negotiation) sequence to the engine, applying
// Apple-Terminal shift-enter normalization.
func (t *ProcessTerminal) forwardInputSequence(sequence string) {
	if t.inputHandler == nil {
		return
	}
	isApple := sequence == "\r" && IsAppleTerminalSession()
	input := NormalizeAppleTerminalInput(sequence, isApple, isApple && IsNativeModifierPressed("shift"))
	t.inputHandler(input)
}

func (t *ProcessTerminal) Stop() error {
	t.stopOnce.Do(func() {
		t.stopMu.Lock()
		t.stopped = true
		t.stopMu.Unlock()

		if t.progressTicker != nil {
			t.progressTicker.Stop()
			t.progressTicker = nil
			t.writeRaw(progressClearSeq)
		}
		t.writeRaw("\x1b[?2004l") // bracketed paste off

		if t.pushedKitty || t.kittyActive {
			t.writeRaw(kittyDisableSeq)
			t.pushedKitty = false
			t.kittyActive = false
			SetKittyProtocolActive(false)
		}
		if t.negotiator != nil && t.negotiator.modifyOtherKeys {
			t.writeRaw(disableModifyOtherKeysSeq)
		}
		if t.negotiator != nil {
			t.negotiator.stop()
		}
		if t.stdinBuffer != nil {
			t.stdinBuffer.Destroy()
		}
		t.teardownResize()

		if t.rawState != nil {
			_ = term.Restore(int(t.input.Fd()), t.rawState)
			t.rawState = nil
		}
	})
	return nil
}

// DrainInput reads pending input for up to maxMs (idleMs quiet gap exits early) to
// flush late Kitty key-release sequences before the parent shell resumes. This is
// best-effort — interrupting a blocking stdin read is platform-dependent.
func (t *ProcessTerminal) DrainInput(maxMs, idleMs int) error {
	if maxMs <= 0 {
		maxMs = 1000
	}
	if idleMs <= 0 {
		idleMs = 50
	}
	deadline := time.Duration(maxMs) * time.Millisecond
	idle := time.Duration(idleMs) * time.Millisecond
	end := time.Now().Add(deadline)
	buf := make([]byte, 4096)
	last := time.Now()
	for time.Now().Before(end) {
		n, err := t.input.Read(buf)
		if n > 0 {
			last = time.Now()
		}
		if err != nil {
			return nil
		}
		if time.Since(last) >= idle {
			return nil
		}
		time.Sleep(minTime(idle, 10*time.Millisecond))
	}
	return nil
}

func minTime(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (t *ProcessTerminal) Write(data string) { t.writeRaw(data) }

func (t *ProcessTerminal) writeRaw(data string) {
	t.output.Write([]byte(data))
	if t.writeLog != nil {
		t.writeLog.WriteString(data)
	}
}

func (t *ProcessTerminal) Columns() int {
	if w, _, err := term.GetSize(int(t.outputFd())); err == nil && w > 0 {
		return w
	}
	if c := osGetenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

func (t *ProcessTerminal) Rows() int {
	if _, h, err := term.GetSize(int(t.outputFd())); err == nil && h > 0 {
		return h
	}
	if c := osGetenv("LINES"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	return 24
}

func (t *ProcessTerminal) outputFd() uintptr {
	if f, ok := t.output.(*os.File); ok {
		return f.Fd()
	}
	return os.Stdout.Fd()
}

func (t *ProcessTerminal) MoveBy(lines int) {
	if lines > 0 {
		t.writeRaw("\x1b[" + strconv.Itoa(lines) + "B")
	} else if lines < 0 {
		t.writeRaw("\x1b[" + strconv.Itoa(-lines) + "A")
	}
}

func (t *ProcessTerminal) HideCursor()       { t.writeRaw("\x1b[?25l") }
func (t *ProcessTerminal) ShowCursor()       { t.writeRaw("\x1b[?25h") }
func (t *ProcessTerminal) ClearLine()        { t.writeRaw("\x1b[K") }
func (t *ProcessTerminal) ClearFromCursor()  { t.writeRaw("\x1b[J") }
func (t *ProcessTerminal) ClearScreen()      { t.writeRaw("\x1b[2J\x1b[H") }
func (t *ProcessTerminal) SetTitle(title string) { t.writeRaw("\x1b]0;" + title + "\x07") }

func (t *ProcessTerminal) SetProgress(active bool) {
	if active {
		t.writeRaw(progressActiveSeq)
		if t.progressTicker == nil {
			t.progressTicker = time.NewTicker(progressKeepaliveInterval)
			go func() {
				for range t.progressTicker.C {
					t.writeRaw(progressActiveSeq)
				}
			}()
		}
		return
	}
	if t.progressTicker != nil {
		t.progressTicker.Stop()
		t.progressTicker = nil
	}
	t.writeRaw(progressClearSeq)
}
