//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func KillProcessTree(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}
