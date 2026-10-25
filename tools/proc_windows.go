//go:build windows

package tools

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {}

func terminateProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func killProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
