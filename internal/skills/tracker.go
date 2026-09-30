package skills

import (
	"sync"
)

// Tracker tracks which skills have been loaded (read) during a session.
// It is safe for concurrent use.
type Tracker struct {
	mu          sync.RWMutex
	loaded      map[string]bool
	activeNames map[string]bool
}

// NewTracker creates a tracker limited to the given active skills.
func NewTracker(activeSkills []*Skill) *Tracker {
	activeNames := make(map[string]bool, len(activeSkills))
	for _, s := range activeSkills {
		activeNames[s.Name] = true
	}
	return &Tracker{
		loaded:      make(map[string]bool),
		activeNames: activeNames,
	}
}

// MarkLoaded marks a skill as loaded if it is active.
func (t *Tracker) MarkLoaded(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.activeNames[name] {
		t.loaded[name] = true
	}
}

// IsLoaded returns true if the skill has been loaded.
func (t *Tracker) IsLoaded(name string) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.loaded[name]
}

// LoadedNames returns loaded skill names, sorted alphabetically.
func (t *Tracker) LoadedNames() []string {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.loaded))
	for name := range t.loaded {
		out = append(out, name)
	}
	return out
}
