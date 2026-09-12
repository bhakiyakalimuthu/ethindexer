package indexer

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"sync"
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
	if result.ReplaceFrom != 10 {
		t.Fatalf("replace from = %d, want 10", result.ReplaceFrom)
	}
	if result.Mode != SyncModeInitialLoad || result.StoredTip != nil || result.Reorg != nil {
		t.Fatalf("initial synchronization metadata = %#v", result)
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

func TestSyncOnceAppendsOnlyNewBlocksAndPrunesRetention(t *testing.T) {
	initialBlocks := canonicalTestBlocks(9, 12)
	chain := &syncTestChain{blocks: initialBlocks, head: initialBlocks[12].Header()}
	memory := store.NewMemory()
	config := validSyncTestConfig()
	config.BlockWindow = 4
	syncer := NewSyncer(NewFetcher(chain, 1), chain, memory, config, zerolog.Nop())

	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial SyncOnce() error = %v", err)
	}
	chain.resetFetchedBlockNumbers()
	extendedBlocks := canonicalTestBlocks(9, 14)
	chain.setCanonicalChain(extendedBlocks, 14)

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("append SyncOnce() error = %v", err)
	}
	if result.FromBlock != 11 || result.ReplaceFrom != 13 || result.BlockCount != 2 {
		t.Fatalf("append result = %#v", result)
	}
	if result.Mode != SyncModeAppend || result.StoredTip == nil || result.StoredTip.Number != 12 || result.StoredTip.Hash != initialBlocks[12].Hash() || result.Reorg != nil {
		t.Fatalf("append synchronization metadata = %#v", result)
	}
	if got, want := chain.fetchedBlockNumbers(), []uint64{13, 14}; !slices.Equal(got, want) {
		t.Fatalf("fetched blocks = %v, want %v", got, want)
	}
	if _, err := memory.BlockByNumber(context.Background(), 10); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pruned block lookup = %v, want store.ErrNotFound", err)
	}
	if _, err := memory.BlockByNumber(context.Background(), 11); err != nil {
		t.Fatalf("retained block lookup: %v", err)
	}
	tip, err := memory.CanonicalTip(context.Background())
	if err != nil || tip == nil || tip.Number != 14 || tip.Hash != extendedBlocks[14].Hash() {
		t.Fatalf("tip after append = %#v, error = %v", tip, err)
	}
}

func TestSyncOnceSkipsBlockFetchWhenAlreadyCurrent(t *testing.T) {
	blocks := canonicalTestBlocks(10, 12)
	chain := &syncTestChain{blocks: blocks, head: blocks[12].Header()}
	memory := store.NewMemory()
	syncer := NewSyncer(NewFetcher(chain, 1), chain, memory, validSyncTestConfig(), zerolog.Nop())

	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial SyncOnce() error = %v", err)
	}
	chain.resetFetchedBlockNumbers()

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("current SyncOnce() error = %v", err)
	}
	if result.FromBlock != 10 || result.ReplaceFrom != 13 || result.BlockCount != 0 {
		t.Fatalf("current result = %#v", result)
	}
	if result.Mode != SyncModeCurrent || result.StoredTip == nil || result.StoredTip.Number != 12 || result.Reorg != nil {
		t.Fatalf("current synchronization metadata = %#v", result)
	}
	if got := chain.fetchedBlockNumbers(); len(got) != 0 {
		t.Fatalf("fetched blocks = %v, want none", got)
	}
}

