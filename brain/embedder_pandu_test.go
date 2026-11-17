package brain

// Tests for the optional Pandu-Micro semantic recall layer (embedder.go).
//
// The parity test against the Python-exported golden vectors skips unless
// the artifacts are present: set PANDU_GOLDEN to the path of
// pandu_encoder_golden.json (its sibling pandu_encoder.rkb is loaded
// automatically, or override with PANDU_ENCODER_RKB). The helper also checks
// the conventional sibling-repo location
// ../pandu-jev/research_decisions/micro/pandu_encoder_golden.json so the
// parity check runs out of the box on a full checkout.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Haslab-dev/RakitKode/store"
)

// panduGolden mirrors pandu_encoder_golden.json (written by
// pandu-jev research_decisions/micro/export_go.py).
type panduGolden struct {
	Format    string  `json:"format"`
	Dims      int     `json:"dims"`
	MaxTokens int     `json:"max_tokens"`
	RKB       string  `json:"rkb"`
	Tolerance float64 `json:"tolerance"`
	Samples   []struct {
		Text      string    `json:"text"`
		Tokens    int       `json:"tokens"`
		Embedding []float32 `json:"embedding"`
	} `json:"samples"`
}

// testPanduGolden locates the golden vectors + weights file, skipping the
// test when they are absent (they are gitignored-style sidecars produced by
// the Python export, not build artifacts of this repo).
func testPanduGolden(t *testing.T) (panduGolden, string) {
	t.Helper()
	candidates := []string{}
	if p := os.Getenv("PANDU_GOLDEN"); p != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "pandu-jev", "research_decisions", "micro", "pandu_encoder_golden.json"))
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		var g panduGolden
		if err := json.Unmarshal(data, &g); err != nil {
			t.Fatalf("golden file %s unreadable: %v", c, err)
		}
		if len(g.Samples) == 0 || g.Dims <= 0 {
			t.Fatalf("golden file %s has no samples", c)
		}
		rkb := os.Getenv("PANDU_ENCODER_RKB")
		if rkb == "" {
			rkb = filepath.Join(filepath.Dir(c), g.RKB)
		}
		if _, err := os.Stat(rkb); err != nil {
			t.Skipf("golden file found at %s but weights missing (%v); drop pandu_encoder.rkb or set PANDU_ENCODER_RKB", c, err)
		}
		return g, rkb
	}
	t.Skip("golden vectors not found (set PANDU_GOLDEN to pandu_encoder_golden.json)")
	return panduGolden{}, ""
}

// newGoldenEncoder attaches a real panduEncoder loaded from the weights file
// located by testPanduGolden.
func newGoldenEncoder(t *testing.T) (*panduEncoder, panduGolden) {
	t.Helper()
	g, rkb := testPanduGolden(t)
	t.Setenv(EnvPanduEncoderPath, rkb)
	enc := NewPanduEncoder(t.TempDir()) // env override points at the real weights
	if dims := enc.Dims(); dims != g.Dims {
		t.Fatalf("encoder dims %d, golden says %d", dims, g.Dims)
	}
	return enc, g
}

// TestPanduParityGolden pins the pure-Go forward pass to the PyTorch model:
// for the exported golden sample texts the per-dimension deviation must stay
// below the exported tolerance (1e-3).
func TestPanduParityGolden(t *testing.T) {
	enc, g := newGoldenEncoder(t)
	tol := g.Tolerance
	if tol <= 0 {
		tol = 1e-3
	}
	for _, s := range g.Samples {
		vec := enc.Embed(s.Text)
		if len(vec) != len(s.Embedding) {
			t.Fatalf("%q: got %d dims, golden has %d", s.Text, len(vec), len(s.Embedding))
		}
		maxDiff := 0.0
		worst := 0
		for i := range vec {
			if d := math.Abs(float64(vec[i] - s.Embedding[i])); d > maxDiff {
				maxDiff, worst = d, i
			}
		}
		if maxDiff >= tol {
			t.Errorf("%q: max abs diff %g at dim %d (tolerance %g) — Go forward pass diverged from the PyTorch model",
				s.Text, maxDiff, worst, tol)
		}
	}
}

