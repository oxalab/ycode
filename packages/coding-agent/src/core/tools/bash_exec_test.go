package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

// --- Story 21: C9 streaming throttle, C10 deadline guard, C11 null exit ---

// fakeBashOps stubs BashOperations for tool-level tests (the opts.Operations
// seam in CreateBashToolDefinition).
type fakeBashOps struct {
	onExec func(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error)
}

func (f *fakeBashOps) Exec(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error) {
	return f.onExec(ctx, command, cwd, opts)
}

func intPtr(i int) *int { return &i }

// partialText extracts the text of a *ToolExecuteResult partial; ok=false for
// the initial empty partial (no content blocks).
func partialText(partial any) (string, bool) {
	ter, ok := partial.(*ToolExecuteResult)
	if !ok || len(ter.Content) == 0 {
		return "", false
	}
	tc, ok := ter.Content[0].(*llm.TextContent)
	if !ok {
		return "", false
	}
	return tc.Text, true
}

// C9: rapid chunks must coalesce through the 100ms trailing-edge throttle —
// 5000 chunks produce far fewer than 5000 partials (the reference's own test
// bound is < 25), the initial partial is empty, and the final flush carries
// the complete output. Driven through ExecuteUpdate; only that form streams.
func TestBashStreamsThrottledPartials(t *testing.T) {
	const chunks = 5000
	ops := &fakeBashOps{onExec: func(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error) {
		for i := 0; i < chunks; i++ {
			opts.OnData([]byte("x"))
		}
		return &BashExecResult{ExitCode: intPtr(0)}, nil
	}}
	def := CreateBashToolDefinition(".", &BashToolOptions{Operations: ops})

	var mu sync.Mutex
	var partials []*ToolExecuteResult
	_, err := def.ExecuteUpdate(context.Background(), json.RawMessage(`{"command":"gen"}`), func(partial any) {
		mu.Lock()
		defer mu.Unlock()
		if ter, ok := partial.(*ToolExecuteResult); ok {
			partials = append(partials, ter)
		}
	})
	if err != nil {
		t.Fatalf("ExecuteUpdate: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(partials) < 2 {
		t.Fatalf("expected initial partial + at least one emission, got %d", len(partials))
	}
	if len(partials[0].Content) != 0 {
		t.Errorf("first partial must be the empty kickoff, got %d content blocks", len(partials[0].Content))
	}
	if len(partials) >= 25 {
		t.Errorf("throttle ineffective: %d partials for %d chunks", len(partials), chunks)
	}
	last, ok := partialText(partials[len(partials)-1])
	if !ok {
		t.Fatalf("last partial has no text")
	}
	if last != strings.Repeat("x", chunks) {
		t.Errorf("final flush incomplete: got %d chars, want %d", len(last), chunks)
	}
}

// C9: chunks spaced beyond the throttle window each emit promptly instead of
// waiting for the trailing timer.
func TestBashThrottleSpreadsEmissions(t *testing.T) {
	ops := &fakeBashOps{onExec: func(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error) {
		for b := 0; b < 3; b++ {
			opts.OnData([]byte(fmt.Sprintf("batch%d\n", b)))
			time.Sleep(130 * time.Millisecond) // > bashUpdateThrottleMs
		}
		return &BashExecResult{ExitCode: intPtr(0)}, nil
	}}
	def := CreateBashToolDefinition(".", &BashToolOptions{Operations: ops})

	var mu sync.Mutex
	var texts []string
	_, err := def.ExecuteUpdate(context.Background(), json.RawMessage(`{"command":"gen"}`), func(partial any) {
		mu.Lock()
		defer mu.Unlock()
		if text, ok := partialText(partial); ok {
			texts = append(texts, text)
		}
	})
	if err != nil {
		t.Fatalf("ExecuteUpdate: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(texts) < 3 || len(texts) > 6 {
		t.Errorf("expected one emission per spaced batch (3, flush may add one), got %d", len(texts))
	}
	if len(texts) > 0 && !strings.Contains(texts[len(texts)-1], "batch2") {
		t.Errorf("last emission missing final batch: %q", texts[len(texts)-1])
	}
}

// C11 (tool level): a signal-killed process surfaces as ExitCode nil, which
// the reference treats as success (bash.ts:392) — no "Command exited" suffix.
func TestBashNilExitCodeIsSuccess(t *testing.T) {
	ops := &fakeBashOps{onExec: func(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error) {
		opts.OnData([]byte("partial output before the kill"))
		return &BashExecResult{ExitCode: nil}, nil
	}}
	def := CreateBashToolDefinition(".", &BashToolOptions{Operations: ops})
	res, err := def.Execute(context.Background(), json.RawMessage(`{"command":"killed"}`))
	if err != nil {
		t.Fatalf("nil exit code must be success, got error: %v", err)
	}
	ter := res.(*ToolExecuteResult)
	if len(ter.Content) == 0 {
		t.Fatal("expected output content")
	}
	if tc := ter.Content[0].(*llm.TextContent); strings.Contains(tc.Text, "Command exited") {
		t.Errorf("success result carries an exit-code status: %q", tc.Text)
	}
}

// C11 control: a real non-zero exit must still produce the status-suffixed
// error, at the same layer as before.
func TestBashNonZeroExitStillErrors(t *testing.T) {
	ops := &fakeBashOps{onExec: func(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error) {
		return &BashExecResult{ExitCode: intPtr(3)}, nil
	}}
	def := CreateBashToolDefinition(".", &BashToolOptions{Operations: ops})
	_, err := def.Execute(context.Background(), json.RawMessage(`{"command":"fail"}`))
	if err == nil {
		t.Fatal("expected exit-code error")
	}
	if !strings.Contains(err.Error(), "Command exited with code 3") {
		t.Errorf("error missing formatted exit status: %q", err.Error())
	}
}

// C10: a caller deadline with NO timeout option must not panic (the old
// unguarded *opts.Timeout deref) and must report the inherited deadline as a
// plain "Command timed out" — no "after N seconds" (timeoutSentinel nil branch).
func TestExecParentDeadlineWithoutTimeoutOption(t *testing.T) {
	newExecOps(t) // gate on bash availability
	def := CreateBashToolDefinition(".", &BashToolOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := def.Execute(ctx, json.RawMessage(`{"command":"sleep 10"}`))
	if err == nil {
		t.Fatal("expected timeout error from inherited deadline")
	}
	if !strings.Contains(err.Error(), "Command timed out") {
		t.Errorf("error missing timeout message: %q", err.Error())
	}
	if strings.Contains(err.Error(), "timed out after") {
		t.Errorf("inherited deadline must not report seconds: %q", err.Error())
	}
}

// C15: the cwd error must carry the reference's exact casing and trailing
// period (bash.ts:71: "Working directory does not exist: ...\nCannot execute
// bash commands.").
func TestExecCwdErrorWording(t *testing.T) {
	ops := newExecOps(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := ops.Exec(ctx, "echo hi", filepath.Join(t.TempDir(), "missing"), BashExecOptions{})
	if err == nil {
		t.Fatal("expected cwd error")
	}
	if !strings.Contains(err.Error(), "Working directory does not exist: ") ||
		!strings.Contains(err.Error(), "Cannot execute bash commands.") {
		t.Errorf("cwd error = %q, want reference wording", err.Error())
	}
}
