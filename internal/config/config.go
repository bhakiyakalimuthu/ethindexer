package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"ethindexer/internal/domain"

	"gopkg.in/yaml.v3"
)

// Duration keeps human-readable values such as "5s" in YAML while exposing
// the parsed time.Duration to the rest of the application.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string")
	}

	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", node.Value, err)
	}

	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

type Config struct {
	App      App      `yaml:"app"`
	HTTP     HTTP     `yaml:"http"`
	Ethereum Ethereum `yaml:"ethereum"`
	Indexer  Indexer  `yaml:"indexer"`
	Database Database `yaml:"database"`
}

type App struct {
	Environment string `yaml:"environment"`
	LogLevel    string `yaml:"log_level"`
}

type HTTP struct {
	Address           string   `yaml:"address"`
	RequestTimeout    Duration `yaml:"request_timeout"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout"`
	IdleTimeout       Duration `yaml:"idle_timeout"`
	ShutdownTimeout   Duration `yaml:"shutdown_timeout"`
}

type Ethereum struct {
	RPCURL         string   `yaml:"rpc_url"`
	ChainID        uint64   `yaml:"chain_id"`
	RPCTimeout     Duration `yaml:"rpc_timeout"`
	RPCConcurrency int      `yaml:"rpc_concurrency"`
}

type Indexer struct {
	PollInterval    Duration        `yaml:"poll_interval"`
	BlockWindow     uint64          `yaml:"block_window"`
	HeadMode        domain.HeadMode `yaml:"head_mode"`
	RetryAttempts   int             `yaml:"retry_attempts"`
	RetryMinBackoff Duration        `yaml:"retry_min_backoff"`
	RetryMaxBackoff Duration        `yaml:"retry_max_backoff"`
}

type Database struct {
	URL            string   `yaml:"url"`
	MaxConnections int32    `yaml:"max_connections"`
	MinConnections int32    `yaml:"min_connections"`
	ConnectTimeout Duration `yaml:"connect_timeout"`
}

func Default() Config {
	return Config{
		App: App{
			Environment: "development",
			LogLevel:    "info",
		},
		HTTP: HTTP{
			Address:           ":8080",
			RequestTimeout:    Duration{Duration: 5 * time.Second},
			ReadHeaderTimeout: Duration{Duration: 5 * time.Second},
			IdleTimeout:       Duration{Duration: 60 * time.Second},
			ShutdownTimeout:   Duration{Duration: 15 * time.Second},
		},
		Ethereum: Ethereum{
			ChainID:        1,
			RPCTimeout:     Duration{Duration: 30 * time.Second},
			RPCConcurrency: 4,
		},
		Indexer: Indexer{
			PollInterval:    Duration{Duration: 4 * time.Second},
			BlockWindow:     50,
			HeadMode:        domain.HeadLatest,
			RetryAttempts:   3,
			RetryMinBackoff: Duration{Duration: time.Second},
			RetryMaxBackoff: Duration{Duration: 10 * time.Second},
		},
		Database: Database{
			MaxConnections: 12,
			MinConnections: 2,
			ConnectTimeout: Duration{Duration: 10 * time.Second},
		},
	}
}

// Load applies defaults, decodes one strict YAML document, applies explicit
// secret overrides, and validates the resulting configuration.
func Load(path string) (Config, error) {
	cfg := Default()

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: multiple YAML documents are not supported")
		}
		return Config{}, fmt.Errorf("decode trailing config: %w", err)
	}

	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func applyEnvironment(cfg *Config) {
	if value := strings.TrimSpace(os.Getenv("ETH_RPC_URL")); value != "" {
		cfg.Ethereum.RPCURL = value
	}
	if value := strings.TrimSpace(os.Getenv("DATABASE_URL")); value != "" {
		cfg.Database.URL = value
	}
	if value := strings.TrimSpace(os.Getenv("LOG_LEVEL")); value != "" {
		cfg.App.LogLevel = value
	}
}

