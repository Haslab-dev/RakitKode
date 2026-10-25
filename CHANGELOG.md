# Changelog

All notable changes to RakitKode. Host apps (ForgeADE, KendaliAI) pin tags
from this file's releases; see CONTRACT.md for the stability policy.

## v0.3.0 — unreleased

### Added
- Prompt-cache foundation: the system prompt is split into a stable prefix
  (identity, project conventions, skills, memory) and a volatile
  `<system-reminder>` tail (git status, budget, todos, notifications,
  summaries). The transcript is no longer slid one message per step; windows
  only open in large chunks after a real context overflow.
- Deterministic prompt assembly: sorted plugin prompts and context-summary
  files, stable tool order, no git status inside the cached prefix.
- Cache accounting: `stream_options.include_usage`, OpenAI
  `prompt_tokens_details.cached_tokens`, Anthropic
  `cache_read_input_tokens` / `cache_creation_input_tokens`, DeepSeek
  `prompt_cache_hit_tokens`, and normalized `CachedTokens`.
- Anthropic cache strategy: four breakpoints (tools, system, previous-turn
  tail, current tail).
- OpenAI `prompt_cache_key` for routing affinity; Zhipu/GLM providers
  (`ZHIPU_API_KEY` / `GLM_API_KEY`).
- TUI: transcript hydration for `--continue`/`--session`, inline colored
  unified diffs for edit/write, and diff expand/collapse.
- Sub-agent pool: concurrent-child cap (default 4, `RAKIT_MAX_SUBAGENTS`) and
  configurable delegation depth (default 3, `RAKIT_MAX_SUBAGENT_DEPTH`), with
  `agent:subagent_update` progress events. Pool slots are released when a
  child turn finishes.
- Long-running durability: the background-task registry persists to
  `<home>/background_tasks.json`; tasks that were running when the process
  exited are marked `lost` (their log file stays readable). Schedules persist
  to `<home>/schedules.json`, cron expressions (5-field) are re-armed on
  startup, and missed one-shot timers fire on restore.
- Steering modes: `SteerMessage` interrupts the in-flight step;
  `SteerMessageQueued` waits for the next loop boundary. TUI `/queue`.
- TUI: `/tasks` panel (background tasks, sub-agents, task ledger, `/tasks stop
  <id>`), context-window percentage in the status line (`llm.ContextWindowFor`),
  `ctrl+↑/↓` block cursor with per-block expand/collapse, and persistent input
  history (`<home>/history`, `ctrl+p`/`ctrl+n`).
- MCP: streamable HTTP transport (URL + optional headers, `Mcp-Session-Id`),
  server notifications (`tools/list_changed` refreshes the tool list;
  `SetNotificationHandler` for progress/logging/message), and tool annotations
  (`readOnlyHint`/`destructiveHint`/title) wired into permissions and the
  scheduler. Remote servers no longer require stdio.
- Skills: `when_to_use`, `allowed-tools`, and `version` frontmatter; the
  catalog surfaces applicability and `${SKILL_DIR}` expands in loaded bodies.
- `rakit doctor`: one-glance diagnostics for providers, MCP servers, skills,
  plugins, permissions, hooks and long-running tasks (never prints API keys).
- `docs/cache.md`: prompt-cache design, provider behaviour and contributor
  rules.
- Memory: relevance/recency-ranked injection with a 4k-character budget, plus
  rule-based post-turn extraction of durable user preferences ("always…",
  "never…", "prefer…") with duplicate suppression.
- Workspace index: the lazy symbol index is wired into the engine and exposed
  as the read-only `symbol_search` tool (exact name + prefix fallback).
- Goal mode: `rakit run --goal` continues across turns until the task ledger is
  complete, bounded by `--max-iterations` and `--budget-tokens`.
- Sub-agent `output_schema` is now validated; results carry
  `structured_output_valid` / `structured_output_error`.
- Plugin marketplace: local (`file`) and `url` marketplace manifests,
  `known_marketplaces.json` / `installed_plugins.json`, install/uninstall into
  the global plugin dir, and TUI `/plugins` (`install`, `uninstall`, list).
  `Engine.ReloadPlugins` re-registers tools after a change.
- TUI: `@`-mention file completion (walk workspace, `↑/↓`, tab/enter inserts)
  and submitted `@path` tokens are attached as mention context.
