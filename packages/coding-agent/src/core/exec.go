package ycode

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

type ExecOptions struct {
	Signal context.Context
	Timeout int // milliseconds
	Cwd string
}

type ExecResult struct {
	Stdout string
	Stderr string
	Code int
	Killed bool
}

func ExecCommand(command string, args []string, cwd string, options *ExecOptions) (*ExecResult, error) {
	ctx := context.Background()
	var cancel context.CancelFunc

	if options != nil {
		if options.Signal != nil {
			ctx = options.Signal
		}
		if options.Timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(options.Timeout)*time.Millisecond)
		}
	}
	if cancel != nil {
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = cwd
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Killed: ctx.Err() == context.Canceled || ctx.Err() == context.DeadlineExceeded,
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Code = exitErr.ExitCode()
		} else {
			result.Code = 1
		}
	}
	return result, nil
}