// Package pubsub provides a lightweight in-process broker for fan-out
// event delivery between services and the UI.
package pubsub

import (
	"context"
	"log/slog"
	"sync"
)

const bufferSize = 256

// EventType identifies the type of event.
type EventType string

const (
	CreatedEvent EventType = "created"
	UpdatedEvent EventType = "updated"
	DeletedEvent EventType = "deleted"
)

// Event represents an event in the lifecycle of a resource.
type Event[T any] struct {
	Type    EventType `json:"type"`
	Payload T         `json:"payload"`
}

// Publisher can publish events of type T.
type Publisher[T any] interface {
	Publish(EventType, T)
}

// Subscriber can subscribe to events of type T.
type Subscriber[T any] interface {
	Subscribe(context.Context) <-chan Event[T]
}

// Broker is a generic in-process pub/sub broker. Publish is best-effort
// and lossy under back-pressure: a full subscriber channel drops the
// event for that subscriber only.
type Broker[T any] struct {
	subs map[chan Event[T]]struct{}
	mu   sync.RWMutex
	done chan struct{}
}

// NewBroker creates a new broker.
func NewBroker[T any]() *Broker[T] {
	return &Broker[T]{
		subs: make(map[chan Event[T]]struct{}),
		done: make(chan struct{}),
	}
}

// Subscribe returns a channel receiving events until ctx is cancelled
// or the broker is closed.
func (b *Broker[T]) Subscribe(ctx context.Context) <-chan Event[T] {
	ch := make(chan Event[T], bufferSize)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-b.done:
		}
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}

// Publish sends an event to every subscriber. Non-blocking; a full
// channel drops the event with a warning.
func (b *Broker[T]) Publish(typ EventType, payload T) {
	b.mu.RLock()
	subs := make([]chan Event[T], 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- Event[T]{Type: typ, Payload: payload}:
		default:
			slog.Warn("Pubsub channel full, dropping event", "type", typ)
		}
	}
}
