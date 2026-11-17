package brain

// Optional local semantic recall for the brain: a pluggable embedder that
// blends cosine similarity into the BM25 applicability score.
//
// The shipped implementation, panduEncoder, is a pure-Go (stdlib-only) forward
// pass of the Pandu-Micro MicroBERT encoder exported by pandu-jev
// (research_decisions/micro/export_go.py) into the compact .rkb container:
// token+position embedding lookup → LayerNorm → 3 pre-LN transformer layers
// (d=192, 4 heads, FFN 512, erf-gelu) → masked mean-pool → L2 normalize.
// It mirrors micro/model.py exactly; the golden-vectors parity test
// (embedder_pandu_test.go) pins Go↔Python agreement to <1e-3 per dimension.
//
// Everything here is OFF unless an embedder is attached. Without one (the
// default) BriefFor/Search are byte-identical to the BM25-only behavior and
// cost nothing — no weights file is read, no vector math runs. A host turns
// semantic recall on by dropping the exported pandu_encoder.rkb into the
// brain directory (auto-attached on Open when present), or by calling
// SetEmbedder explicitly; PANDU_ENCODER_PATH relocates the weights file.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

// Embedder produces dense text embeddings for semantic recall. Implementations
// must be safe for concurrent use. Returning nil from Embed signals "no
// embedding available" (disabled, empty input) — scoring treats it as no
// signal and falls back to the canonical order.
type Embedder interface {
	// Embed returns the L2-normalized embedding of text, or nil.
	Embed(text string) []float32
	// Dims reports the embedding width (0 when unavailable).
	Dims() int
}

// BatchEmbedder is an optional interface for embedders that amortize per-call
// overhead across a batch. The brain uses it when filling the fact-embedding
// cache (the first scoring pass after load); embedders without it are called
// one text at a time.
type BatchEmbedder interface {
	EmbedBatch(texts []string) [][]float32
}

const (
	// DefaultEmbeddingWeight scales the cosine term fused into the BM25
	// applicability score: overlap += cosine * weight. Tuned so a strong
	// semantic match moves a fact about as much as a full BM25 match.
	DefaultEmbeddingWeight = 4.0

	// panduEncoderFile is the weights file the host drops into the brain dir.
	panduEncoderFile = "pandu_encoder.rkb"

	// EnvPanduEncoderPath relocates the weights file away from the brain dir.
	EnvPanduEncoderPath = "PANDU_ENCODER_PATH"

	// maxQueryEmbedTerms caps the joined context terms embedded per turn.
	// A user message rarely carries more distinct content terms than this,
	// and the cap keeps the per-turn forward pass inside the interactive
	// budget (~2-3 ms of the 5 ms BriefFor envelope on Apple Silicon).
	maxQueryEmbedTerms = 16

	// queryEmbCacheSize bounds the query-embedding memo (the block is
	// computed once per turn; retries with identical context are free).
	queryEmbCacheSize = 8
)

// Forward-pass kernel tuning: sequence rows are split across workers (capped)
// when the sequence is long enough to amortize the stage barriers. Kernel
// math runs in float64 with 4-way unrolled accumulators (measured ~2.6 GMAC/s
// serial, ~8.5 GMAC/s across 8 cores on Apple Silicon — a ≤24-token query
// forward lands well under the 5 ms BriefFor envelope).
const (
	maxEncoderWorkers = 8
	minRowsPerWorker  = 4
	layerNormEpsilon  = 1e-5 // PyTorch nn.LayerNorm default
)

// ---------------------------------------------------------------------------
// panduEncoder — pure-Go forward pass of the exported MicroBERT
// ---------------------------------------------------------------------------

// panduLayer holds one pre-LN transformer encoder layer's weights, converted
// to float64 at load for the numeric kernels. Layouts mirror PyTorch
// nn.TransformerEncoderLayer (norm_first=True) state dicts: Linear weight is
// [out_features, in_features] (y = x·Wᵀ + b), attention in-projection rows
// packed Q;K;V.
type panduLayer struct {
	inProjW  []float64 // [3d, d] — rows packed Q;K;V
	inProjB  []float64 // [3d]
	outProjW []float64 // [d, d]
	outProjB []float64 // [d]
	ffW1     []float64 // [ff, d]
	ffB1     []float64 // [ff]
	ffW2     []float64 // [d, ff]
	ffB2     []float64 // [d]
	norm1W   []float64 // [d]
	norm1B   []float64 // [d]
	norm2W   []float64 // [d]
	norm2B   []float64 // [d]
	ff       int       // intermediate width (from the exported ff_w1 shape)
}

