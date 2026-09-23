package orchestrator

import (
	"context"
	"time"

	"nexios-finance/internal/uuid"

	"nexios-finance/internal/domain"
)

type AssetProvider interface {
	GetUserAssets(ctx context.Context, userID uuid.UUID) ([]domain.UserAsset, error)
}

type LiquidityService struct {
	assets   AssetProvider
	planner  *Planner
	executor *Executor
}

func NewLiquidityService(assets AssetProvider, planner *Planner, executor *Executor) *LiquidityService {
	return &LiquidityService{assets: assets, planner: planner, executor: executor}
}

func (s *LiquidityService) AuthorizePayment(ctx context.Context, userID uuid.UUID, amountMinor int64, currency, merchantID, idempotencyKey string) (*domain.Transaction, error) {
	start := time.Now()

	tx := &domain.Transaction{
		ID:               uuid.New(),
		UserID:           userID,
		MerchantID:       merchantID,
		TotalAmountMinor: amountMinor,
		Currency:         currency,
		Status:           domain.StatusInitiated,
		CreatedAt:        start,
		IdempotencyKey:   idempotencyKey,
	}

	assets, err := s.assets.GetUserAssets(ctx, userID)
	if err != nil {
		tx.Status = domain.StatusFailed
		tx.FailureReason = err.Error()
		return tx, err
	}

	tx.Status = domain.StatusPlanning
	plan, err := s.planner.BuildPlan(assets, amountMinor)
	if err != nil {
		tx.Status = domain.StatusFailed
		tx.FailureReason = err.Error()
		return tx, err
	}
	tx.ExecutionPlan = plan

	execErr := s.executor.Execute(ctx, tx)
	tx.ExecutionTimeMs = time.Since(start).Milliseconds()
	return tx, execErr
}
