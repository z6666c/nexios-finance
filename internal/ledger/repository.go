package ledger

import (
	"context"

	"nexios-finance/internal/uuid"

	"nexios-finance/internal/domain"
)

type Repository interface {
	SaveBatch(ctx context.Context, batch domain.LedgerBatch) error
	EntryExists(ctx context.Context, idempotencyKey string) (bool, error)
	GetEntriesByTransaction(ctx context.Context, transactionID uuid.UUID) ([]domain.LedgerEntry, error)
}
