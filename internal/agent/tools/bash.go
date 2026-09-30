package tools

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/fantasy"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"

	"github.com/Haslab-dev/RakitKode/internal/permission"
)

// BashParams are the inputs of the bash tool.
type BashParams struct {
	Description string `json:"description" description:"A brief description of what the command does, try to keep it under 30 characters or so"`
	Command     string `json:"command" description:"The command to execute"`
	WorkingDir  string `json:"working_dir,omitempty" description:"The working directory to execute the command in (defaults to current directory)"`
	Timeout     int    `json:"timeout,omitempty" description:"Seconds to wait before timing out (default: 120)"`
}

// BashPermissionsParams are the params passed to permission prompts.
type BashPermissionsParams struct {
	Description string `json:"description"`
	Command     string `json:"command"`
	WorkingDir  string `json:"working_dir"`
}

// BashResponseMetadata describes the execution outcome.
type BashResponseMetadata struct {
	StartTime        int64  `json:"start_time"`
	EndTime          int64  `json:"end_time"`
	Output           string `json:"output"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
}

const (
	BashToolName  = "bash"
	MaxOutBytes   = 30_000
	DefaultCwd    = "."
	BashNoOutput  = "no output"
	bashTimeout   = 120 * time.Second
	maxOutputSize = 30000
)

var bannedCommands = []string{
	// Network/download tools: the fetch tool covers HTTP.
	"curl", "wget", "httpie", "http", "nc", "ncat", "telnet", "ssh", "scp", "ftp",
	// Privilege escalation and system admin.
	"sudo", "su", "doas",
	// Package managers that mutate the system.
	"apt", "apt-get", "dnf", "yum", "pacman", "brew", "port",
	// Cron and init manipulation.
	"crontab", "systemctl", "launchctl",
	// Destructive disk operations.
	"mkfs", "fdisk", "diskutil", "dd if=/dev/",
}

const bashDescription = `Executes a given bash command in a shell with the user's permission.
Working directory persists between commands? No: every invocation starts in the
working directory; use working_dir or cd within a single command.
Avoid commands producing very large output; output is capped at %d characters.
Avoid interactive commands; use non-interactive variants.
`

// NewBashTool builds the bash tool.
func NewBashTool(permissions permission.Service, workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		BashToolName,
		fmt.Sprintf(bashDescription, maxOutputSize),
		func(ctx context.Context, params BashParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Command == "" {
				return fantasy.NewTextErrorResponse("missing command"), nil
			}
			if err := checkBanned(params.Command); err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			execWorkingDir := cmp.Or(params.WorkingDir, workingDir)
			if !filepath.IsAbs(execWorkingDir) {
				execWorkingDir = filepath.Join(workingDir, execWorkingDir)
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for executing shell command")
			}

			if !isSafeReadOnly(params.Command) {
				p, err := permissions.Request(
					ctx,
					permission.CreatePermissionRequest{
						SessionID:   sessionID,
						Path:        execWorkingDir,
						ToolCallID:  call.ID,
						ToolName:    BashToolName,
						Action:      "execute",
						Description: fmt.Sprintf("Execute command: %s", params.Command),
						Params: BashPermissionsParams{
							Description: params.Description,
							Command:     params.Command,
							WorkingDir:  execWorkingDir,
						},
					},
				)
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !p {
					return NewPermissionDeniedResponse(), nil
				}
			}

			startTime := time.Now()
			timeout := bashTimeout
			if params.Timeout > 0 {
				timeout = time.Duration(params.Timeout) * time.Second
			}
			runCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			output, exitCode, err := runShell(runCtx, params.Command, execWorkingDir)
			endTime := time.Now()

			metadata := BashResponseMetadata{
				StartTime:        startTime.UnixMilli(),
				EndTime:          endTime.UnixMilli(),
				Output:           output,
				Description:      params.Description,
				WorkingDirectory: execWorkingDir,
			}

			if ctx.Err() != nil && runCtx.Err() == context.DeadlineExceeded {
				return fantasy.WithResponseMetadata(
					fantasy.NewTextErrorResponse(fmt.Sprintf("Command timed out after %s", timeout)),
					metadata), nil
			}

			if err != nil {
				if output == "" {
					output = err.Error()
				} else {
					output = fmt.Sprintf("%s\n\nexit code: %d", output, exitCode)
				}
				return fantasy.WithResponseMetadata(
					fantasy.NewTextErrorResponse(output), metadata), nil
			}

			if strings.TrimSpace(output) == "" {
				output = BashNoOutput
			}
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(output),
				metadata,
			), nil
		},
	)
}

// runShell parses and executes a command with mvdan/sh and captures
// combined output.
func runShell(ctx context.Context, command, dir string) (string, int, error) {
	line, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", 1, fmt.Errorf("could not parse command: %w", err)
	}

	var buf limitedBuffer
	env := os.Environ()
	wd := dir
	if abs, err := filepath.Abs(dir); err == nil {
		wd = abs
	}
	runner, err := interp.New(
		interp.Dir(wd),
		interp.Env(expand.ListEnviron(env...)),
		interp.StdIO(nil, &buf, &buf),
		interp.ExecHandler(interp.DefaultExecHandler(15)),
	)
	if err != nil {
		return "", 1, fmt.Errorf("could not start shell: %w", err)
	}

	err = runner.Run(ctx, line)
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(interp.ExitStatus); ok {
			exitCode = int(exitErr)
			if exitCode == 0 {
				err = nil
			}
		}
	}
	out := buf.String()
	if len(out) > maxOutputSize {
		out = out[:maxOutputSize] + "\n\n(output truncated at 30000 characters)"
	}
	return out, exitCode, err
}

// limitedBuffer caps in-memory output at a generous ceiling.
type limitedBuffer struct {
	buf strings.Builder
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const cap = 2 * maxOutputSize
	if b.buf.Len() >= cap {
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

// checkBanned rejects commands invoking blocklisted binaries.
func checkBanned(command string) error {
	lower := strings.ToLower(command)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '&' || r == '|'
	})
	for _, field := range fields {
		base := filepath.Base(strings.Trim(field, "\"'("))
		if slicesContains(bannedCommands, base) {
			slog.Warn("Blocked banned command", "command", command, "match", base)
			return fmt.Errorf("command %q is not allowed", base)
		}
	}
	return nil
}

var safeCommands = []string{
	"ls", "cat", "head", "tail", "grep", "rg", "find", "fd", "tree", "which",
	"pwd", "echo", "wc", "file", "stat", "du", "df", "git status", "git log",
	"git diff", "git show", "git branch", "go version", "node --version",
	"python --version", "python3 --version",
}

// isSafeReadOnly reports whether a single command is a known read-only
// command needing no permission prompt.
func isSafeReadOnly(command string) bool {
	if containsCommandChaining(command) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, safe := range safeCommands {
		if strings.HasPrefix(lower, safe) {
			if len(lower) == len(safe) || lower[len(safe)] == ' ' || lower[len(safe)] == '-' {
				return true
			}
		}
	}
	return false
}

// containsCommandChaining detects shell operators that could smuggle a
// second command past the prefix check.
func containsCommandChaining(command string) bool {
	for _, pattern := range []string{"&&", "||", ";", "|", "&"} {
		if strings.Contains(command, pattern) {
			return true
		}
	}
	return false
}

func slicesContains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
