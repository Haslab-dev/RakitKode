// Package brain implements the "living memory" store for the agent engine:
// an append-only, bi-temporal fact log, freeform markdown notes, and
// per-session digests, kept per-project under the engine storage root
// (<root>/brain/<project-slug>/).
//
// Layout:
//
//	facts.jsonl       — one immutable JSON Fact per line, append-only
//	notes/<name>.md   — model-created freeform notes
//	digests/<sid>.md  — per-session digest (objective/requests/files/outcome)
//
// Design rationale (see docs/BRAIN.md):
//   - ADD-only: facts are never rewritten or deleted by an LLM; corrections
//     are new facts (Mem0's community-validated pivot away from LLM rewrites).
//   - Bi-temporal: supersession stamps invalid_at and superseded_by while the
//     original record stays on disk, so "what did we believe and when" is
//     always reconstructible (Graphiti/Zep).
//   - Visible budget: the injected <project_brain> block reports how much of
//     its char budget it used, so hosts and prompts can reason about it
//     (Letta's visible-budget lesson).
//   - Applicability: injection prefers facts relevant to the current turn
//     (shared terms / touched files) over blind recency (arXiv:2505.16067).
package brain

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Haslab-dev/RakitKode/store"
)

// Fact kinds.
const (
	KindDecision    = "decision"
	KindPreference  = "preference"
	KindPitfall     = "pitfall"
	KindConvention  = "convention"
	KindTaskOutcome = "task_outcome"
	KindNoteRef     = "note_ref"
)

const (
	// MaxFactChars bounds a fact's text: a fact must be one self-contained,
	// absolute-timestamped sentence or clause, not a document.
	MaxFactChars = 280

	// DefaultBudgetChars is the default render budget for Brief.
	DefaultBudgetChars = 2400

	// compactThreshold: facts.jsonl is rewritten (dropping only expired
	// invalid records) once it exceeds this many records.
	compactThreshold = 1500

	// compactInvalidWindow: invalid records newer than this stay on disk
	// during compaction so recent history remains reconstructible.
	compactInvalidWindow = 90 * 24 * time.Hour
)

// knownKinds is the closed set of fact kinds AppendFact accepts.
var knownKinds = map[string]bool{
	KindDecision: true, KindPreference: true, KindPitfall: true,
	KindConvention: true, KindTaskOutcome: true, KindNoteRef: true,
}

// Fact is one immutable record in facts.jsonl. Timestamps are absolute
// (RFC3339 in JSON) — relative words like "recently" are banned because they
// rot as time passes.
type Fact struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Text         string    `json:"text"`
	ValidAt      time.Time `json:"valid_at"`
	InvalidAt    time.Time `json:"invalid_at,omitempty"` // zero while valid
	SupersededBy string    `json:"superseded_by,omitempty"`
	Source       string    `json:"source,omitempty"` // turn id + tool
	Refs         []string  `json:"refs,omitempty"`   // file paths / note paths
	Hash         string    `json:"hash"`             // fnv of canonical kind+text
}

// Valid reports whether the fact is currently believed.
func (f Fact) Valid() bool { return f.InvalidAt.IsZero() }

// Date renders the fact's valid_from date (YYYY-MM-DD).
func (f Fact) Date() string { return f.ValidAt.Format("2006-01-02") }

// NoteInfo describes one markdown note.
type NoteInfo struct {
	Path     string    `json:"path"`
	Title    string    `json:"title"`
	Modified time.Time `json:"modified"`
}

// Brain is the per-project living-memory store. All methods are safe for
// concurrent use; heavy I/O belongs on the async consolidation path.
type Brain struct {
	mu     sync.Mutex
	dir    string
	facts  []Fact
	hashes map[string]int // fact hash → index of the newest record with it
	ix     *bm25Index     // BM25 index over the valid facts (nil until loaded)

	// Optional semantic recall (off by default — see embedder.go). When an
	// embedder is attached, BriefFor/Search fuse cosine(query, fact) *
	// embedWeight into the BM25 applicability score. Fact embeddings are
	// cached in the bm25 index (invalidated on supersede/compact); query
	// embeddings are memoized per joined-terms key for the turn.
	embedder      Embedder
	embedWeight   float64
	queryEmb      map[string][]float32
	queryEmbOrder []string
}