// panduEncoder is the local Pandu-Micro embedder. Weights load lazily on the
// first Embed; a missing file silently disables (semantic recall stays off —
// that is the default state), a corrupt file logs once and disables. Neither
// ever panics.
type panduEncoder struct {
	path string

	mu       sync.Mutex
	loaded   bool
	disabled bool

	dims      int
	heads     int
	maxTokens int

	vocab map[string]int32

	tokenEmb []float64 // [V, d]
	posEmb   []float64 // [maxPos, d]
	embNormW []float64 // [d]
	embNormB []float64 // [d]
	layers   []panduLayer
}

// Compile-time interface checks.
var (
	_ Embedder      = (*panduEncoder)(nil)
	_ BatchEmbedder = (*panduEncoder)(nil)
)

// NewPanduEncoder returns a lazy Pandu-Micro embedder rooted at a brain
// directory: weights load on first use from $PANDU_ENCODER_PATH, or
// <brainDir>/pandu_encoder.rkb. The returned encoder is always non-nil and
// safe to attach even when the weights file is absent — it disables itself
// instead of failing the caller.
func NewPanduEncoder(brainDir string) *panduEncoder {
	return &panduEncoder{path: panduWeightsPath(brainDir)}
}

// panduWeightsPath reports where the brain looks for exported weights.
func panduWeightsPath(brainDir string) string {
	if p := os.Getenv(EnvPanduEncoderPath); p != "" {
		return p
	}
	return filepath.Join(brainDir, panduEncoderFile)
}

// Embed implements Embedder. The mutex serializes lazy loading; the numeric
// pass itself reads only immutable state, so the row-parallel goroutines
// inside need no further synchronization.
func (e *panduEncoder) Embed(text string) []float32 {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureLoadedLocked()
	return e.embedTextLocked(text, true)
}

// EmbedBatch implements BatchEmbedder: texts are spread across workers, each
// running a serial forward pass (one barrier total instead of per-text
// staging) — the fact-cache warm-up for a full store costs ~1-2 s instead of
// ~10 s on an 8-core machine.
func (e *panduEncoder) EmbedBatch(texts []string) [][]float32 {
	if e == nil {
		return nil
	}
	out := make([][]float32, len(texts))
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureLoadedLocked()
	if e.disabled {
		return out
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > maxEncoderWorkers {
		workers = maxEncoderWorkers
	}
	if workers > len(texts) {
		workers = len(texts)
	}
	if workers <= 1 {
		for i, t := range texts {
			out[i] = e.embedTextLocked(t, false)
		}
		return out
	}
	var wg sync.WaitGroup
	chunk := (len(texts) + workers - 1) / workers
	for wr := 0; wr < workers; wr++ {
		lo, hi := wr*chunk, min((wr+1)*chunk, len(texts))
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				out[i] = e.embedTextLocked(texts[i], false)
			}
		}(lo, hi)
	}
	wg.Wait()
	return out
}

// Dims implements Embedder (0 while disabled).
func (e *panduEncoder) Dims() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureLoadedLocked()
	return e.dims
}

func (e *panduEncoder) embedTextLocked(text string, parallel bool) []float32 {
	if e.disabled {
		return nil
	}
	ids := e.encodeIDs(text)
	if len(ids) == 0 {
		return nil
	}
	return e.forward(ids, parallel)
}

// disable permanently turns the encoder off after a load failure, logging
// once. A broken weights file must never take the host down.
func (e *panduEncoder) disable(cause error) {
	if e.disabled {
		return
	}
	e.disabled = true
	log.Printf("[brain] pandu encoder disabled (weights unusable at %s): %v", e.path, cause)
}

