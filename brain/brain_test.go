package brain

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Haslab-dev/RakitKode/store"
)

func newTestBrain(t *testing.T) *Brain {
	t.Helper()
	// Isolate the global store root per test: Open resolves the brain dir
	// through store.ProjectDir.
	store.SetRoot(t.TempDir())
	ws := t.TempDir()
	b, err := Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return b
}

func TestAppendAndRoundTrip(t *testing.T) {
	store.SetRoot(t.TempDir())
	ws := t.TempDir()
	b, err := Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := b.AppendFact(KindDecision, "Use SQLite for the settings store", []string{"/ws/store.go"}, "test:1")
	if err != nil {
		t.Fatalf("AppendFact: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	// Re-open the same project: the record must survive.
	b2, err := Open(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	valid := b2.ValidFacts()
	if len(valid) != 1 {
		t.Fatalf("expected 1 valid fact after reload, got %d", len(valid))
	}
	if valid[0].ID != id || valid[0].Text != "Use SQLite for the settings store" || valid[0].Kind != KindDecision {
		t.Fatalf("round-trip mismatch: %+v", valid[0])
	}
	if valid[0].Hash == "" {
		t.Fatal("expected hash to be set")
	}
	if valid[0].ValidAt.IsZero() {
		t.Fatal("expected absolute timestamp, got zero")
	}
	if len(valid[0].Refs) != 1 || valid[0].Refs[0] != "/ws/store.go" {
		t.Fatalf("refs mismatch: %+v", valid[0].Refs)
	}
}

func TestAppendDedupSameHash(t *testing.T) {
	b := newTestBrain(t)
	id1, err := b.AppendFact(KindPreference, "Always run gofmt before commits", nil, "test:1")
	if err != nil {
		t.Fatalf("append 1: %v", err)
	}
	// Same canonical text (whitespace/case differences collapse) while valid → no-op.
	id2, err := b.AppendFact(KindPreference, "  always   run GoFMT before commits ", nil, "test:2")
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected dedup no-op (same id), got %q vs %q", id1, id2)
	}
	valid, superseded := b.Counts()
	if valid != 1 || superseded != 0 {
		t.Fatalf("expected 1 valid / 0 superseded, got %d/%d", valid, superseded)
	}
	// Different kind → different hash → separate fact.
	if _, err := b.AppendFact(KindDecision, "Always run gofmt before commits", nil, "test:3"); err != nil {
		t.Fatalf("append 3: %v", err)
	}
	if valid, _ = b.Counts(); valid != 2 {
		t.Fatalf("expected 2 valid facts across kinds, got %d", valid)
	}
}

func TestAppendReobservationSupersedesOld(t *testing.T) {
	b := newTestBrain(t)
	id1, _ := b.AppendFact(KindDecision, "Deploy with blue-green releases", nil, "test:1")
	if err := b.Supersede(id1, "manual"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	// Same canonical text re-observed later → a fresh record with a new
	// valid_at; the old record is marked superseded, not deleted.
	id2, err := b.AppendFact(KindDecision, "Deploy with blue-green releases", nil, "test:2")
	if err != nil {
		t.Fatalf("re-append: %v", err)
	}
	if id1 == id2 {
		t.Fatal("expected a new record id for re-observation")
	}
	valid := b.ValidFacts()
	if len(valid) != 1 || valid[0].ID != id2 {
		t.Fatalf("expected only the new record valid, got %+v", valid)
	}
	old, ok := findByID(b, id1)
	if !ok {
		t.Fatal("old record must remain on disk")
	}
	if old.Valid() || old.SupersededBy != id2 {
		t.Fatalf("old record not marked superseded: %+v", old)
	}
}

func TestSupersede(t *testing.T) {
	b := newTestBrain(t)
	id, _ := b.AppendFact(KindConvention, "Commits follow conventional commits", nil, "test:1")
	if err := b.Supersede(id, "f999999"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if valid, superseded := b.Counts(); valid != 0 || superseded != 1 {
		t.Fatalf("expected 0 valid / 1 superseded, got %d/%d", valid, superseded)
	}
	if err := b.Supersede("f-nonexistent", "x"); err == nil {
		t.Fatal("expected error superseding unknown id")
	}
	// Double supersede is a no-op that keeps the first supersession stamp.
	first, _ := findByID(b, id)
	_ = b.Supersede(id, "again")
	second, _ := findByID(b, id)
	if !first.InvalidAt.Equal(second.InvalidAt) {
		t.Fatalf("supersession stamp changed on re-supersede: %v vs %v", first.InvalidAt, second.InvalidAt)
	}
}

func TestCompactKeepsValidAndRecentInvalid(t *testing.T) {
	b := newTestBrain(t)
	keep1, _ := b.AppendFact(KindDecision, "Keep the single-binary architecture", nil, "t:1")
	keep2, _ := b.AppendFact(KindDecision, "Use semantic token styling in UI", nil, "t:2")
	_, _ = b.AppendFact(KindDecision, "Use the legacy renderer", nil, "t:3")
	dropID, _ := b.AppendFact(KindPreference, "Prefer table-driven tests", nil, "t:4")

	// Invalidate one record long beyond the window and one inside it —
	// directly under the lock (Supersede would deadlock while held).
	now := time.Now().UTC()
	b.mu.Lock()
	ancient := &b.facts[2]
	ancient.ValidAt = now.Add(-180 * 24 * time.Hour)
	ancient.InvalidAt = now.Add(-120 * 24 * time.Hour)
	ancient.SupersededBy = keep1
	dropped := &b.facts[3]
	dropped.ValidAt = now.Add(-2 * 24 * time.Hour)
	dropped.InvalidAt = now.Add(-time.Minute)
	dropped.SupersededBy = keep2
	if len(b.facts) != 4 {
		b.mu.Unlock()
		t.Fatalf("expected 4 records before compact, got %d", len(b.facts))
	}
	if err := b.compactLocked(); err != nil {
		b.mu.Unlock()
		t.Fatalf("compact: %v", err)
	}
	after := append([]Fact(nil), b.facts...)
	b.mu.Unlock()

	if len(after) != 3 {
		t.Fatalf("expected 3 records after compact (ancient invalid dropped), got %d", len(after))
	}
	ids := map[string]bool{}
	for _, f := range after {
		ids[f.ID] = true
	}
	if !ids[keep1] || !ids[keep2] || !ids[dropID] {
		t.Fatalf("recent records lost in compaction: %v", ids)
	}
	valid := b.ValidFacts()
	if len(valid) != 2 {
		t.Fatalf("expected 2 valid facts after compact, got %d", len(valid))
	}
}

func TestBriefBudgetAndHeader(t *testing.T) {
	b := newTestBrain(t)
	for i := 0; i < 10; i++ {
		_, err := b.AppendFact(KindDecision, fmt.Sprintf("Decision number %d: ship the feature behind a flag", i), nil, "t")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	_, _ = b.AppendFact(KindPitfall, "Rebuilding the index inside a read transaction deadlocks", nil, "t")
	// Cap decisions at 3 → only 3 decision lines + 1 pitfall line.
	out := b.Brief(map[string]int{KindDecision: 3, KindPitfall: 4})
	if !strings.Contains(out, "<project_brain>") || !strings.Contains(out, "</project_brain>") {
		t.Fatalf("missing project_brain tags:\n%s", out)
	}
	if !strings.Contains(out, "budget: ") || !strings.Contains(out, "/2400 chars") {
		t.Fatalf("missing visible-budget header:\n%s", out)
	}
	if !strings.Contains(out, "facts: 11 valid / 0 superseded") {
		t.Fatalf("missing fact counts header:\n%s", out)
	}
	if got := strings.Count(out, "Decision number"); got != 3 {
		t.Fatalf("expected 3 decision lines (cap), got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "deadlocks") {
		t.Fatalf("pitfall line missing:\n%s", out)
	}
	// Decisions render before pitfalls.
	if strings.Index(out, "Decision number") > strings.Index(out, "deadlocks") {
		t.Fatal("decisions must render before pitfalls")
	}
	// Header budget number must not exceed the default cap.
	idx := strings.Index(out, "budget: ")
	used := out[idx+len("budget: "):]
	used = used[:strings.IndexByte(used, '/')]
	if n := atoi(t, used); n > DefaultBudgetChars {
		t.Fatalf("budget used %d exceeds cap %d", n, DefaultBudgetChars)
	}
	// Zero caps render the header but no fact lines.
	tiny := b.Brief(map[string]int{
		KindDecision: 0, KindPitfall: 0, KindPreference: 0,
		KindConvention: 0, KindTaskOutcome: 0, KindNoteRef: 0,
	})
	if strings.Contains(tiny, "• ") {
		t.Fatalf("zero caps must render no fact lines:\n%s", tiny)
	}
}

func TestBriefEmptyReturnsEmpty(t *testing.T) {
	b := newTestBrain(t)
	if out := b.Brief(nil); out != "" {
		t.Fatalf("expected empty brief with no facts, got %q", out)
	}
}

func TestBriefDeterministicBytes(t *testing.T) {
	b := newTestBrain(t)
	_, _ = b.AppendFact(KindDecision, "Determinism matters for prompt caching", nil, "t:1")
	_, _ = b.AppendFact(KindPitfall, "Random ordering breaks prefix caches", nil, "t:2")
	a := b.Brief(nil)
	time.Sleep(5 * time.Millisecond)
	c := b.Brief(nil)
	if a != c {
		t.Fatalf("Brief not byte-stable:\n---\n%s\n---\n%s", a, c)
	}
}

func TestSearchRankingAndRecency(t *testing.T) {
	b := newTestBrain(t)
	oldID, _ := b.AppendFact(KindDecision, "Use fnv hashing for brain dedup", nil, "t:1")
	_, _ = b.AppendFact(KindPreference, "Prefer short feedback loops in reviews", nil, "t:2")
	recentID, _ := b.AppendFact(KindPitfall, "fnv hashing collides when text differs only in unicode punctuation", nil, "t:3")

	// Make the first fact old so recency ordering is observable.
	b.mu.Lock()
	b.facts[0].ValidAt = time.Now().UTC().Add(-200 * 24 * time.Hour)
	b.mu.Unlock()

	// Term ranking: both "fnv hashing" facts surface, and the pitfall (recent,
	// double term hits) outranks the old decision.
	results := b.Search("fnv hashing", 8)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].ID != recentID {
		t.Fatalf("expected recent pitfall to rank first, got %s (%s)", results[0].ID, results[0].Text)
	}
	foundOld := false
	for _, f := range results {
		if f.ID == oldID {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatal("old matching fact missing from results")
	}
	// Empty query = recency baseline, honoring limit.
	if got := b.Search("", 1); len(got) != 1 {
		t.Fatalf("expected limit=1, got %d", len(got))
	}
	// No term matches → still returns the recency baseline.
	if got := b.Search("zzzqqq", 8); len(got) != 3 {
		t.Fatalf("expected all 3 as recency baseline, got %d", len(got))
	}
}

func TestSearchRespectsValidity(t *testing.T) {
	b := newTestBrain(t)
	id, _ := b.AppendFact(KindDecision, "Retired: deploy on Fridays", nil, "t:1")
	_, _ = b.AppendFact(KindDecision, "Deploy on Tuesdays with canary checks", nil, "t:2")
	if err := b.Supersede(id, "t:2"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	for _, f := range b.Search("deploy", 8) {
		if f.ID == id {
			t.Fatalf("superseded fact leaked into search results: %+v", f)
		}
	}
}

func TestWriteNoteAndList(t *testing.T) {
	b := newTestBrain(t)
	path, err := b.WriteNote("Research findings", "arXiv:2505.16067 argues memory systems need applicability signals.")
	if err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "arXiv:2505.16067") {
		t.Fatalf("note content missing: %v", err)
	}
	notes := b.Notes()
	if len(notes) != 1 || !strings.Contains(notes[0].Title, "research-findings") {
		t.Fatalf("Notes() mismatch: %+v", notes)
	}
	// A note_ref fact was recorded with the note path as its ref.
	valid := b.ValidFacts()
	if len(valid) != 1 || valid[0].Kind != KindNoteRef {
		t.Fatalf("expected note_ref fact, got %+v", valid)
	}
	if len(valid[0].Refs) != 1 || valid[0].Refs[0] != path {
		t.Fatalf("note_ref refs mismatch: %+v", valid[0].Refs)
	}
}

func TestDigests(t *testing.T) {
	b := newTestBrain(t)
	if err := b.WriteDigest("sess-1", "# Session digest — alpha"); err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}
	content, err := b.ReadDigest("sess-1")
	if err != nil || !strings.Contains(content, "alpha") {
		t.Fatalf("ReadDigest: %v %q", err, content)
	}
	if content, _ := b.ReadDigest("missing"); content != "" {
		t.Fatalf("expected empty digest for unknown session, got %q", content)
	}
	if err := b.WriteDigest("sess-2", "# Session digest — beta"); err != nil {
		t.Fatalf("WriteDigest 2: %v", err)
	}
	id, latest, err := b.LatestDigest()
	if err != nil || id != "sess-2" || !strings.Contains(latest, "beta") {
		t.Fatalf("LatestDigest: id=%s err=%v content=%q", id, err, latest)
	}
}

func TestAppendFactValidation(t *testing.T) {
	b := newTestBrain(t)
	if _, err := b.AppendFact("gossip", "text", nil, ""); err == nil {
		t.Fatal("unknown kind must error")
	}
	if _, err := b.AppendFact(KindNoteRef, "", nil, ""); err == nil {
		t.Fatal("empty text must error")
	}
	// Long text is truncated to MaxFactChars.
	long := strings.Repeat("word ", 200)
	id, err := b.AppendFact(KindNoteRef, long, nil, "t")
	if err != nil {
		t.Fatalf("long append: %v", err)
	}
	f, _ := findByID(b, id)
	if len([]rune(f.Text)) > MaxFactChars {
		t.Fatalf("fact not truncated: %d runes", len([]rune(f.Text)))
	}
}

func TestBriefForPrefersApplicable(t *testing.T) {
	b := newTestBrain(t)
	_, _ = b.AppendFact(KindDecision, "The renderer keeps state in a ring buffer", nil, "t:1")
	_, _ = b.AppendFact(KindDecision, "Lunch policy: Friday catered", nil, "t:2")
	out := b.BriefFor([]string{"ring buffer state in renderer.go"}, []string{"/ws/renderer.go"}, nil)
	iRing := strings.Index(out, "ring buffer")
	iLunch := strings.Index(out, "Lunch policy")
	if iRing < 0 || iLunch < 0 {
		t.Fatalf("expected both facts in brief:\n%s", out)
	}
	if iRing > iLunch {
		t.Fatal("applicable fact must render before unrelated facts in its group")
	}
}

func TestCompactThresholdOnAppend(t *testing.T) {
	b := newTestBrain(t)
	b.mu.Lock()
	// Stuff the log past the threshold with old invalid records.
	base := time.Now().UTC().Add(-compactInvalidWindow - 24*time.Hour)
	for i := 0; i < compactThreshold+10; i++ {
		b.facts = append(b.facts, Fact{
			ID: fmt.Sprintf("f%06d", i), Kind: KindNoteRef,
			Text:      fmt.Sprintf("stale record %d", i),
			ValidAt:   base.Add(time.Duration(i) * time.Second),
			InvalidAt: base.Add(time.Duration(i+1) * time.Second),
			Hash:      fmt.Sprintf("h%04d", i),
		})
	}
	b.mu.Unlock()
	// The next append crosses the threshold and compacts in place.
	if _, err := b.AppendFact(KindDecision, "Append triggers compaction", nil, "t"); err != nil {
		t.Fatalf("append: %v", err)
	}
	valid, _ := b.Counts()
	if valid != 1 {
		t.Fatalf("expected exactly the new valid fact after auto-compact, got %d", valid)
	}
	b.mu.Lock()
	n := len(b.facts)
	b.mu.Unlock()
	if n > compactThreshold {
		t.Fatalf("log not compacted: %d records", n)
	}
}

// --- helpers ---

func findByID(b *Brain, id string) (Fact, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, f := range b.facts {
		if f.ID == id {
			return f, true
		}
	}
	return Fact{}, false
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("non-numeric %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
