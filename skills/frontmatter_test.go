package skills

import (
	"strings"
	"testing"
)

func TestParseSkillFrontmatter(t *testing.T) {
	content := `---
name: code-review
description: Review diffs for correctness
when_to_use: reviewing a pull request or diff
allowed-tools: [read, search, bash]
version: 1.2.0
---
# Review

Run the checks.`
	s := parseSkill("dir", "/tmp/skills/code-review/SKILL.md", content, ScopeGlobal)
	if s.Name != "code-review" || s.WhenToUse != "reviewing a pull request or diff" {
		t.Fatalf("bad frontmatter: %+v", s)
	}
	if s.Version != "1.2.0" || len(s.AllowedTools) != 3 {
		t.Fatalf("allowed tools/version: %+v", s)
	}
	if !strings.Contains(s.Body, "# Review") {
		t.Fatalf("body = %q", s.Body)
	}
}

func TestInvocationExpandsSkillDir(t *testing.T) {
	s := Skill{
		Name: "x",
		Path: "/tmp/skills/x/SKILL.md",
		Body: "run ${SKILL_DIR}/scripts/check.sh",
	}
	out := s.InvocationMessage("")
	if !strings.Contains(out, "/tmp/skills/x/scripts/check.sh") {
		t.Fatalf("SKILL_DIR not expanded: %q", out)
	}
}
