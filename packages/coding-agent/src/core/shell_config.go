package ycode

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"ycode/packages/coding-agent/src/core/tools"
)

type ShellConfig struct {
	Shell string
	Args  []string
}

func getShellConfig() (string, []string, error) {
	if runtime.GOOS == "windows" {
		// Try Git Bash in known locations
		paths := []string{}
		pf := os.Getenv("ProgramFiles")
		if pf != "" {
			paths = append(paths, pf+"\\Git\\bin\\bash.exe")
		}
		pf86 := os.Getenv("ProgramFiles(x86)")
		if pf86 != "" {
			paths = append(paths, pf86+"\\Git\\bin\\bash.exe")
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				return p, []string{"-c"}, nil
			}
		}
		// Fallback: search bash.exe on PATH
		bash, err := exec.LookPath("bash.exe")
		if err == nil {
			return bash, []string{"-c"}, nil
		}
		// Last Resort: sh
		return "sh", []string{"-c"}, nil
	}
	// Unix: try /bin/bash first
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash", []string{"-c"}, nil
	}
	return "sh", []string{"-c"}, nil
}

func SanitizeBinaryOutput(str string) string {
	var b strings.Builder
	for _, r := range str {
		if r == 0x09 || r == 0x0a || r == 0x0d {
			b.WriteRune(r)
			continue
		}
		if r <= 0x1f {
			continue
		}
		if r >= 0xfff9 && r <= 0xfffb {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ============================================================================
// Detached child process tracking
// Mirrors TS trackDetachedChildPid / killTrackedDetachedChildren (utils/shell.ts:112-140)
// ============================================================================

var (
	detachedPidsMu sync.Mutex
	detachedPids   = make(map[int]struct{})
)

// TrackDetachedChildPid registers a detached child process PID for cleanup on shutdown.
func TrackDetachedChildPid(pid int) {
	detachedPidsMu.Lock()
	defer detachedPidsMu.Unlock()
	detachedPids[pid] = struct{}{}
}

// UntrackDetachedChildPid removes a PID from tracking.
func UntrackDetachedChildPid(pid int) {
	detachedPidsMu.Lock()
	defer detachedPidsMu.Unlock()
	delete(detachedPids, pid)
}

// KillTrackedDetachedChildren kills all tracked detached child processes.
func KillTrackedDetachedChildren() {
	detachedPidsMu.Lock()
	pids := make([]int, 0, len(detachedPids))
	for pid := range detachedPids {
		pids = append(pids, pid)
	}
	detachedPids = make(map[int]struct{})
	detachedPidsMu.Unlock()

	for _, pid := range pids {
		tools.KillProcessTree(pid)
	}
}