// Open loads (or lazily creates) the brain store for a workspace. An empty
// workspace yields a usable in-memory brain with no backing directory —
// callers should usually check the workspace themselves and skip Open.
//
// If an exported pandu_encoder.rkb is present in the brain directory (or
// $PANDU_ENCODER_PATH points at one), semantic recall auto-attaches; without
// the weights file Open leaves the embedder nil and recall stays BM25-only.
func Open(workspace string) (*Brain, error) {
	dir, err := store.ProjectDir("brain", workspace)
	if err != nil {
		return nil, err
	}
	b := &Brain{dir: dir, hashes: make(map[string]int), embedWeight: DefaultEmbeddingWeight}
	b.loadLocked()
	b.mu.Lock()
	if _, err := os.Stat(panduWeightsPath(dir)); err == nil {
		b.attachEmbedderLocked(NewPanduEncoder(dir))
	}
	b.mu.Unlock()
	return b, nil
}

// SetEmbedder attaches an optional embedder for semantic recall (nil detaches
// and restores pure BM25 behavior). The first attach applies
// DefaultEmbeddingWeight; SetEmbeddingWeight overrides it. Embeddings already
// cached under a different embedder are dropped.
func (b *Brain) SetEmbedder(e Embedder) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.attachEmbedderLocked(e)
}

func (b *Brain) attachEmbedderLocked(e Embedder) {
	b.embedder = e
	b.embedWeight = DefaultEmbeddingWeight
	b.queryEmb = nil
	b.queryEmbOrder = nil
	if b.ix != nil {
		b.ix.emb = make(map[string][]float32) // re-embed under the new embedder
	}
}

// SetEmbeddingWeight overrides the cosine weight fused into the applicability
// score (DefaultEmbeddingWeight when unset).
func (b *Brain) SetEmbeddingWeight(w float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.embedWeight = w
}

// Dir returns the backing directory (useful for diagnostics and tests).
func (b *Brain) Dir() string { return b.dir }

func (b *Brain) factsPath() string { return filepath.Join(b.dir, "facts.jsonl") }
func (b *Brain) notesDir() string  { return filepath.Join(b.dir, "notes") }
func (b *Brain) digestsDir() string {
	return filepath.Join(b.dir, "digests")
}

// loadLocked reads facts.jsonl into memory, tolerating a missing file and
// skipping corrupt lines (a torn tail write must not lose the whole log).
func (b *Brain) loadLocked() {
	b.facts = nil
	b.hashes = make(map[string]int)
	f, err := os.Open(b.factsPath())
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var fact Fact
		if err := json.Unmarshal([]byte(line), &fact); err != nil || fact.ID == "" {
			continue
		}
		b.facts = append(b.facts, fact)
		if fact.Hash != "" {
			b.hashes[fact.Hash] = len(b.facts) - 1
		}
	}
	b.ix = newBM25Index()
	for _, f := range b.facts {
		if f.Valid() {
			b.ix.add(f)
		}
	}
}

// factFileHash is the dedup identity of a fact: its kind plus a
// whitespace/case-insensitive canonicalization of its text.
func factFileHash(kind, text string) string {
	canonical := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	h := fnv.New64a()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0x1f})
	_, _ = h.Write([]byte(canonical))
	return fmt.Sprintf("%016x", h.Sum64())
}

