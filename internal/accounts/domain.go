// Package accounts backs the functionality the two web/ prototypes
// (user-app.html, admin-app.html) currently only simulate in JavaScript:
// a user's linked bank accounts and investment portfolios, their Nexios
// account numbers, and the three transfer types the user app exposes
// (between the user's own accounts, to another Nexios user, or
// distributed across several authorized banks/portfolios at once).
package accounts

import (
	"errors"
	"time"

	"nexios-finance/internal/uuid"
)

// Kind distinguishes a linked bank account from an investment portfolio;
// both are "accounts" for transfer purposes, but the UI groups them
// differently.
type Kind string

const (
	KindBank      Kind = "bank"
	KindPortfolio Kind = "portfolio"
)

// Account is a bank account or investment portfolio a user has linked to
// (authorized) Nexios, identified by its own Nexios-issued account number
// (e.g. NXS-SA-7741-2093-01) in addition to its internal UUID.
type Account struct {
	ID                  uuid.UUID `json:"id"`
	UserID              uuid.UUID `json:"user_id"`
	NexiosAccountNumber string    `json:"nexios_account_number"`
	BankName            string    `json:"bank_name"`
	Label               string    `json:"label"`
	Kind                Kind      `json:"kind"`
	Currency            string    `json:"currency"`
	BalanceMinor        int64     `json:"balance_minor_units"`
	RespectsFloor       bool      `json:"respects_floor"`
	IsActive            bool      `json:"is_active"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// User is a Nexios customer. NexiosAccountNumber is the customer-facing
// identifier shown as "your Nexios account number" in the user app (the
// individual linked accounts each carry their own suffixed number, e.g.
// "<UserNumber>-01", "<UserNumber>-02", ...).
type User struct {
	ID                  uuid.UUID `json:"id"`
	FullName            string    `json:"full_name"`
	Email               string    `json:"email"`
	NexiosAccountNumber string    `json:"nexios_account_number"`
	CreatedAt           time.Time `json:"created_at"`
}

// TransferType is which of the three flows the user app offers moved the
// money.
type TransferType string

const (
	TransferOwn        TransferType = "own"        // between the user's own accounts
	TransferP2P        TransferType = "p2p"        // to another Nexios user
	TransferDistribute TransferType = "distribute" // split across several authorized accounts
)

// Transfer is the human-readable record of a completed transfer, returned
// to the app and listed in transaction history. The authoritative,
// independently-auditable record of the underlying money movement is the
// balanced ledger.LedgerBatch written in the same database transaction.
type Transfer struct {
	ID            uuid.UUID    `json:"id"`
	TransactionID uuid.UUID    `json:"transaction_id"`
	UserID        uuid.UUID    `json:"user_id"`
	Type          TransferType `json:"type"`
	FromAccountID *uuid.UUID   `json:"from_account_id,omitempty"`
	ToAccountID   *uuid.UUID   `json:"to_account_id,omitempty"`
	Counterparty  string       `json:"counterparty,omitempty"`
	Note          string       `json:"note,omitempty"`
	AmountMinor   int64        `json:"amount_minor_units"`
	Currency      string       `json:"currency"`
	Reference     string       `json:"reference"`
	Status        string       `json:"status"`
	CreatedAt     time.Time    `json:"created_at"`
	// Splits is populated only for a "distribute" transfer, one entry per
	// account it drew from.
	Splits []DistributeSplit `json:"splits,omitempty"`
}

// DistributeSplit is one leg of a "distribute" transfer: the amount drawn
// from a single account.
type DistributeSplit struct {
	AccountID   uuid.UUID `json:"account_id"`
	AmountMinor int64     `json:"amount_minor_units"`
}

// SystemUserID and ClearingAccountID identify Nexios's own internal
// settlement account: the offsetting leg for a "distribute" transfer,
// which credits several of the user's own accounts from a single incoming
// amount (e.g. a settlement arriving from outside Nexios) and therefore -
// same as internal/ledger.InternalClearingAccountID - needs a debit
// somewhere to keep the double-entry ledger balanced. Unlike a normal
// account, the clearing account is allowed to go negative: that balance is
// exactly "funds Nexios has allocated to users but not yet reconciled
// externally," which is the whole point of a suspense/clearing account.
var (
	SystemUserID      = uuid.Nil
	ClearingAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
)

var (
	ErrAccountNotFound     = errors.New("accounts: account not found")
	ErrAccountNotOwned     = errors.New("accounts: account does not belong to this user")
	ErrAccountInactive     = errors.New("accounts: account is inactive")
	ErrInsufficientFunds   = errors.New("accounts: insufficient available balance")
	ErrInvalidAmount       = errors.New("accounts: amount must be a positive number of minor currency units")
	ErrSameAccount         = errors.New("accounts: source and destination accounts must differ")
	ErrRecipientNotFound   = errors.New("accounts: recipient Nexios user not found")
	ErrNothingToDistribute = errors.New("accounts: no eligible accounts could cover this amount")
	ErrUserNotFound        = errors.New("accounts: user not found")
)
