// Package main is a port of reference/tui/test/key-tester.ts — an interactive
// raw-key logger for debugging key detection across the kitty / modifyOtherKeys /
// legacy keyboard protocols.
//
// Run:  go run ./packages/tui/cmd/keytester
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"ycode/packages/tui"
)

// keyLogger is the Component from key-tester.ts: it records each raw input
// sequence and renders a scrolling log of its hex/char/repr encodings plus the
// active keyboard protocol.
type keyLogger struct {
	log      []string
	maxLines int
	app      *tui.TUI
	term     *tui.ProcessTerminal
}

// HandleInput logs the sequence. Ctrl+C exits. It runs under the engine's render
// lock, so it does NOT call RequestRender itself (the engine re-renders after
// HandleInput returns) and defers Stop+exit to a goroutine.
func (k *keyLogger) HandleInput(data string) {
	if tui.MatchesKey(data, "ctrl+c") {
		go func() {
			k.app.Stop()
			fmt.Println("\nExiting...")
			os.Exit(0)
		}()
		return
	}

	hexStr := hex.EncodeToString([]byte(data))
	var charParts []string
	for _, b := range []byte(data) {
		charParts = append(charParts, fmt.Sprintf("%d", b))
	}
	chars := strings.Join(charParts, ", ")
	repr := escapeRepr(data)
	logLine := fmt.Sprintf("Hex: %-20s | Chars: [%-15s] | Repr: \"%s\"", hexStr, chars, repr)

	k.log = append(k.log, logLine)
	if len(k.log) > k.maxLines {
		k.log = k.log[len(k.log)-k.maxLines:]
	}
}

// Invalidate is a no-op (no cached state).
func (k *keyLogger) Invalidate() {}

func (k *keyLogger) protocolName() string {
	if k.term.KittyProtocolActive() {
		return "kitty"
	}
	if k.term.ModifyOtherKeysActive() {
		return "modifyOtherKeys"
	}
	return "legacy"
}

func (k *keyLogger) fit(line string, width int) string {
	out := tui.TruncateToWidth(line, width, "", false)
	if pad := width - tui.VisibleWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

func (k *keyLogger) Render(width int) []string {
	sep := strings.Repeat("=", width)
	var lines []string
	lines = append(lines, sep)
	lines = append(lines, k.fit("Key Code Tester - Press keys to see their codes (Ctrl+C to exit)", width))
	lines = append(lines, k.fit("Protocol: "+k.protocolName(), width))
	lines = append(lines, sep)
	lines = append(lines, k.fit("", width))

	for _, entry := range k.log {
		lines = append(lines, k.fit(entry, width))
	}

	if remaining := 25 - len(lines); remaining > 0 {
		empty := strings.Repeat(" ", width)
		for i := 0; i < remaining; i++ {
			lines = append(lines, empty)
		}
	}

	lines = append(lines, sep)
	lines = append(lines, k.fit("Test these:", width))
	lines = append(lines, k.fit("  - Shift + Enter (should show: \\x1b[13;2u with Kitty protocol)", width))
	lines = append(lines, k.fit("  - Alt/Option + Enter", width))
	lines = append(lines, k.fit("  - Option/Alt + Backspace", width))
	lines = append(lines, k.fit("  - Cmd/Ctrl + Backspace", width))
	lines = append(lines, k.fit("  - Regular Backspace", width))
	lines = append(lines, sep)
	return lines
}

func escapeRepr(data string) string {
	r := data
	r = strings.ReplaceAll(r, "\x1b", "\\x1b")
	r = strings.ReplaceAll(r, "\r", "\\r")
	r = strings.ReplaceAll(r, "\n", "\\n")
	r = strings.ReplaceAll(r, "\t", "\\t")
	r = strings.ReplaceAll(r, "\x7f", "\\x7f")
	return r
}

func main() {
	terminal := tui.NewProcessTerminal()
	app := tui.NewTUI(terminal)
	logger := &keyLogger{app: app, term: terminal, maxLines: 20}

	app.AddChild(logger)
	app.SetFocus(logger)

	app.Start()

	// Protocol negotiation completes asynchronously after the first render.
	// Refresh periodically so the displayed protocol state isn't stale.
	ticker := time.NewTicker(100 * time.Millisecond)
	go func() {
		for range ticker.C {
			app.RequestRender()
		}
	}()

	select {}
}
