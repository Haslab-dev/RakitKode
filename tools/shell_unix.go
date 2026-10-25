//go:build !windows

package tools

import (
	"os"
	"strings"
)

// shellCommand picks an interactive shell that exists on this machine.
func shellCommand(command string) (string, []string) {
	for _, sh := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err != nil {
			continue
		}
		if strings.HasSuffix(sh, "zsh") {
			return sh, []string{"-l", "-c", command}
		}
		return sh, []string{"-c", command}
	}
	return "/bin/sh", []string{"-c", command}
}
