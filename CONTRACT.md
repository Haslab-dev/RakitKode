# RakitKode API Contract

RakitKode (`github.com/Haslab-dev/RakitKode`) is the standalone coding-agent
engine extracted from ForgeADE. Hosts — ForgeADE (Wails desktop), KendaliAI
(gateway daemon), the `rakit` CLI — embed it as a Go module.

This document is the stability contract between the engine and its hosts.
The goal: **hosts improve their own product; the harness improves here.**
Breaking changes must never force a host to edit its code.

## Versioning

- The module follows semver on tags (`v0.1.0`, `v0.2.0`, …).
- `0.x` policy: **minor** bumps carry features and, if unavoidable, breaking
  changes (with a migration note in CHANGELOG.md); **patch** bumps are fixes
  only.
- Hosts pin the tag in `go.mod`. A `go.work` pointing at a sibling checkout
  is for local development only — it must never be required to build.
- Every release updates CHANGELOG.md. If a release contains anything under
  "Breaking / migration", the host apps get a same-day adaptation commit.

## Tier 1 — stable facade (breaking changes only on a major/minor + note)

### `engine` — the composition root

Everything a host needs to embed the agent:

- `engine.New(engine.Options) (*Engine, error)` — Options: `Home`,
  `Workspace`, `Model`, `Yolo`, `Bus`, `Term`, `ConnectMCP`, plus optional
  pre-built components (`LLMClient`, `Tools`, `Skills`, `Plugins`, `MCP`,
  `Memory`). Nil components are constructed by the engine; supplied ones are
  adopted as-is.
- Accessors: `Manager()`, `Bus()`, `LLM()`, `Tools()`, `MCP()`, `Plugins()`,
  `SkillManager()`, `Memory()`, `Home()`, `Workspace()`.
- Turn execution: `Run`, `RunIn`; model control: `ModelInfo`, `SetModel`,
  `Profiles`, `ReasoningEffort`, `HasProvider`; maintenance: `Compact`,
  `UsageSummary`, `RegisterMCPTools`.

Rules: existing Options fields and accessor signatures never change.
New capabilities arrive as new optional fields / new methods.

### `events` — the event contract

Event names and payload keys, documented in `events/doc.go`. Names never
change; payload keys are additive-only; `session_id` is always present.

### `store` — storage root

`store.SetRoot(dir)` / `store.Root()`. Must be called before constructing any
engine component. Layout under the root is an implementation detail.

### `llm` — provider configuration types

`ProviderProfile` / `Profile` structs and the `providers_config.json` /
`profiles.json` file shapes they serialize to. Field names are contract
(hosts sync their own provider stores into these).

## Tier 2 — exposed, additive-only

Manager packages reachable through the facade. Methods hosts call today
keep their signatures; new methods appear over time.

- `agent`: `Manager` (SendMessage, CreateSession, GetSession, ListSessions…,
  DeleteSession, SetSessionModel, SetAutoApprove, StopTurn, SteerMessage,
  RespondApproval, RespondAsk, CompactSession, SetJevRouter, MemoryManager),
  `ManagerOptions` + `NewManagerWithOptions` (the stable constructor),
  `Session`, `ContentBlock`, `RoleFilter`, `ShellManager`.
  The positional `NewManager` is deprecated and frozen.
- `tools`: `Registry` (Register, Lookup, Execute, Definitions,
  RegisterMCPToolWithCaller, SetTaskUpdateHandler), `ToolSpec`.
- `mcp`: `Manager` (NewManager, ConnectAll, DisconnectAll, ListServers,
  ListConnectedTools, SaveServer, Remove), `ServerConfig`.
- `skills`, `plugins`, `memory`, `search`, `index`: their Manager types and
  entry records.
- `sandbox`: `Ensure`, `SweepTemp`.
- `agentconfig`: the canonical providers/models/MCP schema and the native
  config adapters (OpenCode, Pi, Oh My Pi, Claude Code, Codex). Canonical
  file: `~/.config/agent-config/agent-config.json` (0600). Adapters merge —
  native entries the canonical config does not know are preserved.

## Tier 3 — no stability promise

Implementation packages a host should not import: `harness`, `conffmt`,
`agentclirc`, `gitignore`, `ignore`, `cordis`, `jev` (configure the router
through `agent.Manager.SetJevRouter` / `Router.SetAPIKey`/`SetEndpoint`
instead). They may change in any release.

## Host rules

1. Compose via `engine.New` (preferred) or `agent.NewManagerWithOptions`.
   Do not call the deprecated positional `NewManager`.
2. Subscribe to events by the constants in `events`; treat payload maps as
   open (tolerate unknown keys).
3. Persist hosts' own configuration (API keys, MCP servers, providers) in
   the host's config store; hand it to the engine at startup or through the
   accessors (`LLM().SaveProviderProfiles`, `MCP().SaveServer`,
   `SkillManager()`, `Memory()`). The engine never reads `.env` files.
4. Coding-agent CLI configuration (opencode/pi/omp/claude/codex) is
   generated from the canonical file via `rakit agentconfig import|sync` —
   do not hand-edit the generated sections; adapters keep `.bak` copies.
5. File formats under the store root and data dirs (`providers_config.json`,
   `profiles.json`, `mcp_servers.json`, `agent_sessions.json`) are Tier 1 as
   documented; anything else under those directories is internal.