- MCP: `resources/list|read` and `prompts/list|get` are exposed as the
  read-only `mcp_list_resources`, `mcp_read_resource`, `mcp_list_prompts` and
  `mcp_get_prompt` tools.
- TUI: `ctrl+k` opens the command palette (the slash menu without typing `/`).
- Incremental indexing: successful `write`/`edit`/`multiedit` calls notify the
  engine, which updates the symbol index and filename index immediately
  (`ToolRegistry.SetFileChangeHandler`).
- Cache diagnostics: `llm.PrefixFingerprint` hashes the stable system+tools
  prefix; `RAKIT_DEBUG=1` logs it per request so cache busts are visible.
- Permission engine (`permission`): modes `plan|build|edit|yolo|auto`,
  per-tool allow/deny/ask rules from `~/.rakit/settings.json` and
  `<workspace>/.rakit/settings.json`, per-call decisions, project/session
  rules, and typed literal rules like `Bash(git push:*)` / `Edit(src/**)`.
- Hooks (`hooks`): Claude-Code-compatible `hooks.json` (global + workspace)
  for `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SessionStart`,
  `SessionEnd`, `PreCompact`. Exit code 2 blocks with a reason; JSON output
  can modify tool input or append context.
- Checkpoints (`checkpoint`): files touched by a mutating batch are snapshotted
  (plus a `git stash create` tree) and restorable via `/undo` and
  `Engine.UndoLast`.
- Resume journal: interrupted approval/question turns are recovered on startup
  and resumable via `/resume` / `Engine.ResumeTurn`; tools are never
  re-executed automatically.
- Real plan mode tools `enter_plan_mode` / `exit_plan_mode`; plans are written
  to `<workspace>/.rakit/plans/`.
- `ToolSpec` classification metadata (read-only / destructive / approval /
  side-effect scope / timeout / max output) is the single source for the
  scheduler, permissions and the agent loop.
- LLM retry with jittered exponential backoff for 429/5xx/network errors,
  `Retry-After` support, and byte-identical retry bodies.
- New events: `agent:updated`, `agent:usage`, `agent:tool_progress`,
  `agent:permission_request`, `agent:subagent_update`, `agent:compaction`,
  `agent:turn_resumed`, `agent:plan_update`.
- TUI: `/mode`, `/undo`, `/resume`; approval prompts can be denied with
  `esc`/`ctrl+c`; submitting while busy now steers the turn instead of
  dropping the draft.

### Changed
- Tool failures are fed back to the model instead of ending the turn; only
  repeated consecutive failures stop it.
- Denied tool calls get an error result immediately; asks pause per call.
- Repository consolidation: the legacy crush-architecture port under
  `internal/` and `cmd/rakitkode` has been removed. The top-level packages
  (`agent`, `harness`, `llm`, `tools`, …) are the single runtime and the
  only engine surface. Legacy code remains available on the
  `legacy-rakitkode` branch.
- `cmd/rakit` is the only CLI. Version is injected at build time
  (`-ldflags -X main.Version=…`); `make build` produces `bin/rakit`.

### Changed
- TUI: CommandCode-style layout — one activity line above the input (spinner,
  verb, elapsed time, output tokens), a `❯` input prompt, permission mode in
  the footer with `shift+tab` cycling, and `?` for shortcuts.
- TUI: tool blocks use uppercase labels (`READ`, `BASH`, `WRITE`), durations,
  and line-numbered code panes when expanded; thoughts render as
  `⏺ Thought for Ns (ctrl+o)`; turns are separated by a blank line.
- TUI: `/resume` opens a session picker (updated time, state, message count,
  token usage, title) newest-first with type-to-filter; `/resume-turn` keeps
  the interrupted-turn behavior.
- TUI: the input is re-focused automatically after submit, turn end and
  session switches, so no click is needed before typing again.

### Fixed
- CI portability: shell commands now resolve `/bin/zsh` → `/bin/bash` →
  `/bin/sh` (or `cmd.exe` on Windows) instead of assuming zsh exists.
- Windows builds: process-group handling lives in `proc_unix.go` /
  `proc_windows.go`, removing Unix-only `syscall` usage from shared code.
