package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ethindexer/internal/config"
	"ethindexer/internal/ethereum"
	"ethindexer/internal/indexer"
	"ethindexer/internal/store"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	APP_NAME = "eth-indexer"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
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

	memoryStore := store.NewMemory()
	defer memoryStore.Close()

	fetcher := indexer.NewFetcher(ethClient, cfg.Ethereum.ChainID)
	syncer := indexer.NewSyncer(fetcher, ethClient, memoryStore, indexer.Config{
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
	result, err := syncer.SyncOnce(ctx)
	if err != nil {
		return fmt.Errorf("synchronize Ethereum window: %w", err)
	}

	logger.Info().
		Str("environment", cfg.App.Environment).
		Uint64("chain_id", cfg.Ethereum.ChainID).
		Uint64("from_block", result.FromBlock).
		Uint64("to_block", result.Head.Number).
		Str("head_hash", result.Head.Hash.Hex()).
		Int("blocks", result.BlockCount).
		Int("transactions", result.TransactionCount).
		Int("events", result.EventCount).
		Msg("Ethereum block window synchronized in memory")

	return nil
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
