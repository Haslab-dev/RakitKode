package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/fantasy"

	"github.com/Haslab-dev/RakitKode/internal/session"
)

// TodosParams are the inputs of the todos tool.
type TodosParams struct {
	Todos []session.Todo `json:"todos" description:"The updated todo list"`
}

// TodosResponseMetadata echoes the new todo state.
type TodosResponseMetadata struct {
	Todos []session.Todo `json:"todos"`
}

const TodosToolName = "todos"

const todosDescription = `Manage a task list for the current session.
Each todo has content and a status (pending, in_progress, completed).
Mark todos in_progress before starting them; completed immediately after.
Only one todo should be in_progress at a time.
Use this tool for complex multi-step tasks (3 or more steps).
Not intended for simple single-step tasks or conversations.
`

// NewTodosTool builds the todos tool.
func NewTodosTool(sessions session.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TodosToolName,
		todosDescription,
		func(ctx context.Context, params TodosParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session_id is required")
			}

			sess, err := sessions.Get(ctx, sessionID)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to get session: %w", err)
			}
			sess.Todos = params.Todos
			if _, err := sessions.Save(ctx, sess); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to save todos: %w", err)
			}

			out, err := json.MarshalIndent(params.Todos, "", "  ")
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to marshal todos: %w", err)
			}
			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(string(out)),
				TodosResponseMetadata{Todos: params.Todos},
			), nil
		},
	)
}
