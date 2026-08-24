package domain

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Block is the canonical block data retained by the indexer.
type Block struct {
	Number           uint64
	Hash             common.Hash
	ParentHash       common.Hash
	Timestamp        uint64
	GasLimit         uint64
	GasUsed          uint64
	BaseFeePerGas    *big.Int
	TransactionCount int
	IndexedAt        time.Time
}

// Transaction contains the stable transaction fields needed by the V1 API.
// Fee fields are pointers because they are not present on every transaction type.
type Transaction struct {
	Hash                 common.Hash
	BlockNumber          uint64
	BlockHash            common.Hash
	Index                uint32
	Type                 uint8
	Nonce                uint64
	Sender               common.Address
	Recipient            *common.Address
	Value                *big.Int
	GasLimit             uint64
	GasPrice             *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
	Input                []byte
}

// Event is a raw EVM log. Address is the emitting contract, not an address
// decoded from a topic or from the log data.
type Event struct {
	BlockNumber      uint64
	BlockHash        common.Hash
	TransactionHash  common.Hash
	TransactionIndex uint32
	LogIndex         uint32
	Address          common.Address
	Topics           []common.Hash
	Data             []byte
}

// BlockBundle is the atomic unit fetched from Ethereum and written to storage.
type BlockBundle struct {
	Block        Block
	Transactions []Transaction
	Events       []Event
}

type BlockResult struct {
	Block             Block
	TransactionHashes []common.Hash
}

type TransactionResult struct {
	Transaction Transaction
	Events      []Event
}

type EventCursor struct {
	BlockNumber uint64
	BlockHash   common.Hash
	LogIndex    uint32
}

type EventQuery struct {
	Address common.Address
	Limit   uint32
	Before  *EventCursor
}

type EventPage struct {
	Events     []Event
	NextCursor *EventCursor
}

type ChainTip struct {
	Number uint64
	Hash   common.Hash
}

// CanonicalUpdate describes one pre-validated atomic chain update. Storage
// removes blocks at and above ReplaceFrom, inserts Blocks in ascending order,
// and prunes blocks below RetainFrom in the same transaction.
type CanonicalUpdate struct {
	ChainID     uint64
	ReplaceFrom uint64
	RetainFrom  uint64
	Blocks      []BlockBundle
	SyncedAt    time.Time
}
