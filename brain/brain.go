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
}

// Open loads (or lazily creates) the brain store for a workspace. An empty
// workspace yields a usable in-memory brain with no backing directory —
// callers should usually check the workspace themselves and skip Open.
func Open(workspace string) (*Brain, error) {
	dir, err := store.ProjectDir("brain", workspace)
	if err != nil {
		return nil, err
	}
	b := &Brain{dir: dir, hashes: make(map[string]int)}
	b.loadLocked()
	return b, nil
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
	}

	b.facts = append(b.facts, fact)
	b.hashes[hash] = len(b.facts) - 1

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
	valid := b.ValidFacts()
	type scored struct {
		fact  Fact
		score float64
	}
	list := make([]scored, 0, len(valid))
	for _, f := range valid {
		list = append(list, scored{fact: f, score: factOverlap(f, terms, nil) + factRecency(f, time.Now())})
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

// containsTerm reports whether haystack contains the term, allowing the
// naive singular/plural forms to match each other ("deadlock" hits
// "deadlocks") so tiny wording differences do not zero out applicability.
func containsTerm(haystack, term string) bool {
	if strings.Contains(haystack, term) {
		return true
	}
	if strings.HasSuffix(term, "s") && strings.Contains(haystack, term[:len(term)-1]) {
		return true
	}
	return strings.Contains(haystack, term+"s")
}

// factOverlap is the applicability component of a fact's score: term hits
// (+2 in text, +1 in refs/kind) and context-path intersections (+3).
// Zero means "no signal" — the fact is then ordered canonically instead of
// by applicability, so the recency baseline never fakes relevance.
func factOverlap(f Fact, terms []string, contextPaths []string) float64 {
	text := strings.ToLower(f.Text)
	refJoin := strings.ToLower(strings.Join(f.Refs, " ") + " " + f.Kind)
	var score float64
	for _, t := range terms {
		if containsTerm(text, t) {
			score += 2
		}
		if containsTerm(refJoin, t) {
			score += 1
		}
	}
	lowerContext := make([]string, 0, len(contextPaths))
	for _, p := range contextPaths {
		lowerContext = append(lowerContext, strings.ToLower(p))
	}
	for _, r := range f.Refs {
		rl := strings.ToLower(r)
		for _, cp := range lowerContext {
			if cp != "" && (rl == cp || strings.HasSuffix(cp, rl) || strings.HasSuffix(rl, cp)) {
				score += 3
				break
			}
		}
	}
	return score
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
	ranked := b.Search("", 0) // all valid, recency-ranked baseline
	type sel struct {
		fact    Fact
		overlap float64 // applicability signal; 0 = none
		recency float64
	}
	selected := make([]sel, 0, len(ranked))
	usedPerKind := make(map[string]int)
	for _, f := range ranked {
		if usedPerKind[f.Kind] >= caps[f.Kind] {
			continue
		}
		usedPerKind[f.Kind]++
		selected = append(selected, sel{
			fact:    f,
			overlap: factOverlap(f, terms, contextPaths),
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
