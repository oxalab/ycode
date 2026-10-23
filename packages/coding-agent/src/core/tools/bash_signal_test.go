//go:build !windows

package tools

import (
	"testing"
	"time"
)

// C11: a process killed by a signal has no exit code — Go reports the raw -1,
// which exitCodeFromRaw maps to nil (the TS null). The tool must not map it to
// a "Command exited with code -1" error (bash.ts:392: null is success).
func TestExecSignalKillYieldsNilExitCode(t *testing.T) {
	ops := newExecOps(t)
	res, err := execWithTimeout(t, ops, "kill -TERM $$", 10*time.Second, BashExecOptions{})
	if err != nil {
		t.Fatalf("Exec error (signal kill must not fail): %v", err)
	}
	if res == nil || res.ExitCode != nil {
		t.Fatalf("signal-killed exit code = %v, want nil", res)
	}
}
