# Brain — persistent project memory

The brain is RakitKode's cross-session memory: a per-project store of durable
facts, session digests, and freeform notes that lets every NEW session start
with accumulated understanding of the project and the way you work with it.

It is deliberately **not** a vector database, a cloud service, or an LLM-driven
rewrite pipeline. Every design choice below is backed by empirical evidence
from shipping agent-memory systems (citations inline).

## Store layout

Everything lives under the established per-project store:
`store.ProjectDir("brain", workspace)` → `<RAKIT_HOME>/brain/<project-slug>/`

```
brain/<project-slug>/
├── facts.jsonl        # append-only immutable fact records
├── notes/             # freeform markdown notes (model-written)
└── digests/           # one markdown file per session
    └── <session-id>.md
```

### Fact records

```json
{"id":"f-3f2a","kind":"decision","text":"Keep retry caps at five attempts",
 "valid_at":"2026-11-16T09:00:00Z","invalid_at":"","superseded_by":"",
 "source":"t:12","refs":["internal/uploader.go"],"hash":"a91f…"}
```

- **ADD-only.** Facts are immutable observations. Contradictions never
  delete: the old fact gets `invalid_at` + `superseded_by` and stays on disk
  for point-in-time reasoning (bi-temporal, the Graphiti/Zep design without
  the graph database).
- **No LLM in the write path.** Facts are mined deterministically from the
  turn record (tool trace, transcript patterns) — Graphiti measured 74.7% of
  its ingestion cost in LLM entity extraction and replaced most of it with
  deterministic extraction in MCP 1.0. Mem0 retreated from LLM-decided
  UPDATE/DELETE after it silently destroyed correct memories (#1674, #2875).
- Facts that are derivable from reading the code are **not stored** (the
  Claude Code auto-memory rule): the graph only keeps what you cannot re-derive
  — decisions, pitfalls, preferences, conventions, outcomes.

## Lifecycle

```
turn start            → <project_brain> block computed ONCE (cached-prefix
  (runAgentTurn)        safe: byte-identical across steps of the same turn)
       │
  loop step 1..N      → loop/alarm signals in the volatile <system-reminder>
       │
turn end (finishTurn) → go brain.ConsolidateTurn(...)   [deterministic]
       │                 mine facts from tool trace + transcript
       │                 write digests/<session>.md
session end           → SessionEnd hook fires
compaction            → PreCompact hook fires (can block); the context
                        summary is the digest seed
```

### Injection (`<project_brain>`)

```
<project_brain>
facts: 2 valid / 0 superseded · budget: 140/2400 chars

## Pitfalls
• Reading the index while rebuilding it deadlocks (2026-11-16)
## Decisions
• Keep retry caps at five attempts (2026-11-16)
</project_brain>
```

- **Bounded** (2400 chars default) with a **visible budget header** — Letta
  renders `chars_current/chars_limit` into context so the model self-regulates.
- **Index-then-fetch, never inject-all** (Goose's inject-all recall is the
  canonical bloat failure). Details live in notes/digests, read on demand via
  `read_digest` / ordinary file tools.
- **Applicability filter**: facts sharing terms with the current user message
  or touching its file paths rank first; pitfalls outrank decisions at equal
  applicability (immediate warnings first); everything unscored keeps the
  canonical order. No user message → no injection at all — the
  experience-following study (arXiv:2505.16067) shows untargeted recall can
  actively mislead.
- **Cache invariant**: the block is computed once per turn, never per step, so
  the provider prompt-cache prefix stays byte-identical.

### Consolidation (end of turn, deterministic)

`ConsolidateTurn` mines the finished turn's record:

| Signal | → Fact kind |
|---|---|
| "decided to / going with / switched to / will use X instead of Y" | `decision` |
| tool failure followed by a corrective message | `pitfall` |
| "always / never / prefer / use X instead" (existing regex miner) | `preference` |
| files touched by tools | `refs` on the turn's facts |
| session goal + outcome | `digests/<session-id>.md` |

Dedup is content-hash based; identical re-observations are no-ops. Compaction
promotes the `<CONTEXT_SUMMARY>` into the digest before it is discarded.

## Tools

| Tool | Purpose |
|---|---|
| `write_note(title, body)` | persist a freeform note (also indexed as a note ref) |
| `search_brain(query)` | top valid facts for a query (term + recency ranked) |
| `read_digest(session_id?)` | read a session digest (latest if omitted) |

`learn_memory` / `recall_memory` (the flat keyword store) remain available;
the brain is the ranked, temporal layer above it.

## Orchestration signals

Pure rules, no model calls:

- **Thrash alarm** — ≥3 consecutive tool failures or ≥3 identical
  tool+argument repetitions emit `agent:loop_warning` and a nudge in the
  system reminder ("delegate to a subagent or change approach"). Same
  threshold as Pandu-Micro's stuck head (`p_stuck > 0.5 ∨ fails ≥ 3`).
- **Phase tracking** — discovery / planning / implementation / debugging /
  verification / wrapup, derived from the last tools + task statuses;
  surfaced to the orchestrator context.

## Design rationale (citations)

- **ADD-only + supersession** — Mem0 abandoned LLM-decided mutations after
  wrong deletions (mem0ai/mem0#1674, #2875); additive writes + explicit CRUD
  is their shipped answer.
- **Bi-temporal without a graph DB** — Zep/Graphiti invalidate overlapping
  facts (`invalid_at = new.valid_at`) preserving point-in-time queries
  (arXiv:2501.13956); deterministic fast paths skip the LLM for verbatim
  matches.
- **Visible budgets** — Letta renders per-block `chars_current/chars_limit`
  into the prompt (MemGPT OS-for-context lineage, arXiv:2310.08560).
- **File + JSON as the substrate** — Letta's current memory end-state is a git
  repository of Markdown files with zero vector index in the active path
  (MemFS); every JSONL append is an audit record and git history is the undo.
- **Review surface** — Cursor shipped opaque sidecar memories and removed the
  feature within five months; durable team knowledge belongs in
  reviewable/committed files. In ForgeADE, learned facts surface in the
  turn-status card rather than silently accumulating.
- **No blind recall** — "How Memory Management Impacts LLM Agents"
  (arXiv:2505.16067): agents blindly follow recalled experiences even when
  inapplicable; selective, context-filtered recall outperforms full retrieval.
- **Compaction is cost control, not durability** — Roo Code's own
  "context poisoning" doc: summarization cannot remove bad context; durable
  state lives in files, poisoned sessions end with a reset.
