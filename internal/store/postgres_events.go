package store

import (
	"context"
	"fmt"
	"math"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
)

const (
	eventsForTransactionQuery = `
		SELECT
			e.block_number,
			b.hash,
			e.transaction_hash,
			e.transaction_index,
			e.log_index,
			e.emitter_address,
			e.topics,
			e.data
		FROM event_logs AS e
		JOIN blocks AS b ON b.number = e.block_number
		WHERE e.transaction_hash = $1
		ORDER BY e.log_index`
	eventsByAddressQuery = `
		SELECT
			e.block_number,
			b.hash,
			e.transaction_hash,
			e.transaction_index,
			e.log_index,
			e.emitter_address,
			e.topics,
			e.data
		FROM event_logs AS e
		JOIN blocks AS b ON b.number = e.block_number
		WHERE e.emitter_address = $1
		ORDER BY e.block_number DESC, e.log_index DESC
		LIMIT $2`
	eventsByAddressBeforeQuery = `
		SELECT
			e.block_number,
			b.hash,
			e.transaction_hash,
			e.transaction_index,
			e.log_index,
			e.emitter_address,
			e.topics,
			e.data
		FROM event_logs AS e
		JOIN blocks AS b ON b.number = e.block_number
		WHERE e.emitter_address = $1
			AND (
				e.block_number < $2 OR
				(e.block_number = $2 AND e.log_index < $3::bigint)
			)
		ORDER BY e.block_number DESC, e.log_index DESC
		LIMIT $4`
)

type postgresQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (p *Postgres) EventsByAddress(ctx context.Context, query domain.EventQuery) (domain.EventPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.EventPage{}, err
	}
	if query.Limit > domain.MaxEventPageSize {
		return domain.EventPage{}, fmt.Errorf(
			"%w: event page limit %d exceeds maximum %d",
			ErrInvalidQuery,
			query.Limit,
			domain.MaxEventPageSize,
		)
	}
	if query.Before == nil {
		return queryAddressEventPage(ctx, p.pool, query)
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.EventPage{}, fmt.Errorf("begin event cursor lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	valid, err := canonicalCursorExists(ctx, tx, *query.Before)
	if err != nil {
		return domain.EventPage{}, err
	}
	if !valid {
		return domain.EventPage{}, ErrStaleCursor
	}
	page, err := queryAddressEventPage(ctx, tx, query)
	if err != nil {
		return domain.EventPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.EventPage{}, fmt.Errorf("commit event cursor lookup: %w", err)
	}
	return page, nil
}

func queryEventsForTransaction(ctx context.Context, querier postgresQuerier, hash common.Hash) ([]domain.Event, error) {
	rows, err := querier.Query(ctx, eventsForTransactionQuery, hash.Bytes())
	if err != nil {
		return nil, fmt.Errorf("query events for transaction %s: %w", hash, err)
	}
	events, err := scanPostgresEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("scan events for transaction %s: %w", hash, err)
	}
	return events, nil
}

func queryAddressEventPage(ctx context.Context, querier postgresQuerier, query domain.EventQuery) (domain.EventPage, error) {
	if query.Limit == 0 {
		return domain.EventPage{Events: []domain.Event{}}, nil
	}

	queryLimit := postgresInt64(uint64(query.Limit) + 1)
	var (
		rows pgx.Rows
		err  error
	)
	if query.Before == nil {
		rows, err = querier.Query(ctx, eventsByAddressQuery, query.Address.Bytes(), queryLimit)
	} else {
		rows, err = querier.Query(
			ctx,
			eventsByAddressBeforeQuery,
			query.Address.Bytes(),
			postgresInt64(query.Before.BlockNumber),
			postgresInt64(uint64(query.Before.LogIndex)),
			queryLimit,
		)
	}
	if err != nil {
		return domain.EventPage{}, fmt.Errorf("query events for address %s: %w", query.Address, err)
	}
	events, err := scanPostgresEvents(rows)
	if err != nil {
		return domain.EventPage{}, fmt.Errorf("scan events for address %s: %w", query.Address, err)
	}

	page := domain.EventPage{Events: events}
	if uint64(len(events)) <= uint64(query.Limit) {
		return page, nil
	}
	page.Events = events[:len(events)-1]
	last := page.Events[len(page.Events)-1]
	page.NextCursor = &domain.EventCursor{
		BlockNumber: last.BlockNumber,
		BlockHash:   last.BlockHash,
		LogIndex:    last.LogIndex,
	}
	return page, nil
}

func canonicalCursorExists(ctx context.Context, querier postgresQuerier, cursor domain.EventCursor) (bool, error) {
	if cursor.BlockNumber > math.MaxInt64 {
		return false, nil
	}

	var exists bool
	if err := querier.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM blocks
			WHERE number = $1 AND hash = $2
		)`,
		postgresInt64(cursor.BlockNumber),
		cursor.BlockHash.Bytes(),
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("validate event cursor: %w", err)
	}
	return exists, nil
}

func scanPostgresEvents(rows pgx.Rows) ([]domain.Event, error) {
	defer rows.Close()

	events := make([]domain.Event, 0)
	for rows.Next() {
		var row postgresEventRow
		if err := rows.Scan(row.scanTargets()...); err != nil {
			return nil, err
		}
		event, err := row.decode()
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