func TestSyncOnceReplacesBlocksAfterCommonAncestor(t *testing.T) {
	oldBlocks := canonicalTestBlocks(9, 12)
	chain := &syncTestChain{blocks: oldBlocks, head: oldBlocks[12].Header()}
	memory := store.NewMemory()
	config := validSyncTestConfig()
	config.BlockWindow = 4
	syncer := NewSyncer(NewFetcher(chain, 1), chain, memory, config, zerolog.Nop())

	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial SyncOnce() error = %v", err)
	}
	chain.resetFetchedBlockNumbers()
	newBlocks := forkedTestBlocks(oldBlocks, 10, 13)
	chain.setCanonicalChain(newBlocks, 13)

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("reorg SyncOnce() error = %v", err)
	}
	if result.FromBlock != 10 || result.ReplaceFrom != 11 || result.BlockCount != 3 {
		t.Fatalf("reorg result = %#v", result)
	}
	if result.Mode != SyncModeReorg || result.StoredTip == nil || result.StoredTip.Number != 12 || result.StoredTip.Hash != oldBlocks[12].Hash() {
		t.Fatalf("reorg synchronization metadata = %#v", result)
	}
	if result.Reorg == nil || result.Reorg.CommonAncestor == nil ||
		result.Reorg.CommonAncestor.Number != 10 || result.Reorg.CommonAncestor.Hash != oldBlocks[10].Hash() ||
		result.Reorg.ReplacedFrom != 11 || result.Reorg.ReplacedTo != 12 || result.Reorg.ReplacedBlockCount != 2 {
		t.Fatalf("reorg details = %#v", result.Reorg)
	}
	if got, want := chain.fetchedBlockNumbers(), []uint64{11, 12, 13}; !slices.Equal(got, want) {
		t.Fatalf("fetched blocks = %v, want %v", got, want)
	}
	retained, err := memory.BlockByNumber(context.Background(), 10)
	if err != nil || retained.Block.Hash != oldBlocks[10].Hash() {
		t.Fatalf("common ancestor = %#v, error = %v", retained.Block, err)
	}
	replaced, err := memory.BlockByNumber(context.Background(), 11)
	if err != nil || replaced.Block.Hash != newBlocks[11].Hash() || replaced.Block.Hash == oldBlocks[11].Hash() {
		t.Fatalf("replacement block = %#v, error = %v", replaced.Block, err)
	}
	if _, err := memory.BlockByNumber(context.Background(), 9); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pruned pre-window block lookup = %v, want store.ErrNotFound", err)
	}
}

func TestSyncOnceReportsReorgWithoutAncestorInRetainedWindow(t *testing.T) {
	oldBlocks := canonicalTestBlocks(8, 12)
	chain := &syncTestChain{blocks: oldBlocks, head: oldBlocks[12].Header()}
	memory := store.NewMemory()
	config := validSyncTestConfig()
	config.BlockWindow = 4
	syncer := NewSyncer(NewFetcher(chain, 1), chain, memory, config, zerolog.Nop())

	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial SyncOnce() error = %v", err)
	}
	newBlocks := forkedTestBlocks(oldBlocks, 8, 13)
	chain.setCanonicalChain(newBlocks, 13)

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("deep reorg SyncOnce() error = %v", err)
	}
	if result.Mode != SyncModeReorg || result.FromBlock != 10 || result.ReplaceFrom != 10 || result.BlockCount != 4 {
		t.Fatalf("deep reorg result = %#v", result)
	}
	if result.Reorg == nil || result.Reorg.CommonAncestor != nil ||
		result.Reorg.ReplacedFrom != 10 || result.Reorg.ReplacedTo != 12 || result.Reorg.ReplacedBlockCount != 3 {
		t.Fatalf("deep reorg details = %#v", result.Reorg)
	}
}

