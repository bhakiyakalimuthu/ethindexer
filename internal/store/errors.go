package store

import "errors"

var (
	ErrNotFound               = errors.New("store: not found")
	ErrInvalidCanonicalUpdate = errors.New("store: invalid canonical update")
	ErrInvalidQuery           = errors.New("store: invalid query")
	ErrStaleCursor            = errors.New("store: stale event cursor")
	ErrInconsistentData       = errors.New("store: inconsistent data")
)
