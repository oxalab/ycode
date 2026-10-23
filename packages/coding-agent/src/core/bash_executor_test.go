package ycode

import (
	"context"
	"testing"

	"ycode/packages/coding-agent/src/core/tools"
)

// fakeSessionBashOps stubs tools.BashOperations for session-path tests.
type fakeSessionBashOps struct {
	result *tools.BashExecResult
	err    error
}

func (f *fakeSessionBashOps) Exec(ctx context.Context, command string, cwd string, opts tools.BashExecOptions) (*tools.BashExecResult, error) {
	return f.result, f.err
}

// C11: a signal-killed process (nil exit code, TS null) must propagate as a
// nil BashResult.ExitCode — `result.exitCode ?? undefined` in the reference —
// not be fabricated into a non-nil pointer.
func TestExecuteBashNilExitCodePropagates(t *testing.T) {
	res, err := ExecuteBashWithOperations("cmd", ".", &fakeSessionBashOps{result: &tools.BashExecResult{ExitCode: nil}}, nil)
	if err != nil {
		t.Fatalf("ExecuteBashWithOperations: %v", err)
	}
	if res.ExitCode != nil {
		t.Errorf("ExitCode = %d, want nil", *res.ExitCode)
	}
	if res.Cancelled {
		t.Error("Cancelled = true, want false (nil exit is not a cancellation)")
	}
}

// Control: a normal exit code still round-trips.
func TestExecuteBashExitCodeRoundTrips(t *testing.T) {
	code := 7
	res, err := ExecuteBashWithOperations("cmd", ".", &fakeSessionBashOps{result: &tools.BashExecResult{ExitCode: &code}}, nil)
	if err != nil {
		t.Fatalf("ExecuteBashWithOperations: %v", err)
	}
	if res.ExitCode == nil || *res.ExitCode != 7 {
		t.Errorf("ExitCode = %v, want 7", res.ExitCode)
	}
}
