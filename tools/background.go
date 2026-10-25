package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Background task registry — the long-running-task layer (port of ZCode's
// background bash + TaskOutput/TaskStop machinery). `bash
// run_in_background:true` spawns a detached process, registers it here, and
// returns immediately; `task_output` polls or blocks on it; `task_stop` kills
// it. Completed tasks queue a <task-notification> that the agent drains into
// the next model step so the model learns the outcome without polling.
// ---------------------------------------------------------------------------

// TaskStatus is the lifecycle state of a background task.
type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed" // exit code 0
	TaskFailed    TaskStatus = "failed"    // non-zero exit
	TaskStopped   TaskStatus = "stopped"   // killed via task_stop
	// TaskLost marks a task that was running when the process exited. The
	// detached process may still be alive, but its pipes are gone, so the
	// registry cannot reattach — the log file remains readable.
	TaskLost TaskStatus = "lost"
)

// BackgroundTask is one detached shell command.
type BackgroundTask struct {
	ID        string     `json:"id"`
	SessionID string     `json:"session_id"`
	Command   string     `json:"command"`
	Cwd       string     `json:"cwd,omitempty"`
	Status    TaskStatus `json:"status"`
	ExitCode  int        `json:"exit_code,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	Finished  *time.Time `json:"finished_at,omitempty"`

	// OutputFile persists the full stdout+stderr stream so output survives
	// beyond the in-memory ring buffer (ZCode's artifact spill).
	OutputFile string `json:"output_file,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
	OutputSize int64  `json:"output_size"`

	notified bool // completion notification already drained into the conversation

	reg    *Registry
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
	tail   *tailWriter
}

// tailWriter tees output to the full log file while keeping a bounded
// in-memory tail for quick task_output reads.
type tailWriter struct {
	mu         sync.Mutex
	file       *os.File
	buf        bytes.Buffer
	max        int
	task       *BackgroundTask
	lastNotify time.Time
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Write(p); err != nil {
		return 0, err
	}
	w.buf.Write(p)
	if w.buf.Len() > w.max {
		drop := w.buf.Len() - w.max
		w.buf.Next(drop)
	}
	if w.task != nil {
		w.task.OutputTail = w.buf.String()
		now := time.Now()
		if now.Sub(w.lastNotify) >= 150*time.Millisecond {
			w.lastNotify = now
			t := w.task
			go t.notify()
		}
	}
	return len(p), nil
}

func (w *tailWriter) tail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

const (
	// outputTailLimit caps the in-memory tail returned by task_output before
	// it points at the full output file.
	outputTailLimit = 16 * 1024
	// maxWaitPollInterval is the poll granularity while blocking in task_output.
	waitPollInterval = 100 * time.Millisecond
)

// bgState holds the background-task registry on the ToolRegistry. Tasks must
// outlive individual tool handlers and agent turns, so they live on the
// registry rather than in handler scope.
type bgState struct {
	mu                    sync.Mutex
	tasks                 map[string]*BackgroundTask
	taskUpdate            func(task *BackgroundTask)
	sessionFolderResolver func(sessionID string) string
	persistPath           string
}

// SetBackgroundStore points the registry at a JSON file and restores any
// tasks persisted by a previous process. Running tasks are marked lost.
func (r *Registry) SetBackgroundStore(path string) {
	r.bg.mu.Lock()
	r.bg.persistPath = path
	r.bg.mu.Unlock()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []*BackgroundTask
	if json.Unmarshal(data, &list) != nil {
		return
	}
	r.RestoreTasks(list)
}

// RestoreTasks rehydrates persisted task records.
func (r *Registry) RestoreTasks(tasks []*BackgroundTask) {
	r.bg.mu.Lock()
	defer r.bg.mu.Unlock()
	for _, t := range tasks {
		if t == nil || t.ID == "" {
			continue
		}
		if _, exists := r.bg.tasks[t.ID]; exists {
			continue
		}
		if t.Status == TaskRunning {
			t.Status = TaskLost
			if t.Finished == nil {
				now := time.Now()
				t.Finished = &now
			}
		}
		t.notified = true // never replay stale completions into a new turn
		t.reg = r
		t.done = make(chan struct{})
		close(t.done)
		r.bg.tasks[t.ID] = t
	}
	r.saveTasksLocked()
}

