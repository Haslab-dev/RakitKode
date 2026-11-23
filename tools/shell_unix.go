//go:build !windows

package tools

import (
	"os"
	"path/filepath"
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

// EnrichedEnviron returns the current environment with extra developer binary paths
// (Homebrew, NVM, FNM, Bun, Cargo, Go) prepended to PATH if present.
func EnrichedEnviron() []string {
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			envMap[k] = v
		}
	}
	currentPath := envMap["PATH"]
	homeDir, _ := os.UserHomeDir()
	extraPaths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		filepath.Join(homeDir, "homebrew/bin"),
		filepath.Join(homeDir, ".cargo/bin"),
		filepath.Join(homeDir, "go/bin"),
		filepath.Join(homeDir, ".bun/bin"),
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
	if nvmMatches, err := filepath.Glob(filepath.Join(homeDir, ".nvm/versions/node/*/bin")); err == nil && len(nvmMatches) > 0 {
		extraPaths = append([]string{nvmMatches[len(nvmMatches)-1]}, extraPaths...)
	}
	for _, candidate := range []string{
		filepath.Join(homeDir, ".local/share/fnm/current/bin"),
		filepath.Join(homeDir, ".fnm/current/bin"),
		filepath.Join(homeDir, ".asdf/shims"),
		filepath.Join(homeDir, ".volta/bin"),
	} {
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			extraPaths = append([]string{candidate}, extraPaths...)
		}
	}
	for _, p := range extraPaths {
		if p != "" && !strings.Contains(currentPath, p) {
			currentPath = p + ":" + currentPath
		}
	}
	envMap["PATH"] = currentPath
	var result []string
	for k, v := range envMap {
		result = append(result, k+"="+v)
	}
	return result
}
