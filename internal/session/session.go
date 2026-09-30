// Package session provides session CRUD backed by SQLite.
package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Haslab-dev/RakitKode/internal/db"
	"github.com/Haslab-dev/RakitKode/internal/pubsub"
)

// TodoStatus is the state of one todo item.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// Todo is one checklist item persisted on the session.
type Todo struct {
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// HasIncompleteTodos reports whether any todo is not completed.
func HasIncompleteTodos(todos []Todo) bool {
	for _, t := range todos {
		if t.Status != TodoCompleted {
			return true
		}
	}
	return false
}

// Session is a conversation container.
type Session struct {
	ID               string
	ParentSessionID  string
	Title            string
	MessageCount     int64
	PromptTokens     int64
	CompletionTokens int64
	Cost             float64
	SummaryMessageID string
	Todos            []Todo
	CreatedAt        int64
	UpdatedAt        int64
}

// Service is the public interface to the session store.
type Service interface {
	pubsub.Publisher[Session]
	pubsub.Subscriber[Session]
	Create(ctx context.Context, title string) (Session, error)
	CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error)
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (Session, error)
	GetLast(ctx context.Context) (Session, error)
	Save(ctx context.Context, s Session) (Session, error)
	Rename(ctx context.Context, id, title string) error
	List(ctx context.Context) ([]Session, error)
}

type service struct {
	*pubsub.Broker[Session]
	q *db.Queries
}

// NewService builds the session service.
func NewService(q *db.Queries) Service {
	return &service{Broker: pubsub.NewBroker[Session](), q: q}
}

// Create starts a new top-level session.
func (s *service) Create(ctx context.Context, title string) (Session, error) {
	if title == "" {
		title = "New session"
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, fmt.Errorf("failed to generate session ID: %w", err)
	}
	sess := Session{ID: id.String(), Title: title}
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:              sess.ID,
		ParentSessionID: sql.NullString{},
		Title:           sess.Title,
	}); err != nil {
		return Session{}, fmt.Errorf("failed to create session: %w", err)
	}
	s.Publish(pubsub.CreatedEvent, sess)
	return sess, nil
}

// CreateTaskSession creates a child session for a sub-agent task.
func (s *service) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, fmt.Errorf("failed to generate session ID: %w", err)
	}
	sess := Session{ID: id.String(), ParentSessionID: parentSessionID, Title: title}
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:              sess.ID,
		ParentSessionID: sql.NullString{String: parentSessionID, Valid: true},
		Title:           sess.Title,
	}); err != nil {
		return Session{}, fmt.Errorf("failed to create task session: %w", err)
	}
	s.Publish(pubsub.CreatedEvent, sess)
	return sess, nil
}

// Delete removes a session (cascades messages and files).
func (s *service) Delete(ctx context.Context, id string) error {
	if err := s.q.DeleteSession(ctx, id); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	s.Publish(pubsub.DeletedEvent, Session{ID: id})
	return nil
}

// Get fetches one session.
func (s *service) Get(ctx context.Context, id string) (Session, error) {
	item, err := s.q.GetSession(ctx, id)
	if err != nil {
		return Session{}, err
	}
	return s.fromDBItem(item)
}

// GetLast returns the most recently updated top-level session.
func (s *service) GetLast(ctx context.Context) (Session, error) {
	item, err := s.q.GetLastSession(ctx)
	if err != nil {
		return Session{}, err
	}
	return s.fromDBItem(item)
}

// Save persists mutable session fields.
func (s *service) Save(ctx context.Context, sess Session) (Session, error) {
	todosJSON, err := marshalTodos(sess.Todos)
	if err != nil {
		return Session{}, err
	}
	item := toDBItem(sess)
	item.Todos = sql.NullString{String: todosJSON, Valid: todosJSON != ""}
	if err := s.q.SaveSession(ctx, item); err != nil {
		return Session{}, fmt.Errorf("failed to save session: %w", err)
	}
	updated, err := s.Get(ctx, sess.ID)
	if err != nil {
		return Session{}, err
	}
	s.Publish(pubsub.UpdatedEvent, updated)
	return updated, nil
}

// Rename updates only the title.
func (s *service) Rename(ctx context.Context, id, title string) error {
	if err := s.q.RenameSession(ctx, id, title); err != nil {
		return fmt.Errorf("failed to rename session: %w", err)
	}
	sess, err := s.Get(ctx, id)
	if err == nil {
		s.Publish(pubsub.UpdatedEvent, sess)
	}
	return nil
}

// List returns all top-level sessions.
func (s *service) List(ctx context.Context) ([]Session, error) {
	items, err := s.q.ListSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	out := make([]Session, 0, len(items))
	for _, item := range items {
		sess, err := s.fromDBItem(item)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s *service) fromDBItem(item db.Session) (Session, error) {
	var todos []Todo
	if item.Todos.Valid && item.Todos.String != "" {
		if err := json.Unmarshal([]byte(item.Todos.String), &todos); err != nil {
			slog.Error("Failed to parse session todos", "error", err)
		}
	}
	sess := Session{
		ID:               item.ID,
		Title:            item.Title,
		MessageCount:     item.MessageCount,
		PromptTokens:     item.PromptTokens,
		CompletionTokens: item.CompletionTokens,
		Cost:             item.Cost,
		SummaryMessageID: item.SummaryMessageID,
		Todos:            todos,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
	if item.ParentSessionID.Valid {
		sess.ParentSessionID = item.ParentSessionID.String
	}
	return sess, nil
}

func toDBItem(sess Session) db.Session {
	item := db.Session{
		ID:               sess.ID,
		Title:            sess.Title,
		PromptTokens:     sess.PromptTokens,
		CompletionTokens: sess.CompletionTokens,
		Cost:             sess.Cost,
		SummaryMessageID: sess.SummaryMessageID,
		CreatedAt:        sess.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
	}
	if sess.ParentSessionID != "" {
		item.ParentSessionID = sql.NullString{String: sess.ParentSessionID, Valid: true}
	}
	return item
}

func marshalTodos(todos []Todo) (string, error) {
	if len(todos) == 0 {
		return "", nil
	}
	data, err := json.Marshal(todos)
	if err != nil {
		return "", fmt.Errorf("failed to marshal todos: %w", err)
	}
	return string(data), nil
}
