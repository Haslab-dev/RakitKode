package tools

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fantasy"

	"github.com/Haslab-dev/RakitKode/internal/permission"
)

// LSParams are the inputs of the ls tool.
type LSParams struct {
	Path string `json:"path,omitempty" description:"The directory to list (defaults to working directory)"`
}

// LSResponseMetadata describes the listing.
type LSResponseMetadata struct {
	NumberOfFiles int  `json:"number_of_files"`
	Truncated     bool `json:"truncated"`
}

const (
	LSToolName = "ls"
	MaxLSRows  = 500
)

const lsDescription = `List files and directories in a given path.
Entries are sorted with directories first. Hidden files are included.
Output is limited to 500 entries.
`

// NewLsTool builds the ls tool.
func NewLsTool(permissions permission.Service, workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		LSToolName,
		lsDescription,
		func(ctx context.Context, params LSParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			searchPath := cmp.Or(params.Path, workingDir)
			if !filepath.IsAbs(searchPath) {
				searchPath = filepath.Join(workingDir, searchPath)
			}

			absWorkingDir, err := filepath.Abs(workingDir)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving working directory: %w", err)
			}
			absPath, err := filepath.Abs(searchPath)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error resolving path: %w", err)
			}
			if !strings.HasPrefix(absPath, absWorkingDir+string(filepath.Separator)) && absPath != absWorkingDir {
				p, err := permissions.Request(ctx, permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        absPath,
					ToolCallID:  call.ID,
					ToolName:    LSToolName,
					Action:      "list",
					Description: fmt.Sprintf("List outside working directory: %s", absPath),
					Params:      params,
				})
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !p {
					return NewPermissionDeniedResponse(), nil
				}
			}

			entries, err := os.ReadDir(absPath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Cannot list %s: %v", absPath, err)), nil
			}

			slices.SortFunc(entries, func(a, b os.DirEntry) int {
				if a.IsDir() != b.IsDir() {
					if a.IsDir() {
						return -1
					}
					return 1
				}
				return strings.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
			})

			var out strings.Builder
			truncated := false
			shown := 0
			dirs, files := 0, 0
			for _, entry := range entries {
				if shown >= MaxLSRows {
					truncated = true
					break
				}
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
					dirs++
				} else {
					files++
				}
				out.WriteString(name + "\n")
				shown++
			}
			fmt.Fprintf(&out, "\n%d directories, %d files", dirs, files)
			if truncated {
				out.WriteString(" (listing truncated)")
			}

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(out.String()),
				LSResponseMetadata{NumberOfFiles: len(entries), Truncated: truncated},
			), nil
		},
	)
}