// TestPanduEmbedderDisabledOnCorruptWeights: a broken weights file must log
// once, disable the encoder, and never panic — subsequent calls keep working
// (returning nil).
func TestPanduEmbedderDisabledOnCorruptWeights(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.rkb")
	if err := os.WriteFile(garbage, []byte("definitely not an encoder"), 0o644); err != nil {
		t.Fatalf("write garbage rkb: %v", err)
	}
	cases := []struct{ name, path string }{
		{"garbage", garbage},
		{"missing", filepath.Join(dir, "absent.rkb")},
	}

	// When the real container is available, also cover a body-truncated copy
	// (valid header, cut-off tensors).
	for _, c := range []string{os.Getenv("PANDU_ENCODER_RKB"),
		filepath.Join("..", "..", "pandu-jev", "research_decisions", "micro", "pandu_encoder.rkb")} {
		if data, err := os.ReadFile(c); err == nil && len(data) > 64 {
			truncated := filepath.Join(dir, "truncated.rkb")
			if err := os.WriteFile(truncated, data[:len(data)/2], 0o644); err != nil {
				t.Fatalf("write truncated rkb: %v", err)
			}
			cases = append(cases, struct{ name, path string }{"truncated", truncated})
			break
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvPanduEncoderPath, tc.path)
			enc := NewPanduEncoder(t.TempDir())
			for i := 0; i < 3; i++ { // repeated calls stay nil, no panic
				if v := enc.Embed("retry caps stay at five"); v != nil {
					t.Fatalf("call %d: expected nil from a disabled encoder, got %d dims", i, len(v))
				}
			}
			if dims := enc.Dims(); dims != 0 {
				t.Fatalf("disabled encoder reported dims %d", dims)
			}
		})
	}
}

// TestPanduEncoderEnvPathRelocation checks the PANDU_ENCODER_PATH override
// (weights live outside the brain dir).
func TestPanduEncoderEnvPathRelocation(t *testing.T) {
	_, rkb := testPanduGolden(t)
	t.Setenv(EnvPanduEncoderPath, rkb)
	enc := NewPanduEncoder("/nonexistent/brain/dir") // dir file absent, env wins
	if dims := enc.Dims(); dims == 0 {
		t.Fatal("encoder did not load via PANDU_ENCODER_PATH override")
	}
	if v := enc.Embed("worker pool deadlock"); len(v) == 0 {
		t.Fatal("empty embedding from a loaded encoder")
	}
}

// TestEmbedderAutoAttachOnOpen: dropping pandu_encoder.rkb into the brain dir
// turns semantic recall on for the next Open; without it the brain stays
// embedder-free.
func TestEmbedderAutoAttachOnOpen(t *testing.T) {
	store.SetRoot(t.TempDir())
	ws := t.TempDir()
	b, err := Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := b.AppendFact(KindDecision, "Keep retry caps at five attempts", nil, "t:1"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if b.embedder != nil {
		t.Fatal("embedder attached without weights present")
	}

	_, rkb := testPanduGolden(t)
	data, err := os.ReadFile(rkb)
	if err != nil {
		t.Fatalf("read rkb: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir(), panduEncoderFile), data, 0o644); err != nil {
		t.Fatalf("drop weights into brain dir: %v", err)
	}
	b2, err := Open(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if b2.embedder == nil {
		t.Fatal("weights present but embedder was not auto-attached")
	}
	if dims := b2.embedder.Dims(); dims == 0 {
		t.Fatal("auto-attached embedder is disabled")
	}
	if b2.embedWeight != DefaultEmbeddingWeight {
		t.Fatalf("auto-attach weight = %v, want default", b2.embedWeight)
	}
}

// stubEmbedder is a deterministic fake for fusion and cache tests.
type stubEmbedder struct {
	mu         sync.Mutex
	vecs       map[string][]float32
	defaultVec []float32
	calls      map[string]int
	dims       int
}

func newStubEmbedder(dims int) *stubEmbedder {
	return &stubEmbedder{vecs: map[string][]float32{}, calls: map[string]int{}, dims: dims}
}

func (s *stubEmbedder) set(text string, v []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vecs[text] = v
}

// setDefault makes Embed return v for every unknown text (exercises the
// cosine math path with uniform vectors).
func (s *stubEmbedder) setDefault(v []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultVec = v
}

func (s *stubEmbedder) Embed(text string) []float32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[text]++
	if v, ok := s.vecs[text]; ok {
		return v
	}
	return s.defaultVec
}

func (s *stubEmbedder) Dims() int { return s.dims }

func (s *stubEmbedder) callCount(text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[text]
}

func (s *stubEmbedder) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		n += c
	}
	return n
}

