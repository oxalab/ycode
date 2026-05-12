package ycode

import (
	"io"
	"os"
	"sync"
)

var stdoutMu sync.Mutex
var stdoutTakenOver bool
var rawStdout *os.File

func TakeOverStdout() {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	if stdoutTakenOver {
		return
	} 
	rawStdout = os.Stdout
	// Replace stdout with stderr so raw output goes to stderr
	os.Stdout = os.Stderr
	stdoutTakenOver = true
}

func RestoreStdout() {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	if !stdoutTakenOver {
		return
	}
	os.Stdout = rawStdout
	rawStdout = nil
	stdoutTakenOver = false
}

func IsStdoutTakenOver() bool {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	return stdoutTakenOver
}

func WriteRawStdout(text string) {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	if stdoutTakenOver && rawStdout != nil {
		rawStdout.WriteString(text)
		return
	}
	os.Stdout.WriteString(text)
}

func FlushRawStdout() error {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	var w io.Writer = os.Stdout
	if stdoutTakenOver && rawStdout != nil {
		w = rawStdout
	}
	if f, ok := w.(interface{ Sync() error }); ok {
		return f.Sync()
	}
	return nil
}