package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInvalidCanonicalUpdate = errors.New("store: invalid canonical update")
	ErrStaleCursor            = errors.New("store: stale event cursor")
)

// Memory is a thread-safe Store implementation intended for tests and local
// verification. It keeps canonical bundles as the source of truth and rebuilds
// query results from at most the configured retention window.
type Memory struct {
	mu           sync.RWMutex
	chainID      uint64
	bundles      map[uint64]domain.BlockBundle
	lastSyncedAt time.Time
}

func NewMemory() *Memory {
	return &Memory{bundles: make(map[uint64]domain.BlockBundle)}
}

func (m *Memory) Ping(ctx context.Context) error {
	return ctx.Err()
}

func (m *Memory) Close() {}

func (m *Memory) CanonicalTip(ctx context.Context) (*domain.ChainTip, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	number, bundle, found := highestBundle(m.bundles)
	if !found {
		return nil, nil
	}
	return &domain.ChainTip{Number: number, Hash: bundle.Block.Hash}, nil
}

func (m *Memory) CanonicalHash(ctx context.Context, number uint64) (common.Hash, error) {
	if err := ctx.Err(); err != nil {
		return common.Hash{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	bundle, found := m.bundles[number]
	if !found {
		return common.Hash{}, ErrNotFound
	}
	return bundle.Block.Hash, nil
}

func (m *Memory) ApplyCanonicalUpdate(ctx context.Context, update domain.CanonicalUpdate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if update.ChainID == 0 {
		return fmt.Errorf("%w: chain ID must be greater than zero", ErrInvalidCanonicalUpdate)
	}
	if update.RetainFrom > update.ReplaceFrom {
		return fmt.Errorf("%w: retain-from block cannot exceed replace-from block", ErrInvalidCanonicalUpdate)
	}
	if err := validateReplacement(update); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.chainID != 0 && m.chainID != update.ChainID {
		return fmt.Errorf(
			"%w: store chain ID is %d, update chain ID is %d",
			ErrInvalidCanonicalUpdate,
			m.chainID,
			update.ChainID,
		)
	}

	candidate := cloneBundles(m.bundles)
	for number := range candidate {
		if number >= update.ReplaceFrom || number < update.RetainFrom {
			delete(candidate, number)
		}
	}
	for _, bundle := range update.Blocks {
		candidate[bundle.Block.Number] = cloneBundle(bundle)
	}
	if err := validateCanonicalBundles(candidate); err != nil {
		return err
	}

	m.chainID = update.ChainID
	m.bundles = candidate
	m.lastSyncedAt = update.SyncedAt
	return nil
}

func (m *Memory) BlockByNumber(ctx context.Context, number uint64) (domain.BlockResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.BlockResult{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	bundle, found := m.bundles[number]
	if !found {
		return domain.BlockResult{}, ErrNotFound
	}

	hashes := make([]common.Hash, len(bundle.Transactions))
	for index, transaction := range bundle.Transactions {
		hashes[index] = transaction.Hash
	}
	return domain.BlockResult{
		Block:             cloneBlock(bundle.Block),
		TransactionHashes: hashes,
	}, nil
}

func (m *Memory) TransactionByHash(ctx context.Context, hash common.Hash) (domain.TransactionResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.TransactionResult{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, bundle := range m.bundles {
		for _, transaction := range bundle.Transactions {
			if transaction.Hash != hash {
				continue
			}

			events := make([]domain.Event, 0)
			for _, event := range bundle.Events {
				if event.TransactionHash == hash {
					events = append(events, cloneEvent(event))
				}
			}
			sort.Slice(events, func(left, right int) bool {
				return events[left].LogIndex < events[right].LogIndex
			})
			return domain.TransactionResult{
				Transaction: cloneTransaction(transaction),
				Events:      events,
			}, nil
		}
	}
	return domain.TransactionResult{}, ErrNotFound
}

func (m *Memory) EventsByAddress(ctx context.Context, query domain.EventQuery) (domain.EventPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.EventPage{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if query.Before != nil {
		bundle, found := m.bundles[query.Before.BlockNumber]
		if !found || bundle.Block.Hash != query.Before.BlockHash {
			return domain.EventPage{}, ErrStaleCursor
		}
	}

	events := make([]domain.Event, 0)
	for _, bundle := range m.bundles {
		for _, event := range bundle.Events {
			if event.Address != query.Address || !eventBeforeCursor(event, query.Before) {
				continue
			}
			events = append(events, cloneEvent(event))
		}
	}
	sort.Slice(events, func(left, right int) bool {
		if events[left].BlockNumber != events[right].BlockNumber {
			return events[left].BlockNumber > events[right].BlockNumber
		}
		return events[left].LogIndex > events[right].LogIndex
	})

	if query.Limit == 0 || len(events) == 0 {
		return domain.EventPage{Events: []domain.Event{}}, nil
	}
	if uint64(len(events)) <= uint64(query.Limit) {
		return domain.EventPage{Events: events}, nil
	}

	limit := int(query.Limit)
	pageEvents := events[:limit]
	last := pageEvents[len(pageEvents)-1]
	return domain.EventPage{
		Events: pageEvents,
		NextCursor: &domain.EventCursor{
			BlockNumber: last.BlockNumber,
			BlockHash:   last.BlockHash,
			LogIndex:    last.LogIndex,
		},
	}, nil
}

func validateReplacement(update domain.CanonicalUpdate) error {
	if len(update.Blocks) == 0 {
		return nil
	}
	if update.Blocks[0].Block.Number != update.ReplaceFrom {
		return fmt.Errorf(
			"%w: first replacement block is %d, expected %d",
			ErrInvalidCanonicalUpdate,
			update.Blocks[0].Block.Number,
			update.ReplaceFrom,
		)
	}
	for index := 1; index < len(update.Blocks); index++ {
		previous := update.Blocks[index-1].Block.Number
		current := update.Blocks[index].Block.Number
		if previous == ^uint64(0) || current != previous+1 {
			return fmt.Errorf("%w: replacement blocks are not contiguous", ErrInvalidCanonicalUpdate)
		}
	}
	return nil
}

func validateCanonicalBundles(bundles map[uint64]domain.BlockBundle) error {
	numbers := sortedBlockNumbers(bundles)
	seenTransactions := make(map[common.Hash]struct{})

	for position, number := range numbers {
		bundle := bundles[number]
		if bundle.Block.Number != number {
			return fmt.Errorf("%w: block map key and block number differ", ErrInvalidCanonicalUpdate)
		}
		if position > 0 {
			previous := bundles[numbers[position-1]].Block
			if number != previous.Number+1 || bundle.Block.ParentHash != previous.Hash {
				return fmt.Errorf("%w: retained blocks do not form one canonical chain", ErrInvalidCanonicalUpdate)
			}
		}
		if bundle.Block.TransactionCount != len(bundle.Transactions) {
			return fmt.Errorf("%w: block %d transaction count does not match", ErrInvalidCanonicalUpdate, number)
		}
		if uint64(len(bundle.Transactions)) > uint64(^uint32(0)) {
			return fmt.Errorf("%w: block %d has too many transactions", ErrInvalidCanonicalUpdate, number)
		}

		blockTransactions := make(map[common.Hash]uint32, len(bundle.Transactions))
		for index, transaction := range bundle.Transactions {
			if transaction.BlockNumber != number || transaction.BlockHash != bundle.Block.Hash || transaction.Index != uint32(index) {
				return fmt.Errorf("%w: transaction %s has inconsistent block metadata", ErrInvalidCanonicalUpdate, transaction.Hash)
			}
			if _, found := seenTransactions[transaction.Hash]; found {
				return fmt.Errorf("%w: duplicate transaction %s", ErrInvalidCanonicalUpdate, transaction.Hash)
			}
			seenTransactions[transaction.Hash] = struct{}{}
			blockTransactions[transaction.Hash] = transaction.Index
		}

		seenLogs := make(map[uint32]struct{}, len(bundle.Events))
		for _, event := range bundle.Events {
			transactionIndex, found := blockTransactions[event.TransactionHash]
			if !found || event.TransactionIndex != transactionIndex {
				return fmt.Errorf("%w: event references an unknown transaction", ErrInvalidCanonicalUpdate)
			}
			if event.BlockNumber != number || event.BlockHash != bundle.Block.Hash {
				return fmt.Errorf("%w: event has inconsistent block metadata", ErrInvalidCanonicalUpdate)
			}
			if len(event.Topics) > 4 {
				return fmt.Errorf("%w: event has more than four topics", ErrInvalidCanonicalUpdate)
			}
			if _, found := seenLogs[event.LogIndex]; found {
				return fmt.Errorf("%w: block %d has duplicate log index %d", ErrInvalidCanonicalUpdate, number, event.LogIndex)
			}
			seenLogs[event.LogIndex] = struct{}{}
		}
	}
	return nil
}

func eventBeforeCursor(event domain.Event, cursor *domain.EventCursor) bool {
	if cursor == nil {
		return true
	}
	if event.BlockNumber != cursor.BlockNumber {
		return event.BlockNumber < cursor.BlockNumber
	}
	return event.LogIndex < cursor.LogIndex
}

func highestBundle(bundles map[uint64]domain.BlockBundle) (uint64, domain.BlockBundle, bool) {
	var highest uint64
	found := false
	for number := range bundles {
		if !found || number > highest {
			highest = number
			found = true
		}
	}
	return highest, bundles[highest], found
}

func sortedBlockNumbers(bundles map[uint64]domain.BlockBundle) []uint64 {
	numbers := make([]uint64, 0, len(bundles))
	for number := range bundles {
		numbers = append(numbers, number)
	}
	sort.Slice(numbers, func(left, right int) bool { return numbers[left] < numbers[right] })
	return numbers
}

func cloneBundles(source map[uint64]domain.BlockBundle) map[uint64]domain.BlockBundle {
	cloned := make(map[uint64]domain.BlockBundle, len(source))
	for number, bundle := range source {
		cloned[number] = cloneBundle(bundle)
	}
	return cloned
}

func cloneBundle(bundle domain.BlockBundle) domain.BlockBundle {
	cloned := domain.BlockBundle{Block: cloneBlock(bundle.Block)}
	cloned.Transactions = make([]domain.Transaction, len(bundle.Transactions))
	for index, transaction := range bundle.Transactions {
		cloned.Transactions[index] = cloneTransaction(transaction)
	}
	cloned.Events = make([]domain.Event, len(bundle.Events))
	for index, event := range bundle.Events {
		cloned.Events[index] = cloneEvent(event)
	}
	return cloned
}

func cloneBlock(block domain.Block) domain.Block {
	block.BaseFeePerGas = cloneBigInt(block.BaseFeePerGas)
	return block
}

func cloneTransaction(transaction domain.Transaction) domain.Transaction {
	transaction.Recipient = cloneAddress(transaction.Recipient)
	transaction.Value = cloneBigInt(transaction.Value)
	transaction.GasPrice = cloneBigInt(transaction.GasPrice)
	transaction.MaxFeePerGas = cloneBigInt(transaction.MaxFeePerGas)
	transaction.MaxPriorityFeePerGas = cloneBigInt(transaction.MaxPriorityFeePerGas)
	transaction.Input = append([]byte(nil), transaction.Input...)
	return transaction
}

func cloneEvent(event domain.Event) domain.Event {
	event.Topics = append([]common.Hash(nil), event.Topics...)
	event.Data = append([]byte(nil), event.Data...)
	return event
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func cloneAddress(value *common.Address) *common.Address {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ Store = (*Memory)(nil)
