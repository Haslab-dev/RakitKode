package db

import (
	"context"
	"database/sql"
	"time"
)

// Session is a row in the sessions table.
type Session struct {
	ID               string
	ParentSessionID  sql.NullString
	Title            string
	MessageCount     int64
	PromptTokens     int64
	CompletionTokens int64
	Cost             float64
	SummaryMessageID string
	Todos            sql.NullString
	UpdatedAt        int64
	CreatedAt        int64
}

// File is a row in the files table (per-session file version history).
type File struct {
	ID        string
	SessionID string
	Path      string
	Content   string
	Version   int64
	CreatedAt int64
	UpdatedAt int64
}

// Message is a row in the messages table.
type Message struct {
	ID               string
	SessionID        string
	Role             string
	Parts            string
	Model            sql.NullString
	Provider         sql.NullString
	IsSummaryMessage bool
	CreatedAt        int64
	UpdatedAt        int64
	FinishedAt       sql.NullInt64
}

// CreateSessionParams are inputs for CreateSession.
type CreateSessionParams struct {
	ID              string
	ParentSessionID sql.NullString
	Title           string
}

type querier struct{}

// Queries is all SQL used by the harness. Hand-written counterpart of
// crush's sqlc-generated layer.
type Queries = querier

// NewQueries builds a Queries.
func NewQueries(conn *sql.DB) *Queries { return &querier{} }

const createSession = `
INSERT INTO sessions (id, parent_session_id, title, updated_at, created_at)
VALUES (?, ?, ?, strftime('%s','now'), strftime('%s','now'))`

// CreateSession inserts a new session.
func (q *querier) CreateSession(ctx context.Context, arg CreateSessionParams) error {
	_, err := exec(ctx, createSession, arg.ID, arg.ParentSessionID, arg.Title)
	return err
}

const getSession = `
SELECT id, parent_session_id, title, message_count, prompt_tokens,
       completion_tokens, cost, summary_message_id, todos, updated_at, created_at
FROM sessions WHERE id = ? LIMIT 1`

// GetSession fetches one session by ID.
func (q *querier) GetSession(ctx context.Context, id string) (Session, error) {
	row := queryRow(ctx, getSession, id)
	if row == nil {
		return Session{}, sql.ErrConnDone
	}
	var s Session
	var summary sql.NullString
	err := row.Scan(
		&s.ID, &s.ParentSessionID, &s.Title, &s.MessageCount,
		&s.PromptTokens, &s.CompletionTokens, &s.Cost, &summary,
		&s.Todos, &s.UpdatedAt, &s.CreatedAt,
	)
	s.SummaryMessageID = summary.String
	return s, err
}

const getLastSession = `
SELECT id, parent_session_id, title, message_count, prompt_tokens,
       completion_tokens, cost, summary_message_id, todos, updated_at, created_at
FROM sessions WHERE parent_session_id IS NULL ORDER BY updated_at DESC LIMIT 1`

// GetLastSession returns the most recently updated top-level session.
func (q *querier) GetLastSession(ctx context.Context) (Session, error) {
	row := queryRow(ctx, getLastSession)
	if row == nil {
		return Session{}, sql.ErrConnDone
	}
	var s Session
	var summary sql.NullString
	err := row.Scan(
		&s.ID, &s.ParentSessionID, &s.Title, &s.MessageCount,
		&s.PromptTokens, &s.CompletionTokens, &s.Cost, &summary,
		&s.Todos, &s.UpdatedAt, &s.CreatedAt,
	)
	s.SummaryMessageID = summary.String
	return s, err
}

const listSessions = `
SELECT id, parent_session_id, title, message_count, prompt_tokens,
       completion_tokens, cost, summary_message_id, todos, updated_at, created_at
FROM sessions WHERE parent_session_id IS NULL ORDER BY updated_at DESC`

// ListSessions returns all top-level sessions, newest first.
func (q *querier) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := query(ctx, listSessions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var summary sql.NullString
		if err := rows.Scan(
			&s.ID, &s.ParentSessionID, &s.Title, &s.MessageCount,
			&s.PromptTokens, &s.CompletionTokens, &s.Cost, &summary,
			&s.Todos, &s.UpdatedAt, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		s.SummaryMessageID = summary.String
		out = append(out, s)
	}
	return out, rows.Err()
}

