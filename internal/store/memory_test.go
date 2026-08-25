package store

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
)

func TestMemoryApplyAndQuery(t *testing.T) {
	ctx := context.Background()
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	block10 := memoryTestBundle(10, common.HexToHash("0x09"), common.HexToHash("0x10"), common.HexToHash("0xa10"), address)
	block11 := memoryTestBundle(11, block10.Block.Hash, common.HexToHash("0x11"), common.HexToHash("0xa11"), address)

	memory := NewMemory()
	err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 10,
		RetainFrom:  10,
		Blocks:      []domain.BlockBundle{block10, block11},
		SyncedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyCanonicalUpdate() error = %v", err)
	}

	tip, err := memory.CanonicalTip(ctx)
	if err != nil {
		t.Fatalf("CanonicalTip() error = %v", err)
	}
	if tip == nil || tip.Number != 11 || tip.Hash != block11.Block.Hash {
		t.Fatalf("tip = %#v", tip)
	}

	storedBlock, err := memory.BlockByNumber(ctx, 10)
	if err != nil {
		t.Fatalf("BlockByNumber() error = %v", err)
	}
	if len(storedBlock.TransactionHashes) != 1 || storedBlock.TransactionHashes[0] != block10.Transactions[0].Hash {
		t.Fatalf("transaction hashes = %#v", storedBlock.TransactionHashes)
	}

	storedTransaction, err := memory.TransactionByHash(ctx, block10.Transactions[0].Hash)
	if err != nil {
		t.Fatalf("TransactionByHash() error = %v", err)
	}
	if len(storedTransaction.Events) != 1 || storedTransaction.Events[0].TransactionHash != block10.Transactions[0].Hash {
		t.Fatalf("transaction events = %#v", storedTransaction.Events)
	}

	firstPage, err := memory.EventsByAddress(ctx, domain.EventQuery{Address: address, Limit: 1})
	if err != nil {
		t.Fatalf("EventsByAddress() error = %v", err)
	}
	if len(firstPage.Events) != 1 || firstPage.Events[0].BlockNumber != 11 || firstPage.NextCursor == nil {
		t.Fatalf("first page = %#v", firstPage)
	}
	secondPage, err := memory.EventsByAddress(ctx, domain.EventQuery{
		Address: address,
		Limit:   1,
		Before:  firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("EventsByAddress(second page) error = %v", err)
	}
	if len(secondPage.Events) != 1 || secondPage.Events[0].BlockNumber != 10 || secondPage.NextCursor != nil {
		t.Fatalf("second page = %#v", secondPage)
	}

	// Query results must not mutate records owned by the store.
	storedTransaction.Transaction.Input[0] = 0xff
	storedTransaction.Events[0].Data[0] = 0xff
	storedAgain, err := memory.TransactionByHash(ctx, block10.Transactions[0].Hash)
	if err != nil {
		t.Fatalf("TransactionByHash(second read) error = %v", err)
	}
	if storedAgain.Transaction.Input[0] == 0xff || storedAgain.Events[0].Data[0] == 0xff {
		t.Fatal("query result aliases memory-store state")
	}
}

func TestMemoryPrunesOutsideRetentionWindow(t *testing.T) {
	ctx := context.Background()
	address := common.Address{}
	block10 := memoryTestBundle(10, common.HexToHash("0x09"), common.HexToHash("0x10"), common.HexToHash("0xa10"), address)
	block11 := memoryTestBundle(11, block10.Block.Hash, common.HexToHash("0x11"), common.HexToHash("0xa11"), address)
	block12 := memoryTestBundle(12, block11.Block.Hash, common.HexToHash("0x12"), common.HexToHash("0xa12"), address)

	memory := NewMemory()
	if err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID: 1, ReplaceFrom: 10, RetainFrom: 10,
		Blocks: []domain.BlockBundle{block10, block11},
	}); err != nil {
		t.Fatalf("initial update: %v", err)
	}
	if err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID: 1, ReplaceFrom: 12, RetainFrom: 11,
		Blocks: []domain.BlockBundle{block12},
	}); err != nil {
		t.Fatalf("append update: %v", err)
	}

	if _, err := memory.BlockByNumber(ctx, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned block lookup error = %v, want ErrNotFound", err)
	}
	if _, err := memory.BlockByNumber(ctx, 11); err != nil {
		t.Fatalf("retained block lookup error = %v", err)
	}
}