// AppendFact adds a fact unless an identical one is already valid.
//
// Dedup rules:
//   - same canonical kind+text and the existing record is valid → no-op,
//     returning the existing id;
//   - same canonical kind+text but the existing record was superseded → the
//     old record is marked superseded by the new one and a fresh record with
//     a new valid_at is appended (re-observations are real signal).
//
// Text longer than MaxFactChars is truncated at a rune boundary. When the
// log outgrows compactThreshold records it is compacted in place.
func (b *Brain) AppendFact(kind, text string, refs []string, source string) (string, error) {
	if !knownKinds[kind] {
		return "", fmt.Errorf("brain: unknown fact kind %q", kind)
	}
	text = truncateRunes(strings.TrimSpace(text), MaxFactChars)
	if text == "" {
		return "", fmt.Errorf("brain: empty fact text")
	}
	hash := factFileHash(kind, text)

	b.mu.Lock()
	defer b.mu.Unlock()

	if idx, ok := b.hashes[hash]; ok && b.facts[idx].Valid() {
		return b.facts[idx].ID, nil
	}

	id := fmt.Sprintf("f%06d", len(b.facts))
	fact := Fact{
		ID:      id,
		Kind:    kind,
		Text:    text,
		ValidAt: time.Now().UTC(),
		Source:  source,
		Refs:    dedupeStrings(refs),
		Hash:    hash,
	}

	// Re-observing a previously superseded fact supersedes the stale record.
	if idx, ok := b.hashes[hash]; ok {
		b.facts[idx].InvalidAt = fact.ValidAt
		b.facts[idx].SupersededBy = id
		if b.ix != nil {
			b.ix.remove(b.facts[idx].ID)
		}
	}

	b.facts = append(b.facts, fact)
	b.hashes[hash] = len(b.facts) - 1
	if b.ix != nil {
		b.ix.add(fact)
	}

	if err := b.persistLocked(); err != nil {
		return "", err
	}
	if len(b.facts) > compactThreshold {
		_ = b.compactLocked()
	}
	return id, nil
}

// Supersede marks a fact invalid. by is the replacing fact id, or a short
// reason tag when no replacement exists (e.g. "manual", "stale"). The record
// itself is never removed — supersession is metadata, not deletion.
func (b *Brain) Supersede(id, by string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.facts {
		if b.facts[i].ID == id {
			if b.facts[i].Valid() {
				b.facts[i].InvalidAt = time.Now().UTC()
				b.facts[i].SupersededBy = by
				if b.ix != nil {
					b.ix.remove(b.facts[i].ID)
				}
				if err := b.persistLocked(); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return fmt.Errorf("brain: fact %q not found", id)
}

// persistLocked rewrites facts.jsonl atomically. Appends are the hot path,
// but a full rewrite keeps supersession metadata correct without a second
// journal; the file stays small (compaction caps it at ~compactThreshold
// records).
func (b *Brain) persistLocked() error {
	if b.dir == "" {
		return nil
	}
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	for _, f := range b.facts {
		line, err := json.Marshal(f)
		if err != nil {
			continue
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return writeFileAtomic(b.factsPath(), []byte(sb.String()), 0o644)
}

// ValidFacts returns copies of all currently-valid facts, oldest first.
func (b *Brain) ValidFacts() []Fact {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Fact, 0, len(b.facts))
	for _, f := range b.facts {
		if f.Valid() {
			out = append(out, f)
		}
	}
	return out
}

// Counts returns (valid, superseded) record counts.
func (b *Brain) Counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	valid, superseded := 0, 0
	for _, f := range b.facts {
		if f.Valid() {
			valid++
		} else {
			superseded++
		}
	}
	return valid, superseded
}

// Search ranks valid facts for a query with a term score over text, refs and
// kind, plus a recency boost. An empty query returns the most recent facts.
func (b *Brain) Search(query string, limit int) []Fact {
	terms := splitTerms(query)
	scoredList, embedWeight := b.validScored(terms)
	type scored struct {
		fact  Fact
		score float64
	}
	list := make([]*scored, 0, len(scoredList))
	for _, sf := range scoredList {
		list = append(list, &scored{fact: sf.fact,
			score: sf.bm25n*6 + sf.sim*embedWeight + factRecency(sf.fact, time.Now()),
		})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].fact.ValidAt.After(list[j].fact.ValidAt)
	})
	if limit <= 0 || limit > len(list) {
		limit = len(list)
	}
	out := make([]Fact, 0, limit)
	for _, s := range list[:limit] {
		out = append(out, s.fact)
	}
	return out
}

// bm25Snapshot returns normalized BM25 relevance (0..1) per fact id for the
// query terms, snapshotted under the lock. Zero means the term query has no
// overlap with that fact — the caller treats it as "no applicability signal".
func (b *Brain) bm25Snapshot(queryTerms []string) map[string]float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ix == nil {
		// Lazily build (brains constructed without Open — tests, hosts that
		// attach a bare &Brain{} — still get BM25 relevance).
		b.ix = newBM25Index()
		for _, f := range b.facts {
			if f.Valid() {
				b.ix.add(f)
			}
		}
	}
	out := make(map[string]float64, len(b.facts))
	for _, f := range b.facts {
		if !f.Valid() {
			continue
		}
		raw := b.ix.score(f.ID, queryTerms)
		if raw > 0 {
			out[f.ID] = raw / (raw + 3) // soft-saturate to (0,1) for fusion
		}
	}
	return out
}

