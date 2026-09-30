// Package cmd implements the CLI commands.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/Haslab-dev/RakitKode/internal/app"
	"github.com/Haslab-dev/RakitKode/internal/config"
	"github.com/Haslab-dev/RakitKode/internal/session"
	"github.com/Haslab-dev/RakitKode/internal/tui"
)

var version = "dev"

// SetVersion overrides the default version string (build-time injection).
func SetVersion(v string) { version = v }

func workingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// NewRootCmd assembles the command tree.
func NewRootCmd() *cobra.Command {
	var (
		dataDir         string
		skipPermissions bool
		sessionID       string
		continueLast    bool
	)

	root := &cobra.Command{
		Use:     "rakitkode",
		Short:   "RakitKode - AI coding agent in your terminal",
		Version: version,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context(), runOptions{
				dataDir:         dataDir,
				skipPermissions: skipPermissions,
				sessionID:       sessionID,
				continueLast:    continueLast,
			})
		},
	}

	root.PersistentFlags().StringVar(&dataDir, "data-dir", "", "data directory (default .rakitkode)")
	root.PersistentFlags().BoolVar(&skipPermissions, "dangerously-skip-permissions", false, "skip all permission prompts")
	root.Flags().StringVar(&sessionID, "session", "", "resume a session by ID")
	root.Flags().BoolVarP(&continueLast, "continue", "c", false, "continue the most recent session")

	root.AddCommand(newRunCmd())
	root.AddCommand(newSessionsCmd())
	return root
}

type runOptions struct {
	dataDir         string
	skipPermissions bool
	sessionID       string
	continueLast    bool
}

func runTUI(ctx context.Context, opts runOptions) error {
	wd := workingDir()
	store, err := config.Load(wd)
	if err != nil {
		return err
	}
	if opts.dataDir != "" {
		store.Config().Options.DataDirectory = opts.dataDir
		if !filepath.IsAbs(opts.dataDir) {
			store.Config().Options.DataDirectory = filepath.Join(wd, opts.dataDir)
		}
	}

	application, err := app.New(ctx, store, opts.skipPermissions)
	if err != nil {
		return err
	}
	defer application.Close()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	if err := application.Start(ctx); err != nil {
		return err
	}

	// Resolve or create the working session.
	sess, err := resolveSession(ctx, application, opts.sessionID, opts.continueLast)
	if err != nil {
		return err
	}

	model := tui.New(application, sess.ID)
	program := tea.NewProgram(model)
	go application.Subscribe(program)

	_, err = program.Run()
	return err
}

// resolveSession finds an existing session or creates one.
func resolveSession(ctx context.Context, application *app.App, sessionID string, continueLast bool) (session.Session, error) {
	if sessionID != "" {
		return application.Sessions.Get(ctx, sessionID)
	}
	if continueLast {
		return application.Sessions.GetLast(ctx)
	}
	return application.Sessions.Create(ctx, "New session")
}

func newSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List saved sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := config.Load(workingDir())
			if err != nil {
				return err
			}
			application, err := app.New(cmd.Context(), store, true)
			if err != nil {
				return err
			}
			defer application.Close()
			sessions, err := application.Sessions.List(cmd.Context())
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("no sessions yet")
				return nil
			}
			for _, s := range sessions {
				fmt.Printf("%s  %s\n", s.ID, s.Title)
			}
			return nil
		},
	}
}

// Execute runs the CLI.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
