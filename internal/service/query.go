package service

import (
	"context"

	"ethindexer/internal/domain"
	"ethindexer/internal/store"

	"github.com/ethereum/go-ethereum/common"
)

type QueryService struct {
	store store.QueryStore
}

func NewQueryService(queryStore store.QueryStore) *QueryService {
	return &QueryService{store: queryStore}
}

func (s *QueryService) Block(ctx context.Context, number uint64) (domain.BlockResult, error) {
	return s.store.BlockByNumber(ctx, number)
}

func (s *QueryService) Transaction(ctx context.Context, hash common.Hash) (domain.TransactionResult, error) {
	return s.store.TransactionByHash(ctx, hash)
}

func (s *QueryService) Events(ctx context.Context, query domain.EventQuery) (domain.EventPage, error) {
	return s.store.EventsByAddress(ctx, query)
}
