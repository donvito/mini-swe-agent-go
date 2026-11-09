//go:build windows

package environments

import "os/exec"

func shellCommand(command string) *exec.Cmd { return exec.Command("cmd", "/C", command) }

func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
