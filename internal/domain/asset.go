package domain

import (
	"time"

	"nexios-finance/internal/uuid"
)

type AssetType string

const (
	AssetTypeCurrentAccount AssetType = "CURRENT_ACCOUNT"
	AssetTypeSavingsAccount AssetType = "SAVINGS_ACCOUNT"
	AssetTypeCreditLine     AssetType = "CREDIT_LINE"
)

type UserAsset struct {
	ID                        uuid.UUID `json:"id"`
	UserID                    uuid.UUID `json:"user_id"`
	ProviderName              string    `json:"provider_name"`
	Type                      AssetType `json:"type"`
	AvailableBalanceMin       int64     `json:"available_balance_minor_units"`
	Currency                  string    `json:"currency"`
	Priority                  int       `json:"priority"`
	IsActive                  bool      `json:"is_active"`
	MinimumSafetyThresholdMin int64     `json:"minimum_safety_threshold_minor_units"`
	Version                   int64     `json:"version"`
	UpdatedAt                 time.Time `json:"updated_at"`
	ConsentID                 string    `json:"consent_id"`
	ConsentExpiresAt          time.Time `json:"consent_expires_at"`
}

func (a *UserAsset) AvailableForLiquidation() int64 {
	if !a.IsActive {
		return 0
	}
	usable := a.AvailableBalanceMin - a.MinimumSafetyThresholdMin
	if usable < 0 {
		return 0
	}
	return usable
}

func (a *UserAsset) ConsentValid(now time.Time) bool {
	return a.ConsentID != "" && now.Before(a.ConsentExpiresAt)
}
