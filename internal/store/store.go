package store

import (
	"context"
	"errors"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
)

var ErrNotFound = errors.New("store: not found")

// QueryStore is the read contract required by the API service.
type QueryStore interface {
	BlockByNumber(ctx context.Context, number uint64) (domain.BlockResult, error)
	TransactionByHash(ctx context.Context, hash common.Hash) (domain.TransactionResult, error)
	EventsByAddress(ctx context.Context, query domain.EventQuery) (domain.EventPage, error)
}

// IndexStore is the write and reconciliation contract required by the indexer.
type IndexStore interface {
	CanonicalTip(ctx context.Context) (*domain.ChainTip, error)
	CanonicalHash(ctx context.Context, number uint64) (common.Hash, error)
	ApplyCanonicalUpdate(ctx context.Context, update domain.CanonicalUpdate) error
}

type HealthStore interface {
	Ping(ctx context.Context) error
	Close()
}

type Store interface {
	QueryStore
	IndexStore
	HealthStore
}
