package orchestrator

import (
	"context"
	"fmt"
	"sync"

	"nexios-finance/internal/uuid"

	"nexios-finance/internal/domain"
	"nexios-finance/internal/ledger"
)

type RollbackHandler struct {
	resolver ProviderResolver
	ledger   *ledger.Service
}

func NewRollbackHandler(resolver ProviderResolver, ledgerSvc *ledger.Service) *RollbackHandler {
	return &RollbackHandler{resolver: resolver, ledger: ledgerSvc}
}

type compensationResult struct {
	sourceID uuid.UUID
	err      error
}

func (r *RollbackHandler) CompensateAll(ctx context.Context, txID uuid.UUID, settledLegs []domain.SplitLeg, txIdempotencyKey string) error {
	if len(settledLegs) == 0 {
		return nil
	}

	results := make([]compensationResult, len(settledLegs))
	var wg sync.WaitGroup

	for i, leg := range settledLegs {
		wg.Add(1)
		go func(i int, leg domain.SplitLeg) {
			defer wg.Done()
			results[i] = compensationResult{
				sourceID: leg.SourceID,
				err:      r.compensateLeg(ctx, txID, leg, txIdempotencyKey),
			}
		}(i, leg)
	}
	wg.Wait()

	var failures []string
	for _, res := range results {
		if res.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", res.sourceID, res.err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("failed to compensate %d/%d legs - manual settlement required: %v", len(failures), len(settledLegs), failures)
	}
	return nil
}

func (r *RollbackHandler) compensateLeg(ctx context.Context, txID uuid.UUID, leg domain.SplitLeg, txIdempotencyKey string) error {
	pis, err := r.resolver.ResolvePIS(leg.Provider)
	if err != nil {
		return err
	}

	compensationKey := fmt.Sprintf("%s:%s:reversal", txIdempotencyKey, leg.SourceID.String())

	if err := pis.ReversePayment(ctx, leg.ExternalRef, compensationKey); err != nil {
		if ledgerErr := r.ledger.RecordCompensation(ctx, txID, leg, compensationKey); ledgerErr != nil {
			return fmt.Errorf("bank reversal failed (%v) and compensating ledger entry also failed (%v)", err, ledgerErr)
		}
		return fmt.Errorf("could not reverse at %s (%v) - internal compensating entry recorded only, manual follow-up with the bank required", leg.Provider, err)
	}

	leg.Status = domain.SplitCompensated
	return r.ledger.RecordCompensation(ctx, txID, leg, compensationKey)
}
