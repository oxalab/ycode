package ycode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// commandOptions
// ============================================================================

type commandOptions struct {
	Cwd       string
	TimeoutMs int
	Env       map[string]string
}

// ============================================================================
// shouldUseWindowsShell
// ============================================================================

// shouldUseWindowsShell returns true if a command should be run through
// cmd.exe on Windows (e.g., npm/cmd are .cmd files).
func shouldUseWindowsShell(command string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(command))
	return ext == "" || ext == ".cmd" || ext == ".bat" || ext == ".exe"
}

// ============================================================================
// getEnv (handles Linux /proc/self/environ fallback)
// ============================================================================

func getCommandEnv() []string {
	if runtime.GOOS != "linux" || len(os.Environ()) > 0 {
		return os.Environ()
	}
	// Linux fallback when environment appears empty
	data, err := os.ReadFile("/proc/self/environ")
	if err != nil {
		return os.Environ()
	}
	var env []string
	for _, entry := range strings.Split(string(data), "\x00") {
		if strings.Contains(entry, "=") {
			env = append(env, entry)
		}
	}
	if len(env) == 0 {
		return os.Environ()
	}
	return env
}

// ============================================================================
// runCommand (async, inherit stdio)
// ============================================================================

func (pm *DefaultPackageManager) runCommand(command string, args []string, opts commandOptions) error {
	cmd := exec.Command(command, args...)
	cmd.Env = getCommandEnv()
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	// Route stdout/stderr: when stdout is taken over by TUI, send to stderr
	if IsStdoutTakenOver() {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if shouldUseWindowsShell(command) {
		// Wrap in cmd.exe for .cmd/.bat files on Windows
		cmd = exec.Command("cmd", append([]string{"/c", command}, args...)...)
		cmd.Env = getCommandEnv()
		if opts.Cwd != "" {
			cmd.Dir = opts.Cwd
		}
	}

	return cmd.Run()
}

// ============================================================================
// runCommandCapture (async, capture stdout/stderr)
// ============================================================================

func (pm *DefaultPackageManager) runCommandCapture(command string, args []string, opts commandOptions) (string, error) {
	if shouldUseWindowsShell(command) {
		args = append([]string{"/c", command}, args...)
		command = "cmd"
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if opts.TimeoutMs > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = opts.Cwd
	env := getCommandEnv()
	if opts.Env != nil {
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
	}
	cmd.Env = env

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s %s timed out after %dms", command, strings.Join(args, " "), opts.TimeoutMs)
	}
	if err != nil {
		stderr := stderrBuf.String()
		stdout := stdoutBuf.String()
		return "", fmt.Errorf("%s %s failed: %s", command, strings.Join(args, " "), firstNonEmptyString(stderr, stdout))
	}
	return strings.TrimSpace(stdoutBuf.String()), nil
}

// ============================================================================
// runCommandSync
// ============================================================================

func (pm *DefaultPackageManager) runCommandSync(command string, args []string) string {
	if shouldUseWindowsShell(command) {
		args = append([]string{"/c", command}, args...)
		command = "cmd"
	}

	cmd := exec.Command(command, args...)
	cmd.Env = getCommandEnv()
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if err != nil {
		return strings.TrimSpace(stderrBuf.String() + stdoutBuf.String())
	}
	return strings.TrimSpace(stdoutBuf.String())
}

// ============================================================================
// runWithConcurrency
// ============================================================================

// runWithConcurrency runs tasks with at most `limit` concurrently.
// Returns the results in the same order as the input tasks.
// A task returning (nil, nil) is preserved as nil in the result.
func runWithConcurrency(tasks []func() (interface{}, error), limit int) ([]interface{}, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	results := make([]interface{}, len(tasks))
	errs := make([]error, len(tasks))

	var indexMu sync.Mutex
	nextIndex := 0

	workerCount := limit
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}

	var wg sync.WaitGroup
	wg.Add(workerCount)

	for w := 0; w < workerCount; w++ {
		go func() {
			defer wg.Done()
			for {
				indexMu.Lock()
				index := nextIndex
				nextIndex++
				indexMu.Unlock()

				if index >= len(tasks) {
					return
				}
				results[index], errs[index] = tasks[index]()
			}
		}()
	}

	wg.Wait()

	// Return first error encountered (if any)
	for _, e := range errs {
		if e != nil {
			return results, e
		}
	}
	return results, nil
}

// ============================================================================
// Helpers
// ============================================================================

func firstNonEmptyString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
