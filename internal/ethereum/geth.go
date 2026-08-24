package ethereum

import (
	"context"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Dial creates the concrete go-ethereum client. RPC timeouts are applied by
// callers to individual operations so cancellation reaches every request.
func Dial(ctx context.Context, rpcURL string) (Client, error) {
	return ethclient.DialContext(ctx, rpcURL)
}
