// Command rakit is the standalone CLI for the rakit agent engine — a coding
// agent for the terminal, usable headlessly (rakit run) or interactively
// (rakit). It behaves like the other agent CLIs: one-shot prompts with
// streaming output, session resume, and a home directory under ~/.rakit.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Haslab-dev/RakitKode/agent"
	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/events"
)

const usage = `rakit — assemble code with an agent.

Usage:
  rakit run [flags] [prompt...]   one-shot turn (reads stdin when piped)
  rakit                           interactive TUI (plain REPL when not a TTY)
  rakit chat                      force the interactive TUI
  rakit sessions                  list saved sessions
  rakit import                    pull providers/MCP/skills from ~/.forge-ade
  rakit version                   print version

Run flags:
  --model provider/model   select model (e.g. openai/gpt-4o)
  --session ID             run inside an existing session
  --continue, -c           continue the most recent session
  --cwd DIR                workspace folder (default: current dir)
  --json                   stream raw agent events as NDJSON
  --yolo                   auto-approve tool calls (trusted use only)
  --home DIR               storage root (default: $RAKIT_HOME or ~/.rakit)
  --timeout duration       turn timeout (default 10m)
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		if isTerminal(os.Stdout) {
			return cmdTUI(nil)
		}
		return cmdInteractive(nil)
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "chat", "tui":
		return cmdTUI(args[1:])
	case "sessions":
		return cmdSessions()
	case "import":
		return cmdImport(args[1:])
	case "version", "--version", "-v":
		fmt.Println("rakit v0.1.0")
		return 0
	case "--help", "-h", "help":
		fmt.Print(usage)
		return 0
	default:
		// `rakit "do a thing"` — treat as a one-shot run.
		return cmdRun(args)
	}
}

type runFlags struct {
	model   string
	session string
	cont    bool
	cwd     string
	jsonOut bool
	yolo    bool
	home    string
	timeout time.Duration
}

func parseRunFlags(args []string) (*runFlags, []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &runFlags{}
	fs.StringVar(&f.model, "model", "", "provider/model")
	fs.StringVar(&f.session, "session", "", "session id")
	fs.BoolVar(&f.cont, "continue", false, "continue most recent session")
	fs.BoolVar(&f.cont, "c", false, "continue most recent session (shorthand)")
	fs.StringVar(&f.cwd, "cwd", "", "workspace folder")
	fs.BoolVar(&f.jsonOut, "json", false, "stream agent events as NDJSON")
	fs.BoolVar(&f.yolo, "yolo", false, "auto-approve tool calls")
	fs.StringVar(&f.home, "home", "", "storage root")
	fs.DurationVar(&f.timeout, "timeout", 10*time.Minute, "turn timeout")
	// Flags may appear before, after, or between prompt words. Go's flag
	// package stops at the first non-flag argument, so re-parse the
	// remainder to keep `rakit run "fix the bug" --yolo` from treating
	// --yolo as prompt text.
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, nil
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	return f, positional
}

func newEngine(f *runFlags) (*engine.Engine, error) {
	return engine.New(engine.Options{
		Home:      f.home,
		Workspace: f.cwd,
		Model:     f.model,
		Yolo:      f.yolo,
	})
}

func cmdRun(args []string) int {
	f, rest := parseRunFlags(args)
	if f == nil {
		return 2
	}

	prompt := strings.TrimSpace(strings.Join(rest, " "))
	if stat, err := os.Stdin.Stat(); err == nil && prompt == "" && stat.Mode()&os.ModeCharDevice == 0 {
		data, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(data))
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "rakit run: empty prompt (pass one or pipe via stdin)")
		return 2
	}

	e, err := newEngine(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	if !e.HasProvider() {
		fmt.Fprintln(os.Stderr, "rakit:", engine.ErrNoProvider)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if f.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}

	sessionID := f.session
	if sessionID == "" && f.cont {
		sessionID = latestSessionID(e)
		if sessionID == "" {
			fmt.Fprintln(os.Stderr, "rakit: no previous session to continue")
			return 1
		}
	}

	// Subscribe before sending so no delta is missed. SessionID filtering
	// happens per event (empty = accept all, for the fresh-session case).
	if f.jsonOut {
		go streamJSON(e, sessionID)
	} else {
		go streamText(e, sessionID)
	}

	var out string
	if sessionID != "" {
		out, err = e.RunIn(ctx, sessionID, prompt)
	} else {
		out, err = e.Run(ctx, prompt)
	}
	if f.jsonOut {
		if err != nil {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "error", "data": map[string]any{"message": err.Error()}})
			return 1
		}
		return 0
	}
	// Text deltas were already streamed; surface errors after the stream.
	if out != "" && !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	return 0
}

// streamText prints text deltas as they arrive (headless stream mode).
func streamText(e *engine.Engine, sessionID string) {
	w := bufio.NewWriter(os.Stdout)
	e.Bus().Subscribe(events.AgentMessageDelta, func(ev events.Event) {
		if id, _ := ev.Data["session_id"].(string); sessionID != "" && id != "" && id != sessionID {
			return
		}
		if kind, _ := ev.Data["kind"].(string); kind == "text" {
			fmt.Fprint(w, ev.Data["delta"])
			_ = w.Flush()
		}
	})
	e.Bus().Subscribe(events.AgentToolStart, func(ev events.Event) {
		if id, _ := ev.Data["session_id"].(string); sessionID != "" && id != "" && id != sessionID {
			return
		}
		fmt.Fprintf(w, "\n[tool] %v\n", ev.Data["name"])
		_ = w.Flush()
	})
}

// streamJSON emits one JSON object per agent event (NDJSON).
func streamJSON(e *engine.Engine, sessionID string) {
	enc := json.NewEncoder(os.Stdout)
	types := []events.EventType{
		events.AgentTurnStart, events.AgentTurnEnd,
		events.AgentMessageStart, events.AgentMessageDelta, events.AgentMessageEnd,
		events.AgentThinkingDelta,
		events.AgentToolStart, events.AgentToolEnd, events.AgentToolDelta,
		events.AgentAsk,
	}
	for _, t := range types {
		t := t
		e.Bus().Subscribe(t, func(ev events.Event) {
			if id, _ := ev.Data["session_id"].(string); sessionID != "" && id != "" && id != sessionID {
				return
			}
			_ = enc.Encode(map[string]any{"type": string(t), "data": ev.Data})
		})
	}
}

func latestSessionID(e *engine.Engine) string {
	sessions := e.Manager().ListSessions()
	if len(sessions) == 0 {
		return ""
	}
	latest := sessions[0]
	for _, s := range sessions {
		if s.UpdatedAt.After(latest.UpdatedAt) {
			latest = s
		}
	}
	return latest.ID
}

func cmdInteractive(args []string) int {
	fs := flag.NewFlagSet("interactive", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &runFlags{}
	fs.StringVar(&f.model, "model", "", "provider/model")
	fs.StringVar(&f.cwd, "cwd", "", "workspace folder")
	fs.StringVar(&f.home, "home", "", "storage root")
	fs.BoolVar(&f.yolo, "yolo", false, "auto-approve tool calls")
	fs.DurationVar(&f.timeout, "timeout", 0, "per-turn timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	e, err := newEngine(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	if !e.HasProvider() {
		fmt.Fprintln(os.Stderr, "rakit:", engine.ErrNoProvider)
		return 1
	}

	sess, err := e.Manager().CreateSession("rakit interactive", agent.RoleCoding, e.Workspace())
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	if f.yolo {
		_ = e.Manager().SetAutoApprove(sess.ID, true)
	}

	fmt.Printf("rakit — session %s — workspace %s\ntype your prompt, /exit to quit\n", sess.ID, e.Workspace())

	// Stream replies as they arrive; RunIn only returns the final text.
	go streamText(e, sess.ID)

	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !sc.Scan() {
			break
		}
		prompt := strings.TrimSpace(sc.Text())
		if prompt == "" {
			continue
		}
		if prompt == "/exit" || prompt == "/quit" {
			break
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		if f.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, f.timeout)
			defer cancel()
		}
		_, err := e.RunIn(ctx, sess.ID, prompt)
		stop()
		fmt.Println()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println("(interrupted)")
				continue
			}
			fmt.Fprintln(os.Stderr, "rakit:", err)
		}
	}
	return 0
}

func cmdSessions() int {
	e, err := engine.New(engine.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	sessions := e.Manager().ListSessions()
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return 0
	}
	for _, s := range sessions {
		fmt.Printf("%s  %s  %s\n", s.ID, s.UpdatedAt.Format("2006-01-02 15:04"), s.Name)
	}
	return 0
}

// cmdImport copies provider profiles, MCP servers, and skills from the
// forge-ade data dir (~/.forge-ade) into the rakit home so an existing
// desktop setup works in the CLI with zero reconfiguration.
func cmdImport(args []string) int {
	dst, err := resolveHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	home, _ := os.UserHomeDir()
	src := filepath.Join(home, ".forge-ade")

	copied := 0
	for _, name := range []string{"providers_config.json", "mcp_servers.json"} {
		if err := copyFile(filepath.Join(src, name), filepath.Join(dst, name)); err == nil {
			fmt.Println("copied", name)
			copied++
		}
	}
	if err := copyDir(filepath.Join(src, "skills"), filepath.Join(dst, "skills")); err == nil {
		fmt.Println("copied skills/")
		copied++
	}
	if copied == 0 {
		fmt.Println("nothing found to import from", src)
		return 1
	}
	fmt.Println("imported into", dst)
	return 0
}

func resolveHome() (string, error) {
	if env := os.Getenv("RAKIT_HOME"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".rakit")
	return dir, os.MkdirAll(dir, 0o755)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}
