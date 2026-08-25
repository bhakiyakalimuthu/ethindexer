package indexer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/ethereum"
	"ethindexer/internal/store"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/rs/zerolog"
)

type Config struct {
	ChainID         uint64
	PollInterval    time.Duration
	BlockWindow     uint64
	HeadMode        domain.HeadMode
	RPCTimeout      time.Duration
	RPCConcurrency  int
	RetryAttempts   int
	RetryMinBackoff time.Duration
	RetryMaxBackoff time.Duration
}

type Syncer struct {
	fetcher *Fetcher
	chain   ethereum.Reader
	store   store.IndexStore
	config  Config
	logger  zerolog.Logger
	now     func() time.Time
}

func NewSyncer(fetcher *Fetcher, chain ethereum.Reader, indexStore store.IndexStore, config Config, logger zerolog.Logger) *Syncer {
	return &Syncer{
		fetcher: fetcher,
		chain:   chain,
		store:   indexStore,
		config:  config,
		logger:  logger,
		now:     time.Now,
	}
}

type SyncResult struct {
	Head             domain.ChainTip
	FromBlock        uint64
	BlockCount       int
	TransactionCount int
	EventCount       int
	SyncedAt         time.Time
}

func (s *Syncer) SyncOnce(ctx context.Context) (SyncResult, error) {
	if err := s.validateSyncOnceConfig(); err != nil {
		return SyncResult{}, err
	}

	head, err := s.selectedHead(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	headNumber, err := validHeaderNumber(head)
	if err != nil {
		return SyncResult{}, err
	}

	fromBlock := windowStart(headNumber, s.config.BlockWindow)
	bundles, err := s.fetcher.FetchRange(
		ctx,
		fromBlock,
		headNumber,
		s.config.RPCConcurrency,
	)
	if err != nil {
		return SyncResult{}, fmt.Errorf("fetch canonical window [%d,%d]: %w", fromBlock, headNumber, err)
	}

	selectedHash := head.Hash()
	fetchedHash := bundles[len(bundles)-1].Block.Hash
	if fetchedHash != selectedHash {
		return SyncResult{}, fmt.Errorf(
			"%w: block %d was %s and became %s",
			ErrHeadChanged,
			headNumber,
			selectedHash,
			fetchedHash,
		)
	}

	syncedAt := s.now().UTC()
	if err := s.store.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     s.config.ChainID,
		ReplaceFrom: fromBlock,
		RetainFrom:  fromBlock,
		Blocks:      bundles,
		SyncedAt:    syncedAt,
	}); err != nil {
		return SyncResult{}, fmt.Errorf("apply canonical window [%d,%d]: %w", fromBlock, headNumber, err)
	}

	result := SyncResult{
		Head: domain.ChainTip{
			Number: headNumber,
			Hash:   selectedHash,
		},
		FromBlock:  fromBlock,
		BlockCount: len(bundles),
		SyncedAt:   syncedAt,
	}
	for _, bundle := range bundles {
		result.TransactionCount += len(bundle.Transactions)
		result.EventCount += len(bundle.Events)
	}
	return result, nil
}

func (s *Syncer) validateSyncOnceConfig() error {
	switch {
	case s.fetcher == nil:
		return fmt.Errorf("%w: fetcher is required", ErrInvalidSyncDependency)
	case s.chain == nil:
		return fmt.Errorf("%w: chain reader is required", ErrInvalidSyncDependency)
	case s.store == nil:
		return fmt.Errorf("%w: index store is required", ErrInvalidSyncDependency)
	case s.config.ChainID == 0:
		return fmt.Errorf("%w: chain ID must be greater than zero", ErrInvalidSyncConfig)
	case s.fetcher.chainID == nil || !s.fetcher.chainID.IsUint64() || s.fetcher.chainID.Uint64() != s.config.ChainID:
		return fmt.Errorf("%w: fetcher chain ID does not match sync config", ErrInvalidSyncDependency)
	case s.config.BlockWindow == 0:
		return fmt.Errorf("%w: block window must be greater than zero", ErrInvalidSyncConfig)
	case s.config.RPCConcurrency < 1:
		return fmt.Errorf("%w: RPC concurrency must be greater than zero", ErrInvalidSyncConfig)
	}

	switch s.config.HeadMode {
	case domain.HeadLatest, domain.HeadSafe, domain.HeadFinalized:
		return nil
	default:
		return fmt.Errorf("%w: unsupported head mode %q", ErrInvalidSyncConfig, s.config.HeadMode)
	}
}

func (s *Syncer) selectedHead(ctx context.Context) (*types.Header, error) {
	var selector *big.Int
	switch s.config.HeadMode {
	case domain.HeadLatest:
		selector = nil
	case domain.HeadSafe:
		selector = big.NewInt(int64(rpc.SafeBlockNumber))
	case domain.HeadFinalized:
		selector = big.NewInt(int64(rpc.FinalizedBlockNumber))
	}

	header, err := s.chain.HeaderByNumber(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("read %s head: %w", s.config.HeadMode, err)
	}
	if header == nil {
		return nil, fmt.Errorf("%w: %s head is nil", ErrInconsistentBlockData, s.config.HeadMode)
	}
	return header, nil
}

func validHeaderNumber(header *types.Header) (uint64, error) {
	if header.Number == nil || !header.Number.IsUint64() {
		actual := "<nil>"
		if header.Number != nil {
			actual = header.Number.String()
		}
		return 0, fmt.Errorf("%w: selected head has invalid block number %s", ErrInconsistentBlockData, actual)
	}
	return header.Number.Uint64(), nil
}

func windowStart(head, blockWindow uint64) uint64 {
	if blockWindow-1 > head {
		return 0
	}
	return head - (blockWindow - 1)
}
