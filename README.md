# RakitKode

A minimal agentic coding harness for the terminal — Go port of the core
crush architecture: TUI, chat, tool calling, MCP, and skills.

## Features

- **Agent loop**: streaming LLM turns with multi-step tool calling,
  auto-summarization near the context limit, loop detection, and
  per-session concurrency control
- **Providers**: Anthropic, OpenAI, Google, and any OpenAI-compatible
  endpoint (via fantasy)
- **Tools**: view, write, edit (exact-match), ls, glob, grep (ripgrep
  with regex fallback), bash (mvdan/sh interpreter + blocklist), todos
- **Permissions**: interactive allow/deny prompts with per-session
  grants; `--dangerously-skip-permissions` for autonomous runs
- **MCP**: stdio and streamable-HTTP clients; server tools are exposed
  to the model as first-class tools
- **Skills**: Agent Skills standard (SKILL.md) discovery with mandatory
  activation injected into the system prompt
- **Persistence**: SQLite (WAL) — sessions, messages with typed parts,
  per-session file history, stale-write detection
- **TUI**: Bubble Tea v2 chat interface with streaming updates,
  permission prompts, and cancel

## Build

```sh
make build    # -> bin/rakitkode (chmod +x, version injected from git)
make run      # build + launch the TUI
make install  # build + install to $GOPATH/bin
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt
make clean    # remove the binary
```

## Configure

`rakitkode.json` (searched upward from the cwd):

```json
{
  "models": {
    "large": { "model": "your-model", "provider": "my-provider" },
    "small": { "model": "your-model", "provider": "my-provider" }
  },
  "providers": {
    "my-provider": {
      "type": "anthropic",
      "base_url": "$ANTHROPIC_BASE_URL",
      "api_key_env": "ANTHROPIC_AUTH_TOKEN",
      "models": [
        {
          "id": "your-model",
          "name": "Your Model",
          "context_window": 200000,
          "default_max_tokens": 8192
        }
      ]
    }
  },
  "mcp": {
    "my-server": { "type": "stdio", "command": "npx", "args": ["-y", "some-mcp-server"] }
  }
}
```

Provider `type`: `anthropic`, `openai`, `google`, or `openai-compat`
(default). API keys resolve from the environment via `api_key_env` or
inline `$VAR` expansion in `api_key`.

## Use

```sh
# Interactive TUI
rakitkode

# Non-interactive run
rakitkode run "explain what this repo does"

# Continue a session
rakitkode run --session <id> "now add tests"
rakitkode sessions            # list saved sessions

# Autonomous one-shot (no permission prompts)
rakitkode run --dangerously-skip-permissions "fix the failing test"

# Model override
rakitkode run --model myairouter/glm/glm-5.3-flash "hello"
```

### TUI keys & slash commands

| Key | Action |
|---|---|
| `enter` | send (`shift+enter` = newline) |
| `/` | open the command menu (fuzzy-filtered, `↑`/`↓` navigate, `tab` complete, `enter` run) |
| `tab` | expand/collapse the latest ✻ thinking block |
| `y` / `n` | grant/deny a pending permission prompt |
| `ctrl+c` | cancel the running turn, then quit |

| Command | Action |
|---|---|
| `/model [provider/model]` | show catalog or switch model live |
| `/mcp` | MCP servers: state + tools |
| `/skills` | discovered skills (✓ = loaded this session) |
| `/tools` | active tool palette incl. MCP tools |
| `/context` | context files injected into the system prompt |
| `/clear` | clear the screen (session history kept) |
| `/quit` | exit |

The status bar shows streaming state, active model, prompt/completion
token counts, and context-window usage. Tool calls render as
`⚙ name args · ✓ result` lines; reasoning models show a collapsible
✻ thinking block per assistant message.

## Layout

```
cmd/rakitkode          CLI entry
internal/agent         Session agent loop, provider builders, coordinator
internal/agent/tools   Built-in tools + MCP adapter
internal/agent/tools/mcp  MCP client manager
internal/app           Service wiring, event feed
internal/config        rakitkode.json loading
internal/db            SQLite connect + migrations + queries
internal/message       Message model (typed parts) + store
internal/session       Session store
internal/permission    Permission service
internal/skills        SKILL.md discovery + tracker
internal/tui           Bubble Tea v2 UI
```

## Test

```sh
go test ./...
```
