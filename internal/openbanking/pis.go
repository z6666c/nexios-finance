package openbanking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"nexios-finance/internal/tracing"
)

type PISService struct {
	client *Client
	signer PayloadSigner
}

func NewPISService(client *Client) *PISService {
	return &PISService{client: client, signer: NoopSigner{}}
}

func NewPISServiceWithSigner(client *Client, signer PayloadSigner) *PISService {
	return &PISService{client: client, signer: signer}
}

type paymentInitiationRequest struct {
	AmountMinor    int64  `json:"amount_minor_units"`
	Currency       string `json:"currency"`
	AccountRef     string `json:"account_ref"`
	IdempotencyKey string `json:"idempotency_key"`
	VRPConsentID   string `json:"vrp_consent_id"`
}

type paymentInitiationResponse struct {
	ExternalRef string `json:"external_ref"`
	Status      string `json:"status"`
}

type PaymentLegRequest struct {
	AmountMinor    int64
	Currency       string
	AccountRef     string
	IdempotencyKey string
	VRPConsentID   string
}

func (s *PISService) InitiateForLeg(ctx context.Context, req PaymentLegRequest) (string, error) {
	return s.InitiatePayment(ctx, paymentInitiationRequest{
		AmountMinor:    req.AmountMinor,
		Currency:       req.Currency,
		AccountRef:     req.AccountRef,
		IdempotencyKey: req.IdempotencyKey,
		VRPConsentID:   req.VRPConsentID,
	})
}

func (s *PISService) InitiatePayment(ctx context.Context, req paymentInitiationRequest) (externalRef string, err error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/payments", s.client.BaseURL)
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	if traceID := tracing.FromContext(ctx); traceID != "" {
		httpReq.Header.Set(tracing.HeaderName, traceID)
	}
	if sig, sigErr := s.signer.Sign(body); sigErr == nil && sig != "" {
		httpReq.Header.Set("x-jws-signature", sig)
	}
	httpReq = s.client.WithContext(ctx, httpReq)

	resp, err := s.client.HTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("payment initiation failed at %s: %w", s.client.Provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("%s rejected the payment with code (%d)", s.client.Provider, resp.StatusCode)
	}

	var parsed paymentInitiationResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("failed to parse payment response from %s: %w", s.client.Provider, err)
	}

	return parsed.ExternalRef, nil
}

func (s *PISService) ReversePayment(ctx context.Context, externalRef, idempotencyKey string) error {
	url := fmt.Sprintf("%s/payments/%s/reversals", s.client.BaseURL, externalRef)
	httpReq, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Idempotency-Key", idempotencyKey)
	httpReq = s.client.WithContext(ctx, httpReq)

	resp, err := s.client.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("payment reversal failed at %s: %w", s.client.Provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("could not reverse the transaction at %s (code %d) - manual intervention required", s.client.Provider, resp.StatusCode)
	}
	return nil
}
