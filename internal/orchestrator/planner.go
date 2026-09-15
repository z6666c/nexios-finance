package orchestrator

import (
	"errors"
	"sort"

	"nexios-finance/internal/domain"
)

var (
	ErrInvalidAmount     = errors.New("invalid requested amount")
	ErrInsufficientFunds = errors.New("total liquidatable balance across connected sources does not cover the transaction")
)

type Planner struct{}

func NewPlanner() *Planner {
	return &Planner{}
}

func (p *Planner) BuildPlan(assets []domain.UserAsset, requestedAmountMinor int64) ([]domain.SplitLeg, error) {
	if requestedAmountMinor <= 0 {
		return nil, ErrInvalidAmount
	}

	sorted := make([]domain.UserAsset, len(assets))
	copy(sorted, assets)

	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	var plan []domain.SplitLeg
	remaining := requestedAmountMinor

	for _, asset := range sorted {
		if remaining <= 0 {
			break
		}
		if !asset.IsActive {
			continue
		}
		usable := asset.AvailableForLiquidation()
		if usable <= 0 {
			continue
		}

		deduct := usable
		if deduct > remaining {
			deduct = remaining
		}

		plan = append(plan, domain.SplitLeg{
			SourceID:    asset.ID,
			Provider:    asset.ProviderName,
			AssetType:   asset.Type,
			AmountMinor: deduct,
			Status:      domain.SplitPending,
		})

		remaining -= deduct
	}

	if remaining > 0 {
		return nil, ErrInsufficientFunds
	}

	return plan, nil
}
