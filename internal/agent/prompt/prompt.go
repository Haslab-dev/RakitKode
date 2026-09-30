// Package prompt builds system prompts from templates with runtime data.
package prompt

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/Haslab-dev/RakitKode/internal/config"
	"github.com/Haslab-dev/RakitKode/internal/skills"
)

// ContextFile is one project/user context file injected into the prompt.
type ContextFile struct {
	Path    string
	Content string
}

// PromptData is the template data for the system prompt.
type PromptData struct {
	WorkingDir         string
	IsGitRepo          bool
	Platform           string
	Date               string
	AvailSkillXML      string
	ContextFiles       []ContextFile
	GlobalContextFiles []ContextFile
}

// defaultContextPaths mirrors crush's defaults.
var defaultContextPaths = []string{
	"AGENTS.md", "RAKITKODE.md", "CLAUDE.md", "GEMINI.md", ".cursorrules",
}

var defaultGlobalContextPaths = []string{}

// Build renders the named template with gathered runtime data.
func Build(name, tmpl string, workingDir string, cfg *config.Config) (string, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	data, err := gather(workingDir, cfg)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := t.Execute(&out, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return out.String(), nil
}

func gather(workingDir string, cfg *config.Config) (PromptData, error) {
	data := PromptData{
		WorkingDir: filepath.ToSlash(workingDir),
		IsGitRepo:  isGitRepo(workingDir),
		Platform:   runtime.GOOS,
		Date:       time.Now().Format("1/2/2006"),
	}

	contextPaths := defaultContextPaths
	globalPaths := defaultGlobalContextPaths
	skillsPaths := []string{filepath.Join(workingDir, ".rakitkode", "skills")}
	if cfg.Options != nil {
		if len(cfg.Options.ContextPaths) > 0 {
			contextPaths = cfg.Options.ContextPaths
		}
		if len(cfg.Options.GlobalContextPaths) > 0 {
			globalPaths = cfg.Options.GlobalContextPaths
		}
		if len(cfg.Options.SkillsPaths) > 0 {
			skillsPaths = cfg.Options.SkillsPaths
		}
	}

	data.ContextFiles = loadContextFiles(contextPaths, workingDir)
	data.GlobalContextFiles = loadContextFiles(globalPaths, homeDir())

	// Skills metadata for the prompt.
	found := skills.Discover(expandPaths(skillsPaths, workingDir))
	if len(found) > 0 {
		data.AvailSkillXML = skills.ToPromptXML(found)
	}
	return data, nil
}

// Skill aliases the skills package type for callers.
type Skill = skills.Skill

func loadContextFiles(paths []string, base string) []ContextFile {
	var files []ContextFile
	seen := map[string]bool{}
	for _, p := range paths {
		expanded := expandPath(p, base)
		if expanded == "" || seen[strings.ToLower(expanded)] {
			continue
		}
		seen[strings.ToLower(expanded)] = true
		content, err := os.ReadFile(expanded)
		if err != nil {
			continue
		}
		files = append(files, ContextFile{Path: expanded, Content: string(content)})
	}
	return files
}

func expandPath(p, base string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return p
}

func expandPaths(paths []string, base string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, expandPath(p, base))
	}
	return out
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
