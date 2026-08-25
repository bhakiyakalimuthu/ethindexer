package store

import (
	"context"
	"errors"
	"fmt"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
)

const transactionByHashQuery = `
	SELECT
		tx_row.hash,
		tx_row.block_number,
		b.hash,
		tx_row.transaction_index,
		tx_row.transaction_type,
		tx_row.nonce,
		tx_row.sender_address,
		tx_row.recipient_address,
		tx_row.value_wei,
		tx_row.gas_limit,
		tx_row.gas_price_wei,
		tx_row.max_fee_per_gas_wei,
		tx_row.max_priority_fee_per_gas_wei,
		tx_row.input
	FROM transactions AS tx_row
	JOIN blocks AS b ON b.number = tx_row.block_number
	WHERE tx_row.hash = $1`

func (p *Postgres) TransactionByHash(ctx context.Context, hash common.Hash) (domain.TransactionResult, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.TransactionResult{}, fmt.Errorf("begin transaction lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var row postgresTransactionRow
	if err := tx.QueryRow(ctx, transactionByHashQuery, hash.Bytes()).Scan(row.scanTargets()...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.TransactionResult{}, ErrNotFound
		}
		return domain.TransactionResult{}, fmt.Errorf("query transaction %s: %w", hash, err)
	}
	transaction, err := row.decode()
	if err != nil {
		return domain.TransactionResult{}, fmt.Errorf("decode transaction %s: %w", hash, err)
	}
	events, err := queryEventsForTransaction(ctx, tx, hash)
	if err != nil {
		return domain.TransactionResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.TransactionResult{}, fmt.Errorf("commit transaction lookup: %w", err)
	}
	return domain.TransactionResult{Transaction: transaction, Events: events}, nil
}
