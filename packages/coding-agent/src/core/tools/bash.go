package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/config"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

const bashUpdateThrottleMs = 100

type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
}

type BashToolDetails struct {
	Truncation     *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string            `json:"fullOutputPath,omitempty"`
}

// BashExecOptions: Contains options for command execution
type BashExecOptions struct {
	OnData  func([]byte)
	Timeout *float64
	Env     []string
}

// BashExecResult: Contains the result of command execution.
type BashExecResult struct {
	ExitCode int
}

type BashOperations interface {
	Exec(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error)
}

// --- Default tool shell operations ---

type localBashOps struct {
	shellPath 	*string
}

// getShellConfig resolves the shell to use for bash command execution. It is a
// faithful port of reference utils/shell.ts:getShellConfig — it NEVER falls back
// to cmd.exe on Windows; bash is a hard requirement.
//
// Resolution order (matches the reference):
//  1. User-specified shellPath (verified to exist)
//  2. Windows: Git Bash in ProgramFiles / ProgramFiles(x86), then bash.exe on PATH
//  3. Unix: /bin/bash, then bash on PATH
//  4. Error if no bash is found (listing install options, like the reference)
func getShellConfig(shellPath *string) (string, []string, error) {
	// 1. User-specified shell path.
	if shellPath != nil && *shellPath != "" {
		if _, err := os.Stat(*shellPath); err != nil {
			return "", nil, fmt.Errorf("Custom shell path not found: %s", *shellPath)
		}
		return *shellPath, []string{"-c"}, nil
	}

	if runtime.GOOS == "windows" {
		// 2. Try Git Bash in known locations.
		var searched []string
		paths := []string{}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			p := pf + "\\Git\\bin\\bash.exe"
			paths = append(paths, p)
		}
		if pf86 := os.Getenv("ProgramFiles(x86)"); pf86 != "" {
			p := pf86 + "\\Git\\bin\\bash.exe"
			paths = append(paths, p)
		}
		for _, p := range paths {
			searched = append(searched, p)
			if _, err := os.Stat(p); err == nil {
				return p, []string{"-c"}, nil
			}
		}

		// 3. Fallback: bash.exe on PATH (Cygwin, MSYS2, WSL, etc.).
		if bash, err := exec.LookPath("bash.exe"); err == nil {
			return bash, []string{"-c"}, nil
		}

		// 4. No bash found — hard error, matching the reference.
		return "", nil, fmt.Errorf(
			"No bash shell found. Options:\n"+
				"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
				"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
				"  3. Set shellPath in settings.json\n\n"+
				"Searched Git Bash in:\n%s",
			formatSearchedPaths(searched))
	}

	// Unix: /bin/bash first.
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash", []string{"-c"}, nil
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		return bash, []string{"-c"}, nil
	}
	return "sh", []string{"-c"}, nil
}

// formatSearchedPaths renders the searched-paths list for the no-bash error.
func formatSearchedPaths(paths []string) string {
	if len(paths) == 0 {
		return "  (no Git Bash locations checked)"
	}
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("  ")
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return b.String()
}



// getShellEnv returns the process environment with the managed-binaries
// directory (config.GetBinDir()) prepended to PATH, so bundled helpers (rg, fd,
// etc.) are discoverable by bash commands. Faithful port of reference
// utils/shell.ts:getShellEnv.
//
// PATH key lookup is case-insensitive (Windows uses "Path", Unix "PATH"). If
// binDir is already on PATH it is left as-is; otherwise it is prepended.
func getShellEnv() []string {
	binDir := config.GetBinDir()
	env := os.Environ()
	if binDir == "" {
		return env
	}
	// Normalize for comparison (abs path).
	binDirAbs, _ := filepath.Abs(binDir)

	pathKey := "PATH"
	pathIdx := -1
	for i, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if strings.EqualFold(kv[:eq], "PATH") {
			pathKey = kv[:eq]
			pathIdx = i
			break
		}
	}

	if pathIdx < 0 {
		// No PATH in env — add one with just binDir.
		return append(env, pathKey+"="+binDirAbs)
	}

	currentPath := env[pathIdx][len(pathKey)+1:]
	entries := strings.Split(currentPath, string(os.PathListSeparator))
	for _, e := range entries {
		if e == "" {
			continue
		}
		eAbs, _ := filepath.Abs(e)
		if strings.EqualFold(eAbs, binDirAbs) {
			// Already present — no change needed.
			return env
		}
	}

	// Prepend binDir.
	updated := binDirAbs
	if currentPath != "" {
		updated += string(os.PathListSeparator) + currentPath
	}
	env[pathIdx] = pathKey + "=" + updated
	return env
}