// scoredFact is one valid fact with its normalized BM25 relevance
// (0 = no term signal) and, when semantic recall is on, the clamped cosine
// similarity to the embedded query context (0 = no semantic signal).
type scoredFact struct {
	fact  Fact
	bm25n float64
	sim   float64
}

// validScored returns all valid facts with normalized BM25 scores in ONE
// locked pass — Brief and Search both need exactly this, and the previous
// shape (ValidFacts copy + separate snapshot + internal re-copy) allocated
// megabytes per call at 1500 facts. With an embedder attached it also fills
// the fact-embedding cache (batched, lazily, once per fact) and computes
// per-fact cosine similarities against the embedded query context; the
// returned weight is the fused cosine weight (0 when semantic recall is off,
// making the entire pass cost-free and behavior-identical).
func (b *Brain) validScored(queryTerms []string) ([]scoredFact, float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ix == nil {
		b.ix = newBM25Index()
		for _, f := range b.facts {
			if f.Valid() {
				b.ix.add(f)
			}
		}
	}
	var qemb []float32
	weight := float64(0)
	if b.embedder != nil {
		weight = b.embedWeight
		qemb = b.queryEmbeddingLocked(queryTerms)
	}
	out := make([]scoredFact, 0, len(b.facts))
	var missIDs, missTexts []string
	for _, f := range b.facts {
		if !f.Valid() {
			continue
		}
		n := float64(0)
		if raw := b.ix.score(f.ID, queryTerms); raw > 0 {
			n = raw / (raw + 3)
		}
		out = append(out, scoredFact{fact: f, bm25n: n})
		if qemb != nil {
			if _, ok := b.ix.emb[f.ID]; !ok {
				missIDs = append(missIDs, f.ID)
				missTexts = append(missTexts, f.Text)
			}
		}
	}
	if len(missIDs) > 0 {
		b.fillEmbeddingsLocked(missIDs, missTexts)
	}
	if qemb != nil {
		for i := range out {
			// Clamp at zero: a dissimilar fact must fall back to the
			// canonical order, not sink below the no-signal baseline.
			out[i].sim = max(0, cosineSim(qemb, b.ix.emb[out[i].fact.ID]))
		}
	}
	return out, weight
}

// fillEmbeddingsLocked computes embeddings for cache misses (all under the
// Brain lock; the batched path spreads texts across workers inside the
// embedder). Facts whose embedder returns nothing simply stay uncached and
// retry on the next pass.
func (b *Brain) fillEmbeddingsLocked(ids, texts []string) {
	if b.embedder == nil {
		return
	}
	if be, ok := b.embedder.(BatchEmbedder); ok {
		for i, v := range be.EmbedBatch(texts) {
			if len(v) > 0 {
				b.ix.emb[ids[i]] = v
			}
		}
		return
	}
	for i, t := range texts {
		if v := b.embedder.Embed(t); len(v) > 0 {
			b.ix.emb[ids[i]] = v
		}
	}
}

// queryEmbeddingLocked embeds the joined context terms (the current turn's
// signal, per embedder.go), memoized per joined key so retries within a turn
// are free and the prompt-cache byte-stability invariant holds.
func (b *Brain) queryEmbeddingLocked(queryTerms []string) []float32 {
	if b.embedder == nil {
		return nil
	}
	terms := queryTerms
	if len(terms) > maxQueryEmbedTerms {
		terms = terms[:maxQueryEmbedTerms]
	}
	if len(terms) == 0 {
		return nil
	}
	key := strings.Join(terms, " ")
	if v, ok := b.queryEmb[key]; ok {
		return v
	}
	v := b.embedder.Embed(key)
	if len(v) == 0 {
		return nil
	}
	if b.queryEmb == nil {
		b.queryEmb = make(map[string][]float32, queryEmbCacheSize)
	}
	if len(b.queryEmbOrder) >= queryEmbCacheSize {
		delete(b.queryEmb, b.queryEmbOrder[0])
		b.queryEmbOrder = b.queryEmbOrder[1:]
	}
	b.queryEmb[key] = v
	b.queryEmbOrder = append(b.queryEmbOrder, key)
	return v
}

