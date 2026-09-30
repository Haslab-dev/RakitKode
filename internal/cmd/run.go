package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/fantasy"
	"github.com/spf13/cobra"

	"github.com/Haslab-dev/RakitKode/internal/agent"
	"github.com/Haslab-dev/RakitKode/internal/app"
	"github.com/Haslab-dev/RakitKode/internal/config"
)

func newRunCmd() *cobra.Command {
	var (
		model     string
		sessionID string
	)

	cmd := &cobra.Command{
		Use:   "run [prompt...]",
		Short: "Run a single non-interactive prompt and exit",
		Long: `Run a single prompt in non-interactive mode and exit.
The prompt can be provided as arguments or piped from stdin.`,
		Example: `
# Run a simple prompt
rakitkode run "explain what this repo does"

# Pipe input from stdin
cat main.go | rakitkode run "review this code"

# Continue a previous session
rakitkode run --session {session-id} "now add tests"
`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := joinPrompt(args, cmd.InOrStdin())
			if prompt == "" {
				return fmt.Errorf("prompt is required")
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			wd := workingDir()
			store, err := config.Load(wd)
			if err != nil {
				return err
			}

			application, err := app.New(ctx, store, store.Config().Options.SkipPermissions)
			if err != nil {
				return err
			}
			defer application.Close()

			// Non-interactive runs never prompt for permission.
			application.Permissions.SetSkipRequests(true)

			if err := application.Start(ctx); err != nil {
				return err
			}

			if model != "" {
				// "provider/model" override for this run.
				if err := overrideModel(ctx, application, model); err != nil {
					return err
				}
			}

			var sess struct{ ID string }
			if sessionID != "" {
				s, err := application.Sessions.Get(ctx, sessionID)
				if err != nil {
					return fmt.Errorf("session %q not found: %w", sessionID, err)
				}
				sess.ID = s.ID
			} else {
				s, err := application.Sessions.Create(ctx, "run")
				if err != nil {
					return err
				}
				sess.ID = s.ID
			}

			// Block until the run finishes; RunComplete events stream
			// through the broker but stdout only needs the final text.
			result, runErr := application.Coordinator.Agent().Run(ctx, agent.AgentCall{
				SessionID: sess.ID,
				Prompt:    prompt,
			})
			if runErr != nil {
				return runErr
			}
			if result != nil {
				text := finalText(result)
				fmt.Fprintln(os.Stdout, text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "model override as provider/model")
	cmd.Flags().StringVar(&sessionID, "session", "", "continue this session")
	return cmd
}

// finalText extracts the assistant text from the agent result.
func finalText(result *fantasy.AgentResult) string {
	if text := result.Response.Content.Text(); text != "" {
		return text
	}
	for i := len(result.Steps) - 1; i >= 0; i-- {
		if text := result.Steps[i].Content.Text(); text != "" {
			return text
		}
	}
	return ""
}

// joinPrompt merges args and stdin into one prompt string.
func joinPrompt(args []string, stdin io.Reader) string {
	out := ""
	for _, a := range args {
		if out != "" {
			out += " "
		}
		out += a
	}
	if len(args) == 0 && stdin != nil {
		if data, err := io.ReadAll(stdin); err == nil {
			out = string(data)
		}
	}
	return trimSpace(out)
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// overrideModel switches the agent model to provider/model for this run.
// Model IDs may contain slashes (e.g. glm/glm-5.3-flash), so the split
// happens at the FIRST slash: the provider is the simple prefix.
func overrideModel(ctx context.Context, application *app.App, model string) error {
	idx := strings.Index(model, "/")
	if idx <= 0 {
		return fmt.Errorf("model override must be provider/model, got %q", model)
	}
	provider, name := model[:idx], model[idx+1:]
	application.Config.Config().Models = map[config.SelectedModelType]config.SelectedModel{
		config.SelectedModelTypeLarge: {Provider: provider, Model: name},
	}
	return application.Coordinator.InitModels(ctx)
}
