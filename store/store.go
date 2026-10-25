// Package store resolves per-project storage under a single home root so no
// helper state leaks into user workspaces. Each kind (sessions, memory,
// skills, plugins, index) gets <root>/<kind>/<slug>/, where <slug> is the
// workspace folder name plus a short hash of its absolute path — same-named
// projects in different locations stay separate.
//
// The root is resolved as: the value passed to SetRoot, else $RAKIT_HOME,
// else ~/.rakit. Hosts that keep their own data layout (e.g. the forge-ade
// desktop app) call SetRoot before constructing any engine component.
package store

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	mu           sync.RWMutex
	rootOverride string
)

// SetRoot pins the storage root to dir. Hosts with their own home layout
// call this once at startup, before any engine component resolves a path.
func SetRoot(dir string) {
	mu.Lock()
	rootOverride = dir
	mu.Unlock()
}

// Root returns the storage root (see package comment), creating it if needed.
func Root() (string, error) {
	mu.RLock()
	override := rootOverride
	mu.RUnlock()

	dir := override
	if dir == "" {
		dir = os.Getenv("RAKIT_HOME")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".rakit")
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// ProjectSlug derives a stable, filesystem-safe per-project name from a
// workspace path: "<leaf>-<8-char hash of the absolute path>".
func ProjectSlug(workspacePath string) string {
	clean := strings.TrimRight(strings.TrimSpace(workspacePath), "/\\")
	if clean == "" || clean == "." {
		return "default"
	}
	leaf := clean
	if i := strings.LastIndexAny(clean, "/\\"); i >= 0 {
		leaf = clean[i+1:]
	}
	if leaf == "" {
		leaf = "default"
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		abs = clean
	}
	sum := sha1.Sum([]byte(abs))
	return leaf + "-" + hex.EncodeToString(sum[:])[:8]
}

// ProjectDir returns <root>/<kind>/<slug>, creating it if needed.
func ProjectDir(kind, workspacePath string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, kind, ProjectSlug(workspacePath))
	return dir, os.MkdirAll(dir, 0o755)
}
