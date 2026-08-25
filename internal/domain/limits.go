package domain

const (
	// MaxBlockWindow is the V1 retention contract, not only a default.
	MaxBlockWindow uint64 = 50
	// MaxEventPageSize bounds memory use in both storage implementations.
	MaxEventPageSize uint32 = 1_000
)
