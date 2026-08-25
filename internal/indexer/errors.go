package indexer

import "errors"

var (
	ErrInconsistentBlockData  = errors.New("indexer: inconsistent block data")
	ErrInvalidLowFetchRange   = errors.New("indexer: invalid low fetch range")
	ErrInvalidLargeFetchRange = errors.New("indexer: invalid large fetch range")
	ErrInvalidSyncConfig      = errors.New("indexer: invalid sync config")
	ErrInvalidSyncDependency  = errors.New("indexer: invalid sync dependency")
	ErrHeadChanged            = errors.New("indexer: selected head changed during sync")
	ErrSyncRetriesExhausted   = errors.New("indexer: sync retries exhausted")
)
