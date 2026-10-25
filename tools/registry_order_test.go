package tools

import (
	"testing"

	"github.com/Haslab-dev/RakitKode/search"
)

func TestDefinitionsOrderIsDeterministic(t *testing.T) {
	r := NewRegistry(search.NewSearchManager())

	first := r.Definitions()
	if len(first) == 0 {
		t.Fatal("expected core tool definitions")
	}
	for i := 1; i < len(first); i++ {
		prev, cur := first[i-1], first[i]
		ps, ok := r.Lookup(prev.Function.Name)
		if !ok {
			t.Fatalf("missing spec for %q", prev.Function.Name)
		}
		cs, ok := r.Lookup(cur.Function.Name)
		if !ok {
			t.Fatalf("missing spec for %q", cur.Function.Name)
		}
		if ps.Order > cs.Order || (ps.Order == cs.Order && ps.Name > cs.Name) {
			t.Fatalf("definitions not sorted: %d/%s before %d/%s", ps.Order, ps.Name, cs.Order, cs.Name)
		}
	}

	for i := 0; i < 20; i++ {
		next := r.Definitions()
		if len(next) != len(first) {
			t.Fatalf("length changed: %d != %d", len(next), len(first))
		}
		for j := range first {
			if first[j].Function.Name != next[j].Function.Name {
				t.Fatalf("order changed at %d: %q != %q", j, first[j].Function.Name, next[j].Function.Name)
			}
		}
	}
}

func TestRegisterAssignsStableOrder(t *testing.T) {
	r := NewRegistry(search.NewSearchManager())
	before := r.Definitions()

	r.Register(ToolSpec{Name: "zz_test_tool", Description: "test"})
	after := r.Definitions()

	if len(after) != len(before)+1 {
		t.Fatalf("expected one new definition, got %d -> %d", len(before), len(after))
	}
	last := after[len(after)-1]
	if last.Function.Name != "zz_test_tool" {
		t.Fatalf("new tool should sort last, got %q", last.Function.Name)
	}
}
