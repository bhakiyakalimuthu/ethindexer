package store

import (
	"context"
	"errors"
	"fmt"
	"math"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
)

const blockByNumberQuery = `
	SELECT
		b.number,
		b.hash,
		b.parent_hash,
		b.block_timestamp,
		b.gas_limit,
		b.gas_used,
		b.base_fee_per_gas_wei,
		b.transaction_count,
		b.indexed_at,
		ARRAY(
			SELECT tx_row.hash
			FROM transactions AS tx_row
			WHERE tx_row.block_number = b.number
			ORDER BY tx_row.transaction_index
		)
	FROM blocks AS b
	WHERE b.number = $1`

func (p *Postgres) BlockByNumber(ctx context.Context, number uint64) (domain.BlockResult, error) {
	if number > math.MaxInt64 {
		return domain.BlockResult{}, ErrNotFound
	}

	var (
		row               postgresBlockRow
		transactionHashes [][]byte
	)
	targets := append(row.scanTargets(), &transactionHashes)
	if err := p.pool.QueryRow(ctx, blockByNumberQuery, postgresInt64(number)).Scan(targets...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BlockResult{}, ErrNotFound
		}
		return domain.BlockResult{}, fmt.Errorf("query block %d: %w", number, err)
	}

	block, err := row.decode()
	if err != nil {
		return domain.BlockResult{}, fmt.Errorf("decode block %d: %w", number, err)
	}
	hashes := make([]common.Hash, len(transactionHashes))
	for index, value := range transactionHashes {
		hash, err := decodeHash(value)
		if err != nil {
			return domain.BlockResult{}, fmt.Errorf("decode block %d transaction %d hash: %w", number, index, err)
		}
		hashes[index] = hash
	}
	if len(hashes) != block.TransactionCount {
		return domain.BlockResult{}, fmt.Errorf(
			"%w: block %d contains %d transaction hashes, expected %d",
			ErrInconsistentData,
			number,
			len(hashes),
			block.TransactionCount,
		)
	}
	return domain.BlockResult{Block: block, TransactionHashes: hashes}, nil
}
