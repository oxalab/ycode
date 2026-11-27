package extensions

// exec_test.go — pins for the execCommand port (F2, TS core/exec.ts parity):
// stdout/stderr/code collection, the never-rejects spawn-failure path, the
// timeout kill, the cwd option, and the signal-death `code ?? 0` mapping.
// The child is this test binary re-spawned in a helper mode (the classic
// exec-package pattern — no shell, matching execCommand's shell:false spawn).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecHelperProcess acts as the subprocess when the YCODE_EXEC_MODE env
// is set (the parent test sets its own env; the child inherits it).
func TestExecHelperProcess(t *testing.T) {
	mode := os.Getenv("YCODE_EXEC_MODE")
	if mode == "" {
		return // normal test run — not the helper
	}
	switch mode {
	case "echo":
		tail := execHelperArgsTail()
		if len(tail) > 0 {
			_, _ = os.Stdout.WriteString("hello-exec " + strings.Join(tail, " "))
		} else {
			_, _ = os.Stdout.WriteString("hello-exec")
		}
	case "exit3":
		os.Exit(3)
	case "stderr":
		_, _ = os.Stderr.WriteString("to-stderr")
	case "slow":
		time.Sleep(30 * time.Second) // killed by the timeout long before this fires
	case "cwdcheck":
		wd, _ := os.Getwd()
		_, _ = os.Stdout.WriteString(wd)
	}
	os.Exit(0)
}

func execHelperArgsTail() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

// execHelperCmd builds a command line that runs this test binary as the
// helper child in the given mode (set YCODE_EXEC_MODE via t.Setenv —
// execCommand passes no env; the child inherits the parent's).
func execHelperCmd(t *testing.T, extraArgs ...string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe, append([]string{"-test.run=^TestExecHelperProcess$", "--"}, extraArgs...)
}

func TestExecCommandStdoutAndCode(t *testing.T) {
	exe, args := execHelperCmd(t, "a1", "a2")
	t.Setenv("YCODE_EXEC_MODE", "echo")
	res := execCommand(exe, args, "", 0)
	if res.Code != 0 {
		t.Errorf("code = %d, want 0", res.Code)
	}
	if res.Killed {
		t.Error("killed = true on a natural exit")
	}
	if !strings.Contains(res.Stdout, "hello-exec a1 a2") {
		t.Errorf("stdout = %q, want it to contain 'hello-exec a1 a2' (args pass through)", res.Stdout)
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want empty", res.Stderr)
	}
}

func TestExecCommandExitCode(t *testing.T) {
	exe, args := execHelperCmd(t)
	t.Setenv("YCODE_EXEC_MODE", "exit3")
	res := execCommand(exe, args, "", 0)
	if res.Code != 3 {
		t.Errorf("code = %d, want 3 (non-zero exits resolve, never reject)", res.Code)
	}
	if res.Killed {
		t.Error("killed = true on a natural exit")
	}
}

func TestExecCommandStderr(t *testing.T) {
	exe, args := execHelperCmd(t)
	t.Setenv("YCODE_EXEC_MODE", "stderr")
	res := execCommand(exe, args, "", 0)
	if res.Stderr != "to-stderr" {
		t.Errorf("stderr = %q, want 'to-stderr'", res.Stderr)
	}
	if res.Code != 0 {
		t.Errorf("code = %d, want 0", res.Code)
	}
}

// TS exec.ts:99-105 — the catch swallows spawn failures: code 1, empty
// output, killed false. The error text must NOT leak into stderr.
func TestExecCommandSpawnFailure(t *testing.T) {
	res := execCommand("ycode-no-such-binary-f2", nil, "", 0)
	if res.Code != 1 {
		t.Errorf("code = %d, want 1 (TS resolves the catch path)", res.Code)
	}
	if res.Stdout != "" || res.Stderr != "" {
		t.Errorf("stdout/stderr = %q/%q, want empty", res.Stdout, res.Stderr)
	}
	if res.Killed {
		t.Error("killed = true on a spawn failure")
	}
}

func TestExecCommandTimeout(t *testing.T) {
	exe, args := execHelperCmd(t)
	t.Setenv("YCODE_EXEC_MODE", "slow")
	start := time.Now()
	res := execCommand(exe, args, "", 150)
	elapsed := time.Since(start)
	if !res.Killed {
		t.Error("killed = false after a timeout kill (the sole TS indicator)")
	}
	if elapsed > 10*time.Second {
		t.Errorf("timeout kill took %v — the process was not killed", elapsed)
	}
	// Code is platform-dependent on a forced kill (TerminateProcess → 1 on
	// Windows, signal death → 0 via execExitCode on Unix) — not pinned.
}

func TestExecCommandCwd(t *testing.T) {
	dir := t.TempDir()
	exe, args := execHelperCmd(t)
	t.Setenv("YCODE_EXEC_MODE", "cwdcheck")
	res := execCommand(exe, args, dir, 0)
	if filepath.Clean(res.Stdout) != filepath.Clean(dir) {
		t.Errorf("child cwd = %q, want %q", res.Stdout, dir)
	}
}

// The waitForChildProcess port: the pumps always finish — a plain command
// completes well inside the grace window (this also guards the finalize
// close path against leaking goroutine hangs into later tests).
func TestExecCommandDrainsStreams(t *testing.T) {
	exe, args := execHelperCmd(t)
	t.Setenv("YCODE_EXEC_MODE", "echo")
	start := time.Now()
	res := execCommand(exe, args, "", 0)
	if time.Since(start) > 5*time.Second {
		t.Errorf("result assembly took %v — the stream pumps did not finish", time.Since(start))
	}
	if res.Code != 0 {
		t.Errorf("code = %d, want 0", res.Code)
	}
}

func TestExecExitCodeMapping(t *testing.T) {
	if got := execExitCode(nil); got != 0 {
		t.Errorf("execExitCode(nil) = %d, want 0", got)
	}
	if got := execExitCode(errors.New("boom")); got != 1 {
		t.Errorf("execExitCode(non-exit error) = %d, want 1 (TS catch path)", got)
	}
	// Signal death: Node reports exit code null → exec.ts `code ?? 0` → 0.
	if got := exitCodeFromRaw(-1); got != 0 {
		t.Errorf("exitCodeFromRaw(-1) = %d, want 0 (signal death)", got)
	}
	if got := exitCodeFromRaw(0); got != 0 {
		t.Errorf("exitCodeFromRaw(0) = %d, want 0", got)
	}
	if got := exitCodeFromRaw(3); got != 3 {
		t.Errorf("exitCodeFromRaw(3) = %d, want 3", got)
	}
}
