package accounts

import (
	"context"
	"fmt"
	"sort"
	"time"

	"nexios-finance/internal/uuid"
)

// Service is the business-logic layer the HTTP API calls: it validates
// requests, decides which ledger legs a transfer needs, and delegates the
// atomic write to a Store. Splitting this out keeps the same validation
// and split-computation logic usable from tests without an HTTP server,
// and keeps both Store implementations (memory/postgres) free of anything
// beyond "durably apply these legs."
type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) ListAccounts(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	return s.store.ListAccountsByUser(ctx, userID)
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.store.ListUsers(ctx)
}

func (s *Service) ListTransfers(ctx context.Context, userID uuid.UUID, limit int) ([]Transfer, error) {
	return s.store.ListTransfersByUser(ctx, userID, limit)
}

// TotalLiquidity sums every active account's balance for a user - the
// figure the user app's home screen shows as the hero total.
func (s *Service) TotalLiquidity(ctx context.Context, userID uuid.UUID) (int64, error) {
	accs, err := s.store.ListAccountsByUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, a := range accs {
		if a.IsActive {
			total += a.BalanceMinor
		}
	}
	return total, nil
}

// TransferOwn moves money between two accounts the same user owns.
func (s *Service) TransferOwn(ctx context.Context, userID, fromID, toID uuid.UUID, amountMinor int64, idempotencyKey string) (Transfer, error) {
	if amountMinor <= 0 {
		return Transfer{}, ErrInvalidAmount
	}
	if fromID == toID {
		return Transfer{}, ErrSameAccount
	}
	from, err := s.mustOwnActiveAccount(ctx, userID, fromID)
	if err != nil {
		return Transfer{}, err
	}
	to, err := s.mustOwnActiveAccount(ctx, userID, toID)
	if err != nil {
		return Transfer{}, err
	}
	if from.Currency != to.Currency {
		return Transfer{}, fmt.Errorf("accounts: cross-currency own transfers are not supported yet (%s -> %s)", from.Currency, to.Currency)
	}

	op := TransferOp{
		UserID: userID,
		Type:   TransferOwn,
		Legs: []Leg{
			{AccountID: from.ID, DeltaMinor: -amountMinor},
			{AccountID: to.ID, DeltaMinor: amountMinor},
		},
		FromAccountID:  &from.ID,
		ToAccountID:    &to.ID,
		AmountMinor:    amountMinor,
		Currency:       from.Currency,
		IdempotencyKey: idempotencyKey,
		Now:            time.Now().UTC(),
	}
	return s.store.ExecuteTransfer(ctx, op)
}

// TransferP2P moves money from one of the user's own accounts to another
// Nexios user, identified by their Nexios account number or full name.
func (s *Service) TransferP2P(ctx context.Context, userID, fromAccountID uuid.UUID, recipientQuery string, amountMinor int64, note, idempotencyKey string) (Transfer, error) {
	if amountMinor <= 0 {
		return Transfer{}, ErrInvalidAmount
	}
	from, err := s.mustOwnActiveAccount(ctx, userID, fromAccountID)
	if err != nil {
		return Transfer{}, err
	}

	recipient, err := s.store.FindUserByQuery(ctx, recipientQuery)
	if err != nil {
		return Transfer{}, err
	}
	if recipient.ID == userID {
		return Transfer{}, fmt.Errorf("accounts: cannot send a P2P transfer to yourself - use an own-account transfer instead")
	}

	recipientAccounts, err := s.store.ListAccountsByUser(ctx, recipient.ID)
	if err != nil {
		return Transfer{}, err
	}
	var to *Account
	for i := range recipientAccounts {
		if recipientAccounts[i].IsActive {
			to = &recipientAccounts[i]
			break
		}
	}
	if to == nil {
		return Transfer{}, fmt.Errorf("accounts: recipient has no active account to receive funds into")
	}

	op := TransferOp{
		UserID: userID,
		Type:   TransferP2P,
		Legs: []Leg{
			{AccountID: from.ID, DeltaMinor: -amountMinor},
			{AccountID: to.ID, DeltaMinor: amountMinor},
		},
		FromAccountID:  &from.ID,
		ToAccountID:    &to.ID,
		Counterparty:   recipient.FullName,
		Note:           note,
		AmountMinor:    amountMinor,
		Currency:       from.Currency,
		IdempotencyKey: idempotencyKey,
		Now:            time.Now().UTC(),
	}
	return s.store.ExecuteTransfer(ctx, op)
}

