package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Resource is one MCP resource.
type Resource struct {
	ServerName  string `json:"server_name"`
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
}

// PromptArgument is one argument accepted by an MCP prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt is one MCP prompt template.
type Prompt struct {
	ServerName  string           `json:"server_name"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// ListResources returns resources across connected servers that support them.
func (m *Manager) ListResources(ctx context.Context) []Resource {
	m.connMu.RLock()
	conns := make([]*serverConnection, 0, len(m.connections))
	for _, c := range m.connections {
		if c.capResources {
			conns = append(conns, c)
		}
	}
	m.connMu.RUnlock()

	var out []Resource
	for _, conn := range conns {
		res, err := conn.transport.request(ctx, "resources/list", map[string]any{})
		if err != nil {
			continue
		}
		var parsed struct {
			Resources []struct {
				URI         string `json:"uri"`
				Name        string `json:"name"`
				Description string `json:"description"`
				MIMEType    string `json:"mimeType"`
			} `json:"resources"`
		}
		if json.Unmarshal(res, &parsed) != nil {
			continue
		}
		for _, r := range parsed.Resources {
			out = append(out, Resource{
				ServerName:  conn.name,
				URI:         r.URI,
				Name:        r.Name,
				Description: r.Description,
				MIMEType:    r.MIMEType,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServerName != out[j].ServerName {
			return out[i].ServerName < out[j].ServerName
		}
		return out[i].URI < out[j].URI
	})
	return out
}

// ReadResource fetches a resource's text content.
func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) (string, error) {
	m.connMu.RLock()
	conn := m.connections[serverName]
	m.connMu.RUnlock()
	if conn == nil {
		return "", fmt.Errorf("no connected MCP server %q", serverName)
	}
	res, err := conn.transport.request(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return "", err
	}
	var parsed struct {
		Contents []struct {
			Text     string `json:"text"`
			Blob     string `json:"blob"`
			MIMEType string `json:"mimeType"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range parsed.Contents {
		if c.Text != "" {
			sb.WriteString(c.Text)
		} else if c.Blob != "" {
			fmt.Fprintf(&sb, "[binary content %s, %d base64 chars]", c.MIMEType, len(c.Blob))
		}
	}
	return sb.String(), nil
}

// ListPrompts returns prompt templates across connected servers.
func (m *Manager) ListPrompts(ctx context.Context) []Prompt {
	m.connMu.RLock()
	conns := make([]*serverConnection, 0, len(m.connections))
	for _, c := range m.connections {
		if c.capPrompts {
			conns = append(conns, c)
		}
	}
	m.connMu.RUnlock()

	var out []Prompt
	for _, conn := range conns {
		res, err := conn.transport.request(ctx, "prompts/list", map[string]any{})
		if err != nil {
			continue
		}
		var parsed struct {
			Prompts []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Arguments   []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Required    bool   `json:"required"`
				} `json:"arguments"`
			} `json:"prompts"`
		}
		if json.Unmarshal(res, &parsed) != nil {
			continue
		}
		for _, p := range parsed.Prompts {
			prompt := Prompt{ServerName: conn.name, Name: p.Name, Description: p.Description}
			for _, a := range p.Arguments {
				prompt.Arguments = append(prompt.Arguments, PromptArgument{Name: a.Name, Description: a.Description, Required: a.Required})
			}
			out = append(out, prompt)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServerName != out[j].ServerName {
			return out[i].ServerName < out[j].ServerName
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// GetPrompt renders a prompt template with arguments.
func (m *Manager) GetPrompt(ctx context.Context, serverName, promptName string, args map[string]any) (string, error) {
	m.connMu.RLock()
	conn := m.connections[serverName]
	m.connMu.RUnlock()
	if conn == nil {
		return "", fmt.Errorf("no connected MCP server %q", serverName)
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := conn.transport.request(ctx, "prompts/get", map[string]any{"name": promptName, "arguments": args})
	if err != nil {
		return "", err
	}
	var parsed struct {
		Description string `json:"description"`
		Messages    []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return "", err
	}
	var sb strings.Builder
	if parsed.Description != "" {
		sb.WriteString(parsed.Description + "\n\n")
	}
	for _, msg := range parsed.Messages {
		if msg.Content.Text != "" {
			fmt.Fprintf(&sb, "[%s] %s\n", msg.Role, msg.Content.Text)
		}
	}
	return sb.String(), nil
}
