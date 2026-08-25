package indexer

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/store"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/rs/zerolog"
)

func TestSyncOnceFetchesAndStoresConfiguredWindow(t *testing.T) {
	blocks := canonicalTestBlocks(10, 12)
	chain := &syncTestChain{blocks: blocks, head: blocks[12].Header()}
	memory := store.NewMemory()
	syncedAt := time.Date(2026, time.August, 25, 20, 0, 0, 0, time.FixedZone("test", 2*60*60))
	syncer := NewSyncer(NewFetcher(chain, 1), chain, memory, validSyncTestConfig(), zerolog.Nop())
	syncer.now = func() time.Time { return syncedAt }

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if result.FromBlock != 10 || result.Head.Number != 12 || result.Head.Hash != blocks[12].Hash() {
		t.Fatalf("SyncOnce() result = %#v", result)
	}
	if result.BlockCount != 3 || result.TransactionCount != 0 || result.EventCount != 0 {
		t.Fatalf("SyncOnce() counts = %#v", result)
	}
	if !result.SyncedAt.Equal(syncedAt.UTC()) {
		t.Fatalf("synced at = %s, want %s", result.SyncedAt, syncedAt.UTC())
	}
	if chain.headerSelector != nil {
		t.Fatalf("latest header selector = %v, want nil", chain.headerSelector)
	}

	tip, err := memory.CanonicalTip(context.Background())
	if err != nil {
		t.Fatalf("CanonicalTip() error = %v", err)
	}
	if tip == nil || tip.Number != 12 || tip.Hash != blocks[12].Hash() {
		t.Fatalf("stored tip = %#v", tip)
	}
	if _, err := memory.BlockByNumber(context.Background(), 10); err != nil {
		t.Fatalf("first retained block lookup: %v", err)
	}
	if _, err := memory.BlockByNumber(context.Background(), 9); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outside-window block lookup = %v, want store.ErrNotFound", err)
	}
}

func TestSyncOnceClampsWindowAtGenesis(t *testing.T) {
	blocks := canonicalTestBlocks(0, 1)
	chain := &syncTestChain{blocks: blocks, head: blocks[1].Header()}
	config := validSyncTestConfig()
	config.BlockWindow = 50

	result, err := NewSyncer(NewFetcher(chain, 1), chain, store.NewMemory(), config, zerolog.Nop()).SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if result.FromBlock != 0 || result.BlockCount != 2 {
		t.Fatalf("SyncOnce() result = %#v", result)
	}
}

func TestSyncOnceUsesConfiguredHeadTag(t *testing.T) {
	tests := []struct {
		mode     domain.HeadMode
		selector *big.Int
	}{
		{mode: domain.HeadLatest, selector: nil},
		{mode: domain.HeadSafe, selector: big.NewInt(int64(rpc.SafeBlockNumber))},
		{mode: domain.HeadFinalized, selector: big.NewInt(int64(rpc.FinalizedBlockNumber))},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			blocks := canonicalTestBlocks(7, 7)
			chain := &syncTestChain{blocks: blocks, head: blocks[7].Header()}
			config := validSyncTestConfig()
			config.BlockWindow = 1
			config.HeadMode = test.mode

			if _, err := NewSyncer(NewFetcher(chain, 1), chain, store.NewMemory(), config, zerolog.Nop()).SyncOnce(context.Background()); err != nil {
				t.Fatalf("SyncOnce() error = %v", err)
			}
			if !equalBigInt(chain.headerSelector, test.selector) {
				t.Fatalf("header selector = %v, want %v", chain.headerSelector, test.selector)
			}
		})
	}
}

func TestSyncOnceRejectsHeadChangeWithoutUpdatingStore(t *testing.T) {
	selected := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(20), Time: 1})
	fetched := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(20), Time: 2})
	chain := &syncTestChain{
		blocks: map[uint64]*types.Block{20: fetched},
		head:   selected.Header(),
	}
	config := validSyncTestConfig()
	config.BlockWindow = 1
	memory := store.NewMemory()

	result, err := NewSyncer(NewFetcher(chain, 1), chain, memory, config, zerolog.Nop()).SyncOnce(context.Background())
	if !errors.Is(err, ErrHeadChanged) {
		t.Fatalf("SyncOnce() error = %v, want ErrHeadChanged", err)
	}
	if result != (SyncResult{}) {
		t.Fatalf("SyncOnce() result = %#v, want zero value", result)
	}
	tip, tipErr := memory.CanonicalTip(context.Background())
	if tipErr != nil || tip != nil {
		t.Fatalf("tip after rejected sync = %#v, error = %v", tip, tipErr)
	}
}