// saveTasksLocked writes the registry to disk. Caller holds bg.mu.
func (r *Registry) saveTasksLocked() {
	if r.bg.persistPath == "" {
		return
	}
	list := make([]*BackgroundTask, 0, len(r.bg.tasks))
	for _, t := range r.bg.tasks {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt.Before(list[j].StartedAt) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(r.bg.persistPath), 0o755)
	tmp := r.bg.persistPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		_ = os.Rename(tmp, r.bg.persistPath)
	}
}

// SetTaskUpdateHandler installs the lifecycle callback invoked on every task
// transition (called once from the agent manager at construction).
func (r *Registry) SetTaskUpdateHandler(fn func(task *BackgroundTask)) {
	r.bg.mu.Lock()
	r.bg.taskUpdate = fn
	r.bg.mu.Unlock()
}

// SetSessionFolderResolver registers the callback resolving a workspace
// folder for an agent session ID.
func (r *Registry) SetSessionFolderResolver(fn func(sessionID string) string) {
	r.bg.mu.Lock()
	r.bg.sessionFolderResolver = fn
	r.bg.mu.Unlock()
}

// StartBackgroundCommand spawns a detached shell command and registers it as a
// background task owned by the given agent session. The process is detached
// from the caller's context on purpose: it must survive turn cancellation.
func (r *Registry) StartBackgroundCommand(sessionID, command, cwd string) (*BackgroundTask, error) {
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}
	r.bg.mu.Lock()
	resolver := r.bg.sessionFolderResolver
	r.bg.mu.Unlock()
	if cwd == "" && sessionID != "" && resolver != nil {
		cwd = resolver(sessionID)
	}
	if cwd == "" {
		cwd = defaultCwd(context.Background())
	}
	if cwd != "" && !filepath.IsAbs(cwd) {
		if abs, err := filepath.Abs(cwd); err == nil {
			cwd = abs
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/zsh", "-l", "-c", command)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group so StopTask kills children too

	outputFile := filepath.Join(os.TempDir(), fmt.Sprintf("forge-ade-task-%s.log", uuid.New().String()[:8]))
	outFile, err := os.Create(outputFile)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create task output file: %w", err)
	}
	tail := &tailWriter{file: outFile, max: outputTailLimit}
	cmd.Stdout = tail
	cmd.Stderr = tail

	task := &BackgroundTask{
		ID:         uuid.New().String()[:8],
		SessionID:  sessionID,
		Command:    command,
		Cwd:        cwd,
		Status:     TaskRunning,
		StartedAt:  time.Now(),
		OutputFile: outputFile,
		tail:       tail,
		done:       make(chan struct{}),
		cmd:        cmd,
		cancel:     cancel,
		reg:        r,
	}
	tail.task = task

	if err := cmd.Start(); err != nil {
		outFile.Close()
		cancel()
		return nil, fmt.Errorf("start command: %w", err)
	}

	r.bg.mu.Lock()
	r.bg.tasks[task.ID] = task
	r.saveTasksLocked()
	r.bg.mu.Unlock()
	notifyTaskUpdate(r, task)

	go func() {
		defer outFile.Close()
		err := cmd.Wait()

		now := time.Now()
		task.Finished = &now
		if ctx.Err() != nil {
			task.Status = TaskStopped
			task.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			task.Status = TaskFailed
			task.ExitCode = exitErr.ExitCode()
		} else if err != nil {
			task.Status = TaskFailed
			task.ExitCode = 1
		} else {
			task.Status = TaskCompleted
			task.ExitCode = 0
		}
		task.OutputTail = task.tail.tail()
		task.OutputSize = fileSizeOrZero(outputFile)
		close(task.done)
		task.notify()
	}()

	return task, nil
}