// unitVec returns an L2-normalized vector with 1 in slot i.
func unitVec(dims, i int) []float32 {
	v := make([]float32, dims)
	v[i] = 1
	return v
}

// TestEmbedderFusionBoostsSemanticMatch: with the embedder attached, a fact
// that is lexically disjoint from the query but semantically close outranks a
// fact with a weak lexical overlap; with the embedder off, the lexical fact
// wins. This is the behavioral payoff of the cosine term.
func TestEmbedderFusionBoostsSemanticMatch(t *testing.T) {
	br := &Brain{hashes: make(map[string]int), embedWeight: DefaultEmbeddingWeight}
	semanticID, err := br.AppendFact(KindDecision, "Ship releases only on Tuesday mornings", nil, "t:1")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	lexicalID, err := br.AppendFact(KindDecision, "The rollout checklist lives in the wiki", nil, "t:2")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	query := "gating rollout schedule"

	// Off: only the lexical overlap counts — the checklist fact leads.
	offOrder := func() []string {
		var ids []string
		for _, f := range br.Search(query, 8) {
			ids = append(ids, f.ID)
		}
		return ids
	}
	if got := offOrder(); len(got) != 2 || got[0] != lexicalID {
		t.Fatalf("without embedder, lexical fact must rank first, got %v", got)
	}

	// On: the Tuesday fact is semantically close to the query, the checklist
	// fact is orthogonal to it.
	stub := newStubEmbedder(8)
	stub.set(query, unitVec(8, 0))
	stub.set("Ship releases only on Tuesday mornings", unitVec(8, 0))
	stub.set("The rollout checklist lives in the wiki", unitVec(8, 1))
	br.SetEmbedder(stub)

	if got := offOrder(); len(got) != 2 || got[0] != semanticID {
		t.Fatalf("with embedder, semantic fact must rank first in Search, got %v", got)
	}
	out := br.BriefFor([]string{query}, nil, nil)
	if strings.Index(out, "Tuesday") > strings.Index(out, "rollout checklist") {
		t.Fatalf("with embedder, semantic fact must render first in BriefFor:\n%s", out)
	}

	// Zero-weight disables the cosine contribution without detaching.
	br.SetEmbeddingWeight(0)
	if got := offOrder(); got[0] != lexicalID {
		t.Fatalf("weight 0 must restore lexical order, got %v", got)
	}
}

