//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresCanonicalUpdateLifecycle(t *testing.T) {
	postgres := newPostgresIntegrationStore(t)
	ctx := context.Background()
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")

	block10 := memoryTestBundle(10, common.HexToHash("0x09"), common.HexToHash("0x10"), common.HexToHash("0xa10"), address)
	block11 := memoryTestBundle(11, block10.Block.Hash, common.HexToHash("0x11"), common.HexToHash("0xa11"), address)
	block12 := memoryTestBundle(12, block11.Block.Hash, common.HexToHash("0x12"), common.HexToHash("0xa12"), address)
	block10.Transactions[0].Input = nil
	block10.Events[0].Topics = nil
	block10.Events[0].Data = nil
	indexedAt := time.Now().UTC().Truncate(time.Microsecond)
	setBundleTimes(indexedAt, &block10, &block11, &block12)

	if err := postgres.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 10,
		RetainFrom:  10,
		Blocks:      []domain.BlockBundle{block10, block11},
		SyncedAt:    indexedAt,
	}); err != nil {
		t.Fatalf("initial ApplyCanonicalUpdate() error = %v", err)
	}
	assertPostgresTip(t, postgres, 11, block11.Block.Hash)

	if err := postgres.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 12,
		RetainFrom:  11,
		Blocks:      []domain.BlockBundle{block12},
		SyncedAt:    indexedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("append ApplyCanonicalUpdate() error = %v", err)
	}
	if _, err := postgres.CanonicalHash(ctx, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned CanonicalHash() error = %v, want ErrNotFound", err)
	}
	assertPostgresTip(t, postgres, 12, block12.Block.Hash)

	replacement12 := memoryTestBundle(12, block11.Block.Hash, common.HexToHash("0xb12"), common.HexToHash("0xc12"), address)
	setBundleTimes(indexedAt.Add(2*time.Second), &replacement12)
	if err := postgres.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 12,
		RetainFrom:  11,
		Blocks:      []domain.BlockBundle{replacement12},
		SyncedAt:    indexedAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("replacement ApplyCanonicalUpdate() error = %v", err)
	}
	assertPostgresTip(t, postgres, 12, replacement12.Block.Hash)
	assertRowCount(t, postgres, "transactions", "hash", block12.Transactions[0].Hash.Bytes(), 0)
	assertRowCount(t, postgres, "transactions", "hash", replacement12.Transactions[0].Hash.Bytes(), 1)
	assertRowCount(t, postgres, "event_logs", "transaction_hash", block12.Transactions[0].Hash.Bytes(), 0)
	assertRowCount(t, postgres, "event_logs", "transaction_hash", replacement12.Transactions[0].Hash.Bytes(), 1)
	assertPostgresSyncState(t, postgres, 1, 12, replacement12.Block.Hash, indexedAt.Add(2*time.Second))
	assertPostgresQueries(t, postgres, block11, replacement12, address)

	badBlock13 := memoryTestBundle(13, common.HexToHash("0xdead"), common.HexToHash("0x13"), common.HexToHash("0xa13"), address)
	setBundleTimes(indexedAt.Add(3*time.Second), &badBlock13)
	err := postgres.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 13,
		RetainFrom:  11,
		Blocks:      []domain.BlockBundle{badBlock13},
		SyncedAt:    indexedAt.Add(3 * time.Second),
	})
	if !errors.Is(err, ErrInvalidCanonicalUpdate) {
		t.Fatalf("invalid parent update error = %v, want ErrInvalidCanonicalUpdate", err)
	}
	assertPostgresTip(t, postgres, 12, replacement12.Block.Hash)

	err = postgres.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     2,
		ReplaceFrom: 13,
		RetainFrom:  11,
		SyncedAt:    indexedAt.Add(4 * time.Second),
	})
	if !errors.Is(err, ErrInvalidCanonicalUpdate) {
		t.Fatalf("different chain update error = %v, want ErrInvalidCanonicalUpdate", err)
	}
	assertPostgresTip(t, postgres, 12, replacement12.Block.Hash)
	assertPostgresSyncState(t, postgres, 1, 12, replacement12.Block.Hash, indexedAt.Add(2*time.Second))
}

