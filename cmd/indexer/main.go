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
	"ethindexer/internal/domain"
	"ethindexer/internal/ethereum"
	"ethindexer/internal/indexer"
	"ethindexer/internal/store"

	"github.com/rs/zerolog"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "eth-indexer: %v\n", err)
		os.Exit(1)
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

	block, eventCount, err := verifyLatestBlock(ctx, ethClient, memoryStore, cfg.Ethereum.ChainID)
	if err != nil {
		return err
	}

	logger.Info().
		Str("environment", cfg.App.Environment).
		Uint64("chain_id", cfg.Ethereum.ChainID).
		Uint64("block_number", block.Block.Number).
		Str("block_hash", block.Block.Hash.Hex()).
		Int("transactions", len(block.TransactionHashes)).
		Int("events", eventCount).
		Msg("latest Ethereum block fetched and verified in memory")

	return nil
}

func verifyLatestBlock(
	ctx context.Context,
	chain ethereum.Reader,
	memoryStore store.Store,
	chainID uint64,
) (domain.BlockResult, int, error) {
	blockNumber, err := chain.BlockNumber(ctx)
	if err != nil {
		return domain.BlockResult{}, 0, fmt.Errorf("read latest block number: %w", err)
	}

	bundle, err := indexer.NewFetcher(chain, chainID).FetchBlock(ctx, blockNumber)
	if err != nil {
		return domain.BlockResult{}, 0, fmt.Errorf("fetch latest block bundle: %w", err)
	}
	if err := memoryStore.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     chainID,
		ReplaceFrom: blockNumber,
		RetainFrom:  blockNumber,
		Blocks:      []domain.BlockBundle{bundle},
		SyncedAt:    bundle.Block.IndexedAt,
	}); err != nil {
		return domain.BlockResult{}, 0, fmt.Errorf("store latest block bundle: %w", err)
	}

	storedBlock, err := memoryStore.BlockByNumber(ctx, blockNumber)
	if err != nil {
		return domain.BlockResult{}, 0, fmt.Errorf("read stored block %d: %w", blockNumber, err)
	}
	return storedBlock, len(bundle.Events), nil
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
