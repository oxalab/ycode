package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llm "ycode/packages/llm/src"
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
// B9: the bash tool's prefix is "ycode-bash" (reference bash.ts: "pi-bash") —
// the old call site used a "ycode-base" prefix paralleled by nothing.
func TestDefaultTempFilePathUnique(t *testing.T) {
	a := defaultTempFilePath("ycode-bash")
	b := defaultTempFilePath("ycode-bash")
	if a == b {
		t.Fatalf("temp file paths must be unique, both %q", a)
	}
	if !strings.HasPrefix(filepath.Base(a), "ycode-bash-") {
		t.Errorf("prefix not honored: %q", a)
	}
	suffix := filepath.Base(a)
	suffix = strings.TrimSuffix(strings.TrimPrefix(suffix, "ycode-bash-"), ".log")
	if len(suffix) != 16 {
		t.Errorf("expected 8-byte hex id, got %q", suffix)
	}
}

// B9: the bash tool's accumulator must spill with the "ycode-bash" prefix
// (reference bash.ts passes "pi-bash") — the call site used to pass
// "ycode-base", which paralleled nothing in the reference. End-to-end through
// the tool Execute so the actual call-site constant is locked.
func TestExecSpillUsesYcodeBashPrefix(t *testing.T) {
	ops := newExecOps(t)
	_ = ops // exec goes through the tool's own default ops; shell presence checked above

	def := CreateBashToolDefinition(".", &BashToolOptions{})
	res, err := def.Execute(context.Background(), json.RawMessage(
		`{"command":"awk 'BEGIN{for(i=0;i<600;i++)print \"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\"}'"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	toolRes, ok := res.(*ToolExecuteResult)
	if !ok || len(toolRes.Content) == 0 {
		t.Fatalf("unexpected result shape: %T %+v", res, res)
	}
	var text string
	for _, block := range toolRes.Content {
		if tc, ok := block.(*llm.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "ycode-bash-") {
		t.Errorf("spilled output path missing ycode-bash- prefix: %q", text)
	}
	if strings.Contains(text, "ycode-base-") {
		t.Errorf("stale ycode-base- prefix still in use: %q", text)
	}
}
