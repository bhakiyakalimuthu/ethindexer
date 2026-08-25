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

	logger.Info().
		Str("environment", cfg.App.Environment).
		Uint64("chain_id", cfg.Ethereum.ChainID).
		Msg("Ethereum RPC client connected")

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