// tailWriter is wired as a live tap on the output stream. We use a simple
// approach instead: read the tail from the output file on demand.
func fileSizeOrZero(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func notifyTaskUpdate(reg *Registry, task *BackgroundTask) {
	reg.bg.mu.Lock()
	fn := reg.bg.taskUpdate
	reg.saveTasksLocked()
	reg.bg.mu.Unlock()
	if fn != nil {
		fn(task)
	}
}

// notify reports a task lifecycle transition to the owning registry.
func (t *BackgroundTask) notify() {
	if t.reg != nil {
		notifyTaskUpdate(t.reg, t)
	}
}

// GetTask returns a snapshot copy of a task.
func (r *Registry) GetTask(taskID string) (*BackgroundTask, bool) {
	r.bg.mu.Lock()
	defer r.bg.mu.Unlock()
	t, ok := r.bg.tasks[taskID]
	return t, ok
}

// WaitTask blocks until the task reaches a terminal state or the timeout
// elapses. timeout <= 0 waits indefinitely (bounded by ctx).
func (r *Registry) WaitTask(ctx context.Context, taskID string, timeout time.Duration) (*BackgroundTask, bool) {
	r.bg.mu.Lock()
	t, ok := r.bg.tasks[taskID]
	r.bg.mu.Unlock()
	if !ok {
		return nil, false
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	select {
	case <-t.done:
	case <-ctx.Done():
	}
	return t, true
}

// StopTask kills a running task's whole process group. Returns false when the
// task is unknown or already terminal.
func (r *Registry) StopTask(taskID string) bool {
	r.bg.mu.Lock()
	t, ok := r.bg.tasks[taskID]
	r.bg.mu.Unlock()
	if !ok || t.Status != TaskRunning {
		return false
	}
	t.cancel()
	// Negative PID targets the whole process group (Setpgid above).
	if t.cmd != nil && t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM)
		go func() {
			select {
			case <-t.done:
			case <-time.After(3 * time.Second):
				_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
			}
		}()
	}
	return true
}

// ListTasks returns tasks for a session (all sessions when sessionID is
// empty), newest first.
func (r *Registry) ListTasks(sessionID string) []*BackgroundTask {
	r.bg.mu.Lock()
	defer r.bg.mu.Unlock()
	out := make([]*BackgroundTask, 0, len(r.bg.tasks))
	for _, t := range r.bg.tasks {
		if sessionID != "" && t.SessionID != sessionID {
			continue
		}
		out = append(out, t)
	}
	if len(out) == 0 && sessionID != "" {
		for _, t := range r.bg.tasks {
			if t.Status == TaskRunning {
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// TaskNotification is a completed-task summary drained into the model's
// context (ZCode's <task-notification> pattern).
type TaskNotification struct {
	TaskID   string
	Command  string
	Status   TaskStatus
	ExitCode int
	Tail     string
}

// DrainTaskNotifications returns notifications for this session's tasks that
// reached a terminal state since the last drain, marking them consumed. The
// agent calls this at the start of every model step so the model learns a
// background command's outcome without polling.
func (r *Registry) DrainTaskNotifications(sessionID string) []TaskNotification {
	if sessionID == "" {
		return nil
	}
	r.bg.mu.Lock()
	defer r.bg.mu.Unlock()
	ids := make([]string, 0)
	for id, t := range r.bg.tasks {
		if t.SessionID == sessionID && t.Status != TaskRunning && !t.notified {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	notes := make([]TaskNotification, 0, len(ids))
	for _, id := range ids {
		t := r.bg.tasks[id]
		t.notified = true
		notes = append(notes, TaskNotification{
			TaskID:   t.ID,
			Command:  t.Command,
			Status:   t.Status,
			ExitCode: t.ExitCode,
			Tail:     t.OutputTail,
		})
	}
	return notes
}

// taskSnapshot builds the JSON-friendly map returned by the task tools.
func (t *BackgroundTask) taskSnapshot() map[string]any {
	size := t.OutputSize
	if t.Status == TaskRunning && t.OutputFile != "" {
		size = fileSizeOrZero(t.OutputFile)
	}
	out := map[string]any{
		"id":          t.ID,
		"task_id":     t.ID,
		"command":     t.Command,
		"status":      string(t.Status),
		"started_at":  t.StartedAt.Format(time.RFC3339),
		"output_size": size,
	}
	if t.Cwd != "" {
		out["cwd"] = t.Cwd
	}
	if t.Finished != nil {
		out["finished_at"] = t.Finished.Format(time.RFC3339)
	}
	if t.Status != TaskRunning {
		out["exit_code"] = t.ExitCode
	}
	tail := t.OutputTail
	if t.tail != nil {
		if curr := t.tail.tail(); curr != "" {
			tail = curr
		}
	}
	if tail == "" && t.OutputFile != "" {
		if content, err := readTailFile(t.OutputFile, outputTailLimit); err == nil && content != "" {
			tail = content
		}
	}
	if tail != "" {
		out["output_tail"] = tail
	}
	if t.OutputFile != "" {
		out["output_file"] = t.OutputFile
	}
	return out
}

// TaskSnapshot returns the JSON-friendly map representation of the task.
func (t *BackgroundTask) TaskSnapshot() map[string]any {
	return t.taskSnapshot()
}

// readTailFile returns the last `max` bytes of a file, split on line boundaries.
func readTailFile(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	offset := size - max
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return "", err
	}
	return string(buf), nil
}
