package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/mcp"
)

func setupTestEngineWithMCPAndSkills(t *testing.T) (*engine.Engine, string) {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	_ = exec.Command("git", "init", ws).Run()

	// 1. Setup mock MCP server in home/mcp_servers.json
	mockServerScript := filepath.Join(ws, "mock_mcp.py")
	scriptContent, err := os.ReadFile(filepath.Join("..", "..", "..", "tools", "mock_mcp_server.py"))
	if err != nil {
		t.Fatalf("failed to read mock_mcp_server.py: %v", err)
	}
	if err := os.WriteFile(mockServerScript, scriptContent, 0755); err != nil {
		t.Fatalf("failed to write mockServerScript: %v", err)
	}

	mcpCfg := []mcp.ServerConfig{
		{
			Name:    "mock-mcp",
			Command: "python3",
			Args:    []string{mockServerScript},
			Enabled: true,
			Type:    "stdio",
		},
	}
	mcpMgr := mcp.NewManager(home)
	for _, s := range mcpCfg {
		if _, err := mcpMgr.SaveServer(s); err != nil {
			t.Fatalf("failed to save mock mcp server: %v", err)
		}
	}

	// 2. Setup mock skills in workspace/.agents/skills/mock-code-audit
	wsSkillDir := filepath.Join(ws, ".agents", "skills", "mock-code-audit")
	if err := os.MkdirAll(filepath.Join(wsSkillDir, "scripts"), 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	skillContent := `---
name: mock-code-audit
description: Security and code quality audit playbook for Go and Python
when_to_use: When reviewing code quality, security vulnerabilities, or concurrency safety
---
# Mock Code Audit Playbook
Audit steps...`
	if err := os.WriteFile(filepath.Join(wsSkillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsSkillDir, "scripts", "audit.sh"), []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatalf("failed to write audit.sh: %v", err)
	}

	// 3. Initialize engine with ConnectMCP: true
	e, err := engine.New(engine.Options{
		Home:       home,
		Workspace:  ws,
		ConnectMCP: true,
	})
	if err != nil {
		t.Fatalf("failed to construct engine: %v", err)
	}

	sess, err := e.Manager().CreateSession("test-feature-session", agent.RoleCoding, ws)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	return e, sess.ID
}

func TestSlashFeaturesComplete(t *testing.T) {
	e, sessID := setupTestEngineWithMCPAndSkills(t)
	m := resize(New(e, sessID), 120, 35)

	// 1. Test /help
	lines, _ := m.handleSlashInput("/help")
	helpText := stripAnsi(strings.Join(lines, "\n"))
	for _, cmdName := range []string{"/help", "/mcp", "/skills", "/tools", "/context", "/cost", "/diff", "/mode", "/yolo", "/new"} {
		if !strings.Contains(helpText, cmdName) {
			t.Errorf("/help missing command %s in output:\n%s", cmdName, helpText)
		}
	}

	// 2. Test /mcp table
	lines, _ = m.handleSlashInput("/mcp")
	mcpText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(mcpText, "mock-mcp") {
		t.Fatalf("/mcp table missing mock-mcp server: %s", mcpText)
	}
	if !strings.Contains(mcpText, "3 tools") {
		t.Fatalf("/mcp table missing 3 tools count: %s", mcpText)
	}

	// 3. Test /mcp off mock-mcp and /mcp on mock-mcp
	lines, _ = m.handleSlashInput("/mcp off mock-mcp")
	offText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(offText, "disabled") && !strings.Contains(offText, "mock-mcp") {
		t.Fatalf("/mcp off failed: %s", offText)
	}

	lines, _ = m.handleSlashInput("/mcp on mock-mcp")
	onText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(onText, "enabled") && !strings.Contains(onText, "mock-mcp") {
		t.Fatalf("/mcp on failed: %s", onText)
	}

	// 4. Test /skills list
	lines, _ = m.handleSlashInput("/skills")
	skillsText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(skillsText, "mock-code-audit") {
		t.Fatalf("/skills missing mock-code-audit: %s", skillsText)
	}

	// 5. Test /skills <name> detail
	lines, _ = m.handleSlashInput("/skills mock-code-audit")
	detailText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(detailText, "mock-code-audit") || !strings.Contains(detailText, "audit.sh") {
		t.Fatalf("/skills mock-code-audit missing detail/script: %s", detailText)
	}

	// 6. Test /tools (verifying both core tools and mock-mcp tools)
	lines, _ = m.handleSlashInput("/tools")
	toolsText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(toolsText, "core") {
		t.Fatalf("/tools missing core section: %s", toolsText)
	}
	if !strings.Contains(toolsText, "mock-mcp") {
		t.Fatalf("/tools missing mock-mcp server section: %s", toolsText)
	}
	if !strings.Contains(toolsText, "mock_calculator") || !strings.Contains(toolsText, "mock_echo") {
		t.Fatalf("/tools missing mock tools: %s", toolsText)
	}

	// 7. Test /status
	lines, _ = m.handleSlashInput("/status")
	statusText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(statusText, "status") || !strings.Contains(statusText, "mcp") {
		t.Fatalf("/status missing sections: %s", statusText)
	}

	// 8. Test /context
	lines, _ = m.handleSlashInput("/context")
	ctxText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(ctxText, "context window") || !strings.Contains(ctxText, "PROMPT CACHE PREFIX") {
		t.Fatalf("/context missing sections: %s", ctxText)
	}

	// 9. Test /cost
	lines, _ = m.handleSlashInput("/cost")
	costText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(costText, "cost & tokens") {
		t.Fatalf("/cost missing header: %s", costText)
	}

	// 10. Test /diff
	lines, _ = m.handleSlashInput("/diff")
	diffText := stripAnsi(strings.Join(lines, "\n"))
	if len(lines) == 0 {
		t.Fatalf("/diff returned empty lines")
	}
	if !strings.Contains(diffText, "workspace changes") && !strings.Contains(diffText, "clean") {
		t.Fatalf("/diff unexpected output: %s", diffText)
	}

	// 11. Test /mode viewing and switching
	lines, _ = m.handleSlashInput("/mode")
	if !strings.Contains(stripAnsi(strings.Join(lines, "\n")), "permission mode: build") {
		t.Fatalf("/mode unexpected default: %v", lines)
	}

	for _, nextMode := range []string{"plan", "edit", "yolo", "build"} {
		lines, _ = m.handleSlashInput("/mode " + nextMode)
		if !strings.Contains(stripAnsi(strings.Join(lines, "\n")), "permission mode: "+nextMode) {
			t.Fatalf("/mode %s failed: %v", nextMode, lines)
		}
		if got := e.PermissionModeFor(sessID); got != nextMode {
			t.Fatalf("engine permission mode mismatch: got %q want %q", got, nextMode)
		}
	}

	// 12. Test /yolo toggle
	lines, _ = m.handleSlashInput("/yolo")
	if !strings.Contains(stripAnsi(strings.Join(lines, "\n")), "yolo on") {
		t.Fatalf("/yolo toggle on failed: %v", lines)
	}
	lines, _ = m.handleSlashInput("/yolo")
	if !strings.Contains(stripAnsi(strings.Join(lines, "\n")), "yolo off") {
		t.Fatalf("/yolo toggle off failed: %v", lines)
	}

	// 13. Test /effort
	lines, _ = m.handleSlashInput("/effort medium")
	if !strings.Contains(stripAnsi(strings.Join(lines, "\n")), "reasoning effort → medium") {
		t.Fatalf("/effort medium failed: %v", lines)
	}
	lines, _ = m.handleSlashInput("/effort off")
	if !strings.Contains(stripAnsi(strings.Join(lines, "\n")), "reasoning effort off") {
		t.Fatalf("/effort off failed: %v", lines)
	}

	// 14. Test /tasks
	lines, _ = m.handleSlashInput("/tasks")
	tasksText := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(tasksText, "tasks") {
		t.Fatalf("/tasks missing header: %s", tasksText)
	}

	// 15. Test /new
	lines, _ = m.handleSlashInput("/new")
	if len(lines) != 0 {
		t.Fatalf("/new returned unexpected output lines: %v", lines)
	}
	if len(m.transcript) != 1 || m.transcript[0].kind != blockLanding {
		t.Fatalf("after /new transcript not reset to landing screen: %v", m.transcript)
	}

	// 16. Test direct MCP Tool Execution through tool registry
	callRes, err := e.Tools().Execute(context.Background(), "mock_calculator", `{"expression": "15 + 27"}`)
	if err != nil {
		t.Fatalf("failed calling mock_calculator MCP tool: %v", err)
	}
	callStr := fmt.Sprintf("%v", callRes)
	if !strings.Contains(callStr, "42") {
		t.Fatalf("mock_calculator returned unexpected result: %s", callStr)
	}
}
