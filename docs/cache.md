# Prompt caching

RakitKode keeps the request prefix byte-stable across steps so provider-side
prefix caching can hit. This matters most for DeepSeek and GLM, whose caching
is automatic and requires no request flags — only an identical prefix.

## What is cached

The request is split into two parts:

1. **Stable prefix** (system message): identity and rules, project conventions
   (`AGENTS.md`), sandbox paths, skills catalog, memory, plugin prompts.
2. **Volatile tail** (trailing user `<system-reminder>`): git status, tool
   budget, task ledger/todos, background and sub-agent notifications, context
   summary, plan state.

Only the volatile tail changes between steps. Earlier messages are never
rewritten; the transcript is kept whole and only cut in large chunks after a
real context-overflow error.

Tool definitions are emitted in registration order (`ToolSpec.Order`) and
plugin prompts / summary file lists are sorted, so repeated requests serialize
identically.

## Provider behaviour

| Provider | Caching | What rakitkode does |
|---|---|---|
| DeepSeek | automatic prefix cache in 64-token blocks | stable prefix; parses `prompt_cache_hit_tokens` / `miss` |
| GLM / Zhipu | automatic context cache | stable prefix; parses `prompt_tokens_details.cached_tokens`; `ZHIPU_API_KEY` / `GLM_API_KEY` |
| OpenAI | automatic prefix cache | `prompt_cache_key` = session id; `stream_options.include_usage`; parses nested `cached_tokens` |
| Anthropic | explicit `cache_control` breakpoints | four breakpoints: last tool, last system block, previous-turn tail, current tail |
| OpenRouter | upstream-dependent | `x-session-affinity`; `cache_control` only for Claude/Qwen |

Usage is normalized into `TokenStats.CachedTokens`, so the TUI and
`rakit doctor` show one hit rate regardless of provider shape.

## Measuring

- TUI status line: `ctx N% (used/window)` and `cache N%`.
- `rakit doctor` shows providers, MCP, hooks, permissions and task counts.
- `RAKIT_DEBUG=1` logs request errors and retries.
- `/cost` shows the session token breakdown and estimated cost.

## Rules for contributors

1. Never put timestamps, UUIDs or per-step counters in the stable prefix.
2. Keep tool schemas and tool ordering deterministic.
3. Do not slide the transcript window one message per turn.
4. Add a prefix-hash test when changing prompt assembly.
5. Compaction is a deliberate event: log the expected full miss and re-anchor
   cache breakpoints afterwards.
