-- Nexios Finance - initial schema.
--
-- Applied automatically at orchestrator startup (see internal/schema) when
-- ledger_driver is "postgres", using CREATE TABLE/INDEX IF NOT EXISTS so it
-- is safe to run against an already-migrated database on every restart.
-- Written in plain, portable SQL (no extensions required - UUIDs are
-- generated application-side by internal/uuid, not by a Postgres extension,
-- since CREATE EXTENSION may be blocked on a locked-down managed database).

CREATE TABLE IF NOT EXISTS users (
    id                     UUID PRIMARY KEY,
    full_name              TEXT NOT NULL,
    email                  TEXT NOT NULL UNIQUE,
    nexios_account_number  TEXT NOT NULL UNIQUE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS accounts (
    id                     UUID PRIMARY KEY,
    user_id                UUID NOT NULL REFERENCES users(id),
    nexios_account_number  TEXT NOT NULL UNIQUE,
    bank_name              TEXT NOT NULL,
    label                  TEXT NOT NULL,
    kind                   TEXT NOT NULL DEFAULT 'bank',      -- 'bank' | 'portfolio'
    currency               TEXT NOT NULL DEFAULT 'SAR',
    balance_minor          BIGINT NOT NULL DEFAULT 0,
    respects_floor         BOOLEAN NOT NULL DEFAULT false,
    is_active              BOOLEAN NOT NULL DEFAULT true,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);

-- Double-entry ledger. Every transfer (own-account, P2P, or multi-bank
-- distribution) writes a *balanced* batch of entries here in the same SQL
-- transaction that updates accounts.balance_minor, so the ledger is always
-- an independently-auditable record of what the materialized balances
-- claim happened.
CREATE TABLE IF NOT EXISTS ledger_entries (
    id               UUID PRIMARY KEY,
    transaction_id   UUID NOT NULL,
    account_id       UUID NOT NULL REFERENCES accounts(id),
    entry_type       TEXT NOT NULL,             -- 'DEBIT' | 'CREDIT'
    amount_minor     BIGINT NOT NULL,
    currency         TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ledger_entries_transaction_id ON ledger_entries(transaction_id);
CREATE INDEX IF NOT EXISTS idx_ledger_entries_account_id ON ledger_entries(account_id);

-- Outbox pattern: settlement/transfer events are enqueued in the same
-- transaction as the ledger write, then published asynchronously, so
-- "the money moved" and "an event was emitted about it" can never
-- disagree with each other.
CREATE TABLE IF NOT EXISTS outbox_events (
    id               UUID PRIMARY KEY,
    transaction_id   UUID NOT NULL,
    event_type       TEXT NOT NULL,
    payload          TEXT NOT NULL,
    published        BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_unpublished ON outbox_events(published) WHERE published = false;

-- Human-readable record of a transfer for the "transactions" list in the
-- apps; ledger_entries remains the source of truth for balances.
CREATE TABLE IF NOT EXISTS transfers (
    id               UUID PRIMARY KEY,
    transaction_id   UUID NOT NULL,
    user_id          UUID NOT NULL REFERENCES users(id),
    transfer_type    TEXT NOT NULL,             -- 'own' | 'p2p' | 'distribute'
    from_account_id  UUID REFERENCES accounts(id),
    to_account_id    UUID REFERENCES accounts(id),
    counterparty     TEXT,                      -- P2P recipient display name/account number
	note             TEXT,
    amount_minor     BIGINT NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'SAR',
    reference        TEXT NOT NULL UNIQUE,
    status           TEXT NOT NULL DEFAULT 'SETTLED',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_transfers_user_id ON transfers(user_id);
