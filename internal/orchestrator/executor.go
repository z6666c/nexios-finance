package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"nexios-finance/internal/uuid"

	"nexios-finance/internal/domain"
	"nexios-finance/internal/ledger"
	"nexios-finance/internal/openbanking"
)

type ProviderResolver interface {
	ResolvePIS(provider string) (*openbanking.PISService, error)
	ResolveAccountRef(sourceID uuid.UUID) (string, error)
}

type Executor struct {
	resolver ProviderResolver
	ledger   *ledger.Service
	rollback *RollbackHandler
	breaker  *CircuitBreaker
	timeout  time.Duration
}

func NewExecutor(resolver ProviderResolver, ledgerSvc *ledger.Service, timeout time.Duration) *Executor {
	if timeout == 0 {
		timeout = 300 * time.Millisecond
	}
	return &Executor{
		resolver: resolver,
		ledger:   ledgerSvc,
		rollback: NewRollbackHandler(resolver, ledgerSvc),
		breaker:  NewCircuitBreaker(3, 5*time.Second, 150*time.Millisecond),
		timeout:  timeout,
	}
}

type legResult struct {
	index int
	leg   domain.SplitLeg
	err   error
}

func (e *Executor) Execute(ctx context.Context, tx *domain.Transaction) error {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	tx.Status = domain.StatusAuthorizing
	results := make([]legResult, len(tx.ExecutionPlan))

	var wg sync.WaitGroup
	for i, leg := range tx.ExecutionPlan {
		wg.Add(1)
		go func(i int, leg domain.SplitLeg) {
			defer wg.Done()
			updated, err := e.executeLeg(ctx, tx.ID, leg, tx.IdempotencyKey)
			results[i] = legResult{index: i, leg: updated, err: err}
		}(i, leg)
	}
	wg.Wait()

	var failed bool
	var firstErr error
	succeeded := make([]domain.SplitLeg, 0, len(results))

	for _, r := range results {
		tx.ExecutionPlan[r.index] = r.leg
		if r.err != nil {
			failed = true
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		succeeded = append(succeeded, r.leg)
	}

	if failed {
		tx.Status = domain.StatusRollingBack
		if rbErr := e.rollback.CompensateAll(context.Background(), tx.ID, succeeded, tx.IdempotencyKey); rbErr != nil {
			tx.FailureReason = fmt.Sprintf("execution failed (%v) and automatic compensation also failed (%v) - urgent manual intervention required", firstErr, rbErr)
			tx.Status = domain.StatusFailed
			return fmt.Errorf("critical: execution and compensation both failed: %w", rbErr)
		}
		tx.Status = domain.StatusRolledBack
		tx.FailureReason = firstErr.Error()
		return firstErr
	}

	tx.Status = domain.StatusApproved
	return nil
}

func (e *Executor) executeLeg(ctx context.Context, txID uuid.UUID, leg domain.SplitLeg, txIdempotencyKey string) (domain.SplitLeg, error) {
	if !e.breaker.Allow(leg.Provider) {
		leg.Status = domain.SplitFailed
		leg.Error = fmt.Sprintf("circuit open for %s - provider temporarily isolated due to repeated failures or latency", leg.Provider)
		return leg, fmt.Errorf("circuit open: %s", leg.Provider)
	}

	pis, err := e.resolver.ResolvePIS(leg.Provider)
	if err != nil {
		leg.Status = domain.SplitFailed
		leg.Error = err.Error()
		return leg, err
	}

	accountRef, err := e.resolver.ResolveAccountRef(leg.SourceID)
	if err != nil {
		leg.Status = domain.SplitFailed
		leg.Error = err.Error()
		return leg, err
	}

	legIdempotencyKey := fmt.Sprintf("%s:%s", txIdempotencyKey, leg.SourceID.String())

	start := time.Now()
	result, err := e.initiateAndRecord(ctx, pis, txID, leg, accountRef, legIdempotencyKey)
	e.breaker.RecordResult(leg.Provider, err, time.Since(start))
	return result, err
}

func (e *Executor) initiateAndRecord(ctx context.Context, pis *openbanking.PISService, txID uuid.UUID, leg domain.SplitLeg, accountRef, idempotencyKey string) (domain.SplitLeg, error) {
	ref, err := pis.InitiateForLeg(ctx, openbanking.PaymentLegRequest{
		AmountMinor:    leg.AmountMinor,
		Currency:       "SAR",
		AccountRef:     accountRef,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		leg.Status = domain.SplitFailed
		leg.Error = err.Error()
		return leg, err
	}

	leg.ExternalRef = ref
	leg.Status = domain.SplitSettled

	if err := e.ledger.RecordSplitSettlement(ctx, txID, leg, idempotencyKey); err != nil {
		leg.Error = fmt.Sprintf("settled but ledger write failed: %v", err)
		return leg, err
	}

	return leg, nil
}
