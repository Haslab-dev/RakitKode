package message

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Haslab-dev/RakitKode/internal/db"
	"github.com/Haslab-dev/RakitKode/internal/pubsub"
)

// CreateMessageParams are inputs for creating a message.
type CreateMessageParams struct {
	Role             MessageRole
	Parts            []ContentPart
	Model            string
	Provider         string
	IsSummaryMessage bool
}

// Service is the public interface to the message store.
type Service interface {
	pubsub.Publisher[Message]
	pubsub.Subscriber[Message]
	Create(ctx context.Context, sessionID string, params CreateMessageParams) (Message, error)
	Update(ctx context.Context, m Message) error
	Get(ctx context.Context, id string) (Message, error)
	List(ctx context.Context, sessionID string) ([]Message, error)
	ListFromSummary(ctx context.Context, sessionID, summaryMessageID string) ([]Message, error)
	ListUserMessages(ctx context.Context, sessionID string) ([]Message, error)
	Delete(ctx context.Context, id string) error
	DeleteSessionMessages(ctx context.Context, sessionID string) error
}

type service struct {
	*pubsub.Broker[Message]
	q *db.Queries
}

// NewService builds the message service.
func NewService(q *db.Queries) Service {
	return &service{Broker: pubsub.NewBroker[Message](), q: q}
}

// contentPartEnvelope tags a part with its concrete type for storage.
type contentPartEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

const (
	partTypeText      = "text"
	partTypeReasoning = "reasoning"
	partTypeToolCall  = "tool_call"
	partTypeToolRes   = "tool_result"
	partTypeFinish    = "finish"
)

// marshalParts serializes parts with concrete-type tags.
func marshalParts(parts []ContentPart) (string, error) {
	if len(parts) == 0 {
		return "[]", nil
	}
	envelopes := make([]contentPartEnvelope, 0, len(parts))
	for _, p := range parts {
		var typ string
		switch p.(type) {
		case TextContent:
			typ = partTypeText
		case ReasoningContent:
			typ = partTypeReasoning
		case ToolCall:
			typ = partTypeToolCall
		case ToolResult:
			typ = partTypeToolRes
		case Finish:
			typ = partTypeFinish
		default:
			return "", fmt.Errorf("unknown content part type %T", p)
		}
		data, err := json.Marshal(p)
		if err != nil {
			return "", err
		}
		envelopes = append(envelopes, contentPartEnvelope{Type: typ, Data: data})
	}
	out, err := json.Marshal(envelopes)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// unmarshalParts decodes tagged part envelopes.
func unmarshalParts(data string) ([]ContentPart, error) {
	if data == "" || data == "[]" {
		return nil, nil
	}
	var envelopes []contentPartEnvelope
	if err := json.Unmarshal([]byte(data), &envelopes); err != nil {
		return nil, err
	}
	parts := make([]ContentPart, 0, len(envelopes))
	for _, env := range envelopes {
		switch env.Type {
		case partTypeText:
			var p TextContent
			if err := json.Unmarshal(env.Data, &p); err != nil {
				return nil, err
			}
			parts = append(parts, p)
		case partTypeReasoning:
			var p ReasoningContent
			if err := json.Unmarshal(env.Data, &p); err != nil {
				return nil, err
			}
			parts = append(parts, p)
		case partTypeToolCall:
			var p ToolCall
			if err := json.Unmarshal(env.Data, &p); err != nil {
				return nil, err
			}
			parts = append(parts, p)
		case partTypeToolRes:
			var p ToolResult
			if err := json.Unmarshal(env.Data, &p); err != nil {
				return nil, err
			}
			parts = append(parts, p)
		case partTypeFinish:
			var p Finish
			if err := json.Unmarshal(env.Data, &p); err != nil {
				return nil, err
			}
			parts = append(parts, p)
		default:
			slog.Warn("Unknown content part type in stored message", "type", env.Type)
		}
	}
	return parts, nil
}

// Create persists a new message.
func (s *service) Create(ctx context.Context, sessionID string, params CreateMessageParams) (Message, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Message{}, fmt.Errorf("failed to generate message ID: %w", err)
	}
	partsJSON, err := marshalParts(params.Parts)
	if err != nil {
		return Message{}, fmt.Errorf("failed to marshal parts: %w", err)
	}
	m := Message{
		ID:               id.String(),
		Role:             params.Role,
		SessionID:        sessionID,
		Parts:            params.Parts,
		Model:            params.Model,
		Provider:         params.Provider,
		IsSummaryMessage: params.IsSummaryMessage,
	}
	err = s.q.CreateMessage(ctx, db.CreateMessageParams{
		ID:               m.ID,
		SessionID:        sessionID,
		Role:             string(params.Role),
		Parts:            partsJSON,
		Model:            sql.NullString{String: params.Model, Valid: params.Model != ""},
		Provider:         sql.NullString{String: params.Provider, Valid: params.Provider != ""},
		IsSummaryMessage: params.IsSummaryMessage,
	})
	if err != nil {
		return Message{}, fmt.Errorf("failed to create message: %w", err)
	}
	s.Publish(pubsub.CreatedEvent, m)
	return m, nil
}

