package brain

import (
	"math"
	"strings"
	"sync"
)

// bm25Index is an in-memory Okapi BM25 index over the currently-valid facts.
// It is guarded by the owning Brain's mutex and rebuilt wholesale on load
// and compaction; appends and supersessions update it incrementally. The
// whole point is recall quality at zero infrastructure: facts are bounded
// (compactThreshold) so every scoring pass is a microsecond-scale in-memory
// walk — no vector store, no external index.
type bm25Index struct {
	mu       sync.Mutex
	tf       map[string]map[string]int // fact id → base-term → count
	df       map[string]int            // base-term → number of facts containing it
	lengths  map[string]int            // fact id → token count
	totalLen int
	n        int

	// emb caches per-fact embeddings for the optional semantic recall layer
	// (embedder.go), keyed by fact id. Guarded by the owning Brain's mutex
	// like every other field; entries are dropped on remove (supersede /
	// re-observation) and the map is rebuilt empty whenever the index is
	// (load, compaction, SetEmbedder).
	emb map[string][]float32
}

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// baseTerm is the naive singular form of a token: plural ("deadlocks") and
// singular ("deadlock") collapse to the same index term so tiny wording
// differences cannot zero out a match.
func baseTerm(t string) string {
	if len(t) > 3 && strings.HasSuffix(t, "s") {
		return t[:len(t)-1]
	}
	return t
}

func newBM25Index() *bm25Index {
	return &bm25Index{
		tf:       make(map[string]map[string]int),
		df:       make(map[string]int),
		lengths:  make(map[string]int),
		totalLen: 0,
		n:        0,
		emb:      make(map[string][]float32),
	}
}

// indexTerms tokenizes text into base terms, counting repeats.
func indexTerms(text string) map[string]int {
	counts := make(map[string]int)
	for _, t := range splitTerms(text) {
		b := baseTerm(t)
		counts[b]++
	}
	return counts
}

func (ix *bm25Index) add(f Fact) {
	ix.remove(f.ID)
	counts := indexTerms(f.Text)
	if len(counts) == 0 {
		return
	}
	tf := make(map[string]int, len(counts))
	for term, c := range counts {
		tf[term] = c
		ix.df[term]++
		ix.totalLen += c
	}
	ix.tf[f.ID] = tf
	ix.lengths[f.ID] = ix.totalLenOf(counts)
	ix.n++
}

// totalLenOf sums the counts — kept separate so totalLen bookkeeping stays
// exact even when counts maps are reused.
func (ix *bm25Index) totalLenOf(counts map[string]int) int {
	sum := 0
	for _, c := range counts {
		sum += c
	}
	return sum
}

func (ix *bm25Index) remove(id string) {
	tf, ok := ix.tf[id]
	if !ok {
		return
	}
	for term, c := range tf {
		ix.df[term] -= c
		if ix.df[term] <= 0 {
			delete(ix.df, term)
		}
		ix.totalLen -= c
	}
	delete(ix.tf, id)
	delete(ix.lengths, id)
	delete(ix.emb, id)
	ix.n--
}

func (ix *bm25Index) avgLen() float64 {
	if ix.n == 0 {
		return 1
	}
	return float64(ix.totalLen) / float64(ix.n)
}

// idf is the standard smooth formulation, always positive:
// ln(1 + (N - df + 0.5) / (df + 0.5)).
func (ix *bm25Index) idf(term string) float64 {
	df := float64(ix.df[term])
	n := float64(ix.n)
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

// score computes the BM25 weight of the query terms for one fact. A fact
// absent from the index (or a query with no overlap) scores 0 — callers
// treat zero as "no applicability signal" and fall back to canonical order.
func (ix *bm25Index) score(factID string, queryTerms []string) float64 {
	tf, ok := ix.tf[factID]
	if !ok || ix.n == 0 {
		return 0
	}
	length := float64(ix.lengths[factID])
	avg := ix.avgLen()
	if avg <= 0 {
		avg = 1
	}
	var score float64
	seen := make(map[string]bool, len(queryTerms))
	for _, raw := range queryTerms {
		term := baseTerm(raw)
		if seen[term] {
			continue
		}
		seen[term] = true
		freq := float64(tf[term])
		if freq == 0 {
			continue
		}
		score += ix.idf(term) * (freq * (bm25K1 + 1)) /
			(freq + bm25K1*(1-bm25B+bm25B*length/avg))
	}
	return score
}

// hasSignal reports whether any query term exists in the index at all —
// distinguishes "query matched nothing" from "query had no terms".
func (ix *bm25Index) hasSignal(queryTerms []string) bool {
	for _, raw := range queryTerms {
		if ix.df[baseTerm(raw)] > 0 {
			return true
		}
	}
	return false
}
