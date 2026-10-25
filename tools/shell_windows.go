//go:build windows

package tools

import "os"

func shellCommand(command string) (string, []string) {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return shell, []string{"/C", command}
}
