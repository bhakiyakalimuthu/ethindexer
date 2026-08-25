package indexer

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/ethereum"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Fetcher reads and validates one complete block bundle. Fetching a block
// includes all transactions, so only one additional block-hash-filtered RPC
// call is needed to obtain every raw EVM log in that block.
type Fetcher struct {
	chain   ethereum.Reader
	chainID *big.Int
	signer  types.Signer
	now     func() time.Time
}

func NewFetcher(chain ethereum.Reader, chainID uint64) *Fetcher {
	id := new(big.Int).SetUint64(chainID)
	return &Fetcher{
		chain:   chain,
		chainID: id,
		signer:  types.LatestSignerForChainID(id),
		now:     time.Now,
	}
}

func (f *Fetcher) FetchBlock(ctx context.Context, number uint64) (domain.BlockBundle, error) {
	requestedNumber := new(big.Int).SetUint64(number)
	block, err := f.chain.BlockByNumber(ctx, requestedNumber)
	if err != nil {
		return domain.BlockBundle{}, fmt.Errorf("fetch block %d: %w", number, err)
	}
	if block == nil {
		return domain.BlockBundle{}, fmt.Errorf("%w: block %d response is nil", ErrInconsistentBlockData, number)
	}
	if err := validateBlockNumber(block, number); err != nil {
		return domain.BlockBundle{}, err
	}

	blockHash := block.Hash()
	transactions, positions, err := f.mapTransactions(block)
	if err != nil {
		return domain.BlockBundle{}, err
	}

	logs, err := f.chain.LogsByBlockHash(ctx, blockHash)
	if err != nil {
		return domain.BlockBundle{}, fmt.Errorf("fetch logs for block %d (%s): %w", number, blockHash, err)
	}
	events, err := mapEvents(number, blockHash, logs, positions)
	if err != nil {
		return domain.BlockBundle{}, err
	}

	return domain.BlockBundle{
		Block: domain.Block{
			Number:           number,
			Hash:             blockHash,
			ParentHash:       block.ParentHash(),
			Timestamp:        block.Time(),
			GasLimit:         block.GasLimit(),
			GasUsed:          block.GasUsed(),
			BaseFeePerGas:    block.BaseFee(),
			TransactionCount: len(transactions),
			IndexedAt:        f.now().UTC(),
		},
		Transactions: transactions,
		Events:       events,
	}, nil
}

func validateBlockNumber(block *types.Block, requested uint64) error {
	number := block.Number()
	if number == nil || !number.IsUint64() || number.Uint64() != requested {
		actual := "<nil>"
		if number != nil {
			actual = number.String()
		}
		return fmt.Errorf(
			"%w: requested block %d, received block %s",
			ErrInconsistentBlockData,
			requested,
			actual,
		)
	}
	return nil
}

func (f *Fetcher) mapTransactions(block *types.Block) ([]domain.Transaction, map[common.Hash]uint32, error) {
	blockNumber := block.NumberU64()
	blockHash := block.Hash()
	ethereumTransactions := block.Transactions()
	if uint64(len(ethereumTransactions)) > uint64(^uint32(0)) {
		return nil, nil, fmt.Errorf("%w: block %d has too many transactions", ErrInconsistentBlockData, blockNumber)
	}

	transactions := make([]domain.Transaction, 0, len(ethereumTransactions))
	positions := make(map[common.Hash]uint32, len(ethereumTransactions))
	for index, transaction := range ethereumTransactions {
		if transaction == nil {
			return nil, nil, fmt.Errorf(
				"%w: block %d contains a nil transaction at index %d",
				ErrInconsistentBlockData,
				blockNumber,
				index,
			)
		}

		if transaction.Protected() && transaction.ChainId().Cmp(f.chainID) != 0 {
			return nil, nil, fmt.Errorf(
				"%w: transaction %s has chain ID %s, expected %s",
				ErrInconsistentBlockData,
				transaction.Hash(),
				transaction.ChainId(),
				f.chainID,
			)
		}

		sender, err := types.Sender(f.signer, transaction)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: derive sender for transaction %s: %w",
				ErrInconsistentBlockData,
				transaction.Hash(),
				err,
			)
		}

		position := uint32(index)
		hash := transaction.Hash()
		if _, exists := positions[hash]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate transaction %s", ErrInconsistentBlockData, hash)
		}
		positions[hash] = position

		mapped := domain.Transaction{
			Hash:        hash,
			BlockNumber: blockNumber,
			BlockHash:   blockHash,
			Index:       position,
			Type:        transaction.Type(),
			Nonce:       transaction.Nonce(),
			Sender:      sender,
			Recipient:   transaction.To(),
			Value:       transaction.Value(),
			GasLimit:    transaction.Gas(),
			Input:       append([]byte(nil), transaction.Data()...),
		}

		switch transaction.Type() {
		case types.LegacyTxType, types.AccessListTxType:
			mapped.GasPrice = transaction.GasPrice()
		default:
			mapped.MaxFeePerGas = transaction.GasFeeCap()
			mapped.MaxPriorityFeePerGas = transaction.GasTipCap()
		}

		transactions = append(transactions, mapped)
	}

	return transactions, positions, nil
}

func mapEvents(
	blockNumber uint64,
	blockHash common.Hash,
	logs []types.Log,
	transactionPositions map[common.Hash]uint32,
) ([]domain.Event, error) {
	events := make([]domain.Event, 0, len(logs))
	seenLogIndexes := make(map[uint32]struct{}, len(logs))

	for _, log := range logs {
		if log.Removed {
			return nil, fmt.Errorf("%w: block %d contains a removed log", ErrInconsistentBlockData, blockNumber)
		}
		if log.BlockHash != blockHash || log.BlockNumber != blockNumber {
			return nil, fmt.Errorf(
				"%w: log %d does not belong to block %d (%s)",
				ErrInconsistentBlockData,
				log.Index,
				blockNumber,
				blockHash,
			)
		}
		if uint64(log.Index) > uint64(^uint32(0)) || uint64(log.TxIndex) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("%w: log or transaction index exceeds uint32", ErrInconsistentBlockData)
		}
		if len(log.Topics) > 4 {
			return nil, fmt.Errorf(
				"%w: log %d has %d topics, maximum is 4",
				ErrInconsistentBlockData,
				log.Index,
				len(log.Topics),
			)
		}

		transactionIndex, exists := transactionPositions[log.TxHash]
		if !exists {
			return nil, fmt.Errorf(
				"%w: log %d references unknown transaction %s",
				ErrInconsistentBlockData,
				log.Index,
				log.TxHash,
			)
		}
		if uint32(log.TxIndex) != transactionIndex {
			return nil, fmt.Errorf(
				"%w: log %d transaction index is %d, expected %d",
				ErrInconsistentBlockData,
				log.Index,
				log.TxIndex,
				transactionIndex,
			)
		}

		logIndex := uint32(log.Index)
		if _, exists := seenLogIndexes[logIndex]; exists {
			return nil, fmt.Errorf("%w: duplicate log index %d", ErrInconsistentBlockData, log.Index)
		}
		seenLogIndexes[logIndex] = struct{}{}

		events = append(events, domain.Event{
			BlockNumber:      blockNumber,
			BlockHash:        blockHash,
			TransactionHash:  log.TxHash,
			TransactionIndex: transactionIndex,
			LogIndex:         logIndex,
			Address:          log.Address,
			Topics:           append([]common.Hash(nil), log.Topics...),
			Data:             append([]byte(nil), log.Data...),
		})
	}

	sort.Slice(events, func(left, right int) bool {
		return events[left].LogIndex < events[right].LogIndex
	})
	return events, nil
}
