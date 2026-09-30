// Package app wires the top-level services into a runnable application.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/Haslab-dev/RakitKode/internal/agent"
	"github.com/Haslab-dev/RakitKode/internal/agent/tools/mcp"
	"github.com/Haslab-dev/RakitKode/internal/config"
	"github.com/Haslab-dev/RakitKode/internal/db"
	"github.com/Haslab-dev/RakitKode/internal/filetracker"
	"github.com/Haslab-dev/RakitKode/internal/history"
	"github.com/Haslab-dev/RakitKode/internal/message"
	"github.com/Haslab-dev/RakitKode/internal/permission"
	"github.com/Haslab-dev/RakitKode/internal/pubsub"
	"github.com/Haslab-dev/RakitKode/internal/session"
)

// App is the assembled application: services, agent, event feed.
type App struct {
	Config      *config.Store
	Sessions    session.Service
	Messages    message.Service
	Files       history.Service
	FileTracker filetracker.Service
	Permissions permission.Service
	Conn        *sql.DB

	Coordinator  *agent.Coordinator
	RunCompletions *pubsub.Broker[agent.RunComplete]

	feed *appFeed
}

type appFeed struct{ app *App }

// PublishRunComplete implements agent.RunCompleteFeed.
func (f *appFeed) PublishRunComplete(rc agent.RunComplete) {
	f.app.RunCompletions.Publish(pubsub.CreatedEvent, rc)
}

// New opens the database, builds services, connects MCP servers, and
// prepares the agent coordinator.
func New(ctx context.Context, store *config.Store, skipPermissions bool) (*App, error) {
	dataDir := store.Config().DataDir()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}
	conn, err := db.Connect(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	db.SetHandle(conn)
	q := db.NewQueries(conn)

	sessions := session.NewService(q)
	messages := message.NewService(q)
	files := history.NewService(q)
	filetrackerSvc := filetracker.NewService(q)

	var allowed []string
	permissions := permission.NewPermissionService(store.WorkingDir(), skipPermissions, allowed)

	app := &App{
		Config:         store,
		Sessions:       sessions,
		Messages:       messages,
		Files:          files,
		FileTracker:    filetrackerSvc,
		Permissions:    permissions,
		Conn:           conn,
		RunCompletions: pubsub.NewBroker[agent.RunComplete](),
	}
	app.feed = &appFeed{app: app}

	coordinator, err := agent.NewCoordinator(agent.CoordinatorOptions{
		Config:      store,
		Sessions:    sessions,
		Messages:    messages,
		Files:       files,
		FileTracker: filetrackerSvc,
		Permissions: permissions,
	})
	if err != nil {
		return nil, err
	}
	coordinator.Agent().RunCompleteFeed = app.feed
	app.Coordinator = coordinator

	return app, nil
}

// Start connects models, MCP servers, and loads the tool palette.
func (app *App) Start(ctx context.Context) error {
	if err := app.Coordinator.InitModels(ctx); err != nil {
		return fmt.Errorf("failed to init models: %w", err)
	}

	// Connect MCP servers synchronously in non-interactive / startup
	// path so the first tool palette already contains their tools.
	if len(app.Config.Config().MCP) > 0 {
		mcp.Initialize(ctx, app.Config.Config())
	}

	tools, err := app.Coordinator.BuildTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to build tools: %w", err)
	}
	app.Coordinator.Agent().SetTools(tools)
	return nil
}

// Close releases resources.
func (app *App) Close() {
	mcp.CloseAll()
	if app.Conn != nil {
		app.Conn.Close()
	}
}

// Subscribe forwards service events (session/message updates) into the
// TUI program as tea.Msg values.
func (app *App) Subscribe(program *tea.Program) {
	ctx := context.Background()
	sessCh := app.Sessions.Subscribe(ctx)
	msgCh := app.Messages.Subscribe(ctx)
	permCh := app.Permissions.Subscribe(ctx)
	permNotifCh := app.Permissions.SubscribeNotifications(ctx)
	done := make(chan struct{})
	go func() {
		program.Wait()
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		case ev, ok := <-sessCh:
			if !ok {
				return
			}
			program.Send(SessionUpdateMsg(ev))
		case ev, ok := <-msgCh:
			if !ok {
				return
			}
			program.Send(MessageUpdateMsg(ev))
		case ev, ok := <-permCh:
			if !ok {
				return
			}
			program.Send(PermissionRequestMsg(ev.Payload))
		case ev, ok := <-permNotifCh:
			if !ok {
				return
			}
			program.Send(PermissionNotificationMsg(ev.Payload))
		}
	}
}
