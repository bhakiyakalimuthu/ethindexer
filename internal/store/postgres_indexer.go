package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	canonicalTipQuery = `
		SELECT number, hash
		FROM blocks
		ORDER BY number DESC
		LIMIT 1`
	canonicalHashQuery = `
		SELECT hash
		FROM blocks
		WHERE number = $1`
	deleteCanonicalRangeQuery = `
		DELETE FROM blocks
		WHERE number >= $1 OR number < $2`
	insertBlockQuery = `
		INSERT INTO blocks (
			number,
			hash,
			parent_hash,
			block_timestamp,
			gas_limit,
			gas_used,
			base_fee_per_gas_wei,
			transaction_count,
			indexed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	insertTransactionQuery = `
		INSERT INTO transactions (
			hash,
			block_number,
			transaction_index,
			transaction_type,
			nonce,
			sender_address,
			recipient_address,
			value_wei,
			gas_limit,
			gas_price_wei,
			max_fee_per_gas_wei,
			max_priority_fee_per_gas_wei,
			input
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	insertEventQuery = `
		INSERT INTO event_logs (
			block_number,
			transaction_hash,
			transaction_index,
			log_index,
			emitter_address,
			topics,
			data
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	upsertSyncStateQuery = `
		INSERT INTO sync_state (
			chain_id,
			last_indexed_number,
			last_indexed_hash,
			last_successful_sync_at
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (chain_id) DO UPDATE SET
			last_indexed_number = EXCLUDED.last_indexed_number,
			last_indexed_hash = EXCLUDED.last_indexed_hash,
			last_successful_sync_at = EXCLUDED.last_successful_sync_at`
)

var (
	_ IndexStore  = (*Postgres)(nil)
	_ HealthStore = (*Postgres)(nil)
)

func (p *Postgres) CanonicalTip(ctx context.Context) (*domain.ChainTip, error) {
	var (
		number int64
		hash   []byte
	)
	if err := p.pool.QueryRow(ctx, canonicalTipQuery).Scan(&number, &hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query canonical tip: %w", err)
	}

	decodedHash, err := decodeHash(hash)
	if err != nil {
		return nil, fmt.Errorf("decode canonical tip: %w", err)
	}
	decodedNumber, err := decodePostgresUint64(number)
	if err != nil {
		return nil, fmt.Errorf("decode canonical tip: %w", err)
	}
	return &domain.ChainTip{Number: decodedNumber, Hash: decodedHash}, nil
}

func (p *Postgres) CanonicalHash(ctx context.Context, number uint64) (common.Hash, error) {
	if number > math.MaxInt64 {
		return common.Hash{}, ErrNotFound
	}

	var hash []byte
	if err := p.pool.QueryRow(ctx, canonicalHashQuery, postgresInt64(number)).Scan(&hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return common.Hash{}, ErrNotFound
		}
		return common.Hash{}, fmt.Errorf("query canonical hash for block %d: %w", number, err)
	}

	decodedHash, err := decodeHash(hash)
	if err != nil {
		return common.Hash{}, fmt.Errorf("decode canonical hash for block %d: %w", number, err)
	}
	return decodedHash, nil
}

func (p *Postgres) ApplyCanonicalUpdate(ctx context.Context, update domain.CanonicalUpdate) error {
	if err := validatePostgresUpdate(update); err != nil {
		return err
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin canonical update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The indexer has one canonical writer. This lock also serializes the first
	// update, when sync_state does not contain a row yet.
	if _, err := tx.Exec(ctx, `LOCK TABLE sync_state IN EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock canonical sync state: %w", err)
	}
	if err := rejectDifferentChain(ctx, tx, update.ChainID); err != nil {
		return err
	}

	if _, err := tx.Exec(
		ctx,
		deleteCanonicalRangeQuery,
		postgresInt64(update.ReplaceFrom),
		postgresInt64(update.RetainFrom),
	); err != nil {
		return fmt.Errorf("delete replaced canonical range: %w", err)
	}
	if err := validateRetainedParent(ctx, tx, update); err != nil {
		return err
	}
	if err := insertCanonicalBundles(ctx, tx, update.Blocks); err != nil {
		return err
	}

	tip, err := canonicalTipInTransaction(ctx, tx)
	if err != nil {
		return err
	}
	if err := writeSyncState(ctx, tx, update, tip); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit canonical update: %w", err)
	}
	return nil
}

func rejectDifferentChain(ctx context.Context, tx pgx.Tx, chainID uint64) error {
	var existing int64
	err := tx.QueryRow(
		ctx,
		`SELECT chain_id FROM sync_state WHERE chain_id <> $1 LIMIT 1`,
		postgresInt64(chainID),
	).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read stored chain ID: %w", err)
	}
	return fmt.Errorf(
		"%w: store chain ID is %d, update chain ID is %d",
		ErrInvalidCanonicalUpdate,
		existing,
		chainID,
	)
}

