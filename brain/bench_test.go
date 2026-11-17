package brain

import (
	"fmt"
	"testing"
)

// newBenchBrain builds an in-memory brain with n valid facts and a mixed
// pitfall/preference tail — the size the compaction policy bounds the store
// to (~compactThreshold records).
func newBenchBrain(tb testing.TB, n int) *Brain {
	br := &Brain{hashes: make(map[string]int)}
	for i := 0; i < n; i++ {
		kind := KindDecision
		text := fmt.Sprintf("Decision number %d: ship feature %d behind a config flag", i, i)
		switch i % 5 {
		case 1:
			kind, text = KindPitfall, fmt.Sprintf("Pitfall number %d: rebuilding the index inside a read transaction deadlocks", i)
		case 2:
			kind, text = KindPreference, fmt.Sprintf("Preference number %d: always format with gofmt before committing", i)
		case 3:
			kind, text = KindConvention, fmt.Sprintf("Convention number %d: handlers live in the ui package and stay thin", i)
		case 4:
			kind, text = KindTaskOutcome, fmt.Sprintf("Outcome number %d: the uploader pool benchmark finished clean", i)
		}
		if _, err := br.AppendFact(kind, text, nil, "bench"); err != nil {
			tb.Fatalf("append: %v", err)
		}
	}
	return br
}

func BenchmarkBriefFor1500(b *testing.B) {
	br := newBenchBrain(b, 1500)
	msg := "fix the retry deadlock in the uploader and keep the config flag convention"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := br.BriefFor([]string{msg}, []string{"/ws/uploader.go"}, nil)
		if out == "" {
			b.Fatal("empty brief")
		}
	}
}

func BenchmarkSearch1500(b *testing.B) {
	br := newBenchBrain(b, 1500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := br.Search("retry deadlock uploader config", 8); len(got) == 0 {
			b.Fatal("empty search")
		}
	}
}

func BenchmarkAppendFact1500(b *testing.B) {
	br := newBenchBrain(b, 1500)
	i := 0
	b.ResetTimer()
	for i < b.N {
		i++
		_, _ = br.AppendFact(KindNoteRef, fmt.Sprintf("note %d: bench append throughput", i), nil, "bench")
	}
}
