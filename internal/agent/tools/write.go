package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/Haslab-dev/RakitKode/internal/diff"
	"github.com/Haslab-dev/RakitKode/internal/filetracker"
	"github.com/Haslab-dev/RakitKode/internal/history"
	"github.com/Haslab-dev/RakitKode/internal/permission"
)

// WriteParams are the inputs of the write tool.
type WriteParams struct {
	FilePath string `json:"file_path" description:"The path to the file to write"`
	Content  string `json:"content" description:"The content to write to the file"`
}

// WritePermissionsParams are the params passed to permission prompts.
type WritePermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

// WriteResponseMetadata carries the diff of the write.
type WriteResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

const WriteToolName = "write"

const writeDescription = `Write a file to the local filesystem.
Overwrites the existing file if it exists and creates a new one otherwise.
If the file exists you MUST read it first (view tool) or the write fails.
`

// NewWriteTool builds the write tool.
func NewWriteTool(
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		WriteToolName,
		writeDescription,
		func(ctx context.Context, params WriteParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session_id is required")
			}

			filePath := params.FilePath
			if !filepath.IsAbs(filePath) {
				filePath = filepath.Join(workingDir, filePath)
			}

			fileInfo, err := os.Stat(filePath)
			if err == nil {
				if fileInfo.IsDir() {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
				}

				modTime := fileInfo.ModTime().Truncate(time.Second)
				lastRead := filetracker.LastReadTime(ctx, sessionID, filePath)
				if modTime.After(lastRead) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf(
						"File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
						filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339))), nil
				}
			}

			var oldContent string
			if content, err := os.ReadFile(filePath); err == nil {
				oldContent = string(content)
			}

			p, err := permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        filePath,
					ToolCallID:  call.ID,
					ToolName:    WriteToolName,
					Action:      "write",
					Description: fmt.Sprintf("Write %s", filePath),
					Params:      WritePermissionsParams{FilePath: filePath, NewContent: params.Content},
				},
			)
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				return NewPermissionDeniedResponse(), nil
			}

			if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to create parent directory: %w", err)
			}
			if err := os.WriteFile(filePath, []byte(params.Content), 0o644); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
			}

			// Track file history: first version keeps the old content so
			// the session can restore it.
			if oldContent != "" {
				_, _ = files.Create(ctx, sessionID, filePath, oldContent)
			}
			filetracker.RecordRead(ctx, sessionID, filePath)

			d := diff.Generate(oldContent, params.Content)
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(fmt.Sprintf("File written successfully: %s", filePath)),
				WriteResponseMetadata{
					Diff:      d,
					Additions: strings.Count(d, "\n+"),
					Removals:  strings.Count(d, "\n-"),
				},
			), nil
		},
	)
}