func validateRetainedParent(ctx context.Context, tx pgx.Tx, update domain.CanonicalUpdate) error {
	if len(update.Blocks) == 0 || update.ReplaceFrom == update.RetainFrom {
		return nil
	}

	previousNumber := update.ReplaceFrom - 1
	var previousHash []byte
	err := tx.QueryRow(ctx, canonicalHashQuery, postgresInt64(previousNumber)).Scan(&previousHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf(
			"%w: retained parent block %d is missing",
			ErrInvalidCanonicalUpdate,
			previousNumber,
		)
	}
	if err != nil {
		return fmt.Errorf("read retained parent block %d: %w", previousNumber, err)
	}

	decodedHash, err := decodeHash(previousHash)
	if err != nil {
		return fmt.Errorf("decode retained parent block %d: %w", previousNumber, err)
	}
	if update.Blocks[0].Block.ParentHash != decodedHash {
		return fmt.Errorf(
			"%w: replacement block %d does not extend retained block %d",
			ErrInvalidCanonicalUpdate,
			update.ReplaceFrom,
			previousNumber,
		)
	}
	return nil
}

func insertCanonicalBundles(ctx context.Context, tx pgx.Tx, bundles []domain.BlockBundle) error {
	if len(bundles) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, bundle := range bundles {
		queueBlock(batch, bundle.Block)
		for _, transaction := range bundle.Transactions {
			queueTransaction(batch, transaction)
		}
		for _, event := range bundle.Events {
			queueEvent(batch, event)
		}
	}

	results := tx.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		return fmt.Errorf("insert canonical bundles: %w", err)
	}
	return nil
}

func queueBlock(batch *pgx.Batch, block domain.Block) {
	batch.Queue(
		insertBlockQuery,
		postgresInt64(block.Number),
		block.Hash.Bytes(),
		block.ParentHash.Bytes(),
		postgresInt64(block.Timestamp),
		postgresInt64(block.GasLimit),
		postgresInt64(block.GasUsed),
		postgresNumeric(block.BaseFeePerGas),
		postgresCount(block.TransactionCount),
		block.IndexedAt,
	)
}

func queueTransaction(batch *pgx.Batch, transaction domain.Transaction) {
	batch.Queue(
		insertTransactionQuery,
		transaction.Hash.Bytes(),
		postgresInt64(transaction.BlockNumber),
		postgresInt32(transaction.Index),
		int16(transaction.Type),
		postgresInt64(transaction.Nonce),
		transaction.Sender.Bytes(),
		postgresAddress(transaction.Recipient),
		postgresNumeric(transaction.Value),
		postgresInt64(transaction.GasLimit),
		postgresNumeric(transaction.GasPrice),
		postgresNumeric(transaction.MaxFeePerGas),
		postgresNumeric(transaction.MaxPriorityFeePerGas),
		transaction.Input,
	)
}

func queueEvent(batch *pgx.Batch, event domain.Event) {
	topics := make([][]byte, len(event.Topics))
	for index, topic := range event.Topics {
		topics[index] = topic.Bytes()
	}
	batch.Queue(
		insertEventQuery,
		postgresInt64(event.BlockNumber),
		event.TransactionHash.Bytes(),
		postgresInt32(event.TransactionIndex),
		postgresInt32(event.LogIndex),
		event.Address.Bytes(),
		topics,
		event.Data,
	)
}

func canonicalTipInTransaction(ctx context.Context, tx pgx.Tx) (*domain.ChainTip, error) {
	var (
		number int64
		hash   []byte
	)
	if err := tx.QueryRow(ctx, canonicalTipQuery).Scan(&number, &hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read updated canonical tip: %w", err)
	}

	decodedHash, err := decodeHash(hash)
	if err != nil {
		return nil, fmt.Errorf("decode updated canonical tip: %w", err)
	}
	decodedNumber, err := decodePostgresUint64(number)
	if err != nil {
		return nil, fmt.Errorf("decode updated canonical tip: %w", err)
	}
	return &domain.ChainTip{Number: decodedNumber, Hash: decodedHash}, nil
}

func writeSyncState(ctx context.Context, tx pgx.Tx, update domain.CanonicalUpdate, tip *domain.ChainTip) error {
	var (
		number any
		hash   any
	)
	if tip != nil {
		number = postgresInt64(tip.Number)
		hash = tip.Hash.Bytes()
	}

	if _, err := tx.Exec(
		ctx,
		upsertSyncStateQuery,
		postgresInt64(update.ChainID),
		number,
		hash,
		update.SyncedAt,
	); err != nil {
		return fmt.Errorf("write canonical sync state: %w", err)
	}
	return nil
}

