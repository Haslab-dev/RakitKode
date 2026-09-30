package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"

	agenttools "github.com/Haslab-dev/RakitKode/internal/agent/tools"
	"github.com/Haslab-dev/RakitKode/internal/config"
	"github.com/Haslab-dev/RakitKode/internal/filetracker"
	"github.com/Haslab-dev/RakitKode/internal/history"
	"github.com/Haslab-dev/RakitKode/internal/message"
	"github.com/Haslab-dev/RakitKode/internal/permission"
	"github.com/Haslab-dev/RakitKode/internal/session"
)

// CoordinatorOptions wires everything the coordinator needs.
type CoordinatorOptions struct {
	Config      *config.Store
	Sessions    session.Service
	Messages    message.Service
	Files       history.Service
	FileTracker filetracker.Service
	Permissions permission.Service
}

// Coordinator assembles tools and models, exposing a single agent.
type Coordinator struct {
	opts  CoordinatorOptions
	agent *SessionAgent
}

// NewCoordinator builds the coordinator and its agent.
func NewCoordinator(opts CoordinatorOptions) (*Coordinator, error) {
	c := &Coordinator{opts: opts}
	c.agent = NewSessionAgent(
		opts.Config,
		opts.Sessions,
		opts.Messages,
		opts.Files,
		opts.FileTracker,
		opts.Permissions,
	)
	return c, nil
}

// ResolveModel builds the primary Model from config for the given slot.
func (c *Coordinator) ResolveModel(ctx context.Context, slot config.SelectedModelType) (Model, error) {
	selected, ok := c.opts.Config.Config().Models[slot]
	if !ok {
		return Model{}, fmt.Errorf("no %s model configured", slot)
	}
	return c.buildModel(ctx, selected)
}

// buildModel constructs provider + language model for a selection.
func (c *Coordinator) buildModel(ctx context.Context, selected config.SelectedModel) (Model, error) {
	provider, err := buildProvider(c.opts.Config, selected.Provider)
	if err != nil {
		return Model{}, err
	}
	lm, err := provider.LanguageModel(ctx, selected.Model)
	if err != nil {
		return Model{}, fmt.Errorf("failed to create language model: %w", err)
	}
	modelCfg := c.opts.Config.Config().GetModel(selected.Provider, selected.Model)
	if modelCfg == nil {
		return Model{}, fmt.Errorf("model %q not found on provider %q", selected.Model, selected.Provider)
	}
	return Model{
		Model:      lm,
		ProviderID: selected.Provider,
		Cfg:        *modelCfg,
	}, nil
}

// InitModels resolves large/small models from config and installs them.
func (c *Coordinator) InitModels(ctx context.Context) error {
	large, err := c.ResolveModel(ctx, config.SelectedModelTypeLarge)
	if err != nil {
		return err
	}
	c.agent.SetModel(large)

	small, err := c.ResolveModel(ctx, config.SelectedModelTypeSmall)
	if err != nil {
		// Fall back to the large model for utility tasks.
		c.agent.SetSmallModel(large)
	} else {
		c.agent.SetSmallModel(small)
	}
	return nil
}

// BuildTools assembles the built-in tool palette plus MCP tools.
func (c *Coordinator) BuildTools(ctx context.Context) ([]fantasy.AgentTool, error) {
	wd := c.opts.Config.WorkingDir()
	cfg := c.opts.Config.Config()

	tools := []fantasy.AgentTool{
		agenttools.NewViewTool(c.opts.Permissions, c.opts.FileTracker, wd),
		agenttools.NewWriteTool(c.opts.Permissions, c.opts.Files, c.opts.FileTracker, wd),
		agenttools.NewEditTool(c.opts.Permissions, c.opts.Files, c.opts.FileTracker, wd),
		agenttools.NewLsTool(c.opts.Permissions, wd),
		agenttools.NewGlobTool(wd),
		agenttools.NewGrepTool(wd),
		agenttools.NewBashTool(c.opts.Permissions, wd),
		agenttools.NewTodosTool(c.opts.Sessions),
	}

	// MCP tools for connected servers.
	if len(cfg.MCP) > 0 {
		for _, t := range agenttools.GetMCPTools(c.opts.Permissions, c.opts.Config, wd) {
			tools = append(tools, t)
		}
	}
	return tools, nil
}
