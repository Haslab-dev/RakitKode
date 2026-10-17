# Changelog

All notable changes to RakitKode. Host apps (ForgeADE, KendaliAI) pin tags
from this file's releases; see CONTRACT.md for the stability policy.

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