func TestPostgresCanonicalUpdateFlushesBoundedBatches(t *testing.T) {
	postgres := newPostgresIntegrationStore(t)
	ctx := context.Background()
	blockHash := common.HexToHash("0x100")
	transactions := make([]domain.Transaction, canonicalBatchQueryLimit+1)
	for index := range transactions {
		transactions[index] = domain.Transaction{
			Hash:        common.BigToHash(big.NewInt(int64(index + 1))),
			BlockNumber: 1,
			BlockHash:   blockHash,
			Index:       uint32(index),
			Value:       big.NewInt(1),
		}
	}
	bundle := domain.BlockBundle{
		Block: domain.Block{
			Number:           1,
			Hash:             blockHash,
			ParentHash:       common.HexToHash("0x99"),
			TransactionCount: len(transactions),
			IndexedAt:        time.Now().UTC(),
		},
		Transactions: transactions,
	}

	if err := postgres.ApplyCanonicalUpdate(ctx, domain.CanonicalUpdate{
		ChainID:     1,
		ReplaceFrom: 1,
		RetainFrom:  1,
		Blocks:      []domain.BlockBundle{bundle},
		SyncedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ApplyCanonicalUpdate() across batches error = %v", err)
	}
	result, err := postgres.BlockByNumber(ctx, 1)
	if err != nil {
		t.Fatalf("BlockByNumber() error = %v", err)
	}
	if len(result.TransactionHashes) != len(transactions) {
		t.Fatalf("transaction hash count = %d, want %d", len(result.TransactionHashes), len(transactions))
	}
}

func assertPostgresQueries(
	t *testing.T,
	postgres *Postgres,
	block11 domain.BlockBundle,
	block12 domain.BlockBundle,
	address common.Address,
) {
	t.Helper()
	ctx := context.Background()

	blockResult, err := postgres.BlockByNumber(ctx, block11.Block.Number)
	if err != nil {
		t.Fatalf("BlockByNumber() error = %v", err)
	}
	if blockResult.Block.Hash != block11.Block.Hash ||
		len(blockResult.TransactionHashes) != 1 ||
		blockResult.TransactionHashes[0] != block11.Transactions[0].Hash {
		t.Fatalf("BlockByNumber() = %#v", blockResult)
	}
	if _, err := postgres.BlockByNumber(ctx, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned BlockByNumber() error = %v, want ErrNotFound", err)
	}

	transactionResult, err := postgres.TransactionByHash(ctx, block12.Transactions[0].Hash)
	if err != nil {
		t.Fatalf("TransactionByHash() error = %v", err)
	}
	if transactionResult.Transaction.BlockHash != block12.Block.Hash ||
		len(transactionResult.Events) != 1 ||
		transactionResult.Events[0].TransactionHash != block12.Transactions[0].Hash {
		t.Fatalf("TransactionByHash() = %#v", transactionResult)
	}
	if _, err := postgres.TransactionByHash(ctx, common.HexToHash("0xdead")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing TransactionByHash() error = %v, want ErrNotFound", err)
	}

	firstPage, err := postgres.EventsByAddress(ctx, domain.EventQuery{Address: address, Limit: 1})
	if err != nil {
		t.Fatalf("EventsByAddress() error = %v", err)
	}
	if len(firstPage.Events) != 1 || firstPage.Events[0].BlockHash != block12.Block.Hash || firstPage.NextCursor == nil {
		t.Fatalf("first EventsByAddress() page = %#v", firstPage)
	}
	secondPage, err := postgres.EventsByAddress(ctx, domain.EventQuery{
		Address: address,
		Limit:   1,
		Before:  firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("EventsByAddress(second page) error = %v", err)
	}
	if len(secondPage.Events) != 1 || secondPage.Events[0].BlockHash != block11.Block.Hash || secondPage.NextCursor != nil {
		t.Fatalf("second EventsByAddress() page = %#v", secondPage)
	}

	staleCursor := *firstPage.NextCursor
	staleCursor.BlockHash = common.HexToHash("0xdead")
	_, err = postgres.EventsByAddress(ctx, domain.EventQuery{
		Address: address,
		Limit:   1,
		Before:  &staleCursor,
	})
	if !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("stale EventsByAddress() error = %v, want ErrStaleCursor", err)
	}

	_, err = postgres.EventsByAddress(ctx, domain.EventQuery{
		Address: address,
		Limit:   domain.MaxEventPageSize + 1,
	})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("oversized EventsByAddress() error = %v, want ErrInvalidQuery", err)
	}
}

func newPostgresIntegrationStore(t *testing.T) *Postgres {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create integration admin pool: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("ethindexer_test_%d", time.Now().UnixNano())
	schemaIdentifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse integration pool config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create integration pool: %v", err)
	}
	t.Cleanup(pool.Close)

	const migrationPath = "../../migrations/000001_initial.up.sql"
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read integration migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(migration), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("apply integration migration: %v", err)
	}

	return &Postgres{pool: pool}
}

func assertPostgresTip(t *testing.T, postgres *Postgres, number uint64, hash common.Hash) {
	t.Helper()

	tip, err := postgres.CanonicalTip(context.Background())
	if err != nil {
		t.Fatalf("CanonicalTip() error = %v", err)
	}
	if tip == nil || tip.Number != number || tip.Hash != hash {
		t.Fatalf("CanonicalTip() = %#v, want block %d (%s)", tip, number, hash)
	}
}

func assertRowCount(t *testing.T, postgres *Postgres, table, column string, value []byte, want int) {
	t.Helper()

	identifier := pgx.Identifier{table}.Sanitize() + "." + pgx.Identifier{column}.Sanitize()
	query := "SELECT count(*) FROM " + pgx.Identifier{table}.Sanitize() + " WHERE " + identifier + " = $1"
	var count int
	if err := postgres.pool.QueryRow(context.Background(), query, value).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s row count = %d, want %d", table, count, want)
	}
}

func assertPostgresSyncState(
	t *testing.T,
	postgres *Postgres,
	chainID uint64,
	number uint64,
	hash common.Hash,
	syncedAt time.Time,
) {
	t.Helper()

	var (
		storedChainID int64
		storedNumber  int64
		storedHash    []byte
		storedAt      time.Time
	)
	err := postgres.pool.QueryRow(context.Background(), `
		SELECT chain_id, last_indexed_number, last_indexed_hash, last_successful_sync_at
		FROM sync_state`,
	).Scan(&storedChainID, &storedNumber, &storedHash, &storedAt)
	if err != nil {
		t.Fatalf("read sync state: %v", err)
	}
	if storedChainID != postgresInt64(chainID) || storedNumber != postgresInt64(number) ||
		common.BytesToHash(storedHash) != hash || !storedAt.Equal(syncedAt) {
		t.Fatalf(
			"sync state = (%d, %d, %x, %s), want (%d, %d, %s, %s)",
			storedChainID,
			storedNumber,
			storedHash,
			storedAt,
			chainID,
			number,
			hash,
			syncedAt,
		)
	}
}

func setBundleTimes(indexedAt time.Time, bundles ...*domain.BlockBundle) {
	for _, bundle := range bundles {
		bundle.Block.IndexedAt = indexedAt
	}
}
