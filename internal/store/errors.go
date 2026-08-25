package store

import "errors"

var (
	ErrNotFound               = errors.New("store: not found")
	ErrInvalidCanonicalUpdate = errors.New("store: invalid canonical update")
	ErrStaleCursor            = errors.New("store: stale event cursor")
	ErrInconsistentData       = errors.New("store: inconsistent data")
)
