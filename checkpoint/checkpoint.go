// Package checkpoint captures a restorable snapshot of the files a mutating
// tool batch is about to touch. It powers /undo and /rewind.
package checkpoint

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxFileBytes = 10 << 20 // 10 MiB per file

// FileRecord remembers whether a file existed before the batch.
type FileRecord struct {
	Path    string `json:"path"` // relative to workspace
	Existed bool   `json:"existed"`
}

// Snapshot is one restorable checkpoint.
type Snapshot struct {
	Turn      int          `json:"turn"`
	CreatedAt time.Time    `json:"created_at"`
	GitCommit string       `json:"git_commit,omitempty"`
	Files     []FileRecord `json:"files"`
}

// Manager stores snapshots for one session.
type Manager struct {
	home      string
	workspace string
	sessionID string
}

func New(home, workspace, sessionID string) *Manager {
	return &Manager{home: home, workspace: workspace, sessionID: sessionID}
}

func (m *Manager) root() string {
	return filepath.Join(m.home, "sessions", m.sessionID, "checkpoints")
}

func (m *Manager) dir(turn int) string {
	return filepath.Join(m.root(), fmt.Sprintf("%04d", turn))
}

func (m *Manager) filesDir(turn int) string {
	return filepath.Join(m.dir(turn), "files")
}

// Capture snapshots paths (absolute or workspace-relative). Missing paths are
// recorded as non-existent so undo can delete files the batch creates.
func (m *Manager) Capture(turn int, paths []string) (*Snapshot, error) {
	snap := &Snapshot{Turn: turn, CreatedAt: time.Now()}
	if err := os.MkdirAll(m.filesDir(turn), 0o755); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, p := range paths {
		rel, err := m.rel(p)
		if err != nil || rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		rec := FileRecord{Path: rel}
		src := filepath.Join(m.workspace, rel)
		info, err := os.Stat(src)
		if err == nil && info.Mode().IsRegular() && info.Size() <= maxFileBytes {
			rec.Existed = true
			dst := filepath.Join(m.filesDir(turn), rel)
			if err := copyFile(src, dst); err != nil {
				return nil, err
			}
		}
		snap.Files = append(snap.Files, rec)
	}

	snap.GitCommit = m.gitStashCreate()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(m.dir(turn), "snapshot.json"), data, 0o644); err != nil {
		return nil, err
	}
	return snap, nil
}

func (m *Manager) rel(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(m.workspace, abs)
	}
	rel, err := filepath.Rel(m.workspace, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside workspace")
	}
	return rel, nil
}

func (m *Manager) gitStashCreate() string {
	if _, err := os.Stat(filepath.Join(m.workspace, ".git")); err != nil {
		return ""
	}
	cmd := exec.Command("git", "stash", "create", "rakit checkpoint")
	cmd.Dir = m.workspace
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Load reads a snapshot for a turn.
func (m *Manager) Load(turn int) (*Snapshot, error) {
	data, err := os.ReadFile(filepath.Join(m.dir(turn), "snapshot.json"))
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// Last returns the highest-numbered snapshot.
func (m *Manager) Last() (*Snapshot, error) {
	entries, err := os.ReadDir(m.root())
	if err != nil {
		return nil, err
	}
	var turns []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.Atoi(e.Name())
		if err == nil {
			turns = append(turns, n)
		}
	}
	if len(turns) == 0 {
		return nil, fmt.Errorf("no checkpoints")
	}
	sort.Ints(turns)
	return m.Load(turns[len(turns)-1])
}

// Restore writes snapshot contents back into the workspace. Files that did not
// exist at capture time are removed.
func (m *Manager) Restore(snap *Snapshot) error {
	if snap == nil {
		return fmt.Errorf("nil snapshot")
	}
	for _, rec := range snap.Files {
		dst := filepath.Join(m.workspace, rec.Path)
		if !rec.Existed {
			_ = os.Remove(dst)
			continue
		}
		src := filepath.Join(m.filesDir(snap.Turn), rec.Path)
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// RestoreLast restores the most recent checkpoint and returns it.
func (m *Manager) RestoreLast() (*Snapshot, error) {
	snap, err := m.Last()
	if err != nil {
		return nil, err
	}
	return snap, m.Restore(snap)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