- `search` is now functional in the CLI/engine path. The engine wires the
  workspace into `SearchManager`, and `path` scopes results to a
  subdirectory instead of being dropped by the adapter.
- Tool definitions are emitted in a deterministic registration order.
  `ToolSpec.Order` removes the Go map iteration that randomised the tool
  array on every request — a prerequisite for prompt-cache hits on
  DeepSeek, GLM, OpenAI and Anthropic.

## v0.2.8 — 2026-10-17

### Added
- Local Decision Small Neural Model (`SmallNeuralEngine`):
  - In-process Multi-Layer Perceptron trained via Python (`scripts/train_small_model.py`) and embedded in pure Go (`small_model.go`, `weights_generated.go`).
  - Zero external dependencies: requires no local model server, Ollama, or Python runtime at inference time.
  - Sub-100 microsecond (<0.1 ms) inference for tool category routing, confidence scoring, `needs_tools` probability, and sub-agent role prediction.
  - Automatically activates when Jev API key is not configured or when Jev remote API errors/times out.
- Co-Development Model (`CONTRACT.md`):
  - Updated stability contract to reflect active co-development across sister repositories (`my-agent`), removing artificial frozen backward-compatibility barriers in favor of atomic, lockstep iteration with ForgeADE and KendaliAI.

## v0.2.7 — 2026-10-17

### Added
- Local Smart Decisions Engine (`jev.LocalDecisionEngine`):
  - Local small model integration via OpenAI-compatible endpoints (Ollama, vLLM, LMStudio, e.g. `qwen2.5-coder:0.5b`, `llama3.2:1b`) with sub-second timeout and offline resilience.
  - Sub-millisecond embedded classifier with semantic category vector scoring (`explore`, `read`, `modify`, `execute`, `git`, `web`, `skills`, `mcp`).
  - Proactive tool safety evaluator (`AssessToolSafety`) categorizing operations into Risk levels (Safe, Low, Medium, High, Critical) to protect against destructive commands, path escapes, and credential exfiltration.
  - Automatic failure reflection (`SuggestReflection`): hints injected into tool errors for self-healing loops on syntax errors, diff mismatches, and command failures.
  - Subagent role router scoring candidate suitability.
- Super Harness Capabilities:
  - Model pricing and live cost tracking (`llm.EstimateCost`, `llm.FormatCost`) covering Claude, OpenAI, DeepSeek, Gemini, and local models.
  - Automatic session cost accumulation and reflection in `agent.Session`.
  - Macro-compaction in harness turn loop to prevent context exhaustion when micro-compaction is insufficient.
- TUI: new slash commands `/cost` (display session token breakdown, pricing details, and total estimated expense) and `/diff` (review uncommitted git changes directly within the TUI).
- TUI: status bar and `actionStatus` now display real-time estimated session cost alongside context tokens.
- TUI: structured tool-call blocks rendered Claude-Code style — a colored
  `⏺` headline with a per-tool label and argument echo (`Bash(ls -la)`,
  `Read(file.go)`), a `⎿` result preview (3 lines collapsed, full output via
  `ctrl+o`), durations, and a distinct color for MCP-bridged tools
  (`server:tool`). MCP names are recognized in both `server/tool` and
  `mcp__server__tool` forms.
- TUI: new slash commands — `/mcp` (server table with status dots, tool
  counts, last errors; `on|off <name>` toggling, `reconnect` running async
  off the UI goroutine), `/skills` (listing with adaptive columns;
  `/skills <name>` detail), `/tools` (core + MCP tools grouped by server),
  `/status` (session/model/tokens/tools/env panel), `/compact` (async
  history compaction), `/effort low|medium|high|off` (reasoning-effort hint).
- TUI: header now shows MCP servers up/total + connected tool count and the
  installed-skill count; the status bar shows context-window tokens.
- TUI: rotating working verbs behind the spinner while a turn runs.
- TUI: `shift+enter` inserts a newline on terminals with keyboard-enhancement
  support; `pgup`/`pgdown`/`home`/`end` and mouse-wheel scrolling with a
  stick-to-bottom pin (scroll up to read, back down to resume following).
- Thinking blocks record how long the model reasoned and show
  "✻ thinking for 3s · preview (tab expands)" once sealed.