func validatePostgresUpdate(update domain.CanonicalUpdate) error {
	switch {
	case update.ChainID == 0:
		return fmt.Errorf("%w: chain ID must be greater than zero", ErrInvalidCanonicalUpdate)
	case update.ChainID > math.MaxInt64:
		return fmt.Errorf("%w: chain ID exceeds PostgreSQL bigint", ErrInvalidCanonicalUpdate)
	case update.RetainFrom > update.ReplaceFrom:
		return fmt.Errorf("%w: retain-from block cannot exceed replace-from block", ErrInvalidCanonicalUpdate)
	case update.RetainFrom > math.MaxInt64 || update.ReplaceFrom > math.MaxInt64:
		return fmt.Errorf("%w: canonical range exceeds PostgreSQL bigint", ErrInvalidCanonicalUpdate)
	}
	if err := validateReplacement(update); err != nil {
		return err
	}

	bundles := make(map[uint64]domain.BlockBundle, len(update.Blocks))
	for _, bundle := range update.Blocks {
		bundles[bundle.Block.Number] = bundle
	}
	if err := validateCanonicalBundles(bundles); err != nil {
		return err
	}

	for _, bundle := range update.Blocks {
		if err := validatePostgresBlock(bundle.Block); err != nil {
			return err
		}
		for _, transaction := range bundle.Transactions {
			if err := validatePostgresTransaction(transaction); err != nil {
				return err
			}
		}
		for _, event := range bundle.Events {
			if event.TransactionIndex > math.MaxInt32 || event.LogIndex > math.MaxInt32 {
				return fmt.Errorf("%w: event position exceeds PostgreSQL integer", ErrInvalidCanonicalUpdate)
			}
		}
	}
	return nil
}

func validatePostgresBlock(block domain.Block) error {
	if block.Number > math.MaxInt64 || block.Timestamp > math.MaxInt64 ||
		block.GasLimit > math.MaxInt64 || block.GasUsed > math.MaxInt64 {
		return fmt.Errorf("%w: block %d contains a value exceeding PostgreSQL bigint", ErrInvalidCanonicalUpdate, block.Number)
	}
	if block.TransactionCount > math.MaxInt32 {
		return fmt.Errorf("%w: block %d transaction count exceeds PostgreSQL integer", ErrInvalidCanonicalUpdate, block.Number)
	}
	if err := validateUint256("block base fee", block.BaseFeePerGas, false); err != nil {
		return err
	}
	return nil
}

func validatePostgresTransaction(transaction domain.Transaction) error {
	if transaction.BlockNumber > math.MaxInt64 || transaction.Nonce > math.MaxInt64 || transaction.GasLimit > math.MaxInt64 {
		return fmt.Errorf("%w: transaction %s contains a value exceeding PostgreSQL bigint", ErrInvalidCanonicalUpdate, transaction.Hash)
	}
	if transaction.Index > math.MaxInt32 {
		return fmt.Errorf("%w: transaction %s index exceeds PostgreSQL integer", ErrInvalidCanonicalUpdate, transaction.Hash)
	}

	values := []struct {
		name     string
		value    *big.Int
		required bool
	}{
		{name: "transaction value", value: transaction.Value, required: true},
		{name: "transaction gas price", value: transaction.GasPrice},
		{name: "transaction max fee", value: transaction.MaxFeePerGas},
		{name: "transaction max priority fee", value: transaction.MaxPriorityFeePerGas},
	}
	for _, value := range values {
		if err := validateUint256(value.name, value.value, value.required); err != nil {
			return err
		}
	}
	return nil
}

func validateUint256(name string, value *big.Int, required bool) error {
	if value == nil {
		if required {
			return fmt.Errorf("%w: %s is required", ErrInvalidCanonicalUpdate, name)
		}
		return nil
	}
	if value.Sign() < 0 || value.BitLen() > 256 {
		return fmt.Errorf("%w: %s is outside uint256", ErrInvalidCanonicalUpdate, name)
	}
	return nil
}

func postgresNumeric(value *big.Int) any {
	if value == nil {
		return nil
	}
	return pgtype.Numeric{Int: new(big.Int).Set(value), Valid: true}
}

func postgresAddress(address *common.Address) any {
	if address == nil {
		return nil
	}
	return address.Bytes()
}

func decodeHash(value []byte) (common.Hash, error) {
	if len(value) != common.HashLength {
		return common.Hash{}, fmt.Errorf("%w: hash has %d bytes", ErrInconsistentData, len(value))
	}
	return common.BytesToHash(value), nil
}

func decodePostgresUint64(value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: PostgreSQL bigint is negative", ErrInconsistentData)
	}
	return uint64(value), nil
}

// The complete update is range-checked before SQL arguments are constructed.
// These guards keep the conversion helpers safe if they are reused elsewhere.
func postgresInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		panic("store: uint64 exceeds PostgreSQL bigint")
	}
	return int64(value)
}

func postgresInt32(value uint32) int32 {
	if value > math.MaxInt32 {
		panic("store: uint32 exceeds PostgreSQL integer")
	}
	return int32(value)
}

func postgresCount(value int) int32 {
	if value < 0 || value > math.MaxInt32 {
		panic("store: int exceeds non-negative PostgreSQL integer")
	}
	return int32(value)
}
