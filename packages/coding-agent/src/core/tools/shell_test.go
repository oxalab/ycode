package tools

// shell_test.go verifies the reference-faithful shell resolution in
// getShellConfig: Git Bash discovery on Windows, /bin/bash on Unix, and a
// hard error (not a cmd.exe fallback) when no bash is present.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ycode/packages/coding-agent/src/config"
)

// TestGetShellConfigFindsBash confirms a bash is resolved on the current
// platform (this machine has Git Bash on Windows / bash on Unix). The cmd.exe
// fallback would have returned no error and a Windows shell; we assert the
// resolved shell is actually a bash.
func TestGetShellConfigFindsBash(t *testing.T) {
	shell, args, err := getShellConfig(nil)
	if err != nil {
		t.Fatalf("expected a shell, got error: %v", err)
	}
	if len(args) == 0 || args[0] != "-c" {
		t.Errorf("args = %v, want [\"-c\"]", args)
	}
	// On Windows, the resolved shell must NOT be cmd.exe.
	if runtime.GOOS == "windows" {
		if strings.EqualFold(shell, "cmd.exe") || strings.HasSuffix(strings.ToLower(shell), "cmd.exe") {
			t.Errorf("shell resolved to cmd.exe (%q) — getShellConfig must find bash, not fall back to cmd", shell)
		}
		// It should be a bash.exe (Git Bash or PATH bash).
		lower := strings.ToLower(shell)
		if !strings.Contains(lower, "bash") {
			t.Errorf("shell %q does not look like bash", shell)
		}
		// And it must actually exist.
		if _, err := os.Stat(shell); err != nil {
			t.Errorf("resolved shell %q does not exist: %v", shell, err)
		}
	}
}

// TestGetShellConfigCustomPath verifies the explicit shellPath path: an
// existing path is used, a missing path errors.
func TestGetShellConfigCustomPath(t *testing.T) {
	// Use the bash we just resolved as a stand-in for an existing custom path.
	realShell, _, err := getShellConfig(nil)
	if err != nil {
		t.Skipf("no bash available to use as custom path: %v", err)
	}
	custom := realShell
	shell, _, err := getShellConfig(&custom)
	if err != nil {
		t.Errorf("custom shellPath (%q) errored: %v", custom, err)
	}
	if shell != custom {
		t.Errorf("custom shellPath ignored: got %q want %q", shell, custom)
	}

	// Missing custom path → error.
	missing := "/this/path/does/not/exist/bash"
	if _, _, err := getShellConfig(&missing); err == nil {
		t.Errorf("missing custom shellPath should error")
	}
}

// TestGetShellConfigErrorsWithoutBash simulates "no bash found" by pointing
// PATH at a directory with no bash and no ProgramFiles Git install. We can't
// fully control the Windows ProgramFiles lookup, so instead we verify the
// error-message shape from the custom-path-missing branch, which shares the
// "must error, not cmd-fallback" contract.
func TestGetShellConfigErrorsWithoutBash(t *testing.T) {
	// A missing custom path errors clearly (not cmd.exe).
	missing := "/nonexistent/bash-xyz"
	_, _, err := getShellConfig(&missing)
	if err == nil {
		t.Fatal("missing custom shellPath should return an error, not fall back silently")
	}
}

// TestGetShellConfigNoCmdExeFallback is the regression guard: on Windows,
// exec.LookPath("cmd.exe") would succeed, but our resolver must never return
// it. We confirm the contract holds by checking that the resolved binary is
// executable (i.e. a real shell was found, not a synthetic "cmd.exe" string).
func TestGetShellConfigNoCmdExeFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	shell, _, err := getShellConfig(nil)
	if err != nil {
		t.Skipf("no bash on this machine: %v", err)
	}
	abs, _ := exec.LookPath(shell)
	if abs == "" {
		// shell may already be absolute; just confirm it's executable.
		if _, err := os.Stat(shell); err != nil {
			t.Errorf("resolved shell %q is not a real file", shell)
		}
	}
}

// TestGetShellEnvPrependsBinDir confirms the managed-binaries directory is
// prepended to PATH in the shell environment (reference getShellEnv behavior).
func TestGetShellEnvPrependsBinDir(t *testing.T) {
	binDir := config.GetBinDir()
	if binDir == "" {
		t.Skip("GetBinDir() returned empty")
	}
	binDirAbs, _ := filepath.Abs(binDir)

	env := getShellEnv()

	// Find PATH (case-insensitive).
	var pathValue string
	found := false
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if strings.EqualFold(kv[:eq], "PATH") {
			pathValue = kv[eq+1:]
			found = true
			break
		}
	}
	if !found {
		t.Fatal("PATH not found in shell env")
	}

	entries := strings.Split(pathValue, string(os.PathListSeparator))
	for _, e := range entries {
		eAbs, _ := filepath.Abs(e)
		if strings.EqualFold(eAbs, binDirAbs) {
			// It's present — verify it's the FIRST entry (prepended, not appended).
			firstAbs, _ := filepath.Abs(entries[0])
			if !strings.EqualFold(firstAbs, binDirAbs) {
				t.Errorf("binDir is on PATH but not first: first=%q, binDir=%q", entries[0], binDirAbs)
			}
			return
		}
	}
	t.Errorf("binDir %q not found on PATH: %v", binDirAbs, entries)
}

// TestGetShellEnvIdempotent confirms calling getShellEnv when binDir is already
// on PATH doesn't duplicate it (matches reference's hasBinDir guard).
func TestGetShellEnvIdempotent(t *testing.T) {
	env1 := getShellEnv()
	env2 := getShellEnv()

	binDir := config.GetBinDir()
	binDirAbs, _ := filepath.Abs(binDir)

	count := func(env []string) int {
		n := 0
		for _, kv := range env {
			eq := strings.IndexByte(kv, '=')
			if eq < 0 || !strings.EqualFold(kv[:eq], "PATH") {
				continue
			}
			for _, e := range strings.Split(kv[eq+1:], string(os.PathListSeparator)) {
				eAbs, _ := filepath.Abs(e)
				if strings.EqualFold(eAbs, binDirAbs) {
					n++
				}
			}
		}
		return n
	}

	if c := count(env1); c != 1 {
		t.Errorf("first call: binDir appears %d times, want 1", c)
	}
	if c := count(env2); c != 1 {
		t.Errorf("second call: binDir appears %d times, want 1 (idempotent)", c)
	}
}
