package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"nexios-finance/internal/domain"
	"nexios-finance/internal/ledger"
	"nexios-finance/internal/orchestrator"
	"nexios-finance/internal/tracing"
)

type devAssetProvider struct{}

func (devAssetProvider) GetUserAssets(ctx context.Context, userID uuid.UUID) ([]domain.UserAsset, error) {
	return []domain.UserAsset{
		{
			ID: uuid.New(), UserID: userID, ProviderName: "Al Rajhi Bank",
			Type: domain.AssetTypeCurrentAccount, AvailableBalanceMin: 40000, Currency: "SAR",
			Priority: 1, IsActive: true, ConsentExpiresAt: time.Now().Add(24 * time.Hour), ConsentID: "demo-consent-1",
		},
		{
			ID: uuid.New(), UserID: userID, ProviderName: "Al Rajhi Bank",
			Type: domain.AssetTypeSavingsAccount, AvailableBalanceMin: 1250000, Currency: "SAR",
			Priority: 2, IsActive: true, MinimumSafetyThresholdMin: 10000,
			ConsentExpiresAt: time.Now().Add(24 * time.Hour), ConsentID: "demo-consent-2",
		},
	}, nil
}

type authorizeHTTPRequest struct {
	UserID         string `json:"user_id"`
	AmountMinor    int64  `json:"amount_minor_units"`
	Currency       string `json:"currency"`
	MerchantID     string `json:"merchant_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func main() {
	repo := ledger.NewInMemoryRepository()
	ledgerSvc := ledger.NewService(repo)

	resolver := orchestrator.NewStaticResolver()

	executor := orchestrator.NewExecutor(resolver, ledgerSvc, 300*time.Millisecond)
	planner := orchestrator.NewPlanner()
	service := orchestrator.NewLiquidityService(devAssetProvider{}, planner, executor)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/payments/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req authorizeHTTPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.IdempotencyKey == "" {
			http.Error(w, "idempotency_key is required", http.StatusBadRequest)
			return
		}

		userID, err := uuid.Parse(req.UserID)
		if err != nil {
			http.Error(w, "invalid user_id", http.StatusBadRequest)
			return
		}

		tx, err := service.AuthorizePayment(r.Context(), userID, req.AmountMinor, req.Currency, req.MerchantID, req.IdempotencyKey)
		log.Printf("[trace=%s] authorize user=%s amount=%d status=%v", tracing.FromContext(r.Context()), userID, req.AmountMinor, tx.Status)

		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		json.NewEncoder(w).Encode(tx)
	})

	handler := tracing.Middleware(mux)

	log.Println("Nexios Finance orchestrator (HTTP dev mode) listening on :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