// TestEmbedderOffZeroCost is the guard for "off by default": with no
// embedder, a disabled panduEncoder, or an embedder returning zero vectors,
// BriefFor/Search must be byte-identical to the BM25-only behavior, and the
// scoring pass must not measurably slow down.
func TestEmbedderOffZeroCost(t *testing.T) {
	newBrain := func() *Brain {
		br := &Brain{hashes: make(map[string]int), embedWeight: DefaultEmbeddingWeight}
		for i := 0; i < 20; i++ {
			kind := KindDecision
			text := fmt.Sprintf("Decision number %d: ship feature %d behind a config flag", i, i)
			if i%3 == 0 {
				kind, text = KindPitfall, fmt.Sprintf("Pitfall number %d: rebuilding the index inside a read transaction deadlocks", i)
			}
			if _, err := br.AppendFact(kind, text, nil, "t"); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
		return br
	}
	ctxTerms := []string{"fix the retry deadlock in the uploader config"}

	baseline := newBrain()
	wantBrief := baseline.BriefFor(ctxTerms, []string{"/ws/uploader.go"}, nil)
	wantSearch := baseline.Search("retry deadlock uploader", 5)

	// Disabled panduEncoder (weights file missing): identical bytes.
	disabledBrain := newBrain()
	disabledBrain.SetEmbedder(NewPanduEncoder(filepath.Join(t.TempDir(), "absent.rkb")))
	if got := disabledBrain.BriefFor(ctxTerms, []string{"/ws/uploader.go"}, nil); got != wantBrief {
		t.Fatalf("disabled embedder changed BriefFor:\n--- want ---\n%s\n--- got ---\n%s", wantBrief, got)
	}
	idsOf := func(fs []Fact) []string {
		out := make([]string, len(fs))
		for i, f := range fs {
			out[i] = f.ID
		}
		return out
	}
	if got, want := idsOf(disabledBrain.Search("retry deadlock uploader", 5)), idsOf(wantSearch); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("disabled embedder changed Search order:\n--- want ---\n%v\n--- got ---\n%v", want, got)
	}

	// Zero-vector embedder (cosine always 0 because the norms are 0):
	// identical bytes even though the cosine path runs.
	zeroBrain := newBrain()
	zero := newStubEmbedder(192)
	zero.setDefault(make([]float32, 192))
	zeroBrain.SetEmbedder(zero)
	if got := zeroBrain.BriefFor(ctxTerms, []string{"/ws/uploader.go"}, nil); got != wantBrief {
		t.Fatalf("zero-vector embedder changed BriefFor:\n--- want ---\n%s\n--- got ---\n%s", wantBrief, got)
	}

	// Cost: the disabled-embedder scoring pass must stay within noise of the
	// embedder-free pass (median of repeated runs).
	medianOf := func(br *Brain) time.Duration {
		var samples []time.Duration
		for i := 0; i < 50; i++ {
			start := time.Now()
			br.BriefFor(ctxTerms, []string{"/ws/uploader.go"}, nil)
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[len(samples)/2]
	}
	offMedian, disabledMedian := medianOf(baseline), medianOf(disabledBrain)
	if disabledMedian > 3*offMedian+2*time.Millisecond {
		t.Fatalf("disabled embedder slowed BriefFor: off median %v, disabled median %v", offMedian, disabledMedian)
	}
}

// TestEmbedderCacheInvalidation: fact embeddings are computed once per fact
// and cached in the BM25 index; supersession and compaction invalidate them
// exactly where the BM25 hooks already run.
func TestEmbedderCacheInvalidation(t *testing.T) {
	br := &Brain{hashes: make(map[string]int), embedWeight: DefaultEmbeddingWeight}
	id1, err := br.AppendFact(KindDecision, "Deploy with blue-green releases", nil, "t:1")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	id2, err := br.AppendFact(KindPitfall, "Friday deploys cause weekend incidents", nil, "t:2")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	stub := newStubEmbedder(8)
	queryKey := "deploys"
	stub.set(queryKey, unitVec(8, 0))
	for _, text := range []string{"Deploy with blue-green releases", "Friday deploys cause weekend incidents"} {
		stub.set(text, unitVec(8, 1))
	}
	br.SetEmbedder(stub)
	_ = br.BriefFor([]string{queryKey}, nil, nil)
	if got := stub.totalCalls(); got != 3 { // 1 query + 2 facts
		t.Fatalf("expected 3 embeds on the first pass (1 query + 2 facts), got %d", got)
	}
	_ = br.BriefFor([]string{queryKey}, nil, nil)
	_ = br.Search(queryKey, 5)
	if got := stub.totalCalls(); got != 3 {
		t.Fatalf("cached pass must not re-embed, got %d calls", got)
	}

	// Superseding a fact drops its cached embedding with the BM25 entry.
	if err := br.Supersede(id1, "t:9"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	br.mu.Lock()
	_, cached := br.ix.emb[id1]
	_, kept := br.ix.emb[id2]
	br.mu.Unlock()
	if cached {
		t.Fatal("superseded fact's embedding survived in the cache")
	}
	if !kept {
		t.Fatal("unrelated fact's embedding was dropped by the supersede")
	}

	// Re-observation creates a NEW record id → fresh embedding.
	id3, err := br.AppendFact(KindDecision, "Deploy with blue-green releases", nil, "t:10")
	if err != nil {
		t.Fatalf("re-append: %v", err)
	}
	if id3 == id1 {
		t.Fatal("expected a new record id for re-observation")
	}
	_ = br.BriefFor([]string{queryKey}, nil, nil)
	if stub.callCount("Deploy with blue-green releases") != 2 {
		t.Fatalf("re-observed fact must be embedded fresh (2 calls), got %d", stub.callCount("Deploy with blue-green releases"))
	}

	// Compaction rebuilds the index (and thus the embedding cache) when it
	// actually drops a record.
	br.mu.Lock()
	ancient := br.facts[0] // the superseded blue-green record
	ancient.InvalidAt = time.Now().UTC().Add(-compactInvalidWindow - 24*time.Hour)
	n := len(br.facts)
	br.mu.Unlock()
	if err := br.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	br.mu.Lock()
	rebuilt := len(br.facts) < n
	br.mu.Unlock()
	if rebuilt {
		if stub.callCount("Friday deploys cause weekend incidents") != 2 {
			t.Fatalf("compaction must clear the embedding cache (fact re-embedded once more), got %d calls",
				stub.callCount("Friday deploys cause weekend incidents"))
		}
	}
}

// TestBriefForWithEmbedderLatency is the performance guard: with semantic
// recall attached to a full-size store, BriefFor must stay inside the
// interactive budget — fact embeddings are computed once and cached, and the
// query embeds once per distinct turn context. Uses fresh query contexts so
// the measured path includes a real query forward pass every call.
func TestBriefForWithEmbedderLatency(t *testing.T) {
	newGoldenEncoder(t) // skips unless weights + golden are present; pins env to the real weights
	br := newBenchBrain(t, 1500)
	br.SetEmbedder(NewPanduEncoder(t.TempDir())) // env override (set above) points at the weights

	msg := "fix the retry deadlock in the uploader and keep the config flag convention"
	// Warm-up pass: fills the 1500 fact embeddings (batched) + memoizes the
	// base query. This cost is once per process, not once per turn.
	if out := br.BriefFor([]string{msg}, []string{"/ws/uploader.go"}, nil); out == "" {
		t.Fatal("empty brief during warm-up")
	}

	perCall := 20
	start := time.Now()
	for i := 0; i < perCall; i++ {
		// Fresh context each call → a real query embedding per turn.
		q := fmt.Sprintf("%s with fresh context term %d", msg, i)
		if out := br.BriefFor([]string{q}, []string{"/ws/uploader.go"}, nil); out == "" {
			t.Fatal("empty brief in timed loop")
		}
	}
	avg := time.Since(start) / time.Duration(perCall)
	limit := 5 * time.Millisecond * panduPerfSlack
	if avg > limit {
		t.Fatalf("BriefFor with embedder on 1500 facts averages %v per call (fresh query each turn), limit %v",
			avg, limit)
	}

	// Repeated identical context (turn retries) must be far below budget:
	// query memo hit + cached fact embeddings.
	start = time.Now()
	for i := 0; i < perCall; i++ {
		if out := br.BriefFor([]string{msg}, []string{"/ws/uploader.go"}, nil); out == "" {
			t.Fatal("empty brief in memoized loop")
		}
	}
	avgMemo := time.Since(start) / time.Duration(perCall)
	if avgMemo > limit/2 {
		t.Fatalf("memoized BriefFor averages %v per call, limit %v", avgMemo, limit/2)
	}
	t.Logf("BriefFor @1500 facts with embedder: fresh-query %v/call, memoized %v/call (limit %v)",
		avg, avgMemo, limit)
}
