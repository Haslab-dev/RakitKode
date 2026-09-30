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

// EditParams are the inputs of the edit tool.
type EditParams struct {
	FilePath   string `json:"file_path" description:"The path to the file to modify"`
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)"`
}

// EditPermissionsParams are the params passed to permission prompts.
type EditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

// EditResponseMetadata carries the diff of the edit.
type EditResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

const EditToolName = "edit"

const editDescription = `Perform exact string matching replacement in a file.
You must perform a full read of the file in this conversation before editing.
The edit will FAIL if OldString is not unique unless ReplaceAll is set.
`

// NewEditTool builds the edit tool.
func NewEditTool(
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		EditToolName,
		editDescription,
		func(ctx context.Context, params EditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			if params.OldString == "" {
				return fantasy.NewTextErrorResponse("old_string is required; use the write tool to create files"), nil
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
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("File not found: %s", filePath)), nil
			}
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

			raw, err := os.ReadFile(filePath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to read file: %w", err)
			}
			oldContent := string(raw)

			newContent, replaced, errResp := applyEdit(oldContent, params.OldString, params.NewString, params.ReplaceAll)
			if errResp.Content != "" {
				return errResp, nil
			}
			if !replaced {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("old_string %q does not appear in %s", params.OldString, filePath)), nil
			}

			p, err := permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        filePath,
					ToolCallID:  call.ID,
					ToolName:    EditToolName,
					Action:      "edit",
					Description: fmt.Sprintf("Edit %s", filePath),
					Params:      EditPermissionsParams{FilePath: filePath, OldContent: oldContent, NewContent: newContent},
				},
			)
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				return NewPermissionDeniedResponse(), nil
			}

			if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
			}

			commitFileChange(ctx, files, filetracker, sessionID, filePath, oldContent, newContent)

			d := diff.Generate(oldContent, newContent)
			additions, removals := diff.CountChanges(d)
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(fmt.Sprintf("File edited successfully: %s", filePath)),
				EditResponseMetadata{Diff: d, Additions: additions, Removals: removals},
			), nil
		},
	)
}

// applyEdit performs the replacement, enforcing uniqueness and the
// replace_all semantics. A response with non-empty Content signals an error.
func applyEdit(content, old, new string, replaceAll bool) (string, bool, fantasy.ToolResponse) {
	if old == new {
		return content, false, fantasy.NewTextErrorResponse(
			"old_string and new_string are identical; no-op edits are not allowed")
	}
	count := strings.Count(content, old)
	if count == 0 {
		return content, false, fantasy.ToolResponse{}
	}
	if count > 1 && !replaceAll {
		return content, false, fantasy.NewTextErrorResponse(fmt.Sprintf(
			"old_string appears %d times in the file; make it unique or set replace_all=true", count))
	}
	if replaceAll {
		return strings.ReplaceAll(content, old, new), true, fantasy.ToolResponse{}
	}
	return strings.Replace(content, old, new, 1), true, fantasy.ToolResponse{}
}

// commitFileChange records history versions and refreshes read state.
func commitFileChange(
	ctx context.Context,
	files history.Service,
	filetracker filetracker.Service,
	sessionID, filePath, oldContent, newContent string,
) {
	if oldContent != "" {
		if _, err := files.GetByPathAndSession(ctx, filePath, sessionID); err == nil {
			_, _ = files.CreateVersion(ctx, sessionID, filePath, oldContent)
		} else {
			_, _ = files.Create(ctx, sessionID, filePath, oldContent)
		}
	}
	filetracker.RecordRead(ctx, sessionID, filePath)
}
