package tools

import (
	"context"
)

// toolSearchTool creates the tool_search meta-tool for dynamic tool discovery (Maki & San parity).
// It enables the agent to search and discover available tools across core tools and MCP servers
// on-demand without bloating every turn's prompt context with dozens of unused schemas.
func toolSearchTool(r *Registry) ToolSpec {
	return ToolSpec{
		Name: "tool_search",
		Description: "Search and inspect available tools in the registry and connected MCP servers by query (e.g. 'git', 'postgres', 'deploy', 'subagent'). " +
			"Returns matching tool schemas, descriptions, and parameter documentation on demand without bloating the prompt context.",
		Cost:     "cheap",
		Category: "meta",
		ReadOnly: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keyword or capability to search for (e.g. 'database', 'git', 'browse', 'bash')",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of tools to return (default 10)",
				},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			query := argString(args, "query")
			if query == "" {
				query = argString(args, "q")
			}
			limit := 10
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			} else if l, ok := args["limit"].(int); ok && l > 0 {
				limit = l
			}
			if limit > 25 {
				limit = 25
			}

			matches := r.Search(query)
			if len(matches) > limit {
				matches = matches[:limit]
			}

			results := make([]map[string]any, 0, len(matches))
			for _, m := range matches {
				entry := map[string]any{
					"name":        m.Name,
					"description": m.Description,
					"parameters":  m.Parameters,
					"is_mcp":      m.IsMCP,
				}
				if m.ServerName != "" {
					entry["server"] = m.ServerName
				}
				if m.Category != "" {
					entry["category"] = m.Category
				}
				results = append(results, entry)
			}

			return toolResult(map[string]any{
				"query":   query,
				"count":   len(results),
				"matches": results,
			}), nil
		},
	}
}
