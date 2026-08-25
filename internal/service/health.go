package service

import (
	"context"
	"fmt"

	"ethindexer/internal/domain"
)

// Health exposes the operational readiness use case to the HTTP layer.
type Health interface {
	Ready(ctx context.Context) (domain.ChainTip, error)
}

type readinessStore interface {
	Ping(ctx context.Context) error
	CanonicalTip(ctx context.Context) (*domain.ChainTip, error)
}

type HealthService struct {
	store readinessStore
}

func NewHealthService(store readinessStore) *HealthService {
	return &HealthService{store: store}
}

func (s *HealthService) Ready(ctx context.Context) (domain.ChainTip, error) {
	if err := s.store.Ping(ctx); err != nil {
		return domain.ChainTip{}, fmt.Errorf("check PostgreSQL connection: %w", err)
	}

	tip, err := s.store.CanonicalTip(ctx)
	if err != nil {
		return domain.ChainTip{}, fmt.Errorf("read canonical tip: %w", err)
	}
	if tip == nil {
		return domain.ChainTip{}, ErrNotReady
	}
	return *tip, nil
}
