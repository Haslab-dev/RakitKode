# rakit

*rakit* (Indonesian: "to assemble") — a Go agent engine for coding tasks, and
the `rakit` CLI that drives it. One brain, many surfaces: embed the engine as
a Go library, or use the CLI in your terminal like any other coding agent.

The engine is extracted from [forge-ade](https://github.com/hasdev/forge-ade)'s
desktop agent: streaming LLM turns with multi-step tool calling, parallel
tool scheduling, permission/approval pipelines, MCP (stdio + remote), the
Agent Skills standard, plugins, memory, background tasks, and subagents.

## Install

```sh
go install github.com/Haslab-dev/RakitKode/cmd/rakit@latest
```

## CLI

```sh
# one-shot, streams to stdout
rakit run "fix the failing test in pkg/x"

# pipe a prompt, emit machine-readable events
git diff | rakit run --json "review this diff"

# interactive session
rakit

# resume the most recent session
rakit run -c "what did we change?"

# auto-approve tool calls (trusted/sandboxed use only)
rakit run --yolo "bump all deps and run tests"
```

Providers come from `$RAKIT_HOME` (default `~/.rakit`): `providers_config.json`
(same format as forge-ade) or standard env keys (`OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`, …). Coming from forge-ade? `rakit import` copies provider
profiles, MCP servers, and skills from `~/.forge-ade`.

## Library

```go
package main

import (
	"context"
	"fmt"

	"github.com/Haslab-dev/RakitKode/engine"
	"github.com/Haslab-dev/RakitKode/events"
)

func main() {
	e, err := engine.New(engine.Options{Workspace: "."})
	if err != nil {
		panic(err)
	}
	e.Bus().Subscribe(events.AgentToolStart, func(ev events.Event) {
		fmt.Println("tool:", ev.Data["name"])
	})
	out, err := e.Run(context.Background(), "list the TODOs in this repo")
	fmt.Println(out, err)
}
```

The `engine` package is a convenience facade; every layer is public —
`agent.Manager`, `harness`, `tools.Registry`, `llm.LLMClient`, `mcp.Manager`,
`skills`, `plugins` — so hosts (desktop apps, daemons, bots) can compose
exactly what they need.

## Layout

| Package | Role |
|---|---|
| `engine` | facade: one constructor, `Run`/`RunIn` one-shot turns |
| `agent` | session lifecycle, turn loop, approvals, subagents |
| `permission` | collaboration modes, allow/deny/ask rules, per-tool decisions |
| `hooks` | lifecycle hooks (`PreToolUse`, `PostToolUse`, `Stop`, …) |
| `checkpoint` | file snapshots, undo/rewind |
| `harness` | provider-agnostic tool scheduler and metadata |
| `llm` | OpenAI-compatible multi-provider client with streaming |
| `tools` | canonical tool set + registry (read/write/edit/bash/search/…) |
| `mcp` | MCP client manager (stdio + HTTP), tool bridging |
| `skills` | SKILL.md discovery across workspace/global/plugin scopes |
| `store` | per-project storage root (`$RAKIT_HOME`, default `~/.rakit`) |
| `events` | in-process pub/sub bus every component emits through |

## Status

v0.3 — active harness work. Headless + interactive modes shipped; the TUI is
under active development. The legacy `internal/` crush port has been removed;
the top-level packages are the single runtime. API may still shift before v1.

## License

MIT — see [LICENSE](LICENSE).
