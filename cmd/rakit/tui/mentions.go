package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const atMaxRows = 8

var atSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, ".next": true, ".cache": true, ".venv": true, "target": true,
	".idea": true, ".vscode": true, "bin": true, "obj": true,
}

// buildFileIndex lists workspace-relative files for @-mention completion.
func buildFileIndex(root string) []string {
	if root == "" {
		return nil
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if atSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		files = append(files, rel)
		if len(files) >= 5000 {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// updateAtMenu opens/closes the @-mention popup based on the current draft.
func (m *Model) updateAtMenu() {
	if m.eng == nil {
		m.atOpen = false
		return
	}
	value := m.textarea.Value()
	idx := strings.LastIndex(value, "@")
	if idx < 0 {
		m.atOpen = false
		return
	}
	if idx > 0 {
		switch value[idx-1] {
		case ' ', '\t', '\n', '(', '"', '\'':
		default:
			m.atOpen = false
			return
		}
	}
	query := value[idx+1:]
	if query == "" || strings.ContainsAny(query, " \t\n") {
		m.atOpen = false
		return
	}
	items := m.fileCandidates(query)
	if len(items) == 0 {
		m.atOpen = false
		return
	}
	if !m.atOpen || m.atStart != idx {
		m.atSelected = 0
	}
	m.atOpen = true
	m.atStart = idx
	m.atItems = items
	if m.atSelected >= len(items) {
		m.atSelected = 0
	}
}

func (m *Model) fileCandidates(query string) []string {
	if m.fileIndex == nil {
		m.fileIndex = buildFileIndex(m.eng.Workspace())
	}
	ql := strings.ToLower(query)
	var prefix, contains []string
	for _, f := range m.fileIndex {
		lf := strings.ToLower(f)
		if strings.HasPrefix(lf, ql) || strings.HasPrefix(filepath.Base(lf), ql) {
			prefix = append(prefix, f)
			if len(prefix) >= atMaxRows {
				break
			}
			continue
		}
		if len(prefix) == 0 && strings.Contains(lf, ql) {
			contains = append(contains, f)
			if len(contains) >= atMaxRows {
				break
			}
		}
	}
	if len(prefix) > 0 {
		return prefix
	}
	return contains
}

// completeAtMention inserts the selected file path.
func (m *Model) completeAtMention() {
	if !m.atOpen || m.atSelected < 0 || m.atSelected >= len(m.atItems) {
		m.atOpen = false
		return
	}
	value := m.textarea.Value()
	if m.atStart < 0 || m.atStart > len(value) {
		m.atOpen = false
		return
	}
	chosen := m.atItems[m.atSelected]
	m.textarea.SetValue(value[:m.atStart] + "@" + chosen + " ")
	m.atOpen = false
	m.atItems = nil
}

// renderAtMenu renders the @-mention candidate popup.
func (m Model) renderAtMenu() string {
	if !m.atOpen || len(m.atItems) == 0 {
		return ""
	}
	start := 0
	if m.atSelected >= atMaxRows {
		start = m.atSelected - atMaxRows + 1
	}
	end := start + atMaxRows
	if end > len(m.atItems) {
		end = len(m.atItems)
	}
	var rows []string
	for i := start; i < end; i++ {
		cursor, style := "  ", menuRowStyle
		if i == m.atSelected {
			cursor, style = "❯ ", menuRowSelectedStyle
		}
		rows = append(rows, style.Render(cursor+truncate(m.atItems[i], 64)))
	}
	hint := fmt.Sprintf("  ↑/↓ navigate · enter/tab insert · esc close  %d/%d", m.atSelected+1, len(m.atItems))
	rows = append(rows, statusStyle.Render(hint))
	return menuStyle.Render(strings.Join(rows, "\n"))
}

// mentionsInDraft resolves @tokens in the submitted value to existing files.
func (m *Model) mentionsInDraft(value string) []string {
	if m.eng == nil {
		return nil
	}
	root := m.eng.Workspace()
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.Fields(value) {
		if !strings.HasPrefix(field, "@") {
			continue
		}
		p := strings.Trim(field[1:], ".,;:!?)]}\"'")
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
