package accounts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"nexios-finance/internal/uuid"
)

// MemoryStore is an in-process, mutex-guarded Store. It needs no external
// database, so it's what the orchestrator falls back to when
// config.LedgerDriver is "memory" (the default) - useful for local
// development and for trying the API with zero setup. Data does not
// survive a restart; use PostgresStore for a real deployment.
type MemoryStore struct {
	mu        sync.Mutex
	users     map[uuid.UUID]User
	accounts  map[uuid.UUID]Account
	transfers []Transfer
}

// NewMemoryStore returns a MemoryStore seeded with the same demo user,
// nine linked accounts/portfolios, and Nexios account-numbering scheme
// that web/user-app.html simulates client-side, so the API and the
// prototype UI agree on what a fresh Nexios account looks like.
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		users:    make(map[uuid.UUID]User),
		accounts: make(map[uuid.UUID]Account),
	}
	s.seed()
	return s
}

func (s *MemoryStore) seed() {
	now := time.Now().UTC()

	demoUser := User{ID: uuid.New(), FullName: "محمد العتيبي", Email: "demo@nexios.sa", NexiosAccountNumber: "NXS-SA-7741-2093", CreatedAt: now}
	s.users[demoUser.ID] = demoUser

	type seedAccount struct {
		suffix, name, bank string
		kind               Kind
		balance            int64
		respectsFloor      bool
	}
	seeds := []seedAccount{
		{"01", "الحساب الجاري (الراجحي)", "بنك الراجحي", KindBank, 40000, false},
		{"02", "صندوق دراية للسيولة", "صندوق دراية", KindBank, 3142700, true},
		{"03", "بطاقة احتياطية (فيزا)", "بنك الأول", KindBank, 600000, false},
		{"04", "الحساب الجاري (الإنماء)", "مصرف الإنماء", KindBank, 230000, false},
		{"05", "حساب التوفير (الفرنسي)", "البنك السعودي الفرنسي", KindBank, 580000, false},
		{"06", "الحساب الجاري (الجزيرة)", "بنك الجزيرة", KindBank, 115000, false},
		{"07", "الحساب الجاري (الأهلي)", "البنك الأهلي السعودي", KindBank, 340000, false},
		{"08", "محفظة الأسهم السعودية", "منصة تداول استثمارية", KindPortfolio, 1860000, true},
		{"09", "محفظة صناديق المؤشرات العالمية", "منصة تداول استثمارية", KindPortfolio, 990000, true},
	}
	for _, sd := range seeds {
		acc := Account{
			ID:                  uuid.New(),
			UserID:              demoUser.ID,
			NexiosAccountNumber: fmt.Sprintf("%s-%s", demoUser.NexiosAccountNumber, sd.suffix),
			BankName:            sd.bank,
			Label:               sd.name,
			Kind:                sd.kind,
			Currency:            "SAR",
			BalanceMinor:        sd.balance,
			RespectsFloor:       sd.respectsFloor,
			IsActive:            true,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		s.accounts[acc.ID] = acc
	}

	otherUsers := []struct{ name, num, bank string }{
		{"سارة الحامد", "NXS-SA-5521-4410", "بنك الراجحي"},
		{"عبدالله الحامد", "NXS-SA-3392-8871", "مصرف الإنماء"},
		{"فهد العتيبي", "NXS-SA-6610-2245", "البنك الأهلي السعودي"},
		{"نورة القحطاني", "NXS-SA-8834-7762", "بنك الجزيرة"},
	}
	for _, u := range otherUsers {
		id := uuid.New()
		s.users[id] = User{ID: id, FullName: u.name, Email: "", NexiosAccountNumber: u.num, CreatedAt: now}
		// Every Nexios user needs at least one account to receive a P2P
		// transfer into, same as the demo user.
		accID := uuid.New()
		s.accounts[accID] = Account{
			ID: accID, UserID: id, NexiosAccountNumber: u.num + "-01",
			BankName: u.bank, Label: "الحساب الجاري", Kind: KindBank, Currency: "SAR",
			BalanceMinor: 0, IsActive: true, CreatedAt: now, UpdatedAt: now,
		}
	}

	// Internal settlement/clearing account: see accounts.ClearingAccountID.
	s.users[SystemUserID] = User{ID: SystemUserID, FullName: "Nexios Settlement", Email: "settlement@nexios.internal", NexiosAccountNumber: "NXS-SYSTEM-CLEARING", CreatedAt: now}
	s.accounts[ClearingAccountID] = Account{
		ID: ClearingAccountID, UserID: SystemUserID, NexiosAccountNumber: "NXS-SYSTEM-CLEARING-00",
		BankName: "Nexios Internal", Label: "حساب المقاصة الداخلي", Kind: KindBank, Currency: "SAR",
		BalanceMinor: 0, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
}

func (s *MemoryStore) ListAccountsByUser(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Account
	for _, a := range s.accounts {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *MemoryStore) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	return a, nil
}

func (s *MemoryStore) ListUsers(ctx context.Context) ([]User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	return out, nil
}

func (s *MemoryStore) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return u, nil
}

func (s *MemoryStore) FindUserByQuery(ctx context.Context, query string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.TrimSpace(strings.ToLower(query))
	for _, u := range s.users {
		if strings.ToLower(u.NexiosAccountNumber) == q || strings.ToLower(u.FullName) == q {
			return u, nil
		}
	}
	return User{}, ErrRecipientNotFound
}

func (s *MemoryStore) ListTransfersByUser(ctx context.Context, userID uuid.UUID, limit int) ([]Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Transfer
	for i := len(s.transfers) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if s.transfers[i].UserID == userID {
			out = append(out, s.transfers[i])
		}
	}
	return out, nil
}