func (l *localBashOps) Exec(ctx context.Context, command string, cwd string, opts BashExecOptions) (*BashExecResult, error) {
	if _, err := os.Stat(cwd); err != nil {
		return nil, fmt.Errorf("working directory does not exist: %s\nCannot execute bash commands", cwd)
	}
	shell, args, err := getShellConfig(l.shellPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(shell, append(args, command)...)
	cmd.Dir = cwd
	cmd.Env = opts.Env
	if opts.Env == nil {
		cmd.Env = getShellEnv()
	}

	setProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}
	pid := cmd.Process.Pid

	// Handle timeout
	var cancel context.CancelFunc
	if opts.Timeout != nil && *opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*opts.Timeout*float64(time.Second)))
		defer cancel()
	}

	// Handle abort via go routine
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			KillProcessTree(pid)
		case <-done:
		}
	} ()

	// Stream stdout and stderr
	var wg sync.WaitGroup
	streamOutput := func(r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 && opts.OnData != nil {
				opts.OnData(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}

	wg.Add(2)
	go streamOutput(stdout)
	go streamOutput(stderr)

	wg.Wait()
	waitErr := cmd.Wait()
	close(done)

	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("timeout:%0.f", *opts.Timeout)
	}
	if ctx.Err() == context.Canceled {
		return nil, fmt.Errorf("aborted")
	}

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, waitErr
		}
	}
	return &BashExecResult{ExitCode: exitCode}, nil
}


// CreateLocalBashOperations: Creates bash operations using local shell execution.
func CreateLocalBashOps(opts *BashToolOptions) BashOperations{
	var shellPath *string
	if opts != nil {
		shellPath = opts.ShellPath
	}
	return &localBashOps{shellPath: shellPath}
}

// --- Bash Tool Options ---

type BashSpawnContext struct {
	Command 	string
	Cwd 		string
	Env 		[]string
}

type BashSpawnHook func(ctx BashSpawnContext) BashSpawnContext

type BashToolOptions struct {
	Operations 		BashOperations
	CommandPrefix 	*string
	ShellPath 		*string
	SpawnHook 		BashSpawnHook
}

func resolveSpawnContext(command string, cwd string, hook BashSpawnHook) BashSpawnContext {
	base := BashSpawnContext{Command: command, Cwd: cwd, Env: getShellEnv()}
	if hook != nil {
		return hook(base)
	}
	return base
}

