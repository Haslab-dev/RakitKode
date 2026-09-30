package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpc "github.com/Haslab-dev/RakitKode/internal/agent/tools/mcp"
	"github.com/Haslab-dev/RakitKode/internal/config"
	"github.com/Haslab-dev/RakitKode/internal/permission"
)

// GetMCPTools wraps every connected MCP server tool as an agent tool.
func GetMCPTools(permissions permission.Service, cfg *config.Store, wd string) []*MCPAgentTool {
	var result []*MCPAgentTool
	for mcpName, tools := range mcpc.AllTools() {
		for _, tool := range tools {
			result = append(result, &MCPAgentTool{
				mcpName:     mcpName,
				tool:        tool,
				permissions: permissions,
				workingDir:  wd,
				cfg:         cfg,
			})
		}
	}
	return result
}

// MCPAgentTool adapts one MCP tool to the fantasy agent tool interface.
type MCPAgentTool struct {
	mcpName         string
	tool            *mcp.Tool
	permissions     permission.Service
	workingDir      string
	cfg             *config.Store
	providerOptions fantasy.ProviderOptions
}

// SetProviderOptions implements the fantasy.AgentTool interface.
func (m *MCPAgentTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	m.providerOptions = opts
}

// ProviderOptions implements the fantasy.AgentTool interface.
func (m *MCPAgentTool) ProviderOptions() fantasy.ProviderOptions {
	return m.providerOptions
}

// MCPToolName returns the raw tool name on the server.
func (m *MCPAgentTool) MCPToolName() string { return m.tool.Name }

// MCP returns the server name.
func (m *MCPAgentTool) MCP() string { return m.mcpName }

// Info implements fantasy.AgentTool.
func (m *MCPAgentTool) Info() fantasy.ToolInfo {
	name := fmt.Sprintf("mcp_%s_%s", m.mcpName, m.tool.Name)

	parameters := map[string]any{}
	required := []string{}
	if input, ok := m.tool.InputSchema.(map[string]any); ok {
		if props, ok := input["properties"].(map[string]any); ok {
			parameters = props
		}
		if req, ok := input["required"].([]any); ok {
			for _, v := range req {
				if s, ok := v.(string); ok {
					required = append(required, s)
				}
			}
		}
	}

	return fantasy.ToolInfo{
		Name:        name,
		Description: m.tool.Description,
		Parameters:  parameters,
		Required:    required,
	}
}

// mcpCallInput is the decoded invocation payload.
type mcpCallInput struct {
	Arguments map[string]any `json:"arguments"`
}

// Run implements fantasy.AgentTool.
func (m *MCPAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	sessionID := GetSessionFromContext(ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session_id is required")
	}

	// fantasy passes the model's arguments JSON verbatim as call.Input.
	var args map[string]any
	raw := strings.TrimSpace(call.Input)
	if raw != "" && raw != "null" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			// Some models wrap arguments in an "arguments" key.
			var wrapped mcpCallInput
			if wrapErr := json.Unmarshal([]byte(raw), &wrapped); wrapErr == nil && wrapped.Arguments != nil {
				args = wrapped.Arguments
			} else {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
			}
		}
	}
	if args == nil {
		args = map[string]any{}
	}

	description := fmt.Sprintf("Run MCP tool %s from %s", m.MCPToolName(), m.mcpName)
	granted, err := m.permissions.Request(ctx, permission.CreatePermissionRequest{
		SessionID:   sessionID,
		ToolCallID:  call.ID,
		ToolName:    m.Info().Name,
		Description: description,
		Action:      "mcp",
		Params:      args,
	})
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if !granted {
		return NewPermissionDeniedResponse(), nil
	}

	result, err := mcpc.CallTool(ctx, m.cfg.Config(), m.mcpName, m.MCPToolName(), args)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if strings.HasPrefix(result, "Error: ") {
		return fantasy.NewTextErrorResponse(strings.TrimPrefix(result, "Error: ")), nil
	}
	return fantasy.NewTextResponse(result), nil
}
