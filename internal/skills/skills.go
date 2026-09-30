// Package skills implements the Agent Skills open standard
// (https://agentskills.io): SKILL.md discovery, parsing, and prompt
// injection.
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	// SkillFileName is the canonical skill descriptor file.
	SkillFileName = "SKILL.md"
	// MaxNameLength bounds the skill name.
	MaxNameLength = 64
	// MaxDescriptionLength bounds the skill description.
	MaxDescriptionLength = 1024
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

// Skill represents a parsed SKILL.md file.
type Skill struct {
	Name                   string            `yaml:"name" json:"name"`
	Description            string            `yaml:"description" json:"description"`
	UserInvocable          bool              `yaml:"user-invocable" json:"user_invocable"`
	DisableModelInvocation bool              `yaml:"disable-model-invocation" json:"disable_model_invocation"`
	License                string            `yaml:"license,omitempty" json:"license,omitempty"`
	Compatibility          string            `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Metadata               map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Instructions           string            `yaml:"-" json:"instructions"`
	Path                   string            `yaml:"-" json:"path"`
	SkillFilePath          string            `yaml:"-" json:"skill_file_path"`
	Builtin                bool              `yaml:"-" json:"builtin"`
}

// Validate checks the parsed frontmatter against the spec.
func (s *Skill) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(s.Name) > MaxNameLength {
		return fmt.Errorf("name must be at most %d characters", MaxNameLength)
	}
	if !namePattern.MatchString(s.Name) {
		return fmt.Errorf("name must be lowercase-ish alphanumeric segments separated by hyphens")
	}
	if s.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(s.Description) > MaxDescriptionLength {
		return fmt.Errorf("description must be at most %d characters", MaxDescriptionLength)
	}
	return nil
}

// Parse reads and parses a SKILL.md file.
func Parse(path string) (*Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	skill, err := ParseContent(content)
	if err != nil {
		return nil, err
	}
	skill.Path = filepath.Dir(path)
	skill.SkillFilePath = path
	return skill, nil
}

// ParseContent splits frontmatter from the body and decodes the YAML.
func ParseContent(content []byte) (*Skill, error) {
	frontmatter, body, err := splitFrontmatter(string(content))
	if err != nil {
		return nil, err
	}
	var skill Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &skill); err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	skill.Instructions = strings.TrimSpace(body)
	return &skill, nil
}

// splitFrontmatter extracts --- delimited YAML frontmatter.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---") {
		return "", content, fmt.Errorf("SKILL.md must start with YAML frontmatter (---)")
	}
	rest := strings.TrimPrefix(content, "---")
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return "", content, fmt.Errorf("frontmatter not closed (expected ---)")
	}
	frontmatter = strings.TrimSpace(rest[:idx])
	body = rest[idx+4:]
	body = strings.TrimPrefix(body, "\n")
	return frontmatter, body, nil
}

// Discover finds all valid skills in the given paths.
func Discover(paths []string) []*Skill {
	var skillsMu sync.Mutex
	var skills []*Skill
	seen := make(map[string]bool)
	var seenMu sync.Mutex

	for _, base := range paths {
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() || d.Name() != SkillFileName {
				return nil
			}
			seenMu.Lock()
			if seen[path] {
				seenMu.Unlock()
				return nil
			}
			seen[path] = true
			seenMu.Unlock()

			skill, err := Parse(path)
			if err != nil {
				return nil
			}
			if err := skill.Validate(); err != nil {
				return nil
			}
			skillsMu.Lock()
			skills = append(skills, skill)
			skillsMu.Unlock()
			return nil
		})
	}

	slices.SortStableFunc(skills, func(a, b *Skill) int {
		if c := strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return skills
}

// Deduplicate removes skills with the same name, keeping the first
// occurrence (call with user skills before builtins to let users
// override).
func Deduplicate(all []*Skill) []*Skill {
	seen := make(map[string]bool, len(all))
	out := make([]*Skill, 0, len(all))
	for _, s := range all {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out
}

// Filter drops disabled skills by name.
func Filter(all []*Skill, disabled []string) []*Skill {
	if len(disabled) == 0 {
		return all
	}
	out := make([]*Skill, 0, len(all))
	for _, s := range all {
		if slices.Contains(disabled, s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// escape escapes XML special characters in skill metadata.
func escape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// ToPromptXML generates the <available_skills> block injected into the
// system prompt. Skills with disable-model-invocation are excluded.
func ToPromptXML(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		sb.WriteString("  <skill>\n")
		fmt.Fprintf(&sb, "    <name>%s</name>\n", escape(s.Name))
		fmt.Fprintf(&sb, "    <description>%s</description>\n", escape(s.Description))
		fmt.Fprintf(&sb, "    <location>%s</location>\n", escape(s.SkillFilePath))
		if s.Builtin {
			sb.WriteString("    <type>builtin</type>\n")
		}
		sb.WriteString("  </skill>\n")
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}

// ReadSkillContent loads the full SKILL.md body for a skill.
func ReadSkillContent(s *Skill) (string, error) {
	content, err := os.ReadFile(s.SkillFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read skill file: %w", err)
	}
	_, body, err := splitFrontmatter(string(content))
	if err != nil {
		return string(content), nil
	}
	return body, nil
}
