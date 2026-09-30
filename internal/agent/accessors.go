package agent

import (
	"context"
	"fmt"
	"sort"

	"charm.land/fantasy"

	"github.com/Haslab-dev/RakitKode/internal/config"
)

// CurrentModelName returns "provider/model" of the active model.
func (a *SessionAgent) CurrentModelName() string {
	a.systemMu.RLock()
	defer a.systemMu.RUnlock()
	return a.largeModel.ProviderID + "/" + a.largeModel.Cfg.ID
}

// CurrentModel returns the active Model bundle.
func (a *SessionAgent) CurrentModel() Model {
	a.systemMu.RLock()
	defer a.systemMu.RUnlock()
	return a.largeModel
}

// ToolNames returns the sorted names of the active tool palette.
func (a *SessionAgent) ToolNames() []string {
	a.systemMu.RLock()
	tools := append([]fantasy.AgentTool(nil), a.tools...)
	a.systemMu.RUnlock()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Info().Name)
	}
	sort.Strings(names)
	return names
}

// LoadedSkillNames returns skills read during this session.
func (a *SessionAgent) LoadedSkillNames() []string { return a.loadedSkills.LoadedNames() }

// SwitchModel rebuilds the primary model from config and installs it,
// keeping the current tool palette. Also re-points the small model at
// the same provider for title generation.
func (c *Coordinator) SwitchModel(ctx context.Context, provider, modelID string) error {
	cfg := c.opts.Config.Config()
	if _, ok := cfg.Providers[provider]; !ok {
		return fmt.Errorf("provider %q is not configured", provider)
	}
	if cfg.GetModel(provider, modelID) == nil {
		return fmt.Errorf("model %q is not in provider %q catalog", modelID, provider)
	}
	large, err := c.buildModel(ctx, config.SelectedModel{Provider: provider, Model: modelID})
	if err != nil {
		return err
	}
	c.agent.SetModel(large)
	c.agent.SetSmallModel(large)
	return nil
}

// Agent returns the underlying session agent.
func (c *Coordinator) Agent() *SessionAgent { return c.agent }
