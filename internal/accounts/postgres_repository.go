package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nexios-finance/internal/uuid"
)

// PostgresStore is a Store backed by a Postgres-wire-compatible database,
// reached through internal/pgwire (this project's dependency-free driver -
// see that package for why it exists instead of pgx/lib-pq). It writes
// every transfer as one SQL transaction: the account balance updates, the
// balanced double-entry ledger batch, the human-readable transfer record,
// and the outbox event are all committed together or not at all.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore wraps an already-open *sql.DB (opened with
// sql.Open("pgwire", dsn); see internal/pgwire). Callers are responsible
// for running internal/schema.Apply against it first.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// SeedDemoData inserts the same demo user, nine linked accounts/portfolios
// and other-Nexios-users that MemoryStore seeds in-process, but only if the
// users table is currently empty - so a fresh Postgres deployment shows
// working, non-empty data on first run (matching what web/user-app.html
// simulates client-side) without ever overwriting real data on a restart.
func (s *PostgresStore) SeedDemoData(ctx context.Context) error {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("accounts: checking existing users: %w", err)
	}
	if count > 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	now := time.Now().UTC()
	demoUserID := uuid.New()
	demoNumber := "NXS-SA-7741-2093"

	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, full_name, email, nexios_account_number, created_at) VALUES ($1,$2,$3,$4,$5)`,
		demoUserID.String(), "محمد العتيبي", "demo@nexios.sa", demoNumber, now); err != nil {
		return fmt.Errorf("accounts: seeding demo user: %w", err)
	}

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
		_, err := tx.ExecContext(ctx, `
			INSERT INTO accounts (id, user_id, nexios_account_number, bank_name, label, kind, currency,
			                       balance_minor, respects_floor, is_active, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,'SAR',$7,$8,true,$9,$9)`,
			uuid.New().String(), demoUserID.String(), fmt.Sprintf("%s-%s", demoNumber, sd.suffix),
			sd.bank, sd.name, string(sd.kind), sd.balance, sd.respectsFloor, now)
		if err != nil {
			return fmt.Errorf("accounts: seeding account %s: %w", sd.suffix, err)
		}
	}

	otherUsers := []struct{ name, num, bank string }{
		{"سارة الحامد", "NXS-SA-5521-4410", "بنك الراجحي"},
		{"عبدالله الحامد", "NXS-SA-3392-8871", "مصرف الإنماء"},
		{"فهد العتيبي", "NXS-SA-6610-2245", "البنك الأهلي السعودي"},
		{"نورة القحطاني", "NXS-SA-8834-7762", "بنك الجزيرة"},
	}
	for _, u := range otherUsers {
		// email has a UNIQUE constraint; these are prototype-only contacts
		// with no real email on file, so derive a stable, unique
		// placeholder from their Nexios account number rather than reusing
		// an empty string (which would collide across rows).
		placeholderEmail := strings.ToLower(strings.ReplaceAll(u.num, "-", "")) + "@placeholder.nexios.sa"
		userID := uuid.New()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, full_name, email, nexios_account_number, created_at) VALUES ($1,$2,$3,$4,$5)`,
			userID.String(), u.name, placeholderEmail, u.num, now); err != nil {
			return fmt.Errorf("accounts: seeding user %s: %w", u.name, err)
		}
		// Every Nexios user needs at least one account to receive a P2P
		// transfer into, same as the demo user.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO accounts (id, user_id, nexios_account_number, bank_name, label, kind, currency,
			                       balance_minor, respects_floor, is_active, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'الحساب الجاري','bank','SAR',0,false,true,$5,$5)`,
			uuid.New().String(), userID.String(), u.num+"-01", u.bank, now); err != nil {
			return fmt.Errorf("accounts: seeding account for %s: %w", u.name, err)
		}
	}

	// Internal settlement/clearing account: see accounts.ClearingAccountID.
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, full_name, email, nexios_account_number, created_at) VALUES ($1,'Nexios Settlement','settlement@nexios.internal','NXS-SYSTEM-CLEARING',$2)`,
		SystemUserID.String(), now); err != nil {
		return fmt.Errorf("accounts: seeding system user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (id, user_id, nexios_account_number, bank_name, label, kind, currency,
		                       balance_minor, respects_floor, is_active, created_at, updated_at)
		VALUES ($1,$2,'NXS-SYSTEM-CLEARING-00','Nexios Internal','حساب المقاصة الداخلي','bank','SAR',0,false,true,$3,$3)`,
		ClearingAccountID.String(), SystemUserID.String(), now); err != nil {
		return fmt.Errorf("accounts: seeding clearing account: %w", err)
	}

	return tx.Commit()
}

func (s *PostgresStore) ListAccountsByUser(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, nexios_account_number, bank_name, label, kind, currency,
		       balance_minor, respects_floor, is_active, created_at, updated_at
		FROM accounts WHERE user_id = $1 ORDER BY created_at`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, nexios_account_number, bank_name, label, kind, currency,
		       balance_minor, respects_floor, is_active, created_at, updated_at
		FROM accounts WHERE id = $1`, id.String())
	a, err := scanAccountRow(row)
	if err == sql.ErrNoRows {
		return Account{}, ErrAccountNotFound
	}
	return a, err
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, full_name, email, nexios_account_number, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, full_name, email, nexios_account_number, created_at FROM users WHERE id = $1`, id.String())
	u, err := scanUserRow(row)
	if err == sql.ErrNoRows {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func (s *PostgresStore) FindUserByQuery(ctx context.Context, query string) (User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, full_name, email, nexios_account_number, created_at FROM users
		WHERE lower(nexios_account_number) = lower($1) OR lower(full_name) = lower($1)
		LIMIT 1`, query)
	u, err := scanUserRow(row)
	if err == sql.ErrNoRows {
		return User{}, ErrRecipientNotFound
	}
	return u, err
}

func (s *PostgresStore) ListTransfersByUser(ctx context.Context, userID uuid.UUID, limit int) ([]Transfer, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, transaction_id, user_id, transfer_type, from_account_id, to_account_id,
		       counterparty, note, amount_minor, currency, reference, status, created_at
		FROM transfers WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID.String(), int64(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ExecuteTransfer runs the whole transfer as one SQL transaction: lock and
// re-check every debited account's balance, apply all legs, write the
// balanced ledger batch, insert the transfer record, and enqueue the
// outbox event. Any failure rolls the entire transaction back, so a
// transfer either fully happens or leaves no trace.
func (s *PostgresStore) ExecuteTransfer(ctx context.Context, op TransferOp) (Transfer, error) {
	var sum int64
	for _, l := range op.Legs {
		sum += l.DeltaMinor
	}
	if sum != 0 {
		return Transfer{}, fmt.Errorf("accounts: unbalanced transfer legs (sum=%d)", sum)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Transfer{}, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	now := op.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	for _, l := range op.Legs {
		var newBalance int64
		// A single conditional UPDATE...RETURNING both applies the delta
		// and enforces "never go negative" atomically under row-level
		// locking, without a separate SELECT ... FOR UPDATE round trip.
		// The internal clearing account is exempt from the non-negative
		// guard - see accounts.ClearingAccountID.
		query := `
			UPDATE accounts SET balance_minor = balance_minor + $1, updated_at = $2
			WHERE id = $3 AND is_active = true AND (balance_minor + $1 >= 0 OR id = '` + ClearingAccountID.String() + `')
			RETURNING balance_minor`
		err := tx.QueryRowContext(ctx, query, l.DeltaMinor, now, l.AccountID.String()).Scan(&newBalance)
		if err == sql.ErrNoRows {
			// Either the account doesn't exist/is inactive, or the debit
			// would have taken it negative; disambiguate for a clearer error.
			var exists bool
			var active bool
			checkErr := tx.QueryRowContext(ctx, `SELECT true, is_active FROM accounts WHERE id = $1`, l.AccountID.String()).Scan(&exists, &active)
			if checkErr == sql.ErrNoRows {
				return Transfer{}, ErrAccountNotFound
			}
			if checkErr != nil {
				return Transfer{}, checkErr
			}
			if !active {
				return Transfer{}, ErrAccountInactive
			}
			return Transfer{}, ErrInsufficientFunds
		}
		if err != nil {
			return Transfer{}, err
		}
	}

	transactionID := uuid.New()
	txIDStr := transactionID.String()

	// Balanced double-entry ledger batch: a DEBIT entry for every negative
	// leg, a CREDIT entry for every positive leg, so total debits always
	// equal total credits for this transaction.
	for i, l := range op.Legs {
		entryType := "CREDIT"
		amount := l.DeltaMinor
		if l.DeltaMinor < 0 {
			entryType = "DEBIT"
			amount = -l.DeltaMinor
		}
		idemKey := fmt.Sprintf("%s:%s:%d", op.IdempotencyKey, txIDStr, i)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount_minor, currency, idempotency_key, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.New().String(), txIDStr, l.AccountID.String(), entryType, amount, op.Currency, idemKey, now)
		if err != nil {
			return Transfer{}, fmt.Errorf("accounts: writing ledger entry: %w", err)
		}
	}

	transfer := Transfer{
		ID:            uuid.New(),
		TransactionID: transactionID,
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

	var fromID, toID interface{}
	if op.FromAccountID != nil {
		fromID = op.FromAccountID.String()
	}
	if op.ToAccountID != nil {
		toID = op.ToAccountID.String()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO transfers (id, transaction_id, user_id, transfer_type, from_account_id, to_account_id,
		                        counterparty, note, amount_minor, currency, reference, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		transfer.ID.String(), txIDStr, op.UserID.String(), string(op.Type), fromID, toID,
		op.Counterparty, op.Note, op.AmountMinor, op.Currency, transfer.Reference, transfer.Status, now)
	if err != nil {
		return Transfer{}, fmt.Errorf("accounts: writing transfer record: %w", err)
	}

	payload, _ := json.Marshal(transfer)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, transaction_id, event_type, payload, published, created_at)
		VALUES ($1, $2, $3, $4, false, $5)`,
		uuid.New().String(), txIDStr, "transfer.settled", string(payload), now)
	if err != nil {
		return Transfer{}, fmt.Errorf("accounts: enqueueing outbox event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Transfer{}, err
	}
	return transfer, nil
}

// --- row scanning helpers ------------------------------------------------

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanAccount(rows *sql.Rows) (Account, error) { return scanAccountRow(rows) }
func scanAccountRow(row scanner) (Account, error) {
	var a Account
	var idStr, userIDStr, kindStr string
	if err := row.Scan(&idStr, &userIDStr, &a.NexiosAccountNumber, &a.BankName, &a.Label, &kindStr,
		&a.Currency, &a.BalanceMinor, &a.RespectsFloor, &a.IsActive, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return Account{}, err
	}
	var err error
	if a.ID, err = uuid.Parse(idStr); err != nil {
		return Account{}, err
	}
	if a.UserID, err = uuid.Parse(userIDStr); err != nil {
		return Account{}, err
	}
	a.Kind = Kind(kindStr)
	return a, nil
}

func scanUser(rows *sql.Rows) (User, error) { return scanUserRow(rows) }
func scanUserRow(row scanner) (User, error) {
	var u User
	var idStr string
	if err := row.Scan(&idStr, &u.FullName, &u.Email, &u.NexiosAccountNumber, &u.CreatedAt); err != nil {
		return User{}, err
	}
	var err error
	if u.ID, err = uuid.Parse(idStr); err != nil {
		return User{}, err
	}
	return u, nil
}

func scanTransfer(rows *sql.Rows) (Transfer, error) {
	var t Transfer
	var idStr, txIDStr, userIDStr, typeStr string
	var fromIDStr, toIDStr, counterparty, note sql.NullString
	if err := rows.Scan(&idStr, &txIDStr, &userIDStr, &typeStr, &fromIDStr, &toIDStr,
		&counterparty, &note, &t.AmountMinor, &t.Currency, &t.Reference, &t.Status, &t.CreatedAt); err != nil {
		return Transfer{}, err
	}
	var err error
	if t.ID, err = uuid.Parse(idStr); err != nil {
		return Transfer{}, err
	}
	if t.TransactionID, err = uuid.Parse(txIDStr); err != nil {
		return Transfer{}, err
	}
	if t.UserID, err = uuid.Parse(userIDStr); err != nil {
		return Transfer{}, err
	}
	t.Type = TransferType(typeStr)
	if fromIDStr.Valid && fromIDStr.String != "" {
		id, err := uuid.Parse(fromIDStr.String)
		if err != nil {
			return Transfer{}, err
		}
		t.FromAccountID = &id
	}
	if toIDStr.Valid && toIDStr.String != "" {
		id, err := uuid.Parse(toIDStr.String)
		if err != nil {
			return Transfer{}, err
		}
		t.ToAccountID = &id
	}
	t.Counterparty = counterparty.String
	t.Note = note.String
	return t, nil
}
