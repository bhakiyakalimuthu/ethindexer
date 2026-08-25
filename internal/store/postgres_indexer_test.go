package store

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
)

func TestValidatePostgresUpdateAcceptsCanonicalBundle(t *testing.T) {
	bundle := memoryTestBundle(
		10,
		common.HexToHash("0x09"),
		common.HexToHash("0x10"),
		common.HexToHash("0xa10"),
		common.Address{},
	)

	err := validatePostgresUpdate(domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 10,
		RetainFrom:  10,
		Blocks:      []domain.BlockBundle{bundle},
	})
	if err != nil {
		t.Fatalf("validatePostgresUpdate() error = %v", err)
	}
}

func TestValidatePostgresUpdateRejectsUnrepresentableValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*domain.CanonicalUpdate)
	}{
		{
			name: "chain ID exceeds bigint",
			change: func(update *domain.CanonicalUpdate) {
				update.ChainID = uint64(math.MaxInt64) + 1
			},
		},
		{
			name: "range exceeds bigint",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks = nil
				update.ReplaceFrom = uint64(math.MaxInt64) + 1
			},
		},
		{
			name: "block timestamp exceeds bigint",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks[0].Block.Timestamp = uint64(math.MaxInt64) + 1
			},
		},
		{
			name: "negative base fee",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks[0].Block.BaseFeePerGas = big.NewInt(-1)
			},
		},
		{
			name: "nil transaction value",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks[0].Transactions[0].Value = nil
			},
		},
		{
			name: "transaction value exceeds uint256",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks[0].Transactions[0].Value = new(big.Int).Lsh(big.NewInt(1), 256)
			},
		},
		{
			name: "transaction nonce exceeds bigint",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks[0].Transactions[0].Nonce = uint64(math.MaxInt64) + 1
			},
		},
		{
			name: "event position exceeds integer",
			change: func(update *domain.CanonicalUpdate) {
				update.Blocks[0].Events[0].LogIndex = uint32(math.MaxInt32) + 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := memoryTestBundle(
				10,
				common.HexToHash("0x09"),
				common.HexToHash("0x10"),
				common.HexToHash("0xa10"),
				common.Address{},
			)
			update := domain.CanonicalUpdate{
				ChainID:     1,
				ReplaceFrom: 10,
				RetainFrom:  10,
				Blocks:      []domain.BlockBundle{bundle},
			}
			test.change(&update)

			err := validatePostgresUpdate(update)
			if !errors.Is(err, ErrInvalidCanonicalUpdate) {
				t.Fatalf("validatePostgresUpdate() error = %v, want ErrInvalidCanonicalUpdate", err)
			}
		})
	}
}

func TestDecodeHash(t *testing.T) {
	want := common.HexToHash("0x1234")
	got, err := decodeHash(want.Bytes())
	if err != nil {
		t.Fatalf("decodeHash() error = %v", err)
	}
	if got != want {
		t.Fatalf("decodeHash() = %s, want %s", got, want)
	}

	_, err = decodeHash(make([]byte, common.HashLength-1))
	if !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("decodeHash(short) error = %v, want ErrInconsistentData", err)
	}
}
