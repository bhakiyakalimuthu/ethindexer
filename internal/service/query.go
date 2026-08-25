package service

import (
	"context"

	"ethindexer/internal/domain"
	"ethindexer/internal/store"

	"github.com/ethereum/go-ethereum/common"
)

type Queries interface {
	Block(ctx context.Context, number uint64) (domain.BlockResult, error)
	Transaction(ctx context.Context, hash common.Hash) (domain.TransactionResult, error)
	Events(ctx context.Context, query domain.EventQuery) (domain.EventPage, error)
}

type QueryService struct {
	store store.QueryStore
}

func NewQueryService(queryStore store.QueryStore) *QueryService {
	return &QueryService{store: queryStore}
}

func (s *QueryService) Block(ctx context.Context, number uint64) (domain.BlockResult, error) {
	result, err := s.store.BlockByNumber(ctx, number)
	return result, translateStoreError(err)
}

func (s *QueryService) Transaction(ctx context.Context, hash common.Hash) (domain.TransactionResult, error) {
	result, err := s.store.TransactionByHash(ctx, hash)
	return result, translateStoreError(err)
}

func (s *QueryService) Events(ctx context.Context, query domain.EventQuery) (domain.EventPage, error) {
	result, err := s.store.EventsByAddress(ctx, query)
	return result, translateStoreError(err)
}