func (cfg Config) Validate() error {
	var problems []error

	if strings.TrimSpace(cfg.App.Environment) == "" {
		problems = append(problems, errors.New("app.environment is required"))
	}
	if strings.TrimSpace(cfg.App.LogLevel) == "" {
		problems = append(problems, errors.New("app.log_level is required"))
	}
	if strings.TrimSpace(cfg.HTTP.Address) == "" {
		problems = append(problems, errors.New("http.address is required"))
	}
	problems = append(problems,
		positiveDuration("http.request_timeout", cfg.HTTP.RequestTimeout),
		positiveDuration("http.read_header_timeout", cfg.HTTP.ReadHeaderTimeout),
		positiveDuration("http.idle_timeout", cfg.HTTP.IdleTimeout),
		positiveDuration("http.shutdown_timeout", cfg.HTTP.ShutdownTimeout),
	)

	problems = append(problems, validateURL("ethereum.rpc_url", cfg.Ethereum.RPCURL, "http", "https"))
	if cfg.Ethereum.ChainID != 1 {
		problems = append(problems, fmt.Errorf("ethereum.chain_id must be 1 for mainnet, got %d", cfg.Ethereum.ChainID))
	}
	problems = append(problems, positiveDuration("ethereum.rpc_timeout", cfg.Ethereum.RPCTimeout))
	if cfg.Ethereum.RPCConcurrency < 1 || cfg.Ethereum.RPCConcurrency > 64 {
		problems = append(problems, errors.New("ethereum.rpc_concurrency must be between 1 and 64"))
	}

	problems = append(problems, positiveDuration("indexer.poll_interval", cfg.Indexer.PollInterval))
	if cfg.Indexer.BlockWindow == 0 || cfg.Indexer.BlockWindow > domain.MaxBlockWindow {
		problems = append(problems, fmt.Errorf("indexer.block_window must be between 1 and %d", domain.MaxBlockWindow))
	}
	switch cfg.Indexer.HeadMode {
	case domain.HeadLatest, domain.HeadSafe, domain.HeadFinalized:
	default:
		problems = append(problems, errors.New("indexer.head_mode must be latest, safe, or finalized"))
	}
	if cfg.Indexer.RetryAttempts < 0 {
		problems = append(problems, errors.New("indexer.retry_attempts cannot be negative"))
	}
	problems = append(problems,
		positiveDuration("indexer.retry_min_backoff", cfg.Indexer.RetryMinBackoff),
		positiveDuration("indexer.retry_max_backoff", cfg.Indexer.RetryMaxBackoff),
	)
	if cfg.Indexer.RetryMinBackoff.Duration > cfg.Indexer.RetryMaxBackoff.Duration {
		problems = append(problems, errors.New("indexer.retry_min_backoff cannot exceed retry_max_backoff"))
	}

	problems = append(problems, validateURL("database.url", cfg.Database.URL, "postgres", "postgresql"))
	if cfg.Database.MaxConnections < 1 {
		problems = append(problems, errors.New("database.max_connections must be greater than zero"))
	}
	if cfg.Database.MinConnections < 0 || cfg.Database.MinConnections > cfg.Database.MaxConnections {
		problems = append(problems, errors.New("database.min_connections must be between zero and max_connections"))
	}
	problems = append(problems, positiveDuration("database.connect_timeout", cfg.Database.ConnectTimeout))

	return errors.Join(compact(problems)...)
}

func positiveDuration(name string, value Duration) error {
	if value.Duration <= 0 {
		return fmt.Errorf("%s must be greater than zero", name)
	}
	return nil
}

func validateURL(name, raw string, allowedSchemes ...string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s is required", name)
	}

	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s must be a valid URL", name)
	}

	for _, scheme := range allowedSchemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%s uses unsupported scheme %q", name, parsed.Scheme)
}

func compact(values []error) []error {
	result := values[:0]
	for _, value := range values {
		if value != nil {
			result = append(result, value)
		}
	}
	return result
}
