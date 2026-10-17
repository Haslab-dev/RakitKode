package tui

import (
	"fmt"
	"sort"
	"strings"
)

// modelPicker is the interactive /model chooser: a filtered list of
// "provider/model" entries. Arrow keys move, enter applies the model to the
// CURRENT session (and the engine default), esc returns to the input.
type modelPicker struct {
	open    bool
	items   []string
	view    []pickerRow // filtered window actually rendered
	sel     int
	current string
}

// pickerRow is one rendered picker entry.
type pickerRow struct {
	model string
	mark  string // "●" current, " " otherwise
}

const pickerMaxRows = 12

// openModelPicker loads the provider catalog and opens the picker, filtered
// by query (case-insensitive substring on provider/model).
func (m *Model) openModelPicker(query string) {
	m.picker.current = m.modelName
	m.picker.items = m.catalogModels()
	m.picker.sel = 0
	m.picker.open = true
	m.filterModelPicker(query)
}

// catalogModels flattens enabled provider profiles into "provider/model"
// entries, current model first, then stable profile order.
func (m *Model) catalogModels() []string {
	if m.eng == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	if m.modelName != "" {
		out = append(out, m.modelName)
		seen[m.modelName] = true
	}
	for _, p := range m.eng.Profiles() {
		if !p.Enabled {
			continue
		}
		models := p.SelectedModels
		if len(models) == 0 {
			models = p.AvailableModels
		}
		for _, id := range models {
			full := p.ID + "/" + id
			if !seen[full] {
				seen[full] = true
				out = append(out, full)
			}
		}
	}
	return out
}

// filterModelPicker applies the query and selects the closest match to the
// current model.
func (m *Model) filterModelPicker(query string) {
	q := strings.ToLower(strings.TrimSpace(query))
	m.picker.view = m.picker.view[:0]
	for _, model := range m.picker.items {
		if q == "" || strings.Contains(strings.ToLower(model), q) {
			mark := " "
			if model == m.picker.current {
				mark = "●"
			}
			m.picker.view = append(m.picker.view, pickerRow{model: model, mark: mark})
		}
	}
	sort.SliceStable(m.picker.view, func(i, j int) bool {
		a, b := m.picker.view[i], m.picker.view[j]
		// current model first, then catalog order
		ai, bi := indexOf(m.picker.items, a.model), indexOf(m.picker.items, b.model)
		return ai < bi
	})
	if m.picker.sel >= len(m.picker.view) {
		m.picker.sel = len(m.picker.view) - 1
	}
	if m.picker.sel < 0 {
		m.picker.sel = 0
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return len(list)
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

// pickerSelection returns the highlighted model ("" when nothing selected).
func (m *Model) pickerSelection() string {
	if !m.picker.open || m.picker.sel < 0 || m.picker.sel >= len(m.picker.view) {
		return ""
	}
	return m.picker.view[m.picker.sel].model
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
		return []string{toolStyle.Render("✓ model → " + model + "  (this session + future sessions)")}
	}
	return []string{statusStyle.Render("model default set to " + model + " (no session attached)")}
}

// renderModelPicker builds the picker popup above the input: a windowed list
// around the selection so huge catalogs stay inside the panel.
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
		text := fmt.Sprintf(" %s %s", row.mark, row.model)
		if i == m.picker.sel {
			rows = append(rows, menuRowSelectedStyle.Render(text))
		} else {
			rows = append(rows, menuRowStyle.Render(text))
		}
	}
	rows = append(rows, statusStyle.Render("↑/↓ move · enter use for this session · esc back"))
	return menuStyle.Render(strings.Join(rows, "\n"))
}
