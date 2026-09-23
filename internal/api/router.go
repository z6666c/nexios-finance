// Package api wires Nexios Finance's HTTP surface: authentication, request
// parsing, and dispatch into internal/accounts (linked accounts and the
// three transfer types) and internal/orchestrator (the existing
// liquidity-authorization Saga). Handlers here do only HTTP concerns -
// decoding, status codes, error mapping - the actual logic lives in the
// service packages so it stays testable without an HTTP server.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"nexios-finance/internal/accounts"
	"nexios-finance/internal/orchestrator"
	"nexios-finance/internal/tracing"
	"nexios-finance/internal/uuid"
)

// Deps are everything the router needs. AuthAPIKey, when non-empty,
// requires "Authorization: Bearer <key>" on every /v1/* request - a
// deliberately simple static-key check, documented in the README as a
// starting point for a local/trusted-network deployment rather than a
// full identity provider.
type Deps struct {
	Accounts   *accounts.Service
	Payments   *orchestrator.LiquidityService
	AuthAPIKey string
}

// NewRouter builds the complete HTTP handler: tracing middleware wrapping
// an (optional) bearer-auth check wrapping the versioned API mux.
func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)

	mux.HandleFunc("GET /v1/users", withDeps(deps, handleListUsers))
	mux.HandleFunc("GET /v1/accounts", withDeps(deps, handleListAccounts))
	mux.HandleFunc("GET /v1/transfers", withDeps(deps, handleListTransfers))
	mux.HandleFunc("POST /v1/transfers/own", withDeps(deps, handleTransferOwn))
	mux.HandleFunc("POST /v1/transfers/p2p", withDeps(deps, handleTransferP2P))
	mux.HandleFunc("POST /v1/transfers/distribute", withDeps(deps, handleTransferDistribute))
	mux.HandleFunc("POST /v1/payments/authorize", withDeps(deps, handleAuthorizePayment))

	return tracing.Middleware(authMiddleware(deps.AuthAPIKey, mux))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// authMiddleware requires a matching bearer token on every request except
// /healthz, when apiKey is non-empty. An empty apiKey (the default) leaves
// the API open, which is only appropriate on a trusted local network -
// see the README's deployment notes.
func authMiddleware(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || got != apiKey {
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type depsHandler func(deps Deps, w http.ResponseWriter, r *http.Request)

func withDeps(deps Deps, h depsHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h(deps, w, r)
	}
}

// --- accounts / transfers handlers ---------------------------------------

func handleListUsers(deps Deps, w http.ResponseWriter, r *http.Request) {
	users, err := deps.Accounts.ListUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func handleListAccounts(deps Deps, w http.ResponseWriter, r *http.Request) {
	userID, err := parseUUIDQuery(r, "user_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	accs, err := deps.Accounts.ListAccounts(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	total, err := deps.Accounts.TotalLiquidity(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"accounts":              accs,
		"total_liquidity_minor": total,
	})
}

func handleListTransfers(deps Deps, w http.ResponseWriter, r *http.Request) {
	userID, err := parseUUIDQuery(r, "user_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	transfers, err := deps.Accounts.ListTransfers(r.Context(), userID, 50)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transfers)
}

type ownTransferRequest struct {
	UserID         string `json:"user_id"`
	FromAccountID  string `json:"from_account_id"`
	ToAccountID    string `json:"to_account_id"`
	AmountMinor    int64  `json:"amount_minor_units"`
	IdempotencyKey string `json:"idempotency_key"`
}

func handleTransferOwn(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req ownTransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, fromID, toID, ok := parseThreeUUIDs(w, req.UserID, req.FromAccountID, req.ToAccountID)
	if !ok {
		return
	}
	if !requireIdempotencyKey(w, req.IdempotencyKey) {
		return
	}
	transfer, err := deps.Accounts.TransferOwn(r.Context(), userID, fromID, toID, req.AmountMinor, req.IdempotencyKey)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, transfer)
}

type p2pTransferRequest struct {
	UserID         string `json:"user_id"`
	FromAccountID  string `json:"from_account_id"`
	Recipient      string `json:"recipient"` // Nexios account number or full name
	AmountMinor    int64  `json:"amount_minor_units"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

func handleTransferP2P(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req p2pTransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	fromID, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from_account_id")
		return
	}
	if strings.TrimSpace(req.Recipient) == "" {
		writeError(w, http.StatusBadRequest, "recipient is required")
		return
	}
	if !requireIdempotencyKey(w, req.IdempotencyKey) {
		return
	}
	transfer, err := deps.Accounts.TransferP2P(r.Context(), userID, fromID, req.Recipient, req.AmountMinor, req.Note, req.IdempotencyKey)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, transfer)
}

type distributeTransferRequest struct {
	UserID         string `json:"user_id"`
	AmountMinor    int64  `json:"amount_minor_units"`
	IdempotencyKey string `json:"idempotency_key"`
}

func handleTransferDistribute(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req distributeTransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	if !requireIdempotencyKey(w, req.IdempotencyKey) {
		return
	}
	transfer, err := deps.Accounts.TransferDistribute(r.Context(), userID, req.AmountMinor, req.IdempotencyKey)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, transfer)
}

type authorizeHTTPRequest struct {
	UserID         string `json:"user_id"`
	AmountMinor    int64  `json:"amount_minor_units"`
	Currency       string `json:"currency"`
	MerchantID     string `json:"merchant_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func handleAuthorizePayment(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req authorizeHTTPRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !requireIdempotencyKey(w, req.IdempotencyKey) {
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	tx, err := deps.Payments.AuthorizePayment(r.Context(), userID, req.AmountMinor, req.Currency, req.MerchantID, req.IdempotencyKey)
	log.Printf("[trace=%s] authorize user=%s amount=%d status=%v", tracing.FromContext(r.Context()), userID, req.AmountMinor, tx.Status)

	status := http.StatusOK
	if err != nil {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, tx)
}

// --- request/response helpers --------------------------------------------

func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body")
		return false
	}
	return true
}

func requireIdempotencyKey(w http.ResponseWriter, key string) bool {
	if strings.TrimSpace(key) == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return false
	}
	return true
}

func parseUUIDQuery(r *http.Request, param string) (uuid.UUID, error) {
	raw := r.URL.Query().Get(param)
	if raw == "" {
		return uuid.Nil, errors.New(param + " query parameter is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errors.New("invalid " + param)
	}
	return id, nil
}

func parseThreeUUIDs(w http.ResponseWriter, a, b, c string) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	ua, err := uuid.Parse(a)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	ub, err := uuid.Parse(b)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from_account_id")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	uc, err := uuid.Parse(c)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to_account_id")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return ua, ub, uc, true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeServiceError maps a service-layer sentinel error to the right HTTP
// status; anything unrecognized is a 422 with its message, which is safe
// here since every accounts.Err* message is already written to be
// user-facing (no internal details leak through it).
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accounts.ErrAccountNotFound), errors.Is(err, accounts.ErrUserNotFound), errors.Is(err, accounts.ErrRecipientNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, accounts.ErrAccountNotOwned):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, accounts.ErrInsufficientFunds), errors.Is(err, accounts.ErrInvalidAmount),
		errors.Is(err, accounts.ErrSameAccount), errors.Is(err, accounts.ErrAccountInactive),
		errors.Is(err, accounts.ErrNothingToDistribute):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}
