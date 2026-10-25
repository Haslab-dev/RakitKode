package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMergeToolCallArgs(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		incoming string
		want     string
	}{
		{"empty incoming keeps existing", `{"a":1}`, "", `{"a":1}`},
		{"empty existing adopts incoming", "", `{"a":1}`, `{"a":1}`},
		{"true fragments concatenate", `{"path": `, `"/x"}`, `{"path": "/x"}`},
		{"complete resend replaces (gateway duplicate)", `{"path":"/x"}`, `{"path":"/x"}`, `{"path":"/x"}`},
		{"complete resend with edits replaces", `{"path":"/old"}`, `{"path":"/new","flag":true}`, `{"path":"/new","flag":true}`},
		{"nested object fragment still appends", `{"outer": `, `{"y":1}`, `{"outer": {"y":1}`},
		{"value fragment appends", `{"n": `, `42`, `{"n": 42`},
	}
	for _, tc := range cases {
		if got := MergeToolCallArgs(tc.existing, tc.incoming); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestParseSSEStreamDuplicateToolArgs drives parseSSEStream with the
// gateway behavior observed on parallel tool calls: an empty establishing
// chunk followed by the complete arguments sent twice. The accumulated
// arguments must end up as one valid JSON object per tool call.
func TestParseSSEStreamDuplicateToolArgs(t *testing.T) {
	line := func(index int, id string, args string) string {
		return fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":%d,"id":"%s","type":"function","function":{"name":"find","arguments":%s}}]}}]}`,
			index, id, args)
	}
	var b strings.Builder
	b.WriteString(line(0, "call-a", `""`) + "\n\n")
	b.WriteString(line(0, "call-a", `"{\"path\": \"**/*.go\"}"`) + "\n\n")
	b.WriteString(line(0, "call-a", `"{\"path\": \"**/*.go\"}"`) + "\n\n")
	b.WriteString(line(1, "call-b", `""`) + "\n\n")
	b.WriteString(line(1, "call-b", `"{\"pattern\": \"x\"}"`) + "\n\n")
	b.WriteString(line(1, "call-b", `"{\"pattern\": \"x\"}"`) + "\n\n")
	b.WriteString("data: [DONE]\n\n")

	resp, err := parseSSEStream(context.Background(), strings.NewReader(b.String()), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(resp.ToolCalls))
	}
	seen := map[string]string{}
	for _, tc := range resp.ToolCalls {
		if !json.Valid([]byte(tc.Function.Arguments)) {
			t.Fatalf("tool call %q has invalid accumulated args: %q", tc.Function.Name, tc.Function.Arguments)
		}
		seen[tc.ID] = tc.Function.Arguments
	}
	if seen["call-a"] != `{"path": "**/*.go"}` {
		t.Fatalf("call-a args = %q", seen["call-a"])
	}
	if seen["call-b"] != `{"pattern": "x"}` {
		t.Fatalf("call-b args = %q", seen["call-b"])
	}
}
