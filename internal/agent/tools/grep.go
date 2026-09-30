package tools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"charm.land/fantasy"
)

// GrepParams are the inputs of the grep tool.
type GrepParams struct {
	Pattern string `json:"pattern" description:"The regular expression pattern to search for"`
	Path    string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
	Include string `json:"include,omitempty" description:"File pattern to include in the search (e.g. *.go)"`
}

// GrepMatch is one search hit.
type GrepMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Char    int    `json:"char"`
	Text    string `json:"text"`
	ModTime int64  `json:"-"`
}

// GrepResponseMetadata describes the search outcome.
type GrepResponseMetadata struct {
	Mode      string `json:"mode"`
	Matches   int    `json:"matches"`
	Truncated bool   `json:"truncated"`
}

const (
	GrepToolName = "grep"
	GrepMaxHits  = 100
)

const grepDescription = `A powerful search tool built on ripgrep (falls back to Go regexp over files).
Searches file contents using regular expressions.
Pattern is case-sensitive; use inline (?i) for case-insensitive matching.
Results are sorted by file modification time, capped at 100 matches.
`

var getRg = sync.OnceValue(func() string {
	path, err := exec.LookPath("rg")
	if err != nil {
		return ""
	}
	return path
})

// NewGrepTool builds the grep tool.
func NewGrepTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		GrepToolName,
		grepDescription,
		func(ctx context.Context, params GrepParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Pattern == "" {
				return fantasy.NewTextErrorResponse("pattern is required"), nil
			}

			searchPath := cmp.Or(params.Path, workingDir)
			if !filepath.IsAbs(searchPath) {
				searchPath = filepath.Join(workingDir, searchPath)
			}
			if _, err := os.Stat(searchPath); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Cannot search %s: %v", searchPath, err)), nil
			}

			var matches []GrepMatch
			var truncated bool
			var mode string
			if rg := getRg(); rg != "" {
				matches = searchWithRipgrep(ctx, rg, params.Pattern, searchPath, params.Include)
				mode = "ripgrep"
			} else {
				var err error
				matches, err = searchWithRegex(params.Pattern, searchPath, params.Include)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("Invalid regex: %v", err)), nil
				}
				mode = "regexp"
			}

			sort.SliceStable(matches, func(i, j int) bool {
				return matches[i].ModTime > matches[j].ModTime
			})
			truncated = len(matches) > GrepMaxHits
			if truncated {
				matches = matches[:GrepMaxHits]
			}

			if len(matches) == 0 {
				return fantasy.WithResponseMetadata(
					fantasy.NewTextResponse("No matches found"),
					GrepResponseMetadata{Mode: mode},
				), nil
			}

			var out strings.Builder
			for _, m := range matches {
				fmt.Fprintf(&out, "%s:%d:%s\n", m.Path, m.Line, m.Text)
			}
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(out.String()),
				GrepResponseMetadata{Mode: mode, Matches: len(matches), Truncated: truncated},
			), nil
		},
	)
}

type ripgrepMatch struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
		Lines      struct {
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"data"`
}

func searchWithRipgrep(ctx context.Context, rg, pattern, path, include string) []GrepMatch {
	args := []string{"--json", "-H", "-n", "-0", pattern}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, path)

	output, err := exec.CommandContext(ctx, rg, args...).Output()
	if err != nil {
		return nil
	}
	var matches []GrepMatch
	for line := range bytes.SplitSeq(bytes.TrimSpace(output), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var m ripgrepMatch
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m.Type != "match" {
			continue
		}
		fi, err := os.Stat(m.Data.Path.Text)
		if err != nil {
			continue
		}
		matches = append(matches, GrepMatch{
			Path:    m.Data.Path.Text,
			Line:    m.Data.LineNumber,
			Text:    strings.TrimSpace(m.Data.Lines.Text),
			ModTime: fi.ModTime().UnixMilli(),
		})
	}
	return matches
}

func searchWithRegex(pattern, rootPath, include string) ([]GrepMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	var matches []GrepMatch
	err = filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name != "." && strings.HasPrefix(name, ".") && path != rootPath {
				return filepath.SkipDir
			}
			return nil
		}
		if include != "" {
			ok, matchErr := matchRecursive("**/"+include, path)
			if matchErr != nil || !ok {
				return nil
			}
		}
		if info.Size() > 2*1024*1024 {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, loc := range re.FindAllIndex(data, -1) {
			lineNum := 1 + bytes.Count(data[:loc[0]], []byte{'\n'})
			lineStart := bytes.LastIndexByte(data[:loc[0]], '\n') + 1
			lineEnd := bytes.IndexByte(data[loc[0]:], '\n')
			if lineEnd == -1 {
				lineEnd = len(data)
			} else {
				lineEnd += loc[0]
			}
			matches = append(matches, GrepMatch{
				Path:    path,
				Line:    lineNum,
				Char:    loc[0] - lineStart + 1,
				Text:    strings.TrimSpace(string(data[lineStart:lineEnd])),
				ModTime: info.ModTime().UnixMilli(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(matches, func(a, b GrepMatch) int {
		return cmp.Compare(a.Path, b.Path)
	})
	return matches, nil
}
