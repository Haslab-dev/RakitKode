package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Haslab-dev/RakitKode/agentconfig"
	_ "github.com/mattn/go-sqlite3"
)

// cmdAgentConfig manages the canonical agent configuration
// (~/.config/agent-config/agent-config.json): import credentials from the
// engine home, and sync native provider/model/MCP config files for the
// supported coding agents.
func cmdAgentConfig(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: rakit agentconfig [import|sync|show] [-targets a,b] [-db path] [-home dir]")
		return 2
	}
	fs := flag.NewFlagSet("agentconfig", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	targets := fs.String("targets", "all", "comma-separated targets (opencode,pi,omp,claude,codex,all)")
	kendaliDB := fs.String("db", "", "KendaliAI sqlite path: import its mcp_servers rows too")
	home := fs.String("home", "", "engine home to import from (default $RAKIT_HOME or ~/.rakit)")
	override := fs.String("override", "", "app override config layered over the base (sync only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	sub := args[0]

	canonicalPath, err := agentconfig.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rakit:", err)
		return 1
	}

	switch sub {
	case "import":
		engineHome := *home
		if engineHome == "" {
			engineHome, err = resolveHome()
			if err != nil {
				fmt.Fprintln(os.Stderr, "rakit:", err)
				return 1
			}
		}
		var base *agentconfig.Config
		if base, err = agentconfig.Load(canonicalPath); err != nil {
			base = nil // fresh
		}
		cfg, err := agentconfig.ImportFromEngine(engineHome, base)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		if *kendaliDB != "" {
			imported, err := importKendaliMCPs(*kendaliDB, cfg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "rakit:", err)
				return 1
			}
			fmt.Printf("imported %d MCP server(s) from KendaliAI\n", imported)
		}
		if err := agentconfig.Save(canonicalPath, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		fmt.Printf("canonical config written: %s (%d providers, %d models, %d mcp servers)\n",
			canonicalPath, len(cfg.Providers), len(cfg.Models), len(cfg.MCPServers))
		return 0

	case "sync":
		cfg, err := agentconfig.LoadLayered(canonicalPath, *override)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rakit: %s\n", err)
			return 1
		}
		if cfg == nil {
			fmt.Fprintln(os.Stderr, "rakit: no config found (run `rakit agentconfig import` first)")
			return 1
		}
		list := agentconfig.AllTargets()
		if *targets != "" && *targets != "all" {
			list = strings.Split(*targets, ",")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		failed := false
		for _, target := range list {
			files, notes, err := cfg.Render(strings.TrimSpace(target), home)
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

	case "show":
		data, err := os.ReadFile(canonicalPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rakit:", err)
			return 1
		}
		fmt.Println(string(data))
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown agentconfig subcommand %q\n", sub)
		return 2
	}
}

// importKendaliMCPs reads the KendaliAI database's mcp_servers table and
// merges each row into the canonical config.
func importKendaliMCPs(dbPath string, cfg *agentconfig.Config) (int, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, transport, COALESCE(url,''), COALESCE(env,''), enabled FROM mcp_servers`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, transport, url, envJSON string
		var enabled bool
		if err := rows.Scan(&id, &transport, &url, &envJSON, &enabled); err != nil {
			return count, err
		}
		if err := agentconfig.ImportMCPRow(cfg, id, transport, url, envJSON, enabled); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

var _ = json.Marshal