// splitTerms lowercases and tokenizes a query/context string.
func splitTerms(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r >= 0x80)
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		if len(f) < 3 || seen[f] { // skip noise words and single chars
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// kindPriority ranks kinds for applicability ties: pitfalls are immediate
// warnings for the task at hand, decisions are the standing policy, the
// rest follow kindOrder.
func kindPriority(kind string) int {
	switch kind {
	case KindPitfall:
		return 0
	case KindDecision:
		return 1
	default:
		return 2
	}
}

// kindIndex returns the kind's position in kindOrder (render fallback).
func kindIndex(kind string) int {
	for i, k := range kindOrder {
		if k == kind {
			return i
		}
	}
	return len(kindOrder)
}

// pitfallPriorityBoost is the flat applicability edge given to pitfall
// facts so near-tie warnings surface before standing decisions.
const pitfallPriorityBoost = 0.12

// factPathBoost is the structural part of applicability: a fact whose refs
// intersect the context paths (+3 each, capped once) is directly about what
// the user is working on. Term relevance lives in the BM25 index now.
func factPathBoost(f Fact, contextPaths []string) float64 {
	if len(contextPaths) == 0 {
		return 0
	}
	lowerContext := make([]string, 0, len(contextPaths))
	for _, p := range contextPaths {
		lowerContext = append(lowerContext, strings.ToLower(p))
	}
	for _, r := range f.Refs {
		rl := strings.ToLower(r)
		for _, cp := range lowerContext {
			if cp != "" && (rl == cp || strings.HasSuffix(cp, rl) || strings.HasSuffix(rl, cp)) {
				return 3
			}
		}
	}
	return 0
}

// factRecency adds up to +1.5 for recent facts; used as a tiebreaker.
func factRecency(f Fact, now time.Time) float64 {
	age := now.Sub(f.ValidAt)
	switch {
	case age <= 7*24*time.Hour:
		return 1.5
	case age <= 30*24*time.Hour:
		return 1
	case age <= 90*24*time.Hour:
		return 0.5
	}
	return 0
}

// defaultKindCaps are the per-kind line caps used by Brief when the budget
// map does not override them.
func defaultKindCaps() map[string]int {
	return map[string]int{
		KindDecision:    6,
		KindPitfall:     4,
		KindPreference:  4,
		KindConvention:  4,
		KindTaskOutcome: 3,
		KindNoteRef:     3,
	}
}

// kindOrder is the render order for fact groups (decisions first: they carry
// the most durable intent).
var kindOrder = []string{
	KindDecision, KindPitfall, KindPreference, KindConvention, KindTaskOutcome, KindNoteRef,
}

var kindLabels = map[string]string{
	KindDecision:    "Decisions",
	KindPitfall:     "Pitfalls",
	KindPreference:  "Preferences",
	KindConvention:  "Conventions",
	KindTaskOutcome: "Task outcomes",
	KindNoteRef:     "Notes",
}

// Brief renders the <project_brain> prompt block: valid facts grouped by
// kind (decisions first), capped per kind, within a char budget, with a
// visible-budget header. Returns "" when there is nothing to show.
func (b *Brain) Brief(budget map[string]int) string {
	return b.BriefFor(nil, nil, budget)
}

// BriefFor is Brief with an applicability filter: facts whose refs intersect
// contextPaths or whose text shares terms with contextTerms are preferred.
// contextTerms entries are raw strings (e.g. the current user message); they
// are tokenized internally.
func (b *Brain) BriefFor(contextTerms []string, contextPaths []string, budget map[string]int) string {
	valid, superseded := b.Counts()
	if valid == 0 {
		return ""
	}
	caps := defaultKindCaps()
	for k, v := range budget {
		caps[k] = v
	}

	now := time.Now()
	terms := splitTerms(strings.Join(contextTerms, " "))
	scoredList, embedWeight := b.validScored(terms) // one locked pass: facts + BM25 + cosine
	type sel struct {
		fact    Fact
		overlap float64 // applicability signal; 0 = none
		recency float64
	}
	selected := make([]*sel, 0, len(scoredList))
	usedPerKind := make(map[string]int)
	for _, sf := range scoredList {
		f := sf.fact
		if usedPerKind[f.Kind] >= caps[f.Kind] {
			continue
		}
		usedPerKind[f.Kind]++
		overlap := sf.bm25n*6 + factPathBoost(f, contextPaths) + sf.sim*embedWeight
		if f.Kind == KindPitfall && (sf.bm25n > 0 || factPathBoost(f, contextPaths) > 0 || sf.sim > 0) {
			// Safety-relevant warnings get a small flat priority so
			// near-ties surface them first (BM25's length normalization
			// would otherwise order by document length alone) — but only
			// when the pitfall actually matches the context, lexically or
			// semantically; without a signal the canonical order stands.
			// A clearly stronger match still outranks a weak pitfall.
			overlap += pitfallPriorityBoost
		}
		selected = append(selected, &sel{
			fact:    f,
			overlap: overlap,
			recency: factRecency(f, now),
		})
	}
	// Applicable facts first, then by overlap, then kind priority (immediate
	// warnings beat standing decisions), then recency. Facts with no signal
	// keep the canonical kind order — recency alone must not fake relevance.
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].overlap > 0 != (selected[j].overlap > 0) {
			return selected[i].overlap > 0
		}
		if selected[i].overlap != selected[j].overlap {
			return selected[i].overlap > selected[j].overlap
		}
		if selected[i].overlap > 0 &&
			kindPriority(selected[i].fact.Kind) != kindPriority(selected[j].fact.Kind) {
			return kindPriority(selected[i].fact.Kind) < kindPriority(selected[j].fact.Kind)
		}
		if selected[i].fact.Kind != selected[j].fact.Kind {
			return kindIndex(selected[i].fact.Kind) < kindIndex(selected[j].fact.Kind)
		}
		return selected[i].recency > selected[j].recency
	})

	// Render grouped by kind, honoring the char budget. Section order
	// follows each kind's best applicability score (the most relevant
	// warning surfaces above standing policy), with kindOrder as the
	// tiebreak for unscored groups.
	var sb strings.Builder
	sb.WriteString("\n<project_brain>\n")
	const budgetLine = "facts: %d valid / %d superseded · budget: %d/%d chars\n"
	// Reserve header space; the final header is spliced in once usedChars is known.
	sb.WriteString("(header)\n\n")
	linesByKind := make(map[string][]string)
	bestScoreByKind := make(map[string]float64)
	used := 0
	omitted := 0
	for _, s := range selected {
		f := s.fact
		line := fmt.Sprintf("• %s (%s)\n", f.Text, f.Date())
		if used+len(line) > DefaultBudgetChars {
			omitted++
			continue
		}
		if s.overlap > bestScoreByKind[f.Kind] {
			bestScoreByKind[f.Kind] = s.overlap
		}
		linesByKind[f.Kind] = append(linesByKind[f.Kind], line)
		used += len(line)
	}
	sectionOrder := make([]string, 0, len(linesByKind))
	for kind := range linesByKind {
		sectionOrder = append(sectionOrder, kind)
	}
	sort.SliceStable(sectionOrder, func(i, j int) bool {
		si, sj := bestScoreByKind[sectionOrder[i]], bestScoreByKind[sectionOrder[j]]
		if si != sj {
			return si > sj
		}
		// No applicability signal anywhere: keep the canonical order.
		// With a signal, immediate warnings (pitfalls) surface first.
		if si > 0 && kindPriority(sectionOrder[i]) != kindPriority(sectionOrder[j]) {
			return kindPriority(sectionOrder[i]) < kindPriority(sectionOrder[j])
		}
		return kindIndex(sectionOrder[i]) < kindIndex(sectionOrder[j])
	})
	for _, kind := range sectionOrder {
		lines := linesByKind[kind]
		if len(lines) == 0 {
			continue
		}
		section := "## " + kindLabels[kind] + "\n"
		if used+len(section) > DefaultBudgetChars {
			omitted += len(lines)
			continue
		}
		sb.WriteString(section)
		used += len(section)
		for _, l := range lines {
			sb.WriteString(l)
		}
	}
	if omitted > 0 {
		omittedLine := fmt.Sprintf("(+%d more facts omitted — search_brain for the rest)\n", omitted)
		if used+len(omittedLine) <= DefaultBudgetChars {
			sb.WriteString(omittedLine)
			used += len(omittedLine)
		}
	}
	sb.WriteString("</project_brain>\n")

	out := sb.String()
	// Splice in the real header with the visible budget numbers.
	header := fmt.Sprintf(budgetLine, valid, superseded, used, DefaultBudgetChars)
	out = strings.Replace(out, "(header)\n", header, 1)
	return out
}