func TestSyncOnceRefetchesFullWindowWithoutStoredOverlap(t *testing.T) {
	oldBlocks := canonicalTestBlocks(1, 3)
	chain := &syncTestChain{blocks: oldBlocks, head: oldBlocks[3].Header()}
	memory := store.NewMemory()
	syncer := NewSyncer(NewFetcher(chain, 1), chain, memory, validSyncTestConfig(), zerolog.Nop())

	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial SyncOnce() error = %v", err)
	}
	chain.resetFetchedBlockNumbers()
	currentBlocks := canonicalTestBlocks(8, 10)
	chain.setCanonicalChain(currentBlocks, 10)

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("non-overlap SyncOnce() error = %v", err)
	}
	if result.FromBlock != 8 || result.ReplaceFrom != 8 || result.BlockCount != 3 {
		t.Fatalf("non-overlap result = %#v", result)
	}
	if result.Mode != SyncModeFullReload || result.StoredTip == nil || result.StoredTip.Number != 3 || result.Reorg != nil {
		t.Fatalf("full reload synchronization metadata = %#v", result)
	}
	if got, want := chain.fetchedBlockNumbers(), []uint64{8, 9, 10}; !slices.Equal(got, want) {
		t.Fatalf("fetched blocks = %v, want %v", got, want)
	}
	if _, err := memory.BlockByNumber(context.Background(), 3); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale block lookup = %v, want store.ErrNotFound", err)
	}
	tip, err := memory.CanonicalTip(context.Background())
	if err != nil || tip == nil || tip.Number != 10 || tip.Hash != currentBlocks[10].Hash() {
		t.Fatalf("tip after full refetch = %#v, error = %v", tip, err)
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
		{name: "block window above V1 limit", change: func(config *Config) { config.BlockWindow = domain.MaxBlockWindow + 1 }},
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
	mu             sync.Mutex
	blocks         map[uint64]*types.Block
	head           *types.Header
	headerSelector *big.Int
	headerCalls    int
	blockNumbers   []uint64
}

func (c *syncTestChain) ChainID(context.Context) (*big.Int, error) {
	return nil, errors.New("unexpected ChainID call")
}

func (c *syncTestChain) BlockNumber(context.Context) (uint64, error) {
	return 0, errors.New("unexpected BlockNumber call")
}

func (c *syncTestChain) BlockByNumber(_ context.Context, number *big.Int) (*types.Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.blockNumbers = append(c.blockNumbers, number.Uint64())
	block, found := c.blocks[number.Uint64()]
	if !found {
		return nil, errors.New("unexpected block number")
	}
	return block, nil
}

func (c *syncTestChain) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.headerCalls++
	if number == nil {
		c.headerSelector = nil
	} else {
		c.headerSelector = new(big.Int).Set(number)
	}
	if number == nil || number.Sign() < 0 {
		if c.head == nil {
			return nil, errors.New("head is not configured")
		}
		return c.head, nil
	}
	if !number.IsUint64() {
		return nil, errors.New("invalid explicit header number")
	}
	block, found := c.blocks[number.Uint64()]
	if !found {
		return nil, errors.New("header is not configured")
	}
	return block.Header(), nil
}

func (c *syncTestChain) LogsByBlockHash(_ context.Context, hash common.Hash) ([]types.Log, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, block := range c.blocks {
		if block.Hash() == hash {
			return nil, nil
		}
	}
	return nil, errors.New("unexpected block hash")
}

func (c *syncTestChain) setCanonicalChain(blocks map[uint64]*types.Block, head uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.blocks = blocks
	block, found := blocks[head]
	if !found {
		panic("test canonical head is missing")
	}
	c.head = block.Header()
}

func (c *syncTestChain) resetFetchedBlockNumbers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.blockNumbers = nil
}

func (c *syncTestChain) fetchedBlockNumbers() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	numbers := append([]uint64(nil), c.blockNumbers...)
	slices.Sort(numbers)
	return numbers
}

func forkedTestBlocks(shared map[uint64]*types.Block, ancestor, head uint64) map[uint64]*types.Block {
	blocks := make(map[uint64]*types.Block, head+1)
	for number, block := range shared {
		if number <= ancestor {
			blocks[number] = block
		}
	}

	parentHash := shared[ancestor].Hash()
	for number := ancestor + 1; number <= head; number++ {
		block := types.NewBlockWithHeader(&types.Header{
			Number:     new(big.Int).SetUint64(number),
			ParentHash: parentHash,
			Time:       number + 1_000,
		})
		blocks[number] = block
		parentHash = block.Hash()
	}
	return blocks
}

func equalBigInt(left, right *big.Int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Cmp(right) == 0
}