func TestMemoryCanonicalReplacementRemovesOldFork(t *testing.T) {
	ctx := context.Background()
	address := common.Address{}
	block10 := memoryTestBundle(10, common.HexToHash("0x09"), common.HexToHash("0x10"), common.HexToHash("0xa10"), address)
	oldBlock11 := memoryTestBundle(11, block10.Block.Hash, common.HexToHash("0x11"), common.HexToHash("0xa11"), address)
	newBlock11 := memoryTestBundle(11, block10.Block.Hash, common.HexToHash("0xb11"), common.HexToHash("0xc11"), address)

	memory := NewMemory()
	if err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID: 1, ReplaceFrom: 10, RetainFrom: 10,
		Blocks: []domain.BlockBundle{block10, oldBlock11},
	}); err != nil {
		t.Fatalf("initial update: %v", err)
	}
	if err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID: 1, ReplaceFrom: 11, RetainFrom: 10,
		Blocks: []domain.BlockBundle{newBlock11},
	}); err != nil {
		t.Fatalf("replacement update: %v", err)
	}

	if _, err := memory.TransactionByHash(ctx, oldBlock11.Transactions[0].Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old transaction lookup error = %v, want ErrNotFound", err)
	}
	if _, err := memory.TransactionByHash(ctx, newBlock11.Transactions[0].Hash); err != nil {
		t.Fatalf("new transaction lookup error = %v", err)
	}
}

func TestMemoryRejectsEventPageAboveLimit(t *testing.T) {
	_, err := NewMemory().EventsByAddress(context.Background(), domain.EventQuery{
		Limit: domain.MaxEventPageSize + 1,
	})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("EventsByAddress() error = %v, want ErrInvalidQuery", err)
	}
}

func TestMemoryRejectsInvalidUpdateAtomically(t *testing.T) {
	ctx := context.Background()
	block10 := memoryTestBundle(10, common.HexToHash("0x09"), common.HexToHash("0x10"), common.HexToHash("0xa10"), common.Address{})
	badBlock11 := memoryTestBundle(11, common.HexToHash("0xdead"), common.HexToHash("0x11"), common.HexToHash("0xa11"), common.Address{})

	memory := NewMemory()
	if err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID: 1, ReplaceFrom: 10, RetainFrom: 10, Blocks: []domain.BlockBundle{block10},
	}); err != nil {
		t.Fatalf("initial update: %v", err)
	}

	err := memory.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID: 1, ReplaceFrom: 11, RetainFrom: 10, Blocks: []domain.BlockBundle{badBlock11},
	})
	if !errors.Is(err, ErrInvalidCanonicalUpdate) {
		t.Fatalf("invalid update error = %v, want ErrInvalidCanonicalUpdate", err)
	}
	tip, tipErr := memory.CanonicalTip(ctx)
	if tipErr != nil || tip == nil || tip.Number != 10 {
		t.Fatalf("tip after rejected update = %#v, error = %v", tip, tipErr)
	}
}

func memoryTestBundle(
	number uint64,
	parentHash common.Hash,
	blockHash common.Hash,
	transactionHash common.Hash,
	address common.Address,
) domain.BlockBundle {
	transaction := domain.Transaction{
		Hash:        transactionHash,
		BlockNumber: number,
		BlockHash:   blockHash,
		Index:       0,
		Value:       big.NewInt(1),
		Input:       []byte{0x01},
	}
	event := domain.Event{
		BlockNumber:      number,
		BlockHash:        blockHash,
		TransactionHash:  transactionHash,
		TransactionIndex: 0,
		LogIndex:         0,
		Address:          address,
		Topics:           []common.Hash{common.HexToHash("0x01")},
		Data:             []byte{0x02},
	}
	return domain.BlockBundle{
		Block: domain.Block{
			Number:           number,
			Hash:             blockHash,
			ParentHash:       parentHash,
			TransactionCount: 1,
		},
		Transactions: []domain.Transaction{transaction},
		Events:       []domain.Event{event},
	}
}
