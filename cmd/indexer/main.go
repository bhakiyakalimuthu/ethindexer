package main

import (
	"flag"
	"fmt"
	"os"

	"ethindexer/internal/config"
	"ethindexer/internal/logging"
)

func main() {
	configPath := flag.String("config", configPathFromEnvironment(), "path to the YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load configuration: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.App.Environment, cfg.App.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure logger: %v\n", err)
		os.Exit(1)
	}

	logger.Info().
		Str("environment", cfg.App.Environment).
		Uint64("chain_id", cfg.Ethereum.ChainID).
		Msg("configuration loaded; runtime wiring follows in the next implementation step")
}

func configPathFromEnvironment() string {
	if path := os.Getenv("CONFIG_FILE"); path != "" {
		return path
	}
	return "configs/config.example.yaml"
}
