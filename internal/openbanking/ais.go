package openbanking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"nexios-finance/internal/domain"
)

type AISService struct {
	client *Client
}

func NewAISService(client *Client) *AISService {
	return &AISService{client: client}
}

type balanceAPIResponse struct {
	AvailableAmountMinor int64  `json:"available_amount_minor_units"`
	Currency             string `json:"currency"`
}

func (s *AISService) GetBalance(ctx context.Context, accountRef string) (int64, string, error) {
	url := fmt.Sprintf("%s/accounts/%s/balances", s.client.BaseURL, accountRef)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req = s.client.WithContext(ctx, req)

	resp, err := s.client.HTTPClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("balance query failed for %s: %w", s.client.Provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("unexpected response (%d) from %s", resp.StatusCode, s.client.Provider)
	}

	var parsed balanceAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, "", fmt.Errorf("failed to parse balance response from %s: %w", s.client.Provider, err)
	}

	return parsed.AvailableAmountMinor, parsed.Currency, nil
}

func (s *AISService) RefreshAssetBalance(ctx context.Context, asset *domain.UserAsset, accountRef string) error {
	balance, currency, err := s.GetBalance(ctx, accountRef)
	if err != nil {
		return err
	}
	asset.AvailableBalanceMin = balance
	asset.Currency = currency
	return nil
}