func formatDuration(ms int64) string {
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func appendStatus(text string, status string) string {
	if text != "" {
		return text + "\n\n" + status
	}
	return status
}

// --- Create Tool ---

func CreateBashToolDefinition(cwd string, opts *BashToolOptions) *extensions.ToolDefinition {
	var ops BashOperations = CreateLocalBashOps(opts)
	if opts != nil && opts.Operations != nil {
		ops = opts.Operations
	}
	commandPrefix := ""
	if opts != nil && opts.CommandPrefix != nil {
		commandPrefix = *opts.CommandPrefix
	}
	spawnHook := BashSpawnHook(nil)
	if opts != nil {
		spawnHook = opts.SpawnHook
	}
	return &extensions.ToolDefinition{
		Name: 			"bash",
		Label: 			"bash",
		Description:   fmt.Sprintf("Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", DefaultMaxLines, DefaultMaxBytes/1024),
		PromptSnippet: "Execute bash commands (ls, grep, find, etc.)",
		Parameters: objectSchema(map[string]prop{
			"command": {typ: propString, description: "Bash command to execute"},
			"timeout": {typ: propNumber, description: "Timeout in seconds (optional, no default timeout)"},
		}, []string{"command"}),
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input BashToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("Invalid arguments: %w", err)
			}
			resolveCommand := input.Command
			if commandPrefix != "" {
				resolveCommand = commandPrefix + "\n" + input.Command
			}
			spawnCtx := resolveSpawnContext(resolveCommand, cwd, spawnHook)
			accum := NewOutputAccumulator(OutputAccumulatorOptions{TempFilePrefix:"ycode-base"})

			execOpts := BashExecOptions{
				OnData: func(data []byte) { accum.Append(data) },
				Timeout: input.Timeout,
				Env: 	spawnCtx.Env,
			}
			result, execErr := ops.Exec(ctx, spawnCtx.Command, spawnCtx.Cwd, execOpts)
			accum.Finish()
			accum.CloseTempFile()

			snapshot := accum.Snapshot(true)
			outputText := snapshot.Content
			if outputText == "" {
				outputText = "(no output)"
			}

			
			if snapshot.Truncation.Truncated {
				startLine := snapshot.Truncation.TotalLines - snapshot.Truncation.OutputLines + 1
				endLine := snapshot.Truncation.TotalLines
				if snapshot.Truncation.LastLinePartial {
					lastLineSize := FormatSize(accum.GetLastLineBytes())
					outputText += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
						FormatSize(snapshot.Truncation.OutputBytes), endLine, lastLineSize, snapshot.FullOutputPath)
				} else if snapshot.Truncation.TruncatedBy == "lines" {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
						startLine, endLine, snapshot.Truncation.TotalLines, snapshot.FullOutputPath)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
						startLine, endLine, snapshot.Truncation.TotalLines, FormatSize(DefaultMaxBytes), snapshot.FullOutputPath)
				}
			}
			if execErr != nil {
				if execErr.Error() == "aborted" {
					return nil, fmt.Errorf(appendStatus(outputText, "Command Aborted"))
				}
				if strings.HasPrefix(execErr.Error(), "timeout:") {
					timeoutSecs := strings.TrimPrefix(execErr.Error(), "tomeout:")
					(appendStatus(outputText, fmt.Sprintf("Command timed out after %s seconds", timeoutSecs)))
				}
				return nil, execErr
			}
			if result.ExitCode != 0 {
				return nil, fmt.Errorf(appendStatus(outputText, fmt.Sprintf("Command exited with code %d", result.ExitCode)))
			}
			return &ToolExecuteResult{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: outputText}},
			}, nil
		},
		RenderCall: func(args any) any {
			argsMap, _ := args.(map[string]any)
			command := "?"
			if cmd, ok := argsMap["command"].(string); ok {
				command = cmd
			}
			text := "$ " + command
			if timeout, ok := argsMap["timeout"].(float64); ok {
				text += fmt.Sprintf(" (timeout %.0fs)", timeout)
			}
			return text
		},
		RenderResult: func(args any, result any, options any) any {
			res, ok := result.(*ToolExecuteResult)
			if !ok || len(res.Content) == 0 {
				return ""
			}
			tc, ok := res.Content[0].(*llm.TextContent)
			if !ok {
				return ""
			}
			return tc.Text
		},
	}
}

func CreateBashTool(cwd string, opts *BashToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateBashToolDefinition(cwd, opts), nil)
}





/*
FOOTNOTE:
 1. No streaming output updates — TS uses throttled onUpdate callbacks to stream 
 partial results. Go's ToolDefinition.Execute doesn't have the onUpdate callback 
 (it was simplified in types.go). Output is returned only after command completes. 
 Streaming requires either enhancing ToolDefinition.Execute signature or using 
 AgentTool directly.

 2. sync.WaitGroup needs "sync" import — The streamOutput goroutines use a WaitGroup. 
 Make sure "sync" is in imports.

 3. runtime.GOOS == "windows" for shell detection — TS uses process.platform. 
 Same logic, different API.

 4. Process tree killing limited on Windows — Uses taskkill /F /T /PID which requires 
 the process to still be running. Go's exec.CommandContext kills with os.Kill on 
 cancellation, but this may not kill child processes. The killProcessTree function 
 handles this but timing is tricky.

 5. CommandPrefix is *string — Changed from TS's string to *string to distinguish 
 "not set" from empty string. If the user writes it as string, an empty string would 
 still prepend a newline. This is a minor Go-idiom difference.

 6. input.Timeout is *float64 — TS uses number | undefined. Go uses pointer for 
 optional. The float64 allows fractional seconds.
*/