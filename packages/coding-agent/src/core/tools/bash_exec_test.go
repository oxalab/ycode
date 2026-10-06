package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newExecOps builds localBashOps with the resolved default shell. Skips when
// no bash is available (same gate as shell_test.go).
func newExecOps(t *testing.T) *localBashOps {
	t.Helper()
	ops := CreateLocalBashOps(nil).(*localBashOps)
	if _, _, err := getShellConfig(ops.shellPath); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	return ops
}

// execWithTimeout wraps Exec with a hard test-side deadline so a regression
// (hang) fails the test instead of wedging the suite.
func execWithTimeout(t *testing.T, ops *localBashOps, command string, timeout time.Duration, opts BashExecOptions) (*BashExecResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	type res struct {
		r   *BashExecResult
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := ops.Exec(ctx, command, ".", opts)
		ch <- res{r, err}
	}()
	select {
	case got := <-ch:
		return got.r, got.err
	case <-time.After(timeout + 10*time.Second):
		t.Fatalf("Exec did not return within %v (hang regression)", timeout+10*time.Second)
		return nil, nil
	}
}

// C1: a backgrounded child inheriting stdio must not hang the tool. The
// process exits immediately; the orphaned `sleep` used to hold the pipe
// write-end open and wg.Wait() blocked for its full duration.
func TestExecBackgroundChildDoesNotHang(t *testing.T) {
	ops := newExecOps(t)

	var collected strings.Builder
	start := time.Now()
	_, err := execWithTimeout(t, ops, "echo hi-from-bg; sleep 30 &", 20*time.Second,
		BashExecOptions{OnData: func(d []byte) { collected.Write(d) }})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Exec error: %v", err)
	}
	if elapsed > 15*time.Second {
		t.Errorf("Exec took %v — backgrounded child blocked completion", elapsed)
	}
	if !strings.Contains(collected.String(), "hi-from-bg") {
		t.Errorf("output missing: %q", collected.String())
	}
}

// C3: a timeout must surface the collected output in the formatted message
// (pre-fix the raw `timeout:N` sentinel discarded it). Driven through the
// tool definition because the output+status mapping lives in Execute, same
// layering as the TS reference.
func TestExecTimeoutPreservesOutput(t *testing.T) {
	newExecOps(t) // gate on bash availability

	def := CreateBashToolDefinition(".", &BashToolOptions{})
	_, err := def.Execute(context.Background(),
		[]byte(`{"command":"echo before-deadline; sleep 30","timeout":2}`))

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "Command timed out after 2 seconds") {
		t.Errorf("error missing formatted timeout message: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "before-deadline") {
		t.Errorf("error lost collected output: %q", err.Error())
	}
}

// C3 abort parity: a canceled context with no output yields exactly
// "Command aborted" — no "(no output)" placeholder, TS capitalization.
func TestExecAbortWording(t *testing.T) {
	newExecOps(t) // gate on bash availability

	def := CreateBashToolDefinition(".", &BashToolOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	type res struct {
		r   any
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := def.Execute(ctx, []byte(`{"command":"sleep 30"}`))
		ch <- res{r, err}
	}()
	time.Sleep(500 * time.Millisecond) // let the command start
	cancel()
	select {
	case got := <-ch:
		if got.err == nil {
			t.Fatal("expected abort error")
		}
		if got.err.Error() != "Command aborted" {
			t.Errorf("abort error = %q, want %q", got.err.Error(), "Command aborted")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Execute did not return after cancel")
	}
}

// C2: spilled temp files must get unique random IDs, not all-zeros.
func TestDefaultTempFilePathUnique(t *testing.T) {
	a := defaultTempFilePath("ycode-base")
	b := defaultTempFilePath("ycode-base")
	if a == b {
		t.Fatalf("temp file paths must be unique, both %q", a)
	}
	if !strings.HasPrefix(filepath.Base(a), "ycode-base-") {
		t.Errorf("prefix not honored: %q", a)
	}
	suffix := filepath.Base(a)
	suffix = strings.TrimSuffix(strings.TrimPrefix(suffix, "ycode-base-"), ".log")
	if len(suffix) != 16 {
		t.Errorf("expected 8-byte hex id, got %q", suffix)
	}
}
