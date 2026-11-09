//go:build unix

package environments

import (
	"os/exec"
	"syscall"
)

func shellCommand(command string) *exec.Cmd { return exec.Command("/bin/sh", "-c", command) }

func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killProcessGroup kills the whole process group so that no children are orphaned.
func killProcessGroup(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
