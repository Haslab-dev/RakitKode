package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Haslab-dev/RakitKode/agentconfig"
	_ "github.com/mattn/go-sqlite3"
)

// cmdAgentConfig generates the native configuration files of the supported
// coding agents (OpenCode, Pi, Oh My Pi, Claude Code, Codex) directly from
// the engine's own config in its home (~/.rakit): providers_config.json,
// mcp_servers.json — no separate canonical file.
func cmdAgentConfig(args []string) int {
	fs := flag.NewFlagSet("agentconfig", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	targets := fs.String("targets", "all", "comma-separated targets (opencode,pi,omp,claude,codex,all)")
	kendaliDB := fs.String("db", "", "KendaliAI sqlite path: import its mcp_servers rows too")
	home := fs.String("home", "", "engine home to read from (default $RAKIT_HOME or ~/.rakit)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	engineHome := *home
	if engineHome == "" {
		var err error
		engineHome, err = resolveHome()
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
	}

	cfg, err := agentconfig.ImportFromEngine(engineHome, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	if *kendaliDB != "" {
		db, err := sql.Open("sqlite3", *kendaliDB)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		rows, err := db.Query(`SELECT id, transport, COALESCE(url,''), COALESCE(env,''), enabled FROM mcp_servers`)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		for rows.Next() {
			var id, transport, url, envJSON string
			var enabled bool
			if err := rows.Scan(&id, &transport, &url, &envJSON, &enabled); err != nil {
				fmt.Fprintln(os.Stderr, "rakit:", err)
				return 1
			}
			_ = agentconfig.ImportMCPRow(cfg, id, transport, url, envJSON, enabled)
		}
		rows.Close()
		db.Close()
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}
	list := agentconfig.AllTargets()
	if *targets != "" && *targets != "all" {
		list = strings.Split(*targets, ",")
	}
	failed := false
	for _, target := range list {
		files, notes, err := cfg.Render(strings.TrimSpace(target), homeDir)
		if err != nil {
			fmt.Printf("✗ %s: %v\n", target, err)
			failed = true
			continue
		}
		written, err := agentconfig.WriteFiles(files)
		if err != nil {
			fmt.Printf("✗ %s: %v\n", target, err)
			failed = true
			continue
		}
		for _, p := range written {
			fmt.Printf("✓ %-9s %s (.bak kept)\n", target, p)
		}
		for _, n := range notes {
			fmt.Printf("  · %s: %s\n", target, n)
		}
	}
	if failed {
		return 1
	}
	return 0
}
