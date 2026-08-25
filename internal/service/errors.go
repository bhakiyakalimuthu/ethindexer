package service

import (
	"errors"
	"fmt"

	"ethindexer/internal/store"
)

var (
	ErrNotFound     = errors.New("service: not found")
	ErrInvalidQuery = errors.New("service: invalid query")
	ErrStaleCursor  = errors.New("service: stale cursor")
)

func translateStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.Is(err, store.ErrInvalidQuery):
		return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	case errors.Is(err, store.ErrStaleCursor):
		return fmt.Errorf("%w: %w", ErrStaleCursor, err)
	default:
		return err
	}
}
