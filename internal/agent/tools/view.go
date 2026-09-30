package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"charm.land/fantasy"

	"github.com/Haslab-dev/RakitKode/internal/filetracker"
	"github.com/Haslab-dev/RakitKode/internal/permission"
)

// ViewParams are the inputs of the view tool.
type ViewParams struct {
	FilePath string `json:"file_path" description:"The path to the file to read"`
	Offset   int    `json:"offset,omitempty" description:"The line number to start reading from (0-based)"`
	Limit    int    `json:"limit,omitempty" description:"The number of lines to read (defaults to 200)"`
}

// ViewResponseMetadata describes what was read.
type ViewResponseMetadata struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

const (
	ViewToolName     = "view"
	MaxViewSize      = 200 * 1024 // 200KB
	DefaultReadLimit = 200
	MaxLineLength    = 2000
)

const viewDescription = `Reads a file from the local filesystem.
You will often be asked to view a file to understand its contents before editing it.
The file_path can be absolute or relative to the working directory.
When reading, lines longer than 2000 characters are truncated.
You can read specific portions of files using offset and limit parameters.
`

// NewViewTool builds the view tool.
func NewViewTool(
	permissions permission.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ViewToolName,
		viewDescription,
		func(ctx context.Context, params ViewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
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

			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}
			absFilePath, err := filepath.Abs(filePath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving path: %w", err)
			}
			if !strings.HasPrefix(absFilePath, absWorkingDir+string(filepath.Separator)) && absFilePath != absWorkingDir {
				p, err := permissions.Request(ctx, permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        absFilePath,
					ToolCallID:  call.ID,
					ToolName:    ViewToolName,
					Action:      "read",
					Description: fmt.Sprintf("Read outside working directory: %s", absFilePath),
					Params:      params,
				})
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !p {
					return NewPermissionDeniedResponse(), nil
				}
			}

			fileInfo, err := os.Stat(filePath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("File not found: %s", filePath)), nil
			}
			if fileInfo.IsDir() {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Path is a directory, use the ls tool: %s", filePath)), nil
			}
			if fileInfo.Size() > MaxViewSize {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"File is too large (%d bytes). Max view size is %d bytes.",
					fileInfo.Size(), MaxViewSize)), nil
			}

			content, err := readLines(filePath, params.Offset, params.Limit)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			if utf8.ValidString(content) {
				filetracker.RecordRead(ctx, sessionID, absFilePath)
			}

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(content),
				ViewResponseMetadata{FilePath: absFilePath, Content: content},
			), nil
		},
	)
}

// readLines extracts [offset, offset+limit) lines, truncating long lines.
func readLines(filePath string, offset, limit int) (string, error) {
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	defer file.Close()

	var out strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxLineLength*8)
	line := 0
	end := offset + limit
	for scanner.Scan() {
		if line >= offset && line < end {
			text := scanner.Text()
			if len(text) > MaxLineLength {
				text = text[:MaxLineLength] + "..."
			}
			fmt.Fprintf(&out, "%6d\t%s\n", line+1, text)
		}
		line++
		if line >= end {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return out.String(), nil
		}
		return "", fmt.Errorf("failed to scan file: %w", err)
	}
	if offset >= line && line > 0 {
		return "", fmt.Errorf("offset %d is beyond end of file (%d lines)", offset, line)
	}
	return out.String(), nil
}
