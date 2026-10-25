package tools

import "context"

// EnterPlanModeTool switches the session into plan mode: read-only tools only,
// and the model produces a plan artifact before exiting.
func EnterPlanModeTool() ToolSpec {
	return ToolSpec{
		Name:        "enter_plan_mode",
		Cost:        "cheap",
		Description: "Switch to plan mode. In plan mode only read-only tools are allowed; investigate and write a plan, then call exit_plan_mode for approval.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return toolResult(map[string]any{"mode": "plan", "status": "enabled"}), nil
		},
	}
}

// ExitPlanModeTool requests approval of the plan and returns to build mode.
func ExitPlanModeTool() ToolSpec {
	return ToolSpec{
		Name:        "exit_plan_mode",
		Cost:        "cheap",
		Description: "Present the current plan for approval and return to build mode. Call only after the plan is complete.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan": map[string]any{"type": "string", "description": "The plan summary to present to the user"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return toolResult(map[string]any{"mode": "build", "status": "exit_requested"}), nil
		},
	}
}