// Compact rewrites facts.jsonl, keeping every valid fact plus invalid facts
// whose invalid_at is within compactInvalidWindow. Older invalid records are
// dropped — they were superseded long ago and are the only safe thing to
// forget. Valid facts are never dropped.
func (b *Brain) Compact() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.compactLocked()
}

func (b *Brain) compactLocked() error {
	cutoff := time.Now().UTC().Add(-compactInvalidWindow)
	kept := make([]Fact, 0, len(b.facts))
	for _, f := range b.facts {
		if f.Valid() || f.InvalidAt.After(cutoff) {
			kept = append(kept, f)
		}
	}
	if len(kept) == len(b.facts) {
		return nil
	}
	b.facts = kept
	b.hashes = make(map[string]int)
	b.ix = newBM25Index()
	for _, f := range b.facts {
		if f.Valid() {
			b.ix.add(f)
		}
	}
	for i, f := range b.facts {
		if f.Hash != "" {
			b.hashes[f.Hash] = i
		}
	}
	return b.persistLocked()
}

// WriteNote stores a freeform markdown note and appends a note_ref fact so
// the note is discoverable from the fact log.
func (b *Brain) WriteNote(title, body string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("brain: note title required")
	}
	slug := slugify(title)
	if slug == "" {
		slug = "note"
	}
	name := fmt.Sprintf("%s-%s.md", slug, time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(b.notesDir(), name)
	content := fmt.Sprintf("# %s\n\n%s\n", title, strings.TrimRight(body, "\n"))
	if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	_, _ = b.AppendFact(KindNoteRef, truncateRunes(title+": "+firstLine(body), MaxFactChars), []string{path}, "write_note")
	return path, nil
}

