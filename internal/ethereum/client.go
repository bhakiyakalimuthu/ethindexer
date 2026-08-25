package ethereum

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	geth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var ErrUnexpectedChainID = errors.New("ethereum: unexpected chain ID")

// Reader is the narrow subset of ethclient used by the polling indexer. Its
// shape makes the synchronizer testable without a live Ethereum endpoint.
type Reader interface {
	ChainID(ctx context.Context) (*big.Int, error)
	BlockNumber(ctx context.Context) (uint64, error)
	BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	LogsByBlockHash(ctx context.Context, hash common.Hash) ([]types.Log, error)
}

// rpcClient is implemented by ethclient.Client and kept private so the rest of
// the service depends on Reader instead of the complete go-ethereum client.
type rpcClient interface {
	ChainID(ctx context.Context) (*big.Int, error)
	BlockNumber(ctx context.Context) (uint64, error)
	BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	FilterLogs(ctx context.Context, query geth.FilterQuery) ([]types.Log, error)
	Close()
}

// Client applies a bounded timeout to every JSON-RPC operation.
type Client struct {
	rpc     rpcClient
	timeout time.Duration
}

func newClient(rpc rpcClient, timeout time.Duration) *Client {
	return &Client{rpc: rpc, timeout: timeout}
}

func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	return c.rpc.ChainID(callCtx)
}

func (c *Client) ValidateChainID(ctx context.Context, expected uint64) error {
	actual, err := c.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("read chain ID: %w", err)
	}
	if actual == nil || !actual.IsUint64() || actual.Uint64() != expected {
		actualValue := "<nil>"
		if actual != nil {
			actualValue = actual.String()
		}
		return fmt.Errorf("%w: expected %d, got %s", ErrUnexpectedChainID, expected, actualValue)
	}
	return nil
}

func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	return c.rpc.BlockNumber(callCtx)
}

func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	return c.rpc.BlockByNumber(callCtx, number)
}

func (c *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	return c.rpc.HeaderByNumber(callCtx, number)
}

func (c *Client) LogsByBlockHash(ctx context.Context, hash common.Hash) ([]types.Log, error) {
	callCtx, cancel := c.callContext(ctx)
	defer cancel()

	return c.rpc.FilterLogs(callCtx, geth.FilterQuery{BlockHash: &hash})
}

func (c *Client) Close() {
	c.rpc.Close()
}

func (c *Client) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

var _ Reader = (*Client)(nil)