const saveSession = `
UPDATE sessions SET
  title = ?, prompt_tokens = ?, completion_tokens = ?, cost = ?,
  summary_message_id = ?, todos = ?
WHERE id = ?`

// SaveSession updates mutable session fields.
func (q *querier) SaveSession(ctx context.Context, s Session) error {
	var todos sql.NullString
	if s.Todos.Valid {
		todos = s.Todos
	}
	_, err := exec(ctx, saveSession,
		s.Title, s.PromptTokens, s.CompletionTokens, s.Cost,
		s.SummaryMessageID, todos, s.ID,
	)
	return err
}

const renameSession = `UPDATE sessions SET title = ? WHERE id = ?`

// RenameSession updates only the title.
func (q *querier) RenameSession(ctx context.Context, id, title string) error {
	_, err := exec(ctx, renameSession, title, id)
	return err
}

const deleteSession = `DELETE FROM sessions WHERE id = ?`

// DeleteSession removes a session and cascades messages/files.
func (q *querier) DeleteSession(ctx context.Context, id string) error {
	_, err := exec(ctx, deleteSession, id)
	return err
}

const createMessage = `
INSERT INTO messages (id, session_id, role, parts, model, provider, is_summary_message, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%s','now'), strftime('%s','now'))`

// CreateMessageParams are inputs for CreateMessage.
type CreateMessageParams struct {
	ID               string
	SessionID        string
	Role             string
	Parts            string
	Model            sql.NullString
	Provider         sql.NullString
	IsSummaryMessage bool
}

// CreateMessage inserts a message row.
func (q *querier) CreateMessage(ctx context.Context, arg CreateMessageParams) error {
	_, err := exec(ctx, createMessage,
		arg.ID, arg.SessionID, arg.Role, arg.Parts,
		arg.Model, arg.Provider, arg.IsSummaryMessage,
	)
	return err
}

const updateMessage = `
UPDATE messages SET parts = ?, finished_at = ? WHERE id = ?`

// UpdateMessage overwrites parts and finished_at for a message.
func (q *querier) UpdateMessage(ctx context.Context, parts string, finishedAt sql.NullInt64, id string) error {
	_, err := exec(ctx, updateMessage, parts, finishedAt, id)
	return err
}

const messageColumns = `
id, session_id, role, parts, model, provider, is_summary_message, created_at, updated_at, finished_at`

const getMessage = `SELECT ` + messageColumns + ` FROM messages WHERE id = ? LIMIT 1`

func scanMessage(row *sql.Row) (Message, error) {
	var m Message
	if row == nil {
		return m, sql.ErrConnDone
	}
	err := row.Scan(&m.ID, &m.SessionID, &m.Role, &m.Parts, &m.Model, &m.Provider,
		&m.IsSummaryMessage, &m.CreatedAt, &m.UpdatedAt, &m.FinishedAt)
	return m, err
}

// GetMessage fetches one message by ID.
func (q *querier) GetMessage(ctx context.Context, id string) (Message, error) {
	return scanMessage(queryRow(ctx, getMessage, id))
}

const listMessages = `
SELECT ` + messageColumns + ` FROM messages WHERE session_id = ? ORDER BY created_at ASC, id ASC`

// ListMessages returns all messages of a session in creation order.
func (q *querier) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := query(ctx, listMessages, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMessages(rows)
}

const listMessagesFromSummary = `
SELECT ` + messageColumns + ` FROM messages
WHERE session_id = ? AND created_at >= (SELECT created_at FROM messages WHERE id = ?)
ORDER BY created_at ASC, id ASC`

