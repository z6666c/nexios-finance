package ledger

import (
	"context"
	"sync"

	"nexios-finance/internal/uuid"

	"nexios-finance/internal/domain"
)

type InMemoryRepository struct {
	mu      sync.Mutex
	entries map[string]domain.LedgerEntry
	byTx    map[uuid.UUID][]domain.LedgerEntry
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		entries: make(map[string]domain.LedgerEntry),
		byTx:    make(map[uuid.UUID][]domain.LedgerEntry),
	}
}

func (r *InMemoryRepository) SaveBatch(ctx context.Context, batch domain.LedgerBatch) error {
	if !batch.IsBalanced() {
		return ErrUnbalancedBatch
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range batch.Entries {
		r.entries[e.IdempotencyKey] = e
		r.byTx[batch.TransactionID] = append(r.byTx[batch.TransactionID], e)
	}
	return nil
}

func (r *InMemoryRepository) EntryExists(ctx context.Context, idempotencyKey string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.entries[idempotencyKey]
	return ok, nil
}

func (r *InMemoryRepository) GetEntriesByTransaction(ctx context.Context, transactionID uuid.UUID) ([]domain.LedgerEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byTx[transactionID], nil
}
