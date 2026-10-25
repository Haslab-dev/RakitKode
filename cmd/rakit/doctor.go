package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/hooks"
	"github.com/Haslab-dev/RakitKode/permission"
)

// cmdDoctor prints a one-glance diagnostic of the engine configuration. It
// never prints API keys.
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	home := fs.String("home", "", "storage root (default: $RAKIT_HOME or ~/.rakit)")
	cwd := fs.String("cwd", "", "workspace folder (default: current dir)")
	_ = fs.Parse(args)

	opts := engine.Options{Home: *home, Workspace: *cwd}
	e, err := engine.New(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine:", err)
		return 1
	}

	fmt.Printf("rakit %s\n", Version)
	fmt.Printf("home:      %s\n", e.Home())
	fmt.Printf("workspace: %s\n", e.Workspace())

	provider, model := e.ModelInfo()
	fmt.Printf("model:     %s/%s\n", provider, model)

	profiles := e.Profiles()
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	fmt.Printf("providers: %d\n", len(profiles))
	for _, p := range profiles {
		state := "disabled"
		if p.Enabled {
			state = "enabled"
		}
		fmt.Printf("  - %-12s %-9s base=%s models=%d selected=%d\n",
			p.ID, state, p.BaseURL, len(p.AvailableModels), len(p.SelectedModels))
	}

	servers := e.MCPStatus()
	fmt.Printf("mcp:       %d server(s)\n", len(servers))
	for _, s := range servers {
		fmt.Printf("  - %-16s %-10s transport=%-5s tools=%d", s.Name, s.Status, transportOf(s.Type), s.ToolCount)
		if s.LastError != "" {
			fmt.Printf(" error=%s", s.LastError)
		}
		fmt.Println()
	}

	fmt.Printf("skills:    %d\n", len(e.SkillManager().List()))
	fmt.Printf("plugins:   %d active tool(s)\n", len(e.Plugins().ActiveTools()))

	settings, err := permission.LoadSettings(permission.DefaultSettingsPaths(e.Home(), e.Workspace())...)
	if err != nil {
		fmt.Printf("permissions: error: %v\n", err)
	} else {
		mode := settings.Mode
		if mode == "" {
			mode = permission.ModeBuild
		}
		fmt.Printf("permissions: mode=%s allow=%d deny=%d ask=%d disallowed=%d\n",
			mode, len(settings.Permissions.Allow), len(settings.Permissions.Deny),
			len(settings.Permissions.Ask), len(settings.DisallowedTools))
	}

	hooksCfg, err := hooks.Load(hooks.DefaultPaths(e.Home(), e.Workspace())...)
	if err != nil {
		fmt.Printf("hooks:     error: %v\n", err)
	} else {
		fmt.Printf("hooks:     %d event(s)\n", len(hooksCfg))
		for event, matchers := range hooksCfg {
			n := 0
			for _, m := range matchers {
				n += len(m.Hooks)
			}
			fmt.Printf("  - %-18s %d hook(s)\n", event, n)
		}
	}

	mgr := e.Manager()
	fmt.Printf("tasks:     %d background · %d schedule(s)\n",
		len(mgr.BackgroundTasks("")), len(mgr.ListSchedules("")))
	return 0
}

func transportOf(t string) string {
	switch t {
	case "http", "sse", "remote":
		return t
	case "", "stdio", "local":
		return "stdio"
	default:
		return t
	}
}
