package ethereum

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Dial creates a go-ethereum JSON-RPC client and applies rpcTimeout to the dial
// and to every subsequent request.
func Dial(ctx context.Context, rpcURL string, rpcTimeout time.Duration) (*Client, error) {
	if strings.TrimSpace(rpcURL) == "" {
		return nil, errors.New("ethereum: RPC URL is required")
	}
	if rpcTimeout <= 0 {
		return nil, errors.New("ethereum: RPC timeout must be greater than zero")
	}

	dialCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	rpc, err := ethclient.DialContext(dialCtx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial ethereum RPC: %w", err)
	}

	return newClient(rpc, rpcTimeout), nil
}
