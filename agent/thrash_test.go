package agent

import (
	"strings"
	"testing"

	"github.com/Haslab-dev/RakitKode/events"
)

func TestThrashConsecutiveFailures(t *testing.T) {
	st := &thrashState{}
	if w := st.record("bash", `{"cmd":"go test"}`, true); w != "" {
		t.Fatalf("no warning before threshold, got %q", w)
	}
	if w := st.record("bash", `{"cmd":"go vet"}`, true); w != "" {
		t.Fatalf("no warning at 2 failures, got %q", w)
	}
	w := st.record("bash", `{"cmd":"go build"}`, true)
	if !strings.Contains(w, ThrashWarningText) || !strings.Contains(w, "3 consecutive tool failures") {
		t.Fatalf("expected warning at 3rd failure, got %q", w)
	}
	// Only one warning per episode.
	if w := st.record("bash", `{"cmd":"go build"}`, true); w != "" {
		t.Fatalf("warning must fire once per episode, got %q", w)
	}
	// take() drains it exactly once.
	if got := st.take(); got == "" {
		t.Fatal("take() should return the pending warning")
	}
	if got := st.take(); got != "" {
		t.Fatalf("take() must clear, got %q", got)
	}
	// A success clears the alarm state entirely.
	_ = st.record("read", `{"path":"x"}`, false)
	if got := st.record("bash", `{"cmd":"x"}`, true); got != "" {
		t.Fatalf("success must rearm the alarm, got %q", got)
	}
}

func TestThrashIdenticalRepeat(t *testing.T) {
	st := &thrashState{}
	// Successes with identical tool+args also grind (wasted calls).
	_ = st.record("read", `{"path":"a.go"}`, false)
	_ = st.record("read", `{"path":"a.go"}`, false)
	w := st.record("read", `{"path":"a.go"}`, false)
	if !strings.Contains(w, ThrashWarningText) || !strings.Contains(w, "identical arguments") {
		t.Fatalf("expected identical-args warning at 3rd repeat, got %q", w)
	}
	// Changed signature rearms.
	_ = st.take()
	if w := st.record("read", `{"path":"b.go"}`, false); w != "" {
		t.Fatalf("new args must reset counting, got %q", w)
	}
	if w := st.record("read", `{"path":"b.go"}`, false); w != "" {
		t.Fatalf("expected no warning at 2nd repeat of new args, got %q", w)
	}
}

func TestThrashReset(t *testing.T) {
	st := &thrashState{}
	_ = st.record("bash", "x", true)
	_ = st.record("bash", "x", true)
	_ = st.record("bash", "x", true)
	st.reset()
	if got := st.take(); got != "" {
		t.Fatalf("reset must clear pending warning, got %q", got)
	}
	if w := st.record("bash", "y", true); w != "" {
		t.Fatalf("reset must clear failure streak, got %q", w)
	}
}

func TestThrashManagerEmitsEventAndReminder(t *testing.T) {
	mgr, _ := newBrainTestManager(t)
	var warnings []events.Event
	unsub := mgr.bus.Subscribe(events.AgentLoopWarning, func(e events.Event) {
		warnings = append(warnings, e)
	})
	defer unsub()

	mgr.recordThrash("s1", "bash", "1", true)
	mgr.recordThrash("s1", "bash", "2", true)
	if got := mgr.takeThrashWarning("s1"); got != "" {
		t.Fatalf("no warning under threshold, got %q", got)
	}
	mgr.recordThrash("s1", "bash", "3", true)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one AgentLoopWarning event, got %d", len(warnings))
	}
	if warnings[0].Data["tool"] != "bash" {
		t.Fatalf("event payload missing tool: %+v", warnings[0].Data)
	}
	got := mgr.takeThrashWarning("s1")
	if got == "" || !strings.Contains(got, ThrashWarningText) {
		t.Fatalf("volatile reminder line missing: %q", got)
	}
	if got2 := mgr.takeThrashWarning("s1"); got2 != "" {
		t.Fatalf("warning must be one-shot per step, got %q", got2)
	}
	// Turn boundary reset.
	mgr.resetThrash("s1")
	if got := mgr.takeThrashWarning("s1"); got != "" {
		t.Fatalf("reset must clear manager-level state, got %q", got)
	}
}
