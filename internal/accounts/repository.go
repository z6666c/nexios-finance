package accounts

import (
	"context"
	"time"

	"nexios-finance/internal/uuid"
)

// Leg is one account-balance movement within a TransferOp: a negative
// DeltaMinor is a debit (money leaving the account), a positive one a
// credit (money arriving). A TransferOp's legs must always sum to zero -
// Store implementations reject one that doesn't via ErrUnbalancedTransfer.
type Leg struct {
	AccountID  uuid.UUID
	DeltaMinor int64
}

// TransferOp is everything a Store needs to atomically move money and
// record why: adjust every account in Legs by its DeltaMinor, write a
// balanced double-entry ledger batch for the same movement, insert one
// Transfer record, and enqueue a settlement event on the outbox - all as
// a single unit of work.
type TransferOp struct {
	UserID         uuid.UUID
	Type           TransferType
	Legs           []Leg
	FromAccountID  *uuid.UUID
	ToAccountID    *uuid.UUID
	Counterparty   string
	Note           string
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
	Splits         []DistributeSplit
	Now            time.Time
}

// Store is the persistence port for accounts, users and transfers. It has
// two implementations: MemoryStore (zero setup, in-process, for local
// development and quick demos) and PostgresStore (durable, for a real
// deployment) - selected at startup by config.LedgerDriver.
type Store interface {
	ListAccountsByUser(ctx context.Context, userID uuid.UUID) ([]Account, error)
	GetAccount(ctx context.Context, id uuid.UUID) (Account, error)
	ListUsers(ctx context.Context) ([]User, error)
	GetUser(ctx context.Context, id uuid.UUID) (User, error)
	// FindUserByQuery looks up a Nexios user by an exact Nexios account
	// number or a case-insensitive full-name match, for the P2P transfer
	// recipient picker.
	FindUserByQuery(ctx context.Context, query string) (User, error)
	ListTransfersByUser(ctx context.Context, userID uuid.UUID, limit int) ([]Transfer, error)

	// ExecuteTransfer performs the atomic balance update + ledger write +
	// transfer record + outbox enqueue described by op. It re-checks every
	// debit leg's available balance itself (never trust a caller-computed
	// balance across a race), returning ErrInsufficientFunds if any debit
	// would take an account negative.
	ExecuteTransfer(ctx context.Context, op TransferOp) (Transfer, error)
}