// ensureLoadedLocked performs the one-time lazy load. A missing file is the
// normal "feature off" state and stays silent; anything unreadable logs and
// disables.
func (e *panduEncoder) ensureLoadedLocked() {
	if e.loaded {
		return
	}
	e.loaded = true
	data, err := os.ReadFile(e.path)
	if err != nil {
		if !os.IsNotExist(err) {
			e.disable(err)
		} else {
			e.disabled = true
		}
		return
	}
	if err := e.parse(data); err != nil {
		e.disable(err)
	}
}

// ---------------------------------------------------------------------------
// .rkb parsing (format authored by pandu-jev research_decisions/micro/export_go.py)
// ---------------------------------------------------------------------------

// cursor is a bounds-checked little-endian reader over the container.
type cursor struct {
	data []byte
	off  int
}

func (c *cursor) take(n int) ([]byte, error) {
	if n < 0 || len(c.data)-c.off < n {
		return nil, fmt.Errorf("truncated container (need %d bytes at offset %d)", n, c.off)
	}
	out := c.data[c.off : c.off+n]
	c.off += n
	return out, nil
}

func (c *cursor) u32() uint32 {
	v := binary.LittleEndian.Uint32(c.data[c.off:])
	c.off += 4
	return v
}

func (c *cursor) i32() int32 {
	return int32(c.u32())
}

func (c *cursor) i32Checked() (int, error) {
	if len(c.data)-c.off < 4 {
		return 0, fmt.Errorf("truncated container (need 4 bytes at offset %d)", c.off)
	}
	return int(c.i32()), nil
}

