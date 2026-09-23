package domain

import (
	"time"

	"nexios-finance/internal/uuid"
)

type TransactionStatus string

const (
	StatusInitiated   TransactionStatus = "INITIATED"
	StatusPlanning    TransactionStatus = "PLANNING"
	StatusAuthorizing TransactionStatus = "AUTHORIZING"
	StatusApproved    TransactionStatus = "APPROVED"
	StatusFailed      TransactionStatus = "FAILED"
	StatusRollingBack TransactionStatus = "ROLLING_BACK"
	StatusRolledBack  TransactionStatus = "ROLLED_BACK"
)

type Transaction struct {
	ID               uuid.UUID         `json:"id"`
	UserID           uuid.UUID         `json:"user_id"`
	MerchantID       string            `json:"merchant_id"`
	TotalAmountMinor int64             `json:"total_amount_minor_units"`
	Currency         string            `json:"currency"`
	Status           TransactionStatus `json:"status"`
	ExecutionPlan    []SplitLeg        `json:"splits"`
	CreatedAt        time.Time         `json:"created_at"`
	ExecutionTimeMs  int64             `json:"execution_time_ms"`
	IdempotencyKey   string            `json:"idempotency_key"`
	FailureReason    string            `json:"failure_reason,omitempty"`
}

type SplitLegStatus string

const (
	SplitPending     SplitLegStatus = "PENDING"
	SplitReserved    SplitLegStatus = "RESERVED"
	SplitSettled     SplitLegStatus = "SETTLED"
	SplitFailed      SplitLegStatus = "FAILED"
	SplitCompensated SplitLegStatus = "COMPENSATED"
)

type SplitLeg struct {
	SourceID    uuid.UUID      `json:"source_id"`
	Provider    string         `json:"provider"`
	AssetType   AssetType      `json:"asset_type"`
	AmountMinor int64          `json:"amount_minor_units"`
	ExternalRef string         `json:"external_ref,omitempty"`
	Status      SplitLegStatus `json:"status"`
	Error       string         `json:"error,omitempty"`
}
