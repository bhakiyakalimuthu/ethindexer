package ethereum

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	geth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestLogsByBlockHashUsesExactBlockFilter(t *testing.T) {
	expectedHash := common.HexToHash("0x1234")
	expectedLogs := []types.Log{{BlockHash: expectedHash}}

	transport := &fakeRPCClient{
		filterLogs: func(_ context.Context, query geth.FilterQuery) ([]types.Log, error) {
			if query.BlockHash == nil || *query.BlockHash != expectedHash {
				t.Fatalf("BlockHash = %v, want %s", query.BlockHash, expectedHash)
			}
			if query.FromBlock != nil || query.ToBlock != nil {
				t.Fatal("block-number range must be empty when BlockHash is used")
			}
			if len(query.Addresses) != 0 || len(query.Topics) != 0 {
				t.Fatal("address and topic filters must be empty")
			}
			return expectedLogs, nil
		},
	}

	client := newClient(transport, time.Second)
	logs, err := client.LogsByBlockHash(context.Background(), expectedHash)
	if err != nil {
		t.Fatalf("LogsByBlockHash() error = %v", err)
	}
	if len(logs) != 1 || logs[0].BlockHash != expectedHash {
		t.Fatalf("LogsByBlockHash() = %#v", logs)
	}
}

func TestClientAppliesTimeoutToRPCOperations(t *testing.T) {
	transport := &fakeRPCClient{
		blockNumber: func(ctx context.Context) (uint64, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	client := newClient(transport, 10*time.Millisecond)
	started := time.Now()
	_, err := client.BlockNumber(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("BlockNumber() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("BlockNumber() respected timeout too slowly: %s", elapsed)
	}
}

func TestValidateChainID(t *testing.T) {
	tests := []struct {
		name      string
		actual    *big.Int
		wantError bool
	}{
		{name: "mainnet", actual: big.NewInt(1)},
		{name: "different chain", actual: big.NewInt(11155111), wantError: true},
		{name: "missing chain ID", actual: nil, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &fakeRPCClient{
				chainID: func(context.Context) (*big.Int, error) {
					return test.actual, nil
				},
			}
			client := newClient(transport, time.Second)

			err := client.ValidateChainID(context.Background(), 1)
			if test.wantError && !errors.Is(err, ErrUnexpectedChainID) {
				t.Fatalf("ValidateChainID() error = %v, want ErrUnexpectedChainID", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("ValidateChainID() error = %v", err)
			}
		})
	}
}

func TestDialValidatesArguments(t *testing.T) {
	if _, err := Dial(context.Background(), "", time.Second); err == nil {
		t.Fatal("Dial() accepted an empty RPC URL")
	}
	if _, err := Dial(context.Background(), "http://localhost:8545", 0); err == nil {
		t.Fatal("Dial() accepted a non-positive timeout")
	}
}

type fakeRPCClient struct {
	chainID        func(context.Context) (*big.Int, error)
	blockNumber    func(context.Context) (uint64, error)
	blockByNumber  func(context.Context, *big.Int) (*types.Block, error)
	headerByNumber func(context.Context, *big.Int) (*types.Header, error)
	filterLogs     func(context.Context, geth.FilterQuery) ([]types.Log, error)
	close          func()
}

func (f *fakeRPCClient) ChainID(ctx context.Context) (*big.Int, error) {
	if f.chainID == nil {
		return nil, errors.New("unexpected ChainID call")
	}
	return f.chainID(ctx)
}

func (f *fakeRPCClient) BlockNumber(ctx context.Context) (uint64, error) {
	if f.blockNumber == nil {
		return 0, errors.New("unexpected BlockNumber call")
	}
	return f.blockNumber(ctx)
}

func (f *fakeRPCClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if f.blockByNumber == nil {
		return nil, errors.New("unexpected BlockByNumber call")
	}
	return f.blockByNumber(ctx, number)
}

func (f *fakeRPCClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if f.headerByNumber == nil {
		return nil, errors.New("unexpected HeaderByNumber call")
	}
	return f.headerByNumber(ctx, number)
}

func (f *fakeRPCClient) FilterLogs(ctx context.Context, query geth.FilterQuery) ([]types.Log, error) {
	if f.filterLogs == nil {
		return nil, errors.New("unexpected FilterLogs call")
	}
	return f.filterLogs(ctx, query)
}

func (f *fakeRPCClient) Close() {
	if f.close != nil {
		f.close()
	}
}
