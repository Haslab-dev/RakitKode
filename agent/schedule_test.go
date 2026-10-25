package agent

import (
	"testing"
	"time"
)

func TestNextCronTime(t *testing.T) {
	// 2026-01-01 is a Thursday.
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		expr string
		want time.Time
	}{
		{"*/5 * * * *", time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)},
		{"0 9 * * *", time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)},
		{"30 14 * * 1", time.Date(2026, 1, 5, 14, 30, 0, 0, time.UTC)},
		{"0 0 1 * *", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		got, err := nextCronTime(tt.expr, base)
		if err != nil {
			t.Fatalf("%s: %v", tt.expr, err)
		}
		if !got.Equal(tt.want) {
			t.Fatalf("%s: got %s, want %s", tt.expr, got, tt.want)
		}
	}
}

func TestNextCronTimeInvalid(t *testing.T) {
	if _, err := nextCronTime("bad", time.Now()); err == nil {
		t.Fatal("expected error for invalid cron")
	}
	if _, err := nextCronTime("* * *", time.Now()); err == nil {
		t.Fatal("expected error for wrong field count")
	}
}
