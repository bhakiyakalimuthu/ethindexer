package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	stdlog "log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"ethindexer/internal/config"
	"ethindexer/internal/ethereum"
	"ethindexer/internal/indexer"
	"ethindexer/internal/server"
	"ethindexer/internal/service"
	"ethindexer/internal/store"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	APP_NAME = "eth-indexer"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		log.Fatal().Err(err).Msg("eth-indexer failed to start")
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("eth-indexer", flag.ContinueOnError)
	configPath := flags.String("config", configPathFromEnvironment(), "path to the YAML configuration file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger, err := NewLogger(cfg.App.Environment, cfg.App.LogLevel)
	if err != nil {
		return fmt.Errorf("configure logger: %w", err)
	}
	postgresStore, err := store.OpenPostgres(ctx, store.PostgresOptions{
		URL:            cfg.Database.URL,
		MaxConnections: cfg.Database.MaxConnections,
		MinConnections: cfg.Database.MinConnections,
		ConnectTimeout: cfg.Database.ConnectTimeout.Duration,
	})
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer postgresStore.Close()

	ethClient, err := ethereum.Dial(
		ctx,
		cfg.Ethereum.RPCURL,
		cfg.Ethereum.RPCTimeout.Duration,
	)
	if err != nil {
		return fmt.Errorf("connect to Ethereum RPC: %w", err)
	}
	defer ethClient.Close()

	if err := ethClient.ValidateChainID(ctx, cfg.Ethereum.ChainID); err != nil {
		return fmt.Errorf("validate Ethereum network: %w", err)
	}

	fetcher := indexer.NewFetcher(ethClient, cfg.Ethereum.ChainID)
	syncer := indexer.NewSyncer(fetcher, ethClient, postgresStore, indexer.Config{
		ChainID:         cfg.Ethereum.ChainID,
		PollInterval:    cfg.Indexer.PollInterval.Duration,
		BlockWindow:     cfg.Indexer.BlockWindow,
		HeadMode:        cfg.Indexer.HeadMode,
		RPCTimeout:      cfg.Ethereum.RPCTimeout.Duration,
		RPCConcurrency:  cfg.Ethereum.RPCConcurrency,
		RetryAttempts:   cfg.Indexer.RetryAttempts,
		RetryMinBackoff: cfg.Indexer.RetryMinBackoff.Duration,
		RetryMaxBackoff: cfg.Indexer.RetryMaxBackoff.Duration,
	}, logger)

	queries := service.NewQueryService(postgresStore)
	health := service.NewHealthService(postgresStore)
	handler := server.NewServer(server.Server{
		QueryService:   queries,
		HealthService:  health,
		Logger:         logger,
		RequestTimeout: cfg.HTTP.RequestTimeout.Duration,
	})
	httpLogger := logger.With().Str("component", "http_server").Logger()
	httpServer := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout.Duration,
		IdleTimeout:       cfg.HTTP.IdleTimeout.Duration,
		ErrorLog:          stdlog.New(httpLogger, "", 0),
	}

	logger.Info().Str("address", cfg.HTTP.Address).Msg("starting HTTP server")
	return runServices(ctx, syncer, httpServer, cfg.HTTP.ShutdownTimeout.Duration)
}

func configPathFromEnvironment() string {
	if path := os.Getenv("CONFIG_FILE"); path != "" {
		return path
	}
	return "configs/config.example.yaml"
}

func NewLogger(environment, level string) (zerolog.Logger, error) {
	parsedLevel, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil {
		return zerolog.Logger{}, fmt.Errorf("parse log level: %w", err)
	}

	var output io.Writer = os.Stdout
	if environment == "development" {
		output = zerolog.ConsoleWriter{Out: os.Stderr}
	}

	return zerolog.New(output).
		Level(parsedLevel).
		With().
		Timestamp().
		Str("service", "eth-indexer").
		Logger(), nil
}
