//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
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
