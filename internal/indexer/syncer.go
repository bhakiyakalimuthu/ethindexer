package indexer

import (
	"time"

	"ethindexer/internal/ethereum"
	"ethindexer/internal/store"

	"github.com/rs/zerolog"
)

type Config struct {
	ChainID         uint64
	PollInterval    time.Duration
	BlockWindow     uint64
	HeadMode        string
	RPCTimeout      time.Duration
	RPCConcurrency  int
	RetryAttempts   int
	RetryMinBackoff time.Duration
	RetryMaxBackoff time.Duration
}

// Syncer is the owner of polling, bounded fetching, reorg repair, and retention.
// Its Run implementation is intentionally deferred to the synchronization step.
type Syncer struct {
	chain  ethereum.Reader
	store  store.IndexStore
	config Config
	logger zerolog.Logger
}

func NewSyncer(chain ethereum.Reader, indexStore store.IndexStore, config Config, logger zerolog.Logger) *Syncer {
	return &Syncer{
		chain:  chain,
		store:  indexStore,
		config: config,
		logger: logger,
	}
}
