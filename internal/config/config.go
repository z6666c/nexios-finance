// Package config loads Nexios Finance's runtime configuration from
// config/config.yaml plus environment variable overrides.
//
// It intentionally does not depend on a YAML library: this project ships
// with zero third-party Go dependencies (see internal/uuid and
// internal/pgwire for why) and config.yaml only ever uses a flat
// "key: value" shape, one setting per line, so a small hand-written parser
// is both sufficient and one less thing that can fail to fetch on a
// network-restricted local server.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is every setting the orchestrator needs at startup.
type Config struct {
	HTTPPort         string
	RequestTimeoutMs int

	ProviderName           string
	ProviderBaseURL        string
	ProviderClientCertPath string
	ProviderClientKeyPath  string
	ProviderCACertPath     string

	// LedgerDriver selects the persistence backend: "memory" (default,
	// zero setup, data lost on restart - fine for a demo/dev run) or
	// "postgres" (durable, required for a real deployment).
	LedgerDriver string
	LedgerDSN    string

	// AuthAPIKey, when non-empty, is required as a "Bearer <key>"
	// Authorization header on every /v1 request. Leave empty to run
	// without authentication (only appropriate on a trusted local
	// network) - see README for the production caveats.
	AuthAPIKey string
}

// Default returns the built-in fallback configuration used when no
// config.yaml is found and no environment variables are set: an in-memory
// ledger on :8080 with no auth, so `go run ./cmd/orchestrator` works with
// zero setup for local development.
func Default() Config {
	return Config{
		HTTPPort:         "8080",
		RequestTimeoutMs: 300,
		ProviderName:     "Al Rajhi Bank",
		LedgerDriver:     "memory",
	}
}

// Load reads path (config/config.yaml by default) if it exists, then
// applies NEXIOS_* environment variable overrides on top. Environment
// variables always win, which is what lets docker-compose (or any other
// orchestrator) inject secrets like the database password without
// touching the checked-in config file.
func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		path = "config/config.yaml"
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := parseInto(&cfg, data); err != nil {
			return cfg, fmt.Errorf("config: parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("config: reading %s: %w", path, err)
	}

	applyEnv(&cfg)
	return cfg, nil
}

func parseInto(cfg *Config, data []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		setField(cfg, key, val)
	}
	return scanner.Err()
}

func setField(cfg *Config, key, val string) {
	switch key {
	case "http_port":
		cfg.HTTPPort = val
	case "request_timeout_ms":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.RequestTimeoutMs = n
		}
	case "provider_name":
		cfg.ProviderName = val
	case "provider_base_url":
		cfg.ProviderBaseURL = val
	case "provider_client_cert_path":
		cfg.ProviderClientCertPath = val
	case "provider_client_key_path":
		cfg.ProviderClientKeyPath = val
	case "provider_ca_cert_path":
		cfg.ProviderCACertPath = val
	case "ledger_driver":
		cfg.LedgerDriver = val
	case "ledger_dsn":
		cfg.LedgerDSN = val
	case "auth_api_key":
		cfg.AuthAPIKey = val
	}
}

func applyEnv(cfg *Config) {
	env := func(key string) (string, bool) { return os.LookupEnv(key) }

	if v, ok := env("NEXIOS_HTTP_PORT"); ok {
		cfg.HTTPPort = v
	}
	if v, ok := env("NEXIOS_LEDGER_DRIVER"); ok {
		cfg.LedgerDriver = v
	}
	if v, ok := env("NEXIOS_LEDGER_DSN"); ok {
		cfg.LedgerDSN = v
	}
	if v, ok := env("NEXIOS_AUTH_API_KEY"); ok {
		cfg.AuthAPIKey = v
	}
	if v, ok := env("NEXIOS_PROVIDER_NAME"); ok {
		cfg.ProviderName = v
	}
}
