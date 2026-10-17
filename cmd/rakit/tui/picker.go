package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Haslab-dev/RakitKode/llm"
)

// modelPicker is the interactive /model chooser: a filtered list of models.
// Rows display "ProviderName/model" but carry the functional
// "provider-id/model" id that engine.SetModel resolves. Arrow keys move,
// enter applies the model to the CURRENT session (and the engine default),
// esc returns to the input.
type modelPicker struct {
	open    bool
	items   []catalogEntry
	view    []pickerRow // filtered window actually rendered
	sel     int
	current string
}

// catalogEntry is one selectable model: the functional id plus its display
// form (profile name instead of the raw provider id).
type catalogEntry struct {
	id      string // "prov-123/model" — what SetModel resolves
	display string // "Kenari/model" — what the UI shows
}

// pickerRow is one rendered picker entry.
type pickerRow struct {
	entry   catalogEntry
	current bool
}

const pickerMaxRows = 12

// openModelPicker loads the provider catalog and opens the picker, filtered
// by query (case-insensitive substring on id or display form).
func (m *Model) openModelPicker(query string) {
	m.picker.current = m.modelName
	m.picker.items = m.catalogModels()
	m.picker.sel = 0
	m.picker.open = true
	m.filterModelPicker(query)
}

// catalogModels flattens enabled provider profiles into entries, current
// model first, then stable profile order.
func (m *Model) catalogModels() []catalogEntry {
	if m.eng == nil {
		return nil
	}
	return buildCatalog(m.eng.Profiles(), m.modelName)
}

// buildCatalog turns provider profiles into display/functional model pairs.
func buildCatalog(profiles []llm.ProviderProfile, current string) []catalogEntry {
	var out []catalogEntry
	seen := map[string]bool{}
	for _, p := range profiles {
		if !p.Enabled {
			continue
		}
		models := p.SelectedModels
		if len(models) == 0 {
			models = p.AvailableModels
		}
		name := p.Name
		if name == "" {
			name = p.ID
		}
		for _, id := range models {
			full := p.ID + "/" + id
			if seen[full] {
				continue
			}
			seen[full] = true
			out = append(out, catalogEntry{id: full, display: name + "/" + id})
		}
	}
	// Sort the current model to the front by matching it against both the
	// functional id and the bare model name.
	sort.SliceStable(out, func(i, j int) bool {
		return catalogMatch(out[i], current) && !catalogMatch(out[j], current)
	})
	return out
}

// catalogMatch reports whether the entry is the session's current model,
// tolerating the bare "model" and full "provider/model" spellings.
func catalogMatch(e catalogEntry, current string) bool {
	if current == "" {
		return false
	}
	if e.id == current {
		return true
	}
	modelPart := e.id
	if i := strings.Index(modelPart, "/"); i >= 0 {
		modelPart = modelPart[i+1:]
	}
	return current == modelPart || strings.HasSuffix(current, "/"+modelPart)
}

// filterModelPicker applies the query and clamps the selection.
func (m *Model) filterModelPicker(query string) {
	q := strings.ToLower(strings.TrimSpace(query))
	m.picker.view = m.picker.view[:0]
	for _, entry := range m.picker.items {
		if q == "" ||
			strings.Contains(strings.ToLower(entry.id), q) ||
			strings.Contains(strings.ToLower(entry.display), q) {
			m.picker.view = append(m.picker.view, pickerRow{
				entry:   entry,
				current: catalogMatch(entry, m.picker.current),
			})
		}
	}
	if m.picker.sel >= len(m.picker.view) {
		m.picker.sel = len(m.picker.view) - 1
	}
	if m.picker.sel < 0 {
		m.picker.sel = 0
	}
}

// movePickerSelection moves the highlight by delta, clamping.
func (m *Model) movePickerSelection(delta int) {
	if !m.picker.open {
		return
	}
	m.picker.sel += delta
	if m.picker.sel < 0 {
		m.picker.sel = 0
	}
	if m.picker.sel >= len(m.picker.view) {
		m.picker.sel = len(m.picker.view) - 1
	}
	if m.picker.sel < 0 {
		m.picker.sel = 0
	}
}

// pickerSelection returns the highlighted model's functional id ("" when
// nothing is selected).
func (m *Model) pickerSelection() string {
	if !m.picker.open || m.picker.sel < 0 || m.picker.sel >= len(m.picker.view) {
		return ""
	}
	return m.picker.view[m.picker.sel].entry.id
}

// applyModelSwitch pins the model on the current session and updates the
// engine-wide default so future sessions match.
func (m *Model) applyModelSwitch(model string) []string {
	if model == "" {
		return nil
	}
	if m.eng == nil {
		return []string{errorStyle.Render("no engine attached")}
	}
	if !m.eng.SetModel(model) {
		return []string{errorStyle.Render(fmt.Sprintf("no provider profile matches %q", model))}
	}
	if m.mgr != nil {
		if err := m.mgr.SetSessionModel(m.sessionID, model); err != nil {
			return []string{errorStyle.Render("error: " + err.Error())}
		}
		m.modelName = model
		return []string{toolDotOKStyle.Render("✓ model → " + m.displayModelName(model) + " (session + default)")}
	}
	return []string{statusStyle.Render("model default set to " + model + " (no session attached)")}
}

// displayModelName renders "prov-id/model" using the profile's display name
// when one matches, falling back to the raw id.
func (m *Model) displayModelName(fullID string) string {
	for _, p := range m.eng.Profiles() {
		if p.ID != "" && strings.HasPrefix(fullID, p.ID+"/") && p.Name != "" {
			return p.Name + "/" + strings.TrimPrefix(fullID, p.ID+"/")
		}
	}
	return fullID
}

// renderModelPicker builds the picker popup above the input: a windowed list
// around the selection so huge catalogs stay inside the panel. Rows show the
// provider dim and the model bright so the model id pops.
func (m Model) renderModelPicker() string {
	if !m.picker.open {
		return ""
	}
	if len(m.picker.view) == 0 {
		return menuStyle.Render(statusStyle.Render("  no models match — esc to go back"))
	}
	start := m.picker.sel - pickerMaxRows/2
	if start < 0 {
		start = 0
	}
	end := start + pickerMaxRows
	if end > len(m.picker.view) {
		end = len(m.picker.view)
		start = end - pickerMaxRows
		if start < 0 {
			start = 0
		}
	}

	rows := []string{headerStyle.Render(fmt.Sprintf("select model  %d/%d", m.picker.sel+1, len(m.picker.view)))}
	for i := start; i < end; i++ {
		row := m.picker.view[i]
		cursor, style := "  ", menuRowStyle
		if i == m.picker.sel {
			cursor, style = "❯ ", menuRowSelectedStyle
		}
		mark := " "
		if row.current {
			mark = "●"
		}
		provider, model := splitProviderModel(row.entry.display)
		rows = append(rows, style.Render(cursor+mark+" "+statusStyle.Render(provider)+model))
	}
	rows = append(rows, statusStyle.Render("↑/↓ move · type to filter · enter use · esc back"))
	return menuStyle.Render(strings.Join(rows, "\n"))
}

// splitProviderModel splits "provider/model" for two-tone rendering.
func splitProviderModel(full string) (provider, model string) {
	if i := strings.Index(full, "/"); i >= 0 {
		return full[:i+1], full[i+1:]
	}
	return "", full
}
