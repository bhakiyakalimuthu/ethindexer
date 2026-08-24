package ethereum

import (
	"context"
	"math/big"

	geth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// Reader is the narrow subset of ethclient used by the polling indexer. Its
// shape makes the synchronizer testable without a live Ethereum endpoint.
type Reader interface {
	ChainID(ctx context.Context) (*big.Int, error)
	BlockNumber(ctx context.Context) (uint64, error)
	BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	FilterLogs(ctx context.Context, query geth.FilterQuery) ([]types.Log, error)
}

type Client interface {
	Reader
	Close()
}
