package ledger

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"nexios-finance/internal/domain"
)

var ErrUnbalancedBatch = errors.New("unbalanced ledger batch - rejecting save")

var InternalClearingAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type Service struct {
	repo   Repository
	outbox OutboxRepository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, outbox: NewInMemoryOutbox()}
}

func NewServiceWithOutbox(repo Repository, outbox OutboxRepository) *Service {
	return &Service{repo: repo, outbox: outbox}
}

func (s *Service) RecordSplitSettlement(ctx context.Context, txID uuid.UUID, leg domain.SplitLeg, idempotencyKey string) error {
	exists, err := s.repo.EntryExists(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	now := time.Now().UTC()
	batch := domain.LedgerBatch{
		TransactionID: txID,
		Entries: []domain.LedgerEntry{
			{ID: uuid.New(), TransactionID: txID, AccountID: InternalClearingAccountID, Type: domain.EntryDebit, AmountMinor: leg.AmountMinor, Currency: "SAR", CreatedAt: now, IdempotencyKey: idempotencyKey},
			{ID: uuid.New(), TransactionID: txID, AccountID: leg.SourceID, Type: domain.EntryCredit, AmountMinor: leg.AmountMinor, Currency: "SAR", CreatedAt: now, IdempotencyKey: idempotencyKey + ":credit"},
		},
	}

	if !batch.IsBalanced() {
		return ErrUnbalancedBatch
	}
	if err := s.repo.SaveBatch(ctx, batch); err != nil {
		return err
	}

	event, err := NewSettlementEvent(txID, leg)
	if err != nil {
		return err
	}
	return s.outbox.Enqueue(ctx, event)
}

func (s *Service) RecordCompensation(ctx context.Context, txID uuid.UUID, leg domain.SplitLeg, idempotencyKey string) error {
	exists, err := s.repo.EntryExists(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	now := time.Now().UTC()
	batch := domain.LedgerBatch{
		TransactionID: txID,
		Entries: []domain.LedgerEntry{
			{ID: uuid.New(), TransactionID: txID, AccountID: leg.SourceID, Type: domain.EntryDebit, AmountMinor: leg.AmountMinor, Currency: "SAR", CreatedAt: now, IdempotencyKey: idempotencyKey},
			{ID: uuid.New(), TransactionID: txID, AccountID: InternalClearingAccountID, Type: domain.EntryCredit, AmountMinor: leg.AmountMinor, Currency: "SAR", CreatedAt: now, IdempotencyKey: idempotencyKey + ":credit"},
		},
	}

	if !batch.IsBalanced() {
		return ErrUnbalancedBatch
	}
	return s.repo.SaveBatch(ctx, batch)
}
