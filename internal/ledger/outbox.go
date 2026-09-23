package ledger

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"nexios-finance/internal/uuid"
)

type OutboxEvent struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	EventType     string    `json:"event_type"`
	Payload       string    `json:"payload"`
	CreatedAt     time.Time `json:"created_at"`
	Published     bool      `json:"published"`
}

type OutboxRepository interface {
	Enqueue(ctx context.Context, event OutboxEvent) error
	FetchUnpublished(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, id uuid.UUID) error
}

type InMemoryOutbox struct {
	mu     sync.Mutex
	events map[uuid.UUID]OutboxEvent
}

func NewInMemoryOutbox() *InMemoryOutbox {
	return &InMemoryOutbox{events: make(map[uuid.UUID]OutboxEvent)}
}

func (o *InMemoryOutbox) Enqueue(ctx context.Context, event OutboxEvent) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events[event.ID] = event
	return nil
}

func (o *InMemoryOutbox) FetchUnpublished(ctx context.Context, limit int) ([]OutboxEvent, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []OutboxEvent
	for _, e := range o.events {
		if !e.Published {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (o *InMemoryOutbox) MarkPublished(ctx context.Context, id uuid.UUID) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if e, ok := o.events[id]; ok {
		e.Published = true
		o.events[id] = e
	}
	return nil
}

func NewSettlementEvent(txID uuid.UUID, payload interface{}) (OutboxEvent, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return OutboxEvent{}, err
	}
	return OutboxEvent{
		ID:            uuid.New(),
		TransactionID: txID,
		EventType:     "ledger.split_settled",
		Payload:       string(data),
		CreatedAt:     time.Now().UTC(),
	}, nil
}
