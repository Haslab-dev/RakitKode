// Package history tracks per-session file versions for diffing.
package history

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Haslab-dev/RakitKode/internal/db"
	"github.com/Haslab-dev/RakitKode/internal/pubsub"
)

// InitialVersion is the version of the first snapshot of a file.
const InitialVersion = 0

// File is one version snapshot of a file touched in a session.
type File struct {
	ID        string
	SessionID string
	Path      string
	Content   string
	Version   int64
	CreatedAt int64
	UpdatedAt int64
}

// Service manages file versions and history for sessions.
type Service interface {
	pubsub.Subscriber[File]
	Create(ctx context.Context, sessionID, path, content string) (File, error)
	CreateVersion(ctx context.Context, sessionID, path, content string) (File, error)
	GetByPathAndSession(ctx context.Context, path, sessionID string) (File, error)
	ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error)
	DeleteSessionFiles(ctx context.Context, sessionID string) error
}

type service struct {
	*pubsub.Broker[File]
	q *db.Queries
}

// NewService builds the file history service.
func NewService(q *db.Queries) Service {
	return &service{Broker: pubsub.NewBroker[File](), q: q}
}

// Create records the first version of a file.
func (s *service) Create(ctx context.Context, sessionID, path, content string) (File, error) {
	return s.createWithVersion(ctx, sessionID, path, content, InitialVersion)
}

// CreateVersion records a subsequent version of a file.
func (s *service) CreateVersion(ctx context.Context, sessionID, path, content string) (File, error) {
	latest, err := s.q.GetFileByPathAndSession(ctx, path, sessionID)
	if err != nil {
		return s.Create(ctx, sessionID, path, content)
	}
	return s.createWithVersion(ctx, sessionID, path, content, latest.Version+1)
}

func (s *service) createWithVersion(ctx context.Context, sessionID, path, content string, version int64) (File, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return File{}, fmt.Errorf("failed to generate file ID: %w", err)
	}
	if err := s.q.CreateFile(ctx, db.CreateFileParams{
		ID:        id.String(),
		SessionID: sessionID,
		Path:      path,
		Content:   content,
		Version:   version,
	}); err != nil {
		return File{}, fmt.Errorf("failed to create file version: %w", err)
	}
	file, err := s.q.GetFileByPathAndSession(ctx, path, sessionID)
	if err != nil {
		return File{}, err
	}
	out := File{
		ID:        file.ID,
		SessionID: file.SessionID,
		Path:      file.Path,
		Content:   file.Content,
		Version:   file.Version,
		CreatedAt: file.CreatedAt,
		UpdatedAt: file.UpdatedAt,
	}
	s.Publish(pubsub.CreatedEvent, out)
	return out, nil
}

// GetByPathAndSession returns the latest version of a tracked path.
func (s *service) GetByPathAndSession(ctx context.Context, path, sessionID string) (File, error) {
	file, err := s.q.GetFileByPathAndSession(ctx, path, sessionID)
	if err != nil {
		return File{}, err
	}
	return File{
		ID:        file.ID,
		SessionID: file.SessionID,
		Path:      file.Path,
		Content:   file.Content,
		Version:   file.Version,
		CreatedAt: file.CreatedAt,
		UpdatedAt: file.UpdatedAt,
	}, nil
}

// DeleteSessionFiles removes file history of a session.
func (s *service) DeleteSessionFiles(ctx context.Context, sessionID string) error {
	if err := s.q.DeleteSessionFiles(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to delete session files: %w", err)
	}
	return nil
}

// ListLatestSessionFiles returns the newest version per tracked path.
func (s *service) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error) {
	items, err := s.q.ListLatestSessionFiles(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list session files: %w", err)
	}
	out := make([]File, 0, len(items))
	for _, item := range items {
		out = append(out, File{
			ID:        item.ID,
			SessionID: item.SessionID,
			Path:      item.Path,
			Content:   item.Content,
			Version:   item.Version,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		})
	}
	return out, nil
}

