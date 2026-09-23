package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"nexios-finance/internal/accounts"
	"nexios-finance/internal/api"
	"nexios-finance/internal/config"
	"nexios-finance/internal/domain"
	"nexios-finance/internal/ledger"
	"nexios-finance/internal/orchestrator"
	_ "nexios-finance/internal/pgwire" // registers the "pgwire" database/sql driver
	"nexios-finance/internal/schema"
	"nexios-finance/internal/uuid"
)

// devAssetProvider is a placeholder AssetProvider for the /v1/payments/authorize
// demo Saga: it returns two synthetic assets for any user rather than
// calling a real Open Banking AIS endpoint. See internal/openbanking for
// the (currently unused-by-main) real provider client scaffolding, and the
// README's "production gaps" section for what wiring that up requires.
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

func main() {
	cfg, err := config.Load("config/config.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	accountsStore, closeStore := mustBuildAccountsStore(cfg)
	defer closeStore()

	ledgerRepo, outboxRepo := mustBuildLedgerStores(cfg)

	resolver := orchestrator.NewStaticResolver()
	ledgerSvc := ledger.NewService(ledgerRepo)
	executor := orchestrator.NewExecutor(resolver, ledgerSvc, time.Duration(cfg.RequestTimeoutMs)*time.Millisecond)
	planner := orchestrator.NewPlanner()
	paymentsSvc := orchestrator.NewLiquidityService(devAssetProvider{}, planner, executor)

	_ = outboxRepo // reserved for a future event-publishing worker; see README

	router := api.NewRouter(api.Deps{
		Accounts:   accounts.NewService(accountsStore),
		Payments:   paymentsSvc,
		AuthAPIKey: cfg.AuthAPIKey,
	})

	addr := ":" + cfg.HTTPPort
	log.Printf("Nexios Finance orchestrator listening on %s (ledger_driver=%s, auth=%v)", addr, cfg.LedgerDriver, cfg.AuthAPIKey != "")
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// mustBuildAccountsStore selects the accounts.Store backend from
// cfg.LedgerDriver: "postgres" opens a real database, applies migrations,
// and seeds demo data on first run; anything else (including the default,
// empty value) falls back to an in-process MemoryStore so the orchestrator
// runs with zero external setup. It returns a cleanup func the caller
// should defer.
func mustBuildAccountsStore(cfg config.Config) (accounts.Store, func()) {
	if cfg.LedgerDriver != "postgres" {
		log.Println("ledger_driver is not \"postgres\": using the in-memory account store (data will not survive a restart)")
		return accounts.NewMemoryStore(), func() {}
	}

	if cfg.LedgerDSN == "" {
		log.Fatal("ledger_driver is \"postgres\" but ledger_dsn is not set (config/config.yaml or NEXIOS_LEDGER_DSN)")
	}

	db, err := sql.Open("pgwire", cfg.LedgerDSN)
	if err != nil {
		log.Fatalf("opening postgres connection: %v", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("connecting to postgres at startup: %v\n(is the database running and reachable? see deployments/docker-compose.yml)", err)
	}

	if err := schema.Apply(ctx, db); err != nil {
		log.Fatalf("applying database schema: %v", err)
	}

	store := accounts.NewPostgresStore(db)
	if err := store.SeedDemoData(ctx); err != nil {
		log.Fatalf("seeding demo data: %v", err)
	}

	log.Println("connected to postgres: schema applied, using durable account storage")
	return store, func() { db.Close() }
}

// mustBuildLedgerStores wires internal/ledger's Saga-authorization ledger
// (distinct from internal/accounts' own persistence above - it backs the
// /v1/payments/authorize demo endpoint's double-entry settlement records).
// It currently always uses the in-memory implementations; wiring it to the
// same Postgres database as the accounts store is a natural next step
// (see the README's production-readiness notes) but was intentionally
// kept separate here to avoid coupling the two service areas' schemas
// before either has a real caller relying on cross-referencing them.
func mustBuildLedgerStores(cfg config.Config) (ledger.Repository, ledger.OutboxRepository) {
	_ = cfg
	return ledger.NewInMemoryRepository(), ledger.NewInMemoryOutbox()
}
