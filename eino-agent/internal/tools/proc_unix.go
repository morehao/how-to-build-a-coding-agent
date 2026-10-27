//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the command in its own process group so a timeout can
// kill the shell and its children, not just the shell.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
