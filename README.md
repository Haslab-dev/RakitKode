# RakitKode

**RakitKode** is an AI Developer OS for the terminal. A TUI-based coding agent built with Bun + TypeScript, featuring multi-provider LLM support, human-in-the-loop approval, and a Claude Code-inspired interface.

![RakitKode Preview](preview.png)

## Features

- **Multi-Provider Support**: 9 providers — DeepSeek, OpenAI, Gemini, Groq, Mistral, Together AI, OpenRouter, Azure OpenAI, Ollama (local)
- **Claude Code-Style TUI**: Clean interface with diff previews, inline approval banner, and status bar
- **Human-in-the-Loop**: `y`/`n` to accept/reject, `yy` for always-approve, `nn` for never-approve (stops retries)
- **Provider Profiles**: Save provider config to `.rakitkode-profile.json` per project
- **Ollama Discovery**: Auto-detect local Ollama models with goal-based recommendation (latency/balanced/coding)
- **Runtime Diagnostics**: `bun run doctor` checks provider, API key, reachability, and Ollama status
- **Tool System**: File read/write, grep, symbol search, git operations, command execution

## Quick Start

### Install

```bash
bun install
```

### Configure

```bash
# Option 1: Environment variable
export DEEPSEEK_API_KEY=your_key_here

# Option 2: Provider profile (saved per project)
bun run profile:deepseek

# Option 3: Auto-detect best available provider
bun run profile:auto

# Option 4: Ollama (local, no API key needed)
ollama pull llama3.1:8b
bun run profile:ollama --goal coding
```

### Run

```bash
bun run dev
```

### Approve/Reject Changes

```
Allow write_file? y/n │ yes/yy always │ no/nn never
```

| Key   | Action                    |
|-------|---------------------------|
| `y`   | Accept this change        |
| `n`   | Reject and stop retries   |
| `yy`  | Accept + auto-approve all |
| `nn`  | Reject + stop agent       |

## Available Providers

| Provider     | Env Key                | Default Model                       |
|--------------|------------------------|-------------------------------------|
| DeepSeek     | `DEEPSEEK_API_KEY`     | `deepseek-chat`                     |
| OpenAI       | `OPENAI_API_KEY`       | `gpt-4o`                            |
| Gemini       | `GEMINI_API_KEY`       | `gemini-2.0-flash`                  |
| Groq         | `GROQ_API_KEY`         | `llama-3.3-70b-versatile`           |
| Mistral      | `MISTRAL_API_KEY`      | `mistral-large-latest`              |
| Together AI  | `TOGETHER_API_KEY`     | `meta-llama/Llama-3.3-70B-Instruct` |
| OpenRouter   | `OPENROUTER_API_KEY`   | `openai/gpt-4o`                     |
| Azure OpenAI | `AZURE_OPENAI_API_KEY` | `gpt-4o`                            |
| Ollama       | *(none)*               | `llama3.1:8b`                       |

## Slash Commands

| Command         | Description                        |
|-----------------|------------------------------------|
| `/yolo`         | Toggle auto-approve mode           |
| `/diff`         | Show pending patches               |
| `/files`        | Show modified files                |
| `/models`       | Show current provider and model    |
| `/tools`        | List available tools               |
| `/accept <id>`  | Accept a specific patch            |
| `/reject <id>`  | Reject a specific patch            |
| `/accept-all`   | Accept all pending patches         |
| `/run <cmd>`    | Run a shell command                |
| `/clear`        | Clear terminal screen              |
| `/help`         | Show help                          |
| `/exit`         | Exit RakitKode                     |

## Scripts

```bash
bun run dev              # Start RakitKode
bun run doctor           # Runtime diagnostics
bun run profile:init     # Interactive provider setup
bun run profile:auto     # Auto-detect best provider
bun run profile:ollama   # Setup Ollama provider
bun run profile:deepseek # Setup DeepSeek provider
bun run profile:openai   # Setup OpenAI provider
bun run check            # typecheck + build + doctor
bun run build            # Production build
```

## Tech Stack

- **Runtime**: [Bun](https://bun.sh)
- **UI**: [Ink](https://github.com/vadimdemedes/ink) (React for CLI)
- **LLM**: OpenAI-compatible API (works with any provider)
- **Language**: TypeScript (strict mode)

## License

MIT © RakitKode Team
