package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KnownRuleFilePaths lists the standard AI coding agent rule and instruction files
// (Claude Code, OpenCode, Cursor, ForgeADE, GitHub Copilot).
var KnownRuleFilePaths = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursorrules",
	".forge/rules.md",
	".github/copilot-instructions.md",
}

// WorkspaceRuleFile represents one detected instruction file.
type WorkspaceRuleFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
}

// DiscoverWorkspaceRules scans workspaceDir for standard project rule files and
// returns both the individual file records and a unified markdown prompt block.
func DiscoverWorkspaceRules(workspaceDir string) ([]WorkspaceRuleFile, string) {
	if workspaceDir == "" {
		return nil, ""
	}

	var rules []WorkspaceRuleFile
	var promptParts []string

	for _, relPath := range KnownRuleFilePaths {
		fullPath := filepath.Join(workspaceDir, relPath)
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			rules = append(rules, WorkspaceRuleFile{Path: relPath, Exists: false})
			continue
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			rules = append(rules, WorkspaceRuleFile{Path: relPath, Exists: false})
			continue
		}

		content := strings.TrimSpace(string(data))
		rules = append(rules, WorkspaceRuleFile{
			Path:    relPath,
			Content: content,
			Exists:  true,
		})

		if content != "" {
			promptParts = append(promptParts, fmt.Sprintf("### Project Instructions: %s\n%s", relPath, content))
		}
	}

	if len(promptParts) == 0 {
		return rules, ""
	}

	unified := "# Project Rules & Coding Instructions\n" + strings.Join(promptParts, "\n\n")
	return rules, unified
}