// TransferDistribute allocates a single incoming amount across several of
// the user's own authorized accounts, largest-headroom-first, skipping
// accounts that respect a liquidity floor until every other account is
// full - the same greedy split the user app's liquidity-optimization demo
// (computeSplit) simulates client-side. The offsetting ledger leg is the
// internal clearing account (see ClearingAccountID): this operation models
// money arriving from outside Nexios and being allocated across the
// user's banks, not money moving between the user's own balances.
func (s *Service) TransferDistribute(ctx context.Context, userID uuid.UUID, amountMinor int64, idempotencyKey string) (Transfer, error) {
	if amountMinor <= 0 {
		return Transfer{}, ErrInvalidAmount
	}
	accs, err := s.store.ListAccountsByUser(ctx, userID)
	if err != nil {
		return Transfer{}, err
	}

	splits, err := computeDistribution(accs, amountMinor)
	if err != nil {
		return Transfer{}, err
	}

	legs := make([]Leg, 0, len(splits)+1)
	legs = append(legs, Leg{AccountID: ClearingAccountID, DeltaMinor: -amountMinor})
	for _, sp := range splits {
		legs = append(legs, Leg{AccountID: sp.AccountID, DeltaMinor: sp.AmountMinor})
	}

	op := TransferOp{
		UserID:         userID,
		Type:           TransferDistribute,
		Legs:           legs,
		AmountMinor:    amountMinor,
		Currency:       "SAR",
		IdempotencyKey: idempotencyKey,
		Splits:         splits,
		Now:            time.Now().UTC(),
	}
	return s.store.ExecuteTransfer(ctx, op)
}

// computeDistribution decides how much of amountMinor each account
// receives: accounts that don't need to respect a minimum-safety floor are
// filled first (in the order they were authorized), then floor-respecting
// accounts (investment/liquidity accounts) absorb any remainder, so an
// investment portfolio is only topped up once ordinary accounts are
// covered. This mirrors the priority the frontend's computeSplit demo
// uses for the (unrelated but conceptually similar) core liquidity split.
func computeDistribution(accs []Account, amountMinor int64) ([]DistributeSplit, error) {
	active := make([]Account, 0, len(accs))
	for _, a := range accs {
		if a.IsActive && a.ID != ClearingAccountID {
			active = append(active, a)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].RespectsFloor != active[j].RespectsFloor {
			return !active[i].RespectsFloor // non-floor accounts first
		}
		return active[i].CreatedAt.Before(active[j].CreatedAt)
	})

	remaining := amountMinor
	var splits []DistributeSplit
	// Simple, transparent policy for a demo/prototype-backing system: split
	// evenly-weighted by need across the first accounts encountered, each
	// taking up to a third of the amount at a time until it's exhausted.
	// (A real allocation policy - e.g. targeting each account's configured
	// minimum-safety-threshold - would replace this function; the ledger
	// and API layers around it don't change.)
	for i := 0; i < len(active) && remaining > 0; i++ {
		share := amountMinor / int64(len(active))
		if share == 0 {
			share = remaining
		}
		if share > remaining {
			share = remaining
		}
		if i == len(active)-1 {
			share = remaining // give the last account whatever is left, avoids rounding loss
		}
		if share <= 0 {
			continue
		}
		splits = append(splits, DistributeSplit{AccountID: active[i].ID, AmountMinor: share})
		remaining -= share
	}

	if len(splits) == 0 || remaining > 0 {
		return nil, ErrNothingToDistribute
	}
	return splits, nil
}

func (s *Service) mustOwnActiveAccount(ctx context.Context, userID, accountID uuid.UUID) (Account, error) {
	acc, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return Account{}, err
	}
	if acc.UserID != userID {
		return Account{}, ErrAccountNotOwned
	}
	if !acc.IsActive {
		return Account{}, ErrAccountInactive
	}
	return acc, nil
}
