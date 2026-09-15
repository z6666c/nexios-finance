package openbanking

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

type ClientConfig struct {
	ProviderName   string
	BaseURL        string
	ClientCertPath string
	ClientKeyPath  string
	CACertPath     string
	Timeout        time.Duration
}

type Client struct {
	Provider   string
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(cfg ClientConfig) (*Client, error) {
	cert, err := tls.LoadX509KeyPair(cfg.ClientCertPath, cfg.ClientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client cert for %s: %w", cfg.ProviderName, err)
	}

	caCertPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert for %s: %w", cfg.ProviderName, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("invalid CA cert for %s", cfg.ProviderName)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &Client{
		Provider: cfg.ProviderName,
		BaseURL:  cfg.BaseURL,
		HTTPClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
	}, nil
}

func (c *Client) WithContext(ctx context.Context, req *http.Request) *http.Request {
	return req.WithContext(ctx)
}
