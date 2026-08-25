package indexer

import (
	"context"
	"errors"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestFetchRangeBoundsConcurrencyAndPreservesOrder(t *testing.T) {
	const (
		from        = uint64(100)
		to          = uint64(104)
		concurrency = int32(2)
	)
	blocks := canonicalTestBlocks(from, to)

	var active atomic.Int32
	var maximum atomic.Int32
	var calls atomic.Int32
	reachedConcurrency := make(chan struct{})
	release := make(chan struct{})
	var reachedOnce sync.Once

	chain := &fakeChainReader{
		blockByNumber: func(ctx context.Context, number *big.Int) (*types.Block, error) {
			calls.Add(1)
			current := active.Add(1)
			defer active.Add(-1)
			updateTestMaximum(&maximum, current)
			if current == concurrency {
				reachedOnce.Do(func() { close(reachedConcurrency) })
			}

			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			block, found := blocks[number.Uint64()]
			if !found {
				return nil, errors.New("unexpected block number")
			}
			return block, nil
		},
		logsByBlockHash: func(context.Context, common.Hash) ([]types.Log, error) {
			return nil, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		bundles []uint64
		err     error
	}
	resultChannel := make(chan result, 1)
	go func() {
		bundles, err := NewFetcher(chain, 1).FetchRange(ctx, from, to, int(concurrency))
		numbers := make([]uint64, len(bundles))
		for i, bundle := range bundles {
			numbers[i] = bundle.Block.Number
		}
		resultChannel <- result{bundles: numbers, err: err}
	}()

	select {
	case <-reachedConcurrency:
	case <-ctx.Done():
		t.Fatalf("range fetch did not reach configured concurrency: %v", ctx.Err())
	}
	if got := maximum.Load(); got != concurrency {
		t.Fatalf("maximum concurrent fetches = %d, want %d", got, concurrency)
	}
	close(release)

	var resultValue result
	select {
	case resultValue = <-resultChannel:
	case <-ctx.Done():
		t.Fatalf("FetchRange() did not complete: %v", ctx.Err())
	}
	if resultValue.err != nil {
		t.Fatalf("FetchRange() error = %v", resultValue.err)
	}
	if got, want := calls.Load(), int32(to-from+1); got != want {
		t.Fatalf("block calls = %d, want %d", got, want)
	}
	for i, number := range resultValue.bundles {
		want := from + uint64(i)
		if number != want {
			t.Fatalf("result[%d] = %d, want %d", i, number, want)
		}
	}
}

func TestFetchRangeDefaultsInvalidConcurrencyToOne(t *testing.T) {
	tests := []struct {
		name        string
		concurrency int
	}{
		{name: "zero", concurrency: 0},
		{name: "negative", concurrency: -2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			blocks := canonicalTestBlocks(1, 2)
			var active atomic.Int32
			var maximum atomic.Int32
			chain := &fakeChainReader{
				blockByNumber: func(_ context.Context, number *big.Int) (*types.Block, error) {
					current := active.Add(1)
					defer active.Add(-1)
					updateTestMaximum(&maximum, current)
					time.Sleep(time.Millisecond)
					return blocks[number.Uint64()], nil
				},
				logsByBlockHash: func(context.Context, common.Hash) ([]types.Log, error) {
					return nil, nil
				},
			}

			bundles, err := NewFetcher(chain, 1).FetchRange(context.Background(), 1, 2, test.concurrency)
			if err != nil {
				t.Fatalf("FetchRange() error = %v", err)
			}
			if len(bundles) != 2 || maximum.Load() != 1 {
				t.Fatalf("bundles = %d, maximum concurrency = %d", len(bundles), maximum.Load())
			}
		})
	}
}

func TestFetchRangeRejectsInvalidRangesBeforeRPC(t *testing.T) {
	var calls atomic.Int32
	chain := &fakeChainReader{
		blockByNumber: func(context.Context, *big.Int) (*types.Block, error) {
			calls.Add(1)
			return nil, errors.New("unexpected RPC call")
		},
	}
	fetcher := NewFetcher(chain, 1)

	tests := []struct {
		name string
		from uint64
		to   uint64
		want error
	}{
		{name: "reversed", from: 2, to: 1, want: ErrInvalidLowFetchRange},
		{name: "too large", from: 0, to: uint64(math.MaxInt), want: ErrInvalidLargeFetchRange},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundles, err := fetcher.FetchRange(context.Background(), test.from, test.to, 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("FetchRange() error = %v, want %v", err, test.want)
			}
			if bundles != nil {
				t.Fatalf("FetchRange() bundles = %#v, want nil", bundles)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("block calls = %d, want zero", calls.Load())
	}
}

func TestFetchRangeCancelsPeersAndReturnsNoPartialResult(t *testing.T) {
	expectedError := errors.New("RPC failed")
	peerStarted := make(chan struct{})
	peerCanceled := make(chan struct{})
	var peerStartedOnce sync.Once
	var peerCanceledOnce sync.Once

	chain := &fakeChainReader{
		blockByNumber: func(ctx context.Context, number *big.Int) (*types.Block, error) {
			switch number.Uint64() {
			case 1:
				select {
				case <-peerStarted:
					return nil, expectedError
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case 2:
				peerStartedOnce.Do(func() { close(peerStarted) })
				<-ctx.Done()
				peerCanceledOnce.Do(func() { close(peerCanceled) })
				return nil, ctx.Err()
			default:
				return nil, errors.New("unexpected additional block fetch")
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	bundles, err := NewFetcher(chain, 1).FetchRange(ctx, 1, 4, 2)
	if !errors.Is(err, expectedError) {
		t.Fatalf("FetchRange() error = %v, want %v", err, expectedError)
	}
	if bundles != nil {
		t.Fatalf("FetchRange() returned partial bundles: %#v", bundles)
	}
	select {
	case <-peerCanceled:
	case <-ctx.Done():
		t.Fatalf("peer fetch was not canceled: %v", ctx.Err())
	}
}

func TestFetchRangeReturnsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	chain := &fakeChainReader{
		blockByNumber: func(ctx context.Context, _ *big.Int) (*types.Block, error) {
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := NewFetcher(chain, 1).FetchRange(ctx, 1, 3, 2)
		result <- err
	}()
	<-started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FetchRange() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchRange() did not return after cancellation")
	}
}

func TestFetchRangeRejectsNonCanonicalParents(t *testing.T) {
	blocks := canonicalTestBlocks(10, 11)
	blocks[11] = types.NewBlockWithHeader(&types.Header{
		Number:     big.NewInt(11),
		ParentHash: common.HexToHash("0xdead"),
	})
	chain := &fakeChainReader{
		blockByNumber: func(_ context.Context, number *big.Int) (*types.Block, error) {
			return blocks[number.Uint64()], nil
		},
		logsByBlockHash: func(context.Context, common.Hash) ([]types.Log, error) {
			return nil, nil
		},
	}

	bundles, err := NewFetcher(chain, 1).FetchRange(context.Background(), 10, 11, 2)
	if !errors.Is(err, ErrInconsistentBlockData) {
		t.Fatalf("FetchRange() error = %v, want ErrInconsistentBlockData", err)
	}
	if bundles != nil {
		t.Fatalf("FetchRange() returned invalid bundles: %#v", bundles)
	}
}

func canonicalTestBlocks(from, to uint64) map[uint64]*types.Block {
	blocks := make(map[uint64]*types.Block, to-from+1)
	parentHash := common.HexToHash("0x01")
	for number := from; ; number++ {
		block := types.NewBlockWithHeader(&types.Header{
			Number:     new(big.Int).SetUint64(number),
			ParentHash: parentHash,
		})
		blocks[number] = block
		parentHash = block.Hash()
		if number == to {
			return blocks
		}
	}
}

func updateTestMaximum(maximum *atomic.Int32, candidate int32) {
	for {
		current := maximum.Load()
		if candidate <= current || maximum.CompareAndSwap(current, candidate) {
			return
		}
	}
}