func (s *MemoryStore) ExecuteTransfer(ctx context.Context, op TransferOp) (Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sum int64
	for _, l := range op.Legs {
		sum += l.DeltaMinor
	}
	if sum != 0 {
		return Transfer{}, fmt.Errorf("accounts: unbalanced transfer legs (sum=%d)", sum)
	}

	// Validate every debit can be covered before mutating anything, so a
	// rejected transfer never partially applies.
	for _, l := range op.Legs {
		if l.DeltaMinor >= 0 {
			continue
		}
		acc, ok := s.accounts[l.AccountID]
		if !ok {
			return Transfer{}, ErrAccountNotFound
		}
		if !acc.IsActive {
			return Transfer{}, ErrAccountInactive
		}
		if l.AccountID != ClearingAccountID && acc.BalanceMinor+l.DeltaMinor < 0 {
			return Transfer{}, ErrInsufficientFunds
		}
	}

	now := op.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, l := range op.Legs {
		acc := s.accounts[l.AccountID]
		acc.BalanceMinor += l.DeltaMinor
		acc.UpdatedAt = now
		s.accounts[l.AccountID] = acc
	}

	transfer := Transfer{
		ID:            uuid.New(),
		TransactionID: uuid.New(),
		UserID:        op.UserID,
		Type:          op.Type,
		FromAccountID: op.FromAccountID,
		ToAccountID:   op.ToAccountID,
		Counterparty:  op.Counterparty,
		Note:          op.Note,
		AmountMinor:   op.AmountMinor,
		Currency:      op.Currency,
		Reference:     newReference(),
		Status:        "SETTLED",
		CreatedAt:     now,
		Splits:        op.Splits,
	}
	s.transfers = append(s.transfers, transfer)
	return transfer, nil
}

func newReference() string {
	u := uuid.New()
	// Short, human-friendly reference in the same "TRF-xxxxxx" shape the
	// user-app.html prototype simulates client-side.
	return fmt.Sprintf("TRF-%06d", int(u[0])<<16|int(u[1])<<8|int(u[2])%1000000)
}