// Notes lists markdown notes, newest first.
func (b *Brain) Notes() []NoteInfo {
	entries, err := os.ReadDir(b.notesDir())
	if err != nil {
		return nil
	}
	out := make([]NoteInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, NoteInfo{
			Path:     filepath.Join(b.notesDir(), e.Name()),
			Title:    strings.TrimSuffix(e.Name(), ".md"),
			Modified: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// WriteDigest persists the digest markdown for a session.
func (b *Brain) WriteDigest(sessionID, content string) error {
	if sessionID == "" {
		return fmt.Errorf("brain: session id required")
	}
	return writeFileAtomic(filepath.Join(b.digestsDir(), sessionID+".md"), []byte(content), 0o644)
}

// ReadDigest returns the digest for a session ("" when none exists).
func (b *Brain) ReadDigest(sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("brain: session id required")
	}
	data, err := os.ReadFile(filepath.Join(b.digestsDir(), sessionID+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// LatestDigest returns the most recently written digest (id, content).
func (b *Brain) LatestDigest() (string, string, error) {
	entries, err := os.ReadDir(b.digestsDir())
	if err != nil {
		return "", "", nil
	}
	var best os.DirEntry
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == nil || info.ModTime().After(bestTime) {
			best, bestTime = e, info.ModTime()
		}
	}
	if best == nil {
		return "", "", nil
	}
	data, err := os.ReadFile(filepath.Join(b.digestsDir(), best.Name()))
	if err != nil {
		return "", "", err
	}
	return strings.TrimSuffix(best.Name(), ".md"), string(data), nil
}

// writeFileAtomic writes data via temp file + rename so readers never see a
// torn file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".brain-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncateRunes(s, 160)
}

func slugify(s string) string {
	var sb strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x80:
			sb.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				sb.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(sb.String(), "-")
}
