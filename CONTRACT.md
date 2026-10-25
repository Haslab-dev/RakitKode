# RakitKode API Contract & Co-Development Model

RakitKode (`github.com/Haslab-dev/RakitKode`) is the core agent harness engine.
Hosts — ForgeADE (Wails desktop IDE), KendaliAI (agent gateway daemon), and the
`rakit` standalone TUI CLI — are active sister projects developed together in
the same workspace (`my-agent`).

## Active Co-Development Policy

ForgeADE, KendaliAI, and RakitKode are in **active development together**:
- **No Frozen Backwards Compatibility Barrier**: The architecture evolves rapidly
  to bring state-of-the-art harness capabilities. Sibling projects are updated
  and tested in lockstep without being held back by legacy constraints.
- **Contract as Architectural Boundaries**: This document specifies module
  boundaries, separation of concerns, and integration protocols between the
  engine and host applications.
- **Atomic Iteration**: When an engine capability changes or is refactored,
  host apps (ForgeADE, KendaliAI) receive matching updates in the same iteration cycle.

## Versioning & Releases

- Releases use semver tags (`v0.1.0`, `v0.2.7`, `v0.2.8`, …).
- Host apps pin the latest tag in their respective `go.mod`.
- Releases update `CHANGELOG.md` with features, harness improvements, and host adaptation notes.

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
- `agentconfig`: adapters that generate the native configuration of the
  coding agents (OpenCode, Pi, Oh My Pi, Claude Code, Codex) from the
  engine home config (`providers_config.json`, `mcp_servers.json`). The
  engine config is the single source; adapters merge and never replace
  native entries they do not manage.

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
   generated from the engine home config via `rakit agentconfig` — do not
   hand-edit the generated sections; adapters keep `.bak` copies.
5. File formats under the store root and data dirs (`providers_config.json`,
   `profiles.json`, `mcp_servers.json`, `agent_sessions.json`) are Tier 1 as
   documented; anything else under those directories is internal.
