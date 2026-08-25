package store

import (
	"fmt"
	"math/big"
	"time"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5/pgtype"
)

type postgresBlockRow struct {
	number           int64
	hash             []byte
	parentHash       []byte
	timestamp        int64
	gasLimit         int64
	gasUsed          int64
	baseFeePerGas    pgtype.Numeric
	transactionCount int32
	indexedAt        time.Time
}

func (row *postgresBlockRow) scanTargets() []any {
	return []any{
		&row.number,
		&row.hash,
		&row.parentHash,
		&row.timestamp,
		&row.gasLimit,
		&row.gasUsed,
		&row.baseFeePerGas,
		&row.transactionCount,
		&row.indexedAt,
	}
}

func (row postgresBlockRow) decode() (domain.Block, error) {
	number, err := decodePostgresUint64(row.number)
	if err != nil {
		return domain.Block{}, fmt.Errorf("decode block number: %w", err)
	}
	hash, err := decodeHash(row.hash)
	if err != nil {
		return domain.Block{}, fmt.Errorf("decode block hash: %w", err)
	}
	parentHash, err := decodeHash(row.parentHash)
	if err != nil {
		return domain.Block{}, fmt.Errorf("decode block parent hash: %w", err)
	}
	timestamp, err := decodePostgresUint64(row.timestamp)
	if err != nil {
		return domain.Block{}, fmt.Errorf("decode block timestamp: %w", err)
	}
	gasLimit, err := decodePostgresUint64(row.gasLimit)
	if err != nil {
		return domain.Block{}, fmt.Errorf("decode block gas limit: %w", err)
	}
	gasUsed, err := decodePostgresUint64(row.gasUsed)
	if err != nil {
		return domain.Block{}, fmt.Errorf("decode block gas used: %w", err)
	}
	baseFee, err := decodePostgresNumeric("block base fee", row.baseFeePerGas, false)
	if err != nil {
		return domain.Block{}, err
	}
	if row.transactionCount < 0 {
		return domain.Block{}, fmt.Errorf("%w: block transaction count is negative", ErrInconsistentData)
	}

	return domain.Block{
		Number:           number,
		Hash:             hash,
		ParentHash:       parentHash,
		Timestamp:        timestamp,
		GasLimit:         gasLimit,
		GasUsed:          gasUsed,
		BaseFeePerGas:    baseFee,
		TransactionCount: int(row.transactionCount),
		IndexedAt:        row.indexedAt,
	}, nil
}

type postgresTransactionRow struct {
	hash                 []byte
	blockNumber          int64
	blockHash            []byte
	index                int32
	typeID               int16
	nonce                int64
	sender               []byte
	recipient            []byte
	value                pgtype.Numeric
	gasLimit             int64
	gasPrice             pgtype.Numeric
	maxFeePerGas         pgtype.Numeric
	maxPriorityFeePerGas pgtype.Numeric
	input                []byte
}

func (row *postgresTransactionRow) scanTargets() []any {
	return []any{
		&row.hash,
		&row.blockNumber,
		&row.blockHash,
		&row.index,
		&row.typeID,
		&row.nonce,
		&row.sender,
		&row.recipient,
		&row.value,
		&row.gasLimit,
		&row.gasPrice,
		&row.maxFeePerGas,
		&row.maxPriorityFeePerGas,
		&row.input,
	}
}

func (row postgresTransactionRow) decode() (domain.Transaction, error) {
	hash, err := decodeHash(row.hash)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction hash: %w", err)
	}
	blockNumber, err := decodePostgresUint64(row.blockNumber)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction block number: %w", err)
	}
	blockHash, err := decodeHash(row.blockHash)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction block hash: %w", err)
	}
	index, err := decodePostgresUint32(row.index)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction index: %w", err)
	}
	typeID, err := decodePostgresUint8(row.typeID)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction type: %w", err)
	}
	nonce, err := decodePostgresUint64(row.nonce)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction nonce: %w", err)
	}
	sender, err := decodeAddress(row.sender)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction sender: %w", err)
	}
	recipient, err := decodeOptionalAddress(row.recipient)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction recipient: %w", err)
	}
	value, err := decodePostgresNumeric("transaction value", row.value, true)
	if err != nil {
		return domain.Transaction{}, err
	}
	gasLimit, err := decodePostgresUint64(row.gasLimit)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("decode transaction gas limit: %w", err)
	}
	gasPrice, err := decodePostgresNumeric("transaction gas price", row.gasPrice, false)
	if err != nil {
		return domain.Transaction{}, err
	}
	maxFee, err := decodePostgresNumeric("transaction max fee", row.maxFeePerGas, false)
	if err != nil {
		return domain.Transaction{}, err
	}
	maxPriorityFee, err := decodePostgresNumeric("transaction max priority fee", row.maxPriorityFeePerGas, false)
	if err != nil {
		return domain.Transaction{}, err
	}

	return domain.Transaction{
		Hash:                 hash,
		BlockNumber:          blockNumber,
		BlockHash:            blockHash,
		Index:                index,
		Type:                 typeID,
		Nonce:                nonce,
		Sender:               sender,
		Recipient:            recipient,
		Value:                value,
		GasLimit:             gasLimit,
		GasPrice:             gasPrice,
		MaxFeePerGas:         maxFee,
		MaxPriorityFeePerGas: maxPriorityFee,
		Input:                append([]byte(nil), row.input...),
	}, nil
}

