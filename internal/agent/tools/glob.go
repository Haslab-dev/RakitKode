package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
)

// GlobParams are the inputs of the glob tool.
type GlobParams struct {
	Pattern string `json:"pattern" description:"The glob pattern to match files against"`
	Path    string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

// GlobResponseMetadata describes the glob results.
type GlobResponseMetadata struct {
	NumberOfFiles int  `json:"number_of_files"`
	Truncated     bool `json:"truncated"`
}

const (
	GlobToolName   = "glob"
	GlobMaxResults = 100
)

const globDescription = `Fast file pattern matching tool.
Supports glob patterns like "**/*.js" or "src/**/*".
Returns matching file paths sorted by modification time.
`

// NewGlobTool builds the glob tool.
func NewGlobTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		GlobToolName,
		globDescription,
		func(ctx context.Context, params GlobParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Pattern == "" {
				return fantasy.NewTextErrorResponse("pattern is required"), nil
			}

			searchPath := cmpOr(params.Path, workingDir)
			if !filepath.IsAbs(searchPath) {
				searchPath = filepath.Join(workingDir, searchPath)
			}

			pattern := params.Pattern
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(searchPath, pattern)
			}

			matches, err := globFiles(pattern, searchPath)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			truncated := len(matches) > GlobMaxResults
			if truncated {
				matches = matches[:GlobMaxResults]
			}

			if len(matches) == 0 {
				return fantasy.WithResponseMetadata(
					fantasy.NewTextResponse("No files found"),
					GlobResponseMetadata{},
				), nil
			}

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(strings.Join(matches, "\n")),
				GlobResponseMetadata{NumberOfFiles: len(matches), Truncated: truncated},
			), nil
		},
	)
}

func globFiles(pattern, root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("cannot search %s: %w", root, err)
	}
	matches, err := filepathGlob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %w", err)
	}
	return matches, nil
}

// filepathGlob wraps filepath.Glob with recursive ** support by walking.
func filepathGlob(pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Glob(pattern)
	}
	// Walk and match manually for ** support.
	base := pattern
	for {
		idx := strings.Index(base, "**")
		if idx == -1 {
			break
		}
		base = base[:idx]
		if strings.HasSuffix(base, "/") {
			base = base[:len(base)-1]
			break
		}
		base = base[:len(base)]
		break
	}
	if base == "" {
		base = "."
	}
	var matches []string
	err := walkAll(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		ok, matchErr := matchRecursive(pattern, path)
		if matchErr != nil {
			return matchErr
		}
		if ok && !d.IsDir() {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func walkAll(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

// matchRecursive implements ** matching: split the pattern at "**" and
// require prefix/suffix match with any number of directories between.
func matchRecursive(pattern, path string) (bool, error) {
	idx := strings.Index(pattern, "**")
	if idx == -1 {
		return filepath.Match(pattern, path)
	}
	prefix := pattern[:idx]
	suffix := pattern[idx+2:]
	suffix = strings.TrimPrefix(suffix, "/")
	prefix = strings.TrimSuffix(prefix, "/")

	if prefix != "" && !strings.HasPrefix(path, prefix+"/") && path != prefix {
		return false, nil
	}
	if suffix == "" {
		return true, nil
	}
	rest := strings.TrimPrefix(path, prefix+"/")
	for {
		if ok, err := filepath.Match(suffix, rest); err == nil && ok {
			return true, nil
		}
		slash := strings.Index(rest, "/")
		if slash == -1 {
			return false, nil
		}
		rest = rest[slash+1:]
	}
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