// ListMessagesFromSummary returns messages at or after the summary
// message; when summaryID is empty it returns everything.
func (q *querier) ListMessagesFromSummary(ctx context.Context, sessionID, summaryID string) ([]Message, error) {
	if summaryID == "" {
		return q.ListMessages(ctx, sessionID)
	}
	rows, err := query(ctx, listMessagesFromSummary, sessionID, summaryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMessages(rows)
}

const listUserMessages = `
SELECT ` + messageColumns + ` FROM messages WHERE session_id = ? AND role = 'user' ORDER BY created_at ASC, id ASC`

// ListUserMessages returns the user messages of a session.
func (q *querier) ListUserMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := query(ctx, listUserMessages, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMessages(rows)
}

const getLastAssistantMessage = `
SELECT ` + messageColumns + ` FROM messages
WHERE session_id = ? AND role = 'assistant' ORDER BY created_at DESC, id DESC LIMIT 1`

// GetLastAssistantMessage returns the newest assistant message.
func (q *querier) GetLastAssistantMessage(ctx context.Context, sessionID string) (Message, error) {
	return scanMessage(queryRow(ctx, getLastAssistantMessage, sessionID))
}

const deleteMessage = `DELETE FROM messages WHERE id = ?`

// DeleteMessage removes a single message.
func (q *querier) DeleteMessage(ctx context.Context, id string) error {
	_, err := exec(ctx, deleteMessage, id)
	return err
}

const deleteSessionMessages = `DELETE FROM messages WHERE session_id = ?`

// DeleteSessionMessages removes all messages of a session.
func (q *querier) DeleteSessionMessages(ctx context.Context, sessionID string) error {
	_, err := exec(ctx, deleteSessionMessages, sessionID)
	return err
}

const createFile = `
INSERT INTO files (id, session_id, path, content, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, strftime('%s','now'), strftime('%s','now'))`

// CreateFileParams are inputs for CreateFile.
type CreateFileParams struct {
	ID        string
	SessionID string
	Path      string
	Content   string
	Version   int64
}

// CreateFile inserts a file version row.
func (q *querier) CreateFile(ctx context.Context, arg CreateFileParams) error {
	_, err := exec(ctx, createFile, arg.ID, arg.SessionID, arg.Path, arg.Content, arg.Version)
	return err
}

const getFileByPathAndSession = `
SELECT id, session_id, path, content, version, created_at, updated_at
FROM files WHERE path = ? AND session_id = ? ORDER BY version DESC LIMIT 1`

// GetFileByPathAndSession returns the latest version of a tracked file.
func (q *querier) GetFileByPathAndSession(ctx context.Context, path, sessionID string) (File, error) {
	row := queryRow(ctx, getFileByPathAndSession, path, sessionID)
	if row == nil {
		return File{}, sql.ErrConnDone
	}
	var f File
	err := row.Scan(&f.ID, &f.SessionID, &f.Path, &f.Content, &f.Version, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

const listLatestSessionFiles = `
SELECT f.id, f.session_id, f.path, f.content, f.version, f.created_at, f.updated_at
FROM files f
JOIN (
  SELECT path, MAX(version) AS max_version
  FROM files WHERE session_id = ? GROUP BY path
) latest ON f.path = latest.path AND f.version = latest.max_version
WHERE f.session_id = ?`

// ListLatestSessionFiles returns the newest version per tracked path.
func (q *querier) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error) {
	rows, err := query(ctx, listLatestSessionFiles, sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.SessionID, &f.Path, &f.Content, &f.Version, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const deleteSessionFiles = `DELETE FROM files WHERE session_id = ?`

// DeleteSessionFiles removes file history of a session.
func (q *querier) DeleteSessionFiles(ctx context.Context, sessionID string) error {
	_, err := exec(ctx, deleteSessionFiles, sessionID)
	return err
}

const recordFileRead = `
INSERT INTO read_files (session_id, path, read_at)
VALUES (?, ?, strftime('%s','now'))
ON CONFLICT(path, session_id) DO UPDATE SET read_at = excluded.read_at`

// RecordFileRead upserts the last-read timestamp for a path.
func (q *querier) RecordFileRead(ctx context.Context, sessionID, path string) error {
	_, err := exec(ctx, recordFileRead, sessionID, path)
	return err
}

const getFileRead = `SELECT read_at FROM read_files WHERE path = ? AND session_id = ?`

// GetFileRead returns when a path was last read in a session.
func (q *querier) GetFileRead(ctx context.Context, sessionID, path string) (time.Time, error) {
	row := queryRow(ctx, getFileRead, path, sessionID)
	if row == nil {
		return time.Time{}, sql.ErrConnDone
	}
	var ts int64
	if err := row.Scan(&ts); err != nil {
		return time.Time{}, err
	}
	return time.Unix(ts, 0), nil
}

const listReadFiles = `SELECT path FROM read_files WHERE session_id = ?`

// ListReadFiles returns every path read in a session.
func (q *querier) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := query(ctx, listReadFiles, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func collectMessages(rows *sql.Rows) ([]Message, error) {
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Parts, &m.Model, &m.Provider,
			&m.IsSummaryMessage, &m.CreatedAt, &m.UpdatedAt, &m.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
