package extensions

// exec.go — the pi.exec backend: a faithful port of the TS reference's
// core/exec.ts (execCommand). The module is self-contained there (imported
// only by the extension loader), so its home here is the extensions package —
// stdlib os/exec only, no core dependency (core imports extensions; never the
// reverse). Semantics verified at source (exec.ts:30-107):
//   - direct spawn, shell:false — no shell wrapping, no Windows .cmd
//     special-casing (that is shouldUseWindowsShell, used by the bash tool,
//     never by execCommand);
//   - options are exactly {signal, timeout, cwd} — no env (the child inherits
//     the process environment); no default timeout (armed only when > 0);
//   - timeout and abort both funnel into killProcess (SIGTERM; Node maps that
//     to TerminateProcess on Windows); the 5s SIGKILL escalation is DEAD CODE
//     in TS — Node sets proc.killed on the first kill — and is not ported;
//   - the promise never rejects: even a spawn failure (ENOENT) resolves
//     {stdout:"", stderr:"", code:1, killed:false} (the catch swallows the
//     error), so execCommand returns a result struct, never an error;
//   - signal-death exit code is Node's null → `code ?? 0` (execExitCode);
//   - killed:true is the sole timeout/abort indicator.

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// execResult mirrors TS ExecResult (exec.ts:20-28). The JSON keys are the
// JS-observable contract ({stdout, stderr, code, killed}).
type execResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	Killed bool   `json:"killed"`
}

// execStdioGraceMs caps how long result assembly waits for the stdout/stderr
// pumps after the process exits — the waitForChildProcess port
// (../utils/child-process.ts): a detached descendant inheriting the write-ends
// must never block completion (the same finalize guard as the bash tool's
// exitStdioGraceMs).
const execStdioGraceMs = 100 * time.Millisecond

// execCommand runs command with args (no shell) and collects the result. It
// never returns an error — TS's execCommand resolves even on spawn failure.
func execCommand(command string, args []string, cwd string, timeoutMs int64) execResult {
	cmd := exec.Command(command, args...) // no shell — TS spawn(..., shell: false)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return execResult{Code: 1}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return execResult{Code: 1}
	}
	if err := cmd.Start(); err != nil {
		// Spawn failure (ENOENT &c.): TS swallows the error and resolves
		// code 1 with empty output — stderr stays empty too.
		return execResult{Code: 1}
	}

	var killedOnce sync.Once
	var killedFlag atomic.Bool
	killProcess := func() {
		killedOnce.Do(func() {
			killedFlag.Store(true)
			// TS sends SIGTERM (exec.ts:55); on Windows Node's SIGTERM is
			// TerminateProcess, and Go's Signal refuses non-Kill signals —
			// the fallback lands on the same force-terminate.
			if cmd.Process != nil {
				if serr := cmd.Process.Signal(syscall.SIGTERM); serr != nil {
					_ = cmd.Process.Kill()
				}
			}
		})
	}

	// TS arms the timer only when timeout > 0 (exec.ts:73-76) — no default.
	var timer *time.Timer
	if timeoutMs > 0 {
		timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, killProcess)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	pump := func(r io.Reader, w io.Writer) {
		defer wg.Done()
		_, _ = io.Copy(w, r)
	}
	wg.Add(2)
	go pump(stdout, &stdoutBuf)
	go pump(stderr, &stderrBuf)

	// Wait in its own goroutine: it returns at process exit (never blocked on
	// stdio in StdoutPipe mode) and closes the read-ends, which unblocks the
	// pumps when a descendant inherited the write-ends (the raw-Node hang that
	// waitForChildProcess exists for).
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()
	waitErr := <-exitCh
	if timer != nil {
		timer.Stop()
	}

	// Result assembly: wait for the pumps with a short grace window, then
	// force-close the read-ends so a descendant holding the write-ends can
	// never block completion; after the close the pumps are done for sure.
	streamsDone := make(chan struct{})
	go func() { wg.Wait(); close(streamsDone) }()
	select {
	case <-streamsDone:
	case <-time.After(execStdioGraceMs):
	}
	stdout.Close()
	stderr.Close()
	<-streamsDone

	return execResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
		Code:   execExitCode(waitErr),
		Killed: killedFlag.Load(),
	}
}

// execExitCode maps Wait's error to the TS exit code (exec.ts:92 `code ?? 0`):
// a natural exit keeps its code; signal death is Node's null → 0; non-exit
// Wait failures take the exec.ts catch path (code 1).
func execExitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitCodeFromRaw(exitErr.ExitCode())
	}
	return 1
}

// exitCodeFromRaw maps a raw exit code to the TS shape (`code ?? 0`):
// negative = signal death = Node's null → 0. (Same idiom as the bash tool's
// exitCodeFromRaw in package tools — the -1 case is unreachable on Windows,
// where a forced kill reports a real exit code, but it is the Unix shape.)
func exitCodeFromRaw(raw int) int {
	if raw < 0 {
		return 0
	}
	return raw
}
