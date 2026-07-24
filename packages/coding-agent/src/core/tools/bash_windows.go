package tools

import (
	"fmt"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {
	// Windows doesn't support process groups via Setpgid
}

func KillProcessTree(pid int) error {
	return exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
}
