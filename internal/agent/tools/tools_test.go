package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/Haslab-dev/RakitKode/internal/filetracker"
	"github.com/Haslab-dev/RakitKode/internal/permission"
)

// denyService denies every request immediately.
type denyService struct{ permission.Service }

func (d denyService) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
	return false, nil
}

// fantasyCall builds a fantasy ToolCall from a raw JSON input.
func fantasyCall(input string) fantasy.ToolCall {
	return fantasy.ToolCall{ID: "test-call", Name: "test", Input: input}
}

// testContext injects a session ID like the agent loop does.
func testContext() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "test-session")
}

// TestWriteDeniedPermission verifies a denied permission stops the write
// and the file is not created.
func TestWriteDeniedPermission(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "denied.txt")

	perms := denyService{permission.NewPermissionService(dir, true, nil)}
	tool := NewWriteTool(perms, nil, filetracker.NewService(nil), dir)

	resp, err := tool.Run(testContext(), fantasyCall(`{"file_path":"`+target+`","content":"x"}`))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected error response on denial, got: %s", resp.Content)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("file should not exist after denial, statErr=%v", statErr)
	}
}

// TestEditStaleRead verifies editing a file modified after the last
// recorded read fails with the staleness error.
func TestEditStaleRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "stale.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	ft := filetracker.NewService(nil)
	perms := permission.NewPermissionService(dir, true, nil)
	tool := NewEditTool(perms, nil, ft, dir)

	ctx := testContext()
	// Record a read, then move the file's mtime forward past it.
	ft.RecordRead(ctx, "test-session", target)
	later := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(target, later, later); err != nil {
		t.Fatal(err)
	}

	resp, err := tool.Run(ctx, fantasyCall(`{"file_path":"`+target+`","old_string":"original","new_string":"changed"}`))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected stale-read error, got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "has been modified since it was last read") {
		t.Fatalf("expected staleness message, got: %s", resp.Content)
	}
}

// TestBannedCommand verifies the bash tool rejects blocklisted binaries
// before any permission check.
func TestBannedCommand(t *testing.T) {
	t.Parallel()
	perms := denyService{permission.NewPermissionService(t.TempDir(), true, nil)}
	tool := NewBashTool(perms, t.TempDir())

	resp, err := tool.Run(testContext(), fantasyCall(`{"command":"curl http://example.com","description":"fetch"}`))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected error for banned command, got: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "not allowed") {
		t.Fatalf("expected banned-command message, got: %s", resp.Content)
	}
}