### Fixed
- TUI: transcript now soft-wraps long lines. The bubbles viewport defaults
  to horizontal scrolling, so one over-long line (e.g. the hint banner)
  shifted every row sideways and truncated the left edge.
- TUI: typing no longer scrolls the transcript. Viewport keys were fed the
  whole key stream, and its default map binds plain letters (`f`/`b`/`d`/
  `j`/`k`/space) to paging — typing "medium" yanked the view. Keys now
  reach the viewport only through the explicit scroll bindings; mouse wheel
  is forwarded directly.
- TUI: MCP header counters accumulated on every session refresh (`mcp 1/4`
  for two servers); they reset per refresh now and only count enabled
  connected servers.
- TUI: `/model` prefix matching hijacked the slash menu for any input
  starting with `/model`-ish text (typing `/m` on the way to `/mcp` was
  safe, but `/modelx` opened the picker); the trigger is now the exact
  command word.
- TUI: `/mcp off <name>` no longer persists a stale `connected` status on
  the disabled server.
- CLI: `rakit chat`/`tui` fall back to the plain line REPL when stdin/stdout
  are not a terminal instead of dying on a bubbletea TTY error.

### Changed
- TUI: model picker rows render the provider dim and the model bright, with
  a `❯` cursor; the command menu uses the same cursor.
- TUI: models display as `ProviderName/model` (e.g. `Kenari/glm-5-3-flash`)
  in the picker, header, `/status`, and the switch echoes — the raw
  provider id (`prov-1788…/model`) stays the functional key underneath and
  is never shown. A session pinned to the bare model name no longer
  duplicates its row in the picker.
- TUI: streaming thinking renders compactly — the `✻ thinking…` header plus
  the last few reasoning lines — instead of dumping the full reasoning and
  scrolling its header off screen. Tab still expands a sealed block to the
  full body.
- TUI: the slash menu windows around the selection (max 8 rows) and its
  height is reserved in the layout, so the footer can no longer clip past
  the window bottom (visible on 24-row terminals); the layout re-splits
  whenever an overlay opens, closes, or filters.
- TUI: the startup banner no longer repeats the app header; it is one hint
  line (`/help commands · /model pick model · …`).

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

## v0.2.3 — 2026-10-17

### Fixed
- Thought scanner: gateways that strip reasoning tags per chunk (deepseek via
  OpenAI-compatible gateways) left orphan `<` characters that leaked into the
  visible assistant text as broken markup. The scanner now drops tag-like
  orphan brackets while preserving legit prose (`x < 10`).

## v0.2.4 — 2026-10-17

### Fixed
- Parallel tool calls lost their arguments ("Arguments provided: null",
  empty `command`). Some gateways re-send the complete tool-call arguments
  JSON on consecutive stream chunks; the accumulators (llm layer and the
  agent's delta assembler) concatenated the duplicates into invalid JSON.
  Merging is now idempotent — a complete JSON object re-send replaces the
  accumulated value, true fragments still concatenate. Hit hardest by
  research sub-agents firing parallel `find`/`search`/`read` calls.

## v0.2.5 — 2026-10-17

### Fixed
- Sub-agent spawn storms: a blocked `invoke_subagent` waited only 120 s, so
  research sub-agents still working looked "truncated/failed" and the parent
  re-spawned duplicates (4 sub-agents for one task). Defaults raised
  (blocked wait 120 s → 300 s, poll 30 s → 120 s), running results now carry
  an explicit `next_step` ("do NOT spawn a duplicate; keep polling"), and the
  system prompt forbids duplicate sub-agents for a running task.

## v0.2.6 — 2026-10-17

### Changed
- Clarified the sub-agent rule: running multiple sub-agents in parallel for
  different subtasks is encouraged (dispatch together in one block); only
  same-task duplicates are forbidden — poll a running sub-agent instead of
  re-spawning it.

## v0.2.2 — 2026-10-17

### Fixed
- Goal tracking: every message's first line was treated as an active "goal",
  which made the system prompt mandate `update_tasks` decomposition even for
  simple greetings ("hi there"). Goals are now tracked only from explicit
  `/goal` directives.
- Persona precedence: the system prompt now states it overrides any
  conflicting persona/identity instructions injected by the model provider
  or gateway (e.g. upstream "MiMo/OpenClaw" prompts served by gateways).

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