type postgresEventRow struct {
	blockNumber      int64
	blockHash        []byte
	transactionHash  []byte
	transactionIndex int32
	logIndex         int32
	address          []byte
	topics           [][]byte
	data             []byte
}

func (row *postgresEventRow) scanTargets() []any {
	return []any{
		&row.blockNumber,
		&row.blockHash,
		&row.transactionHash,
		&row.transactionIndex,
		&row.logIndex,
		&row.address,
		&row.topics,
		&row.data,
	}
}

func (row postgresEventRow) decode() (domain.Event, error) {
	blockNumber, err := decodePostgresUint64(row.blockNumber)
	if err != nil {
		return domain.Event{}, fmt.Errorf("decode event block number: %w", err)
	}
	blockHash, err := decodeHash(row.blockHash)
	if err != nil {
		return domain.Event{}, fmt.Errorf("decode event block hash: %w", err)
	}
	transactionHash, err := decodeHash(row.transactionHash)
	if err != nil {
		return domain.Event{}, fmt.Errorf("decode event transaction hash: %w", err)
	}
	transactionIndex, err := decodePostgresUint32(row.transactionIndex)
	if err != nil {
		return domain.Event{}, fmt.Errorf("decode event transaction index: %w", err)
	}
	logIndex, err := decodePostgresUint32(row.logIndex)
	if err != nil {
		return domain.Event{}, fmt.Errorf("decode event log index: %w", err)
	}
	address, err := decodeAddress(row.address)
	if err != nil {
		return domain.Event{}, fmt.Errorf("decode event address: %w", err)
	}
	topics, err := decodeTopics(row.topics)
	if err != nil {
		return domain.Event{}, err
	}

	return domain.Event{
		BlockNumber:      blockNumber,
		BlockHash:        blockHash,
		TransactionHash:  transactionHash,
		TransactionIndex: transactionIndex,
		LogIndex:         logIndex,
		Address:          address,
		Topics:           topics,
		Data:             append([]byte(nil), row.data...),
	}, nil
}

func decodePostgresNumeric(name string, value pgtype.Numeric, required bool) (*big.Int, error) {
	if !value.Valid {
		if required {
			return nil, fmt.Errorf("%w: %s is null", ErrInconsistentData, name)
		}
		return nil, nil
	}
	if value.NaN || value.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("%w: %s is not finite", ErrInconsistentData, name)
	}
	if value.Exp < -78 || value.Exp > 78 {
		return nil, fmt.Errorf("%w: %s has unsupported exponent %d", ErrInconsistentData, name, value.Exp)
	}

	integer := new(big.Int)
	if value.Int != nil {
		integer.Set(value.Int)
	}
	exponent := int64(value.Exp)
	if exponent < 0 {
		exponent = -exponent
	}
	power := new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil)
	if value.Exp > 0 {
		integer.Mul(integer, power)
	} else if value.Exp < 0 {
		remainder := new(big.Int)
		integer.QuoRem(integer, power, remainder)
		if remainder.Sign() != 0 {
			return nil, fmt.Errorf("%w: %s is not an integer", ErrInconsistentData, name)
		}
	}
	if integer.Sign() < 0 || integer.BitLen() > 256 {
		return nil, fmt.Errorf("%w: %s is outside uint256", ErrInconsistentData, name)
	}
	return integer, nil
}

func decodeAddress(value []byte) (common.Address, error) {
	if len(value) != common.AddressLength {
		return common.Address{}, fmt.Errorf("%w: address has %d bytes", ErrInconsistentData, len(value))
	}
	return common.BytesToAddress(value), nil
}

func decodeOptionalAddress(value []byte) (*common.Address, error) {
	if value == nil {
		return nil, nil
	}
	address, err := decodeAddress(value)
	if err != nil {
		return nil, err
	}
	return &address, nil
}

func decodeTopics(values [][]byte) ([]common.Hash, error) {
	if len(values) > 4 {
		return nil, fmt.Errorf("%w: event has %d topics", ErrInconsistentData, len(values))
	}
	topics := make([]common.Hash, len(values))
	for index, value := range values {
		topic, err := decodeHash(value)
		if err != nil {
			return nil, fmt.Errorf("decode event topic %d: %w", index, err)
		}
		topics[index] = topic
	}
	return topics, nil
}

func decodePostgresUint32(value int32) (uint32, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: PostgreSQL integer is negative", ErrInconsistentData)
	}
	return uint32(value), nil
}

func decodePostgresUint8(value int16) (uint8, error) {
	if value < 0 || value > 255 {
		return 0, fmt.Errorf("%w: PostgreSQL smallint is outside uint8", ErrInconsistentData)
	}
	return uint8(value), nil
}
