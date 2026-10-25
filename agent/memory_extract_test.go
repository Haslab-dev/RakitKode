package agent

import "testing"

func TestExtractPreferenceClauses(t *testing.T) {
	text := `Please always use tabs for indentation.
Never commit generated files.
Remember: the staging database is read-only.
Use make check instead of go test directly.
This is just a normal sentence.`
	got := extractPreferenceClauses(text)
	if len(got) != 4 {
		t.Fatalf("got %d clauses: %#v", len(got), got)
	}
	want := []string{
		"Please always use tabs for indentation",
		"Never commit generated files",
		"Remember: the staging database is read-only",
		"Use make check instead of go test directly",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("clause %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractPreferenceClausesSkipsNoise(t *testing.T) {
	if got := extractPreferenceClauses("use this function\nnormal text"); len(got) != 0 {
		t.Fatalf("unexpected: %#v", got)
	}
}
