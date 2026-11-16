package tools

import (
	"context"
	"fmt"
)

// Brain ("living memory") tools. They follow the learn_memory pattern: a
// thin spec here, all state behind SessionBridge, and cost classification in
// metadata.go. The store itself lives in the brain package; hosts that never
// attach a Brain make these tools return a clear "not initialized" error.

func writeNoteTool() ToolSpec {
	return ToolSpec{
		Name:        "write_note",
		Description: "Write a freeform markdown note into the project's living-memory store (brain). Use for knowledge too large or too contextual for a single fact: research findings, design rationales, runbooks. A note_ref fact is recorded so the note stays discoverable via search_brain.",
		Cost:        "cheap",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string", "description": "Short title of the note"},
				"body":  map[string]any{"type": "string", "description": "Markdown body of the note"},
			},
			"required": []string{"title", "body"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			bridge := SessionBridgeFrom(ctx)
			if bridge == nil {
				return nil, fmt.Errorf("write_note is unavailable outside an agent session")
			}
			title := argString(args, "title")
			body := argString(args, "body")
			if title == "" {
				return nil, fmt.Errorf("title is required")
			}
			return bridge.WriteBrainNote(title, body)
		},
	}
}

func searchBrainTool() ToolSpec {
	return ToolSpec{
		Name:        "search_brain",
		Description: "Search the project's living-memory store for durable facts: decisions, pitfalls, preferences, conventions, task outcomes, and note references. Returns the top matching valid facts with their dates and file refs.",
		Cost:        "cheap",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Keywords to rank facts by (empty lists the most recent)"},
				"limit": map[string]any{"type": "integer", "description": "Max results (default 8)"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			bridge := SessionBridgeFrom(ctx)
			if bridge == nil {
				return nil, fmt.Errorf("search_brain is unavailable outside an agent session")
			}
			query := argString(args, "query")
			limit := 8
			if v, ok := args["limit"].(float64); ok && v > 0 {
				limit = int(v)
			}
			facts, err := bridge.SearchBrain(query, limit)
			if err != nil {
				return nil, err
			}
			return toolResult(map[string]any{"facts": facts, "count": len(facts)}), nil
		},
	}
}

func readDigestTool() ToolSpec {
	return ToolSpec{
		Name:        "read_digest",
		Description: "Read a past session's digest (objective, requests, decisions, files touched, outcome) from the project's living-memory store. Without session_id, returns the most recent digest.",
		Cost:        "cheap",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{"type": "string", "description": "Session id whose digest to read (omit for the latest)"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			bridge := SessionBridgeFrom(ctx)
			if bridge == nil {
				return nil, fmt.Errorf("read_digest is unavailable outside an agent session")
			}
			sessionID := argString(args, "session_id")
			return bridge.ReadBrainDigest(sessionID)
		},
	}
}
