package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesDefaultsAndEnvironmentOverrides(t *testing.T) {
	t.Setenv("ETH_RPC_URL", "https://mainnet.example.invalid")
	t.Setenv("DATABASE_URL", "postgres://override:secret@db:5432/indexer")
	t.Setenv("LOG_LEVEL", "debug")

	path := writeConfig(t, `
ethereum:
  rpc_url: http://localhost:8545
database:
  url: postgres://local:local@localhost:5432/indexer
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Ethereum.RPCURL != "https://mainnet.example.invalid" {
		t.Fatalf("RPC URL = %q", cfg.Ethereum.RPCURL)
	}
	if cfg.Database.URL != "postgres://override:secret@db:5432/indexer" {
		t.Fatalf("database URL = %q", cfg.Database.URL)
	}
	if cfg.App.LogLevel != "debug" {
		t.Fatalf("log level = %q", cfg.App.LogLevel)
	}
	if cfg.Indexer.BlockWindow != 50 {
		t.Fatalf("block window = %d", cfg.Indexer.BlockWindow)
	}
	if cfg.HTTP.RequestTimeout.Duration != 5*time.Second {
		t.Fatalf("request timeout = %s", cfg.HTTP.RequestTimeout.Duration)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `
ethereum:
  rpc_url: http://localhost:8545
  rpc_concurency: 8
database:
  url: postgres://local:local@localhost:5432/indexer
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "rpc_concurency") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestValidateRejectsNonMainnetConfiguration(t *testing.T) {
	cfg := Default()
	cfg.Ethereum.RPCURL = "https://mainnet.example.invalid"
	cfg.Ethereum.ChainID = 11155111
	cfg.Database.URL = "postgres://indexer:secret@db:5432/indexer"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "chain_id must be 1") {
		t.Fatalf("Validate() error = %v, want chain ID error", err)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
