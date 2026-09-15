package domain

import (
	"time"

	"github.com/google/uuid"
)

type EntryType string

const (
	EntryDebit  EntryType = "DEBIT"
	EntryCredit EntryType = "CREDIT"
)

type LedgerEntry struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Type          EntryType `json:"type"`
	AmountMinor   int64     `json:"amount_minor_units"`
	Currency      string    `json:"currency"`
	CreatedAt     time.Time `json:"created_at"`
	IdempotencyKey string `json:"idempotency_key"`
}

type LedgerBatch struct {
	TransactionID uuid.UUID     `json:"transaction_id"`
	Entries       []LedgerEntry `json:"entries"`
}

func (b *LedgerBatch) IsBalanced() bool {
	var debit, credit int64
	for _, e := range b.Entries {
		switch e.Type {
		case EntryDebit:
			debit += e.AmountMinor
		case EntryCredit:
			credit += e.AmountMinor
		}
	}
	return debit == credit
}
