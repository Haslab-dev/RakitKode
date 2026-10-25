package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Haslab-dev/RakitKode/events"
)

// schedulesPath is the durable schedule store.
func (m *Manager) schedulesPath() string {
	if m.storePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.storePath), "schedules.json")
}

// saveSchedulesLocked persists all schedules. Caller holds schedulesMu.
func (m *Manager) saveSchedulesLocked() {
	path := m.schedulesPath()
	if path == "" {
		return
	}
	list := make([]*AgentSchedule, 0, len(m.schedules))
	for _, s := range m.schedules {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		_ = os.Rename(tmp, path)
	}
}

// loadSchedules restores persisted schedules and re-arms active ones.
func (m *Manager) loadSchedules() {
	path := m.schedulesPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []*AgentSchedule
	if json.Unmarshal(data, &list) != nil {
		return
	}
	m.schedulesMu.Lock()
	defer m.schedulesMu.Unlock()
	for _, s := range list {
		if s == nil || s.ID == "" {
			continue
		}
		m.schedules[s.ID] = s
		if s.Active {
			m.armSchedule(s)
		}
	}
}

// armSchedule starts the timer/cron loop for one schedule. Caller holds
// schedulesMu (or owns the schedule exclusively during creation).
func (m *Manager) armSchedule(item *AgentSchedule) {
	if item == nil || !item.Active || item.armed {
		return
	}
	item.armed = true
	ctx, cancel := context.WithCancel(context.Background())
	item.cancel = cancel

	next := item.NextRun
	if item.CronExpr != "" {
		if n, err := nextCronTime(item.CronExpr, time.Now()); err == nil {
			next = n
		}
	}
	if next.IsZero() || next.Before(time.Now()) {
		if item.CronExpr != "" {
			next = time.Now().Add(time.Minute)
		} else {
			next = time.Now() // missed one-shot: fire immediately
		}
	}
	item.NextRun = next

	go func() {
		defer cancel()
		for {
			wait := time.Until(next)
			if wait < 0 {
				wait = 0
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if !item.Active {
				return
			}

			m.fireSchedule(item)

			if item.CronExpr == "" {
				m.schedulesMu.Lock()
				item.Active = false
				m.saveSchedulesLocked()
				m.schedulesMu.Unlock()
				m.emitAgentEvent(events.AgentScheduleUpdate, item.SessionID, map[string]any{
					"id":         item.ID,
					"session_id": item.SessionID,
					"status":     "fired",
					"prompt":     item.Prompt,
					"task_id":    item.TaskID,
					"active":     false,
				})
				return
			}

			n, err := nextCronTime(item.CronExpr, time.Now())
			if err != nil {
				m.schedulesMu.Lock()
				item.Active = false
				m.saveSchedulesLocked()
				m.schedulesMu.Unlock()
				m.emitAgentEvent(events.AgentScheduleUpdate, item.SessionID, map[string]any{
					"id":         item.ID,
					"session_id": item.SessionID,
					"status":     "error",
					"error":      err.Error(),
					"active":     false,
				})
				return
			}
			m.schedulesMu.Lock()
			item.NextRun = n
			m.saveSchedulesLocked()
			m.schedulesMu.Unlock()
			m.emitAgentEvent(events.AgentScheduleUpdate, item.SessionID, map[string]any{
				"id":         item.ID,
				"session_id": item.SessionID,
				"status":     "rearmed",
				"next_run":   n.Format(time.RFC3339),
				"active":     true,
			})
			next = n
		}
	}()
}

// fireSchedule wakes the owning session with the scheduled prompt.
func (m *Manager) fireSchedule(item *AgentSchedule) {
	wakeUpMsg := fmt.Sprintf("<schedule_notification>\n[Scheduled task]: %s\n</schedule_notification>", item.Prompt)
	targetTaskID := item.TaskID
	if targetTaskID == "" {
		for _, t := range m.toolReg.ListTasks("") {
			if t.Status == "running" {
				targetTaskID = t.ID
				break
			}
		}
	}
	if targetTaskID != "" {
		if t, ok := m.toolReg.GetTask(targetTaskID); ok {
			wakeUpMsg += fmt.Sprintf("\n<background_task_status task_id=%q command=%q status=%q exit_code=%d>\nLast Output:\n%s\n</background_task_status>", t.ID, t.Command, t.Status, t.ExitCode, t.OutputTail)
		}
	}
	_ = m.SendMessage(context.Background(), item.SessionID, wakeUpMsg, nil)
}

// ---------------------------------------------------------------------------
// Minimal 5-field cron parser (minute hour dom month dow).
// Supports *, */n, a-b, a-b/n and comma lists.
// ---------------------------------------------------------------------------

type cronSpec struct {
	minute, hour, dom, month, dow map[int]bool
	domStar, dowStar              bool
}

func parseCron(expr string) (*cronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}
	minute, _, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hour, _, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	dom, domStar, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	month, _, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	dow, dowStar, err := parseCronField(fields[4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	if dow[7] {
		dow[0] = true
	}
	return &cronSpec{minute: minute, hour: hour, dom: dom, month: month, dow: dow, domStar: domStar, dowStar: dowStar}, nil
}

func parseCronField(field string, min, max int) (map[int]bool, bool, error) {
	star := field == "*" || strings.HasPrefix(field, "*/")
	set := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("empty field")
		}
		step := 1
		base := part
		if i := strings.Index(part, "/"); i >= 0 {
			base = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return nil, false, fmt.Errorf("invalid step %q", part)
			}
			step = n
		}
		start, end := min, max
		switch {
		case base == "*":
			// full range
		case strings.Contains(base, "-"):
			parts := strings.SplitN(base, "-", 2)
			a, err1 := strconv.Atoi(parts[0])
			b, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, false, fmt.Errorf("invalid range %q", part)
			}
			start, end = a, b
		default:
			n, err := strconv.Atoi(base)
			if err != nil {
				return nil, false, fmt.Errorf("invalid value %q", part)
			}
			start, end = n, n
		}
		if start < min || end > max || start > end {
			return nil, false, fmt.Errorf("value out of range %q", part)
		}
		for v := start; v <= end; v += step {
			set[v] = true
		}
	}
	return set, star, nil
}

func (s *cronSpec) matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domOK := s.dom[t.Day()]
	dowOK := s.dow[int(t.Weekday())]
	if s.domStar || s.dowStar {
		return domOK && dowOK
	}
	return domOK || dowOK
}

// nextCronTime returns the first matching minute strictly after `after`.
func nextCronTime(expr string, after time.Time) (time.Time, error) {
	spec, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(2, 0, 0)
	for t.Before(limit) {
		if spec.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no matching time within 2 years")
}
