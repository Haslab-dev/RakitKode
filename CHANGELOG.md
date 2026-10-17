# Changelog

All notable changes to RakitKode. Host apps (ForgeADE, KendaliAI) pin tags
from this file's releases; see CONTRACT.md for the stability policy.

## v0.4.0 — 2026-10-17

### Added
- Layered canonical config: base `~/.rakit/agent-config.json`, optional
  per-app overrides merged on top (`agentconfig.Merge` / `LoadLayered`).
- `rakit agentconfig sync -override <path>`: generate native configs from
  base + an app override layer.
- `agentconfig.SyncToEngineDir` / `UpsertProviderProfiles`: propagate the
  canonical registry into (and back from) an app's engine config files.
  `SyncToEngineDir` upserts — engine-side entries the canonical config does
  not know are preserved — and only writes stdio MCP servers (HTTP/SSE
  stay canonical-side until the engine gains remote transports).

### Changed
- Canonical config moved from `~/.config/agent-config/agent-config.json` to
  `~/.rakit/agent-config.json`: the engine home is the base layer; apps keep
  their own engine-format copies (duplication is intentional) and may layer
  overrides.

## v0.2.1 — 2026-10-17

### Added
- `agentconfig`: adapters that generate the native configuration of the
  coding agents (OpenCode, Pi, Oh My Pi, Claude Code, Codex) directly from
  the engine home config (`providers_config.json`, `mcp_servers.json`) —
  no separate canonical file. `rakit agentconfig [-targets …] [-db
  kendaliai.db]`; existing native entries (e.g. per-agent servers) are
  preserved and `.bak` copies are kept.
- `agent.Manager.SetSessionModel(sessionID, model)`: per-session model
  switching.
- TUI (`rakit` chat): alt-screen shell, markdown rendering, live spinner,
  interactive `/model` picker applying to the current session,
  auto-collapsing thinking, engine logs silenced, layout fits embedded
  terminal panels.
- jev routing credentials configured from host app settings
  (`Router.SetAPIKey`/`SetEndpoint`); `.env` scanning removed.

### Fixed
- Turn loop: LLM errors were misread as mid-turn steering and retried
  forever; they now end the turn with a visible error message.
- `llm`: ResponseHeaderTimeout 30s → 120s; jev timeout 1.2s → 3s.

## v0.2.0 — 2026-10-17

### Added
- `agent.ManagerOptions` + `agent.NewManagerWithOptions`: the stable
  constructor. New harness capabilities land as optional fields — hosts that
  construct with options never break. The positional `NewManager` is
  deprecated and frozen at the v0.1.x signature.
- `agent.Manager.SetSessionModel(sessionID, model)`: per-session model
  switching without touching the engine-wide default.
- `engine.Options` optional pre-built components (`LLMClient`, `Tools`,
  `Skills`, `Plugins`, `MCP`, `Memory`) for hosts that construct their own
  managers; engine accessors `LLM()`, `Tools()`, `MCP()`, `Plugins()`,
  `SkillManager()`, `Memory()`.
- `CONTRACT.md` (stability tiers) and the events payload contract
  (`events/doc.go`).
- `jev.Router.SetAPIKey` / `SetEndpoint`: routing credentials come from host
  app config; the `.env` file scanning was removed (env `TYPESAFE_API_KEY`
  remains a fallback).

### Fixed
- Turn loop: `stepCtx` was cancelled by cleanup *before* the steering check,
  so every LLM error (timeout, HTTP failure, empty response) was misread as
  mid-turn steering and silently retried forever. Errors now end the turn
  and surface as `agent:turn_end` + an assistant error message.
- `llm`: `ResponseHeaderTimeout` 30s → 120s (slow reasoning gateways);
  jev client timeout 1.2s → 3s.

### Changed
- TUI (`rakit` chat): alt-screen app shell, markdown rendering (glamour),
  live spinner, interactive `/model` picker applying to the current session,
  auto-collapsing thinking blocks, engine logs silenced (RAKIT_DEBUG=1
  diverts to a file), layout that fits embedded terminal panels.

## v0.1.0 — initial extraction

Standalone engine module extracted from ForgeADE: agent turn loop, tools,
harness scheduler, llm providers, mcp, memory, plugins, skills, sandbox,
search/index, events bus, jev router, store — plus the `engine` facade and
the `rakit` CLI (run, REPL, TUI, sessions, import).