// parse decodes the RKBE v1 container: magic + version + dims header, then a
// length-prefixed vocab JSON array, then named flat float32 tensors. Any
// structural surprise is an error — the caller disables the encoder.
func (e *panduEncoder) parse(data []byte) error {
	cur := cursor{data: data}
	magic, err := cur.take(4)
	if err != nil || string(magic) != "RKBE" {
		return fmt.Errorf("bad magic (want RKBE)")
	}
	if version := cur.u32(); version != 1 {
		return fmt.Errorf("unsupported format version %d", version)
	}
	if len(data) < 32 {
		return fmt.Errorf("container shorter than header")
	}
	dims := int(cur.i32())
	layers := int(cur.i32())
	heads := int(cur.i32())
	maxPos := int(cur.i32())
	maxTokens := int(cur.i32())
	if dims <= 0 || dims > 4096 || heads <= 0 || heads > 64 || dims%heads != 0 ||
		layers <= 0 || layers > 32 || maxPos <= 0 || maxTokens <= 0 || maxTokens > maxPos {
		return fmt.Errorf("implausible header dims=%d layers=%d heads=%d maxPos=%d maxTokens=%d",
			dims, layers, heads, maxPos, maxTokens)
	}
	vocabLen, err := cur.i32Checked()
	if err != nil {
		return err
	}
	vocabJSON, err := cur.take(vocabLen)
	if err != nil {
		return fmt.Errorf("truncated vocab json: %w", err)
	}
	var vocabList []string
	if err := json.Unmarshal(vocabJSON, &vocabList); err != nil {
		return fmt.Errorf("vocab json: %w", err)
	}
	if len(vocabList) == 0 || len(vocabList) > 1<<20 {
		return fmt.Errorf("implausible vocab size %d", len(vocabList))
	}

	tensorCount, err := cur.i32Checked()
	if err != nil {
		return err
	}
	if tensorCount < 0 || tensorCount > 4096 {
		return fmt.Errorf("implausible tensor count %d", tensorCount)
	}
	type tensor struct {
		shape []int
		flat  []float32
	}
	tensors := make(map[string]tensor, tensorCount)
	for i := 0; i < tensorCount; i++ {
		nameLen, err := cur.i32Checked()
		if err != nil {
			return err
		}
		nameBytes, err := cur.take(nameLen)
		if err != nil {
			return fmt.Errorf("truncated tensor name: %w", err)
		}
		rank, err := cur.i32Checked()
		if err != nil {
			return err
		}
		if rank <= 0 || rank > 4 {
			return fmt.Errorf("tensor %q: bad rank %d", nameBytes, rank)
		}
		shape := make([]int, rank)
		product := 1
		for r := range shape {
			if shape[r], err = cur.i32Checked(); err != nil {
				return err
			}
			if shape[r] <= 0 || shape[r] > 1<<24 {
				return fmt.Errorf("tensor %q: bad shape %v", nameBytes, shape)
			}
			product *= shape[r]
		}
		n := product * 4
		raw, err := cur.take(n)
		if err != nil {
			return fmt.Errorf("tensor %q: %w", nameBytes, err)
		}
		flat := make([]float32, product)
		for i := range flat {
			flat[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		tensors[string(nameBytes)] = tensor{shape: shape, flat: flat}
	}

	// Pull out the required tensors with shape validation; any unknown extra
	// tensors are ignored (forward-compatible exports).
	pick := func(name string, want ...int) ([]float32, error) {
		t, ok := tensors[name]
		if !ok {
			return nil, fmt.Errorf("missing tensor %q", name)
		}
		if len(t.shape) != len(want) {
			return nil, fmt.Errorf("tensor %q: rank %d, want %d", name, len(t.shape), len(want))
		}
		for i, d := range want {
			if t.shape[i] != d {
				return nil, fmt.Errorf("tensor %q: shape %v, want %v", name, t.shape, want)
			}
		}
		return t.flat, nil
	}
	rows := len(vocabList)
	toF64 := func(src []float32) []float64 {
		out := make([]float64, len(src))
		for i, v := range src {
			out[i] = float64(v)
		}
		return out
	}
	pickF64 := func(name string, want ...int) ([]float64, error) {
		t, err := pick(name, want...)
		if err != nil {
			return nil, err
		}
		return toF64(t), nil
	}

	if e.tokenEmb, err = pickF64("token_emb", rows, dims); err != nil {
		return err
	}
	if e.posEmb, err = pickF64("pos_emb", maxPos, dims); err != nil {
		return err
	}
	if e.embNormW, err = pickF64("emb_norm_w", dims); err != nil {
		return err
	}
	if e.embNormB, err = pickF64("emb_norm_b", dims); err != nil {
		return err
	}

	e.layers = make([]panduLayer, layers)
	for i := range e.layers {
		p := fmt.Sprintf("l%d_", i)
		l := &e.layers[i]
		if l.inProjW, err = pickF64(p+"attn_inproj_w", 3*dims, dims); err != nil {
			return err
		}
		if l.inProjB, err = pickF64(p+"attn_inproj_b", 3*dims); err != nil {
			return err
		}
		if l.outProjW, err = pickF64(p+"attn_outproj_w", dims, dims); err != nil {
			return err
		}
		if l.outProjB, err = pickF64(p+"attn_outproj_b", dims); err != nil {
			return err
		}
		// The intermediate width is not in the header; take it from the
		// declared shape of ff_w1 ([ff, dims]).
		t, ok := tensors[p+"ff_w1"]
		if !ok || len(t.shape) != 2 {
			return fmt.Errorf("missing/malformed tensor %q", p+"ff_w1")
		}
		ff := t.shape[0]
		if ff <= 0 || ff > 16384 {
			return fmt.Errorf("tensor %q: implausible ff width %d", p+"ff_w1", ff)
		}
		if l.ffW1, err = pickF64(p+"ff_w1", ff, dims); err != nil {
			return err
		}
		if l.ffB1, err = pickF64(p+"ff_b1", ff); err != nil {
			return err
		}
		if l.ffW2, err = pickF64(p+"ff_w2", dims, ff); err != nil {
			return err
		}
		if l.ffB2, err = pickF64(p+"ff_b2", dims); err != nil {
			return err
		}
		if l.norm1W, err = pickF64(p+"norm1_w", dims); err != nil {
			return err
		}
		if l.norm1B, err = pickF64(p+"norm1_b", dims); err != nil {
			return err
		}
		if l.norm2W, err = pickF64(p+"norm2_w", dims); err != nil {
			return err
		}
		if l.norm2B, err = pickF64(p+"norm2_b", dims); err != nil {
			return err
		}
		l.ff = ff
	}

	vocab := make(map[string]int32, len(vocabList))
	for id, tok := range vocabList {
		vocab[tok] = int32(id)
	}

	e.dims = dims
	e.heads = heads
	e.maxTokens = maxTokens
	e.vocab = vocab
	return nil
}

// ---------------------------------------------------------------------------
// Tokenizer — faithful port of pandu-jev micro/tokenizer.py
// ---------------------------------------------------------------------------

// token IDs for the fixed special tokens (micro/tokenizer.py SPECIAL_TOKENS).
const (
	padTokenID    = 0
	unkTokenID    = 1
	clsTokenID    = 2
	sepTokenID    = 3
	maskTokenID   = 4
	goalTokenID   = 5
	turnTokenID   = 6
	failTokenID   = 7
	intentTokenID = 8
)

// isMicroWord mirrors Python's \w for str patterns: letters, digits/numbers,
// underscore (Unicode-aware; domain text is ASCII in practice).
func isMicroWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsDigit(r)
}

// lexMicro replicates re.findall(r"\[[\w]+\]|__[\w]+__|[\w]+|[^\w\s]", text)
// including the greedy+backtracking behavior of the __...__ branch, so
// special markers, dunder flags, words and punctuation split exactly as the
// Python tokenizer splits them.
func lexMicro(s string) []string {
	runes := []rune(s)
	out := make([]string, 0, len(runes))
	n := len(runes)
	for i := 0; i < n; {
		r := runes[i]
		// Branch 1: \[[\w]+\] — bracketed special markers like [goal].
		if r == '[' {
			j := i + 1
			for j < n && isMicroWord(runes[j]) {
				j++
			}
			if j > i+1 && j < n && runes[j] == ']' {
				out = append(out, string(runes[i:j+1]))
				i = j + 1
				continue
			}
		}
		// Branch 2: __[\w]+__ — dunder markers like __has_stacktrace__.
		// [\w]+ is greedy including underscores; the regex backtracks the
		// run until a trailing "__" fits, so scan candidate run ends from
		// longest to shortest.
		if r == '_' && i+1 < n && runes[i+1] == '_' {
			last := i + 2
			for last < n && isMicroWord(runes[last]) {
				last++
			}
			matched := -1
			for m := last; m >= i+3; m-- {
				if m+1 < n && runes[m] == '_' && runes[m+1] == '_' {
					matched = m
					break
				}
			}
			if matched >= 0 {
				out = append(out, string(runes[i:matched+2]))
				i = matched + 2
				continue
			}
		}
		// Branch 3: [\w]+ — maximal word run.
		if isMicroWord(r) {
			j := i
			for j < n && isMicroWord(runes[j]) {
				j++
			}
			out = append(out, string(runes[i:j]))
			i = j
			continue
		}
		// Branch 4: [^\w\s] — single punctuation char; whitespace is skipped.
		if !unicode.IsSpace(r) {
			out = append(out, string(r))
		}
		i++
	}
	return out
}

// encodeIDs ports micro/tokenizer.py encode(): lowercase, lex, vocabulary
// lookup with per-character fallback ([unk] for unknown chars), wrap in
// [cls] … [sep], truncate at maxTokens keeping a trailing [sep].
func (e *panduEncoder) encodeIDs(text string) []int32 {
	lexed := lexMicro(strings.ToLower(text))
	ids := make([]int32, 0, len(lexed)+2)
	ids = append(ids, clsTokenID)
	for _, tok := range lexed {
		if id, ok := e.vocab[tok]; ok {
			ids = append(ids, id)
			continue
		}
		for _, r := range tok {
			if id, ok := e.vocab[string(r)]; ok {
				ids = append(ids, id)
			} else {
				ids = append(ids, unkTokenID)
			}
		}
	}
	ids = append(ids, sepTokenID)
	if len(ids) > e.maxTokens {
		ids = append(ids[:e.maxTokens-1], sepTokenID)
	}
	return ids
}

// ---------------------------------------------------------------------------
// Forward pass
// ---------------------------------------------------------------------------

// parallelRows splits [0,n) row ranges across workers for one stage of the
// forward pass. Small sequences (or single-core hosts) run inline — the
// barrier overhead would dominate.
func parallelRows(n int, fn func(lo, hi int)) {
	workers := runtime.GOMAXPROCS(0)
	if workers > maxEncoderWorkers {
		workers = maxEncoderWorkers
	}
	if workers <= 1 || n < 2*minRowsPerWorker {
		fn(0, n)
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for wr := 0; wr < workers; wr++ {
		lo, hi := wr*chunk, min((wr+1)*chunk, n)
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			fn(lo, hi)
		}(lo, hi)
	}
	wg.Wait()
}

// matvec computes y[o] = Σ_k x[k]·W[o][k] + b[o] for a [outLen, inLen]
// row-major weight (the PyTorch Linear layout). Four unrolled accumulators
// break the FMA dependency chain (~2.5× measured throughput).
func matvec(x, w, b []float64, outLen, inLen int, y []float64) {
	for o := 0; o < outLen; o++ {
		row := w[o*inLen : (o+1)*inLen]
		var a0, a1, a2, a3 float64
		k := 0
		for ; k+4 <= inLen; k += 4 {
			a0 += x[k] * row[k]
			a1 += x[k+1] * row[k+1]
			a2 += x[k+2] * row[k+2]
			a3 += x[k+3] * row[k+3]
		}
		var tail float64
		for ; k < inLen; k++ {
			tail += x[k] * row[k]
		}
		y[o] = (a0 + a1) + (a2 + a3) + tail + b[o]
	}
}

// layerNormRow applies PyTorch LayerNorm (biased variance, eps=1e-5):
// y = (x − mean) / sqrt(var + eps) * γ + β.
func layerNormRow(x, w, b, out []float64, d int) {
	var sum float64
	for j := 0; j < d; j++ {
		sum += x[j]
	}
	mean := sum / float64(d)
	var vsum float64
	for j := 0; j < d; j++ {
		diff := x[j] - mean
		vsum += diff * diff
	}
	inv := 1 / math.Sqrt(vsum/float64(d)+layerNormEpsilon)
	for j := 0; j < d; j++ {
		out[j] = (x[j]-mean)*inv*w[j] + b[j]
	}
}

// gelu is the exact (erf) GELU PyTorch uses for activation="gelu":
// 0.5·x·(1 + erf(x/√2)).
func gelu(x float64) float64 {
	return 0.5 * x * (1 + math.Erf(x/math.Sqrt2))
}

// softmax computes max-subtracted row softmax in place.
func softmaxRow(s []float64) {
	maxV := s[0]
	for _, v := range s[1:] {
		if v > maxV {
			maxV = v
		}
	}
	var sum float64
	for i, v := range s {
		e := math.Exp(v - maxV)
		s[i] = e
		sum += e
	}
	inv := 1 / sum
	for i := range s {
		s[i] *= inv
	}
}

// forward runs the exported encoder trunk and returns the L2-normalized
// masked mean-pool of the final hidden states — the embedding definition
// documented in pandu-jev research_decisions/micro/export_go.py. There is no
// padding (single unpadded sequence), so the attention mask is all-ones.
func (e *panduEncoder) forward(ids []int32, parallel bool) []float32 {
	n := len(ids)
	d := e.dims
	stage := func(fn func(lo, hi int)) {
		if parallel {
			parallelRows(n, fn)
		} else {
			fn(0, n)
		}
	}

	// Embeddings: token + position, then the embedding LayerNorm.
	x := make([]float64, n*d)
	for t, id := range ids {
		if id < 0 || int(id)*d+d > len(e.tokenEmb) || t*d+d > len(e.posEmb) {
			continue // unreachable by construction; guards OOB rather than panicking
		}
		src := e.tokenEmb[int(id)*d : int(id)*d+d]
		pos := e.posEmb[t*d : t*d+d]
		dst := x[t*d : t*d+d]
		for j := 0; j < d; j++ {
			dst[j] = src[j] + pos[j]
		}
	}
	stage(func(lo, hi int) {
		for t := lo; t < hi; t++ {
			layerNormRow(x[t*d:t*d+d], e.embNormW, e.embNormB, x[t*d:t*d+d], d)
		}
	})

	h1 := make([]float64, d) // LayerNorm scratch (serial path; parallel path
	// allocates per-goroutine scratch inside each stage — shared scratch under
	// concurrent workers races and poisons the result with NaN, which the
	// final `norm > 0` guard then turns into a silent zero vector).
	ff := 0 // set from layer 0; every layer shares it
	if len(e.layers) > 0 {
		ff = e.layers[0].ff
	}
	u := make([]float64, ff)   // FFN intermediate scratch
	proj := make([]float64, d) // attention output-projection scratch
	qkv := make([]float64, n*3*d)
	attn := make([]float64, n*d)
	scores := make([]float64, n)
	scale := 1 / math.Sqrt(float64(d/e.heads))

	for li := range e.layers {
		l := &e.layers[li]

		// Stage 1 (per row): LayerNorm → Q, K, V projections.
		stage(func(lo, hi int) {
			h1 := h1
			if lo != 0 || hi != n { // parallel worker: private scratch
				h1 = make([]float64, d)
			}
			for t := lo; t < hi; t++ {
				row := x[t*d : t*d+d]
				layerNormRow(row, l.norm1W, l.norm1B, h1, d)
				matvec(h1, l.inProjW, l.inProjB, 3*d, d, qkv[t*3*d:])
			}
		})

		// Stage 2 (per query row): multi-head self-attention → output
		// projection → residual add. Rows are independent; each scans all
		// K/V rows.
		stage(func(lo, hi int) {
			scores, proj := scores, proj
			if lo != 0 || hi != n { // parallel worker: private scratch
				scores = make([]float64, n)
				proj = make([]float64, d)
			}
			for t1 := lo; t1 < hi; t1++ {
				q := qkv[t1*3*d : t1*3*d+d]
				for head := 0; head < e.heads; head++ {
					off := head * (d / e.heads)
					hd := d / e.heads
					for t2 := 0; t2 < n; t2++ {
						k := qkv[t2*3*d+d : t2*3*d+2*d]
						var dot float64
						for j := 0; j < hd; j++ {
							dot += q[off+j] * k[off+j]
						}
						scores[t2] = dot * scale
					}
					softmaxRow(scores[:n])
					out := attn[t1*d+off : t1*d+off+hd]
					for j := 0; j < hd; j++ {
						var acc float64
						for t2 := 0; t2 < n; t2++ {
							v := qkv[t2*3*d+2*d : t2*3*d+3*d]
							acc += scores[t2] * v[off+j]
						}
						out[j] = acc
					}
				}
				matvec(attn[t1*d:(t1+1)*d], l.outProjW, l.outProjB, d, d, proj)
				row := x[t1*d : t1*d+d]
				for j := 0; j < d; j++ {
					row[j] += proj[j]
				}
			}
		})

		// Stage 3 (per row): LayerNorm → FFN (erf-GELU) → residual add.
		stage(func(lo, hi int) {
			h1, u, proj := h1, u, proj
			if lo != 0 || hi != n { // parallel worker: private scratch
				h1 = make([]float64, d)
				u = make([]float64, ff)
				proj = make([]float64, d)
			}
			for t := lo; t < hi; t++ {
				row := x[t*d : t*d+d]
				layerNormRow(row, l.norm2W, l.norm2B, h1, d)
				matvec(h1, l.ffW1, l.ffB1, ff, d, u)
				for j := 0; j < ff; j++ {
					u[j] = gelu(u[j])
				}
				matvec(u, l.ffW2, l.ffB2, d, ff, proj)
				for j := 0; j < d; j++ {
					row[j] += proj[j]
				}
			}
		})
	}

	// Masked mean-pool over tokens, then L2 normalize (float32 out).
	v := make([]float64, d)
	for t := 0; t < n; t++ {
		row := x[t*d : t*d+d]
		for j := 0; j < d; j++ {
			v[j] += row[j]
		}
	}
	var norm float64
	for j := 0; j < d; j++ {
		v[j] /= float64(n)
		norm += v[j] * v[j]
	}
	norm = math.Sqrt(norm)
	out := make([]float32, d)
	if norm > 0 {
		for j := 0; j < d; j++ {
			out[j] = float32(v[j] / norm)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Cosine scoring
// ---------------------------------------------------------------------------

// cosineSim returns the cosine similarity of two embeddings (0 when either is
// missing or the widths disagree). Inputs are normally already L2-normalized;
// the full norm is computed anyway so unnormalized embedders still rank
// correctly.
func cosineSim(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