// Update overwrites the parts of a message.
func (s *service) Update(ctx context.Context, m Message) error {
	partsJSON, err := marshalParts(m.Parts)
	if err != nil {
		return fmt.Errorf("failed to marshal parts: %w", err)
	}
	var finishedAt sql.NullInt64
	if m.FinishedAt != nil {
		finishedAt = sql.NullInt64{Int64: *m.FinishedAt, Valid: true}
	} else if m.IsFinished() {
		now := m.UpdatedAt
		finishedAt = sql.NullInt64{Int64: now, Valid: now > 0}
	}
	if err := s.q.UpdateMessage(ctx, partsJSON, finishedAt, m.ID); err != nil {
		return fmt.Errorf("failed to update message: %w", err)
	}
	s.Publish(pubsub.UpdatedEvent, m)
	return nil
}

// Get fetches one message.
func (s *service) Get(ctx context.Context, id string) (Message, error) {
	item, err := s.q.GetMessage(ctx, id)
	if err != nil {
		return Message{}, err
	}
	return fromDBItem(item)
}

// List returns every message of a session.
func (s *service) List(ctx context.Context, sessionID string) ([]Message, error) {
	items, err := s.q.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	return fromDBItems(items)
}

// ListFromSummary returns messages at or after the summary message.
func (s *service) ListFromSummary(ctx context.Context, sessionID, summaryMessageID string) ([]Message, error) {
	items, err := s.q.ListMessagesFromSummary(ctx, sessionID, summaryMessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	return fromDBItems(items)
}

// ListUserMessages returns the user messages of a session.
func (s *service) ListUserMessages(ctx context.Context, sessionID string) ([]Message, error) {
	items, err := s.q.ListUserMessages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list user messages: %w", err)
	}
	return fromDBItems(items)
}

// Delete removes one message.
func (s *service) Delete(ctx context.Context, id string) error {
	if err := s.q.DeleteMessage(ctx, id); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	s.Publish(pubsub.DeletedEvent, Message{ID: id})
	return nil
}

// DeleteSessionMessages removes all messages of a session.
func (s *service) DeleteSessionMessages(ctx context.Context, sessionID string) error {
	if err := s.q.DeleteSessionMessages(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to delete session messages: %w", err)
	}
	return nil
}

func fromDBItems(items []db.Message) ([]Message, error) {
	out := make([]Message, 0, len(items))
	for _, item := range items {
		m, err := fromDBItem(item)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func fromDBItem(item db.Message) (Message, error) {
	parts, err := unmarshalParts(item.Parts)
	if err != nil {
		slog.Error("Failed to unmarshal message parts", "error", err, "message_id", item.ID)
		parts = nil
	}
	m := Message{
		ID:               item.ID,
		SessionID:        item.SessionID,
		Role:             MessageRole(item.Role),
		Parts:            parts,
		IsSummaryMessage: item.IsSummaryMessage,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
	if item.Model.Valid {
		m.Model = item.Model.String
	}
	if item.Provider.Valid {
		m.Provider = item.Provider.String
	}
	if item.FinishedAt.Valid {
		ts := item.FinishedAt.Int64
		m.FinishedAt = &ts
	}
	return m, nil
}