func TestSyncOnceRejectsInvalidConfigBeforeRPC(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{name: "chain ID", change: func(config *Config) { config.ChainID = 0 }},
		{name: "block window", change: func(config *Config) { config.BlockWindow = 0 }},
		{name: "RPC concurrency", change: func(config *Config) { config.RPCConcurrency = 0 }},
		{name: "head mode", change: func(config *Config) { config.HeadMode = domain.HeadMode("pending") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validSyncTestConfig()
			test.change(&config)
			chain := &syncTestChain{}

			_, err := NewSyncer(NewFetcher(chain, 1), chain, store.NewMemory(), config, zerolog.Nop()).SyncOnce(context.Background())
			if !errors.Is(err, ErrInvalidSyncConfig) {
				t.Fatalf("SyncOnce() error = %v, want ErrInvalidSyncConfig", err)
			}
			if chain.headerCalls != 0 {
				t.Fatalf("header calls = %d, want zero", chain.headerCalls)
			}
		})
	}
}

func TestSyncOnceRejectsInvalidDependenciesBeforeRPC(t *testing.T) {
	tests := []struct {
		name      string
		newSyncer func(*syncTestChain) *Syncer
	}{
		{
			name: "missing fetcher",
			newSyncer: func(chain *syncTestChain) *Syncer {
				return NewSyncer(nil, chain, store.NewMemory(), validSyncTestConfig(), zerolog.Nop())
			},
		},
		{
			name: "missing chain reader",
			newSyncer: func(chain *syncTestChain) *Syncer {
				return NewSyncer(NewFetcher(chain, 1), nil, store.NewMemory(), validSyncTestConfig(), zerolog.Nop())
			},
		},
		{
			name: "missing index store",
			newSyncer: func(chain *syncTestChain) *Syncer {
				return NewSyncer(NewFetcher(chain, 1), chain, nil, validSyncTestConfig(), zerolog.Nop())
			},
		},
		{
			name: "fetcher chain ID mismatch",
			newSyncer: func(chain *syncTestChain) *Syncer {
				return NewSyncer(NewFetcher(chain, 5), chain, store.NewMemory(), validSyncTestConfig(), zerolog.Nop())
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chain := &syncTestChain{}
			_, err := test.newSyncer(chain).SyncOnce(context.Background())
			if !errors.Is(err, ErrInvalidSyncDependency) {
				t.Fatalf("SyncOnce() error = %v, want ErrInvalidSyncDependency", err)
			}
			if chain.headerCalls != 0 {
				t.Fatalf("header calls = %d, want zero", chain.headerCalls)
			}
		})
	}
}

func validSyncTestConfig() Config {
	return Config{
		ChainID:        1,
		BlockWindow:    3,
		HeadMode:       domain.HeadLatest,
		RPCConcurrency: 2,
	}
}

type syncTestChain struct {
	blocks         map[uint64]*types.Block
	head           *types.Header
	headerSelector *big.Int
	headerCalls    int
}

func (c *syncTestChain) ChainID(context.Context) (*big.Int, error) {
	return nil, errors.New("unexpected ChainID call")
}

func (c *syncTestChain) BlockNumber(context.Context) (uint64, error) {
	return 0, errors.New("unexpected BlockNumber call")
}

func (c *syncTestChain) BlockByNumber(_ context.Context, number *big.Int) (*types.Block, error) {
	block, found := c.blocks[number.Uint64()]
	if !found {
		return nil, errors.New("unexpected block number")
	}
	return block, nil
}

func (c *syncTestChain) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	c.headerCalls++
	if number == nil {
		c.headerSelector = nil
	} else {
		c.headerSelector = new(big.Int).Set(number)
	}
	if c.head == nil {
		return nil, errors.New("head is not configured")
	}
	return c.head, nil
}

func (c *syncTestChain) LogsByBlockHash(_ context.Context, hash common.Hash) ([]types.Log, error) {
	for _, block := range c.blocks {
		if block.Hash() == hash {
			return nil, nil
		}
	}
	return nil, errors.New("unexpected block hash")
}

func equalBigInt(left, right *big.Int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Cmp(right) == 0
}
