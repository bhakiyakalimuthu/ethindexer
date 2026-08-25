package indexer

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestFetcherFetchBlockBuildsCompleteBundle(t *testing.T) {
	const blockNumber = uint64(42)

	block, expectedSender := signedTestBlock(t, blockNumber)
	blockHash := block.Hash()
	contract := common.HexToAddress("0x1000000000000000000000000000000000000001")
	topic := common.HexToHash("0xfeed")
	logs := []types.Log{
		{
			Address:     contract,
			Topics:      []common.Hash{topic},
			Data:        []byte{0x02},
			BlockNumber: blockNumber,
			TxHash:      block.Transactions()[1].Hash(),
			TxIndex:     1,
			BlockHash:   blockHash,
			Index:       1,
		},
		{
			Address:     contract,
			Topics:      []common.Hash{topic},
			Data:        []byte{0x01},
			BlockNumber: blockNumber,
			TxHash:      block.Transactions()[0].Hash(),
			TxIndex:     0,
			BlockHash:   blockHash,
			Index:       0,
		},
		{
			Address:     contract,
			BlockNumber: blockNumber,
			TxHash:      block.Transactions()[2].Hash(),
			TxIndex:     2,
			BlockHash:   blockHash,
			Index:       2,
		},
	}

	var blockCalls, logCalls int
	chain := &fakeChainReader{
		blockByNumber: func(_ context.Context, number *big.Int) (*types.Block, error) {
			blockCalls++
			if !number.IsUint64() || number.Uint64() != blockNumber {
				t.Fatalf("requested block = %s", number)
			}
			return block, nil
		},
		logsByBlockHash: func(_ context.Context, hash common.Hash) ([]types.Log, error) {
			logCalls++
			if hash != blockHash {
				t.Fatalf("requested log block hash = %s, want %s", hash, blockHash)
			}
			return logs, nil
		},
	}

	indexedAt := time.Date(2026, time.August, 25, 8, 30, 0, 0, time.FixedZone("test", 2*60*60))
	fetcher := NewFetcher(chain, 1)
	fetcher.now = func() time.Time { return indexedAt }

	bundle, err := fetcher.FetchBlock(context.Background(), blockNumber)
	if err != nil {
		t.Fatalf("FetchBlock() error = %v", err)
	}

	if blockCalls != 1 || logCalls != 1 {
		t.Fatalf("RPC calls: blocks=%d logs=%d, want one each", blockCalls, logCalls)
	}
	if bundle.Block.Number != blockNumber || bundle.Block.Hash != blockHash {
		t.Fatalf("block = %#v", bundle.Block)
	}
	if !bundle.Block.IndexedAt.Equal(indexedAt.UTC()) {
		t.Fatalf("indexed at = %s, want %s", bundle.Block.IndexedAt, indexedAt.UTC())
	}
	if bundle.Block.TransactionCount != 3 || len(bundle.Transactions) != 3 {
		t.Fatalf("transactions = %d, count = %d", len(bundle.Transactions), bundle.Block.TransactionCount)
	}
	if bundle.Transactions[0].Sender != expectedSender {
		t.Fatalf("sender = %s, want %s", bundle.Transactions[0].Sender, expectedSender)
	}
	if bundle.Transactions[0].GasPrice == nil || bundle.Transactions[0].GasPrice.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("legacy gas price = %v", bundle.Transactions[0].GasPrice)
	}
	if bundle.Transactions[0].MaxFeePerGas != nil || bundle.Transactions[0].MaxPriorityFeePerGas != nil {
		t.Fatal("legacy transaction unexpectedly has dynamic fee fields")
	}
	if bundle.Transactions[1].GasPrice != nil {
		t.Fatal("dynamic-fee transaction unexpectedly has legacy gas price")
	}
	if bundle.Transactions[1].MaxFeePerGas == nil || bundle.Transactions[1].MaxPriorityFeePerGas == nil ||
		bundle.Transactions[1].MaxFeePerGas.Cmp(big.NewInt(30)) != 0 ||
		bundle.Transactions[1].MaxPriorityFeePerGas.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf(
			"dynamic fees = cap %v, tip %v",
			bundle.Transactions[1].MaxFeePerGas,
			bundle.Transactions[1].MaxPriorityFeePerGas,
		)
	}
	if bundle.Transactions[2].Input == nil || len(bundle.Transactions[2].Input) != 0 {
		t.Fatalf("empty transaction input = %#v, want non-nil empty bytes", bundle.Transactions[2].Input)
	}
	if len(bundle.Events) != 3 ||
		bundle.Events[0].LogIndex != 0 ||
		bundle.Events[1].LogIndex != 1 ||
		bundle.Events[2].LogIndex != 2 {
		t.Fatalf("events are not sorted by log index: %#v", bundle.Events)
	}
	if bundle.Events[2].Topics == nil || len(bundle.Events[2].Topics) != 0 ||
		bundle.Events[2].Data == nil || len(bundle.Events[2].Data) != 0 {
		t.Fatalf("empty event payload = %#v, want non-nil empty collections", bundle.Events[2])
	}
	if bundle.Events[0].TransactionHash != block.Transactions()[0].Hash() {
		t.Fatalf("first event transaction = %s", bundle.Events[0].TransactionHash)
	}

	// Returned records own mutable byte slices rather than aliasing RPC data.
	block.Transactions()[0].Data()[0] = 0xff
	logs[1].Data[0] = 0xff
	logs[1].Topics[0] = common.Hash{}
	if bundle.Transactions[0].Input[0] == 0xff || bundle.Events[0].Data[0] == 0xff || bundle.Events[0].Topics[0] == (common.Hash{}) {
		t.Fatal("mapped transaction or event data aliases RPC-owned memory")
	}
}

func TestFetcherRejectsLogForUnknownTransaction(t *testing.T) {
	const blockNumber = uint64(7)
	block := types.NewBlockWithHeader(&types.Header{Number: new(big.Int).SetUint64(blockNumber)})

	chain := &fakeChainReader{
		blockByNumber: func(context.Context, *big.Int) (*types.Block, error) {
			return block, nil
		},
		logsByBlockHash: func(context.Context, common.Hash) ([]types.Log, error) {
			return []types.Log{{
				BlockNumber: blockNumber,
				BlockHash:   block.Hash(),
				TxHash:      common.HexToHash("0xdead"),
			}}, nil
		},
	}

	_, err := NewFetcher(chain, 1).FetchBlock(context.Background(), blockNumber)
	if !errors.Is(err, ErrInconsistentBlockData) {
		t.Fatalf("FetchBlock() error = %v, want ErrInconsistentBlockData", err)
	}
}

func TestFetcherDoesNotFetchLogsWhenBlockFetchFails(t *testing.T) {
	expectedError := errors.New("RPC unavailable")
	logCalls := 0
	chain := &fakeChainReader{
		blockByNumber: func(context.Context, *big.Int) (*types.Block, error) {
			return nil, expectedError
		},
		logsByBlockHash: func(context.Context, common.Hash) ([]types.Log, error) {
			logCalls++
			return nil, nil
		},
	}

	_, err := NewFetcher(chain, 1).FetchBlock(context.Background(), 1)
	if !errors.Is(err, expectedError) {
		t.Fatalf("FetchBlock() error = %v, want %v", err, expectedError)
	}
	if logCalls != 0 {
		t.Fatalf("log calls = %d, want zero", logCalls)
	}
}

func TestFetcherRejectsMismatchedBlockNumber(t *testing.T) {
	chain := &fakeChainReader{
		blockByNumber: func(context.Context, *big.Int) (*types.Block, error) {
			return types.NewBlockWithHeader(&types.Header{Number: big.NewInt(99)}), nil
		},
	}

	_, err := NewFetcher(chain, 1).FetchBlock(context.Background(), 100)
	if !errors.Is(err, ErrInconsistentBlockData) {
		t.Fatalf("FetchBlock() error = %v, want ErrInconsistentBlockData", err)
	}
}

func signedTestBlock(t *testing.T, number uint64) (*types.Block, common.Address) {
	t.Helper()

	key, err := crypto.HexToECDSA("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	chainID := big.NewInt(1)
	signer := types.LatestSignerForChainID(chainID)
	recipient := common.HexToAddress("0x2000000000000000000000000000000000000002")

	legacy := types.MustSignNewTx(key, signer, &types.LegacyTx{
		Nonce:    1,
		To:       &recipient,
		Value:    big.NewInt(10),
		Gas:      21_000,
		GasPrice: big.NewInt(2),
		Data:     []byte{0x01},
	})
	dynamic := types.MustSignNewTx(key, signer, &types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     2,
		To:        &recipient,
		Value:     big.NewInt(20),
		Gas:       30_000,
		GasFeeCap: big.NewInt(30),
		GasTipCap: big.NewInt(2),
		Data:      []byte{0x02},
	})
	emptyInput := types.MustSignNewTx(key, signer, &types.LegacyTx{
		Nonce:    3,
		To:       &recipient,
		Value:    big.NewInt(0),
		Gas:      21_000,
		GasPrice: big.NewInt(2),
	})

	header := &types.Header{
		Number:     new(big.Int).SetUint64(number),
		ParentHash: common.HexToHash("0xbeef"),
		Time:       1_777_777_777,
		GasLimit:   30_000_000,
		GasUsed:    51_000,
		BaseFee:    big.NewInt(1_000_000_000),
	}
	block := types.NewBlockWithHeader(header).WithBody(types.Body{
		Transactions: []*types.Transaction{legacy, dynamic, emptyInput},
	})
	return block, crypto.PubkeyToAddress(key.PublicKey)
}

type fakeChainReader struct {
	blockByNumber   func(context.Context, *big.Int) (*types.Block, error)
	logsByBlockHash func(context.Context, common.Hash) ([]types.Log, error)
}

func (f *fakeChainReader) ChainID(context.Context) (*big.Int, error) {
	return nil, errors.New("unexpected ChainID call")
}

func (f *fakeChainReader) BlockNumber(context.Context) (uint64, error) {
	return 0, errors.New("unexpected BlockNumber call")
}

func (f *fakeChainReader) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if f.blockByNumber == nil {
		return nil, errors.New("unexpected BlockByNumber call")
	}
	return f.blockByNumber(ctx, number)
}

func (f *fakeChainReader) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, errors.New("unexpected HeaderByNumber call")
}

func (f *fakeChainReader) LogsByBlockHash(ctx context.Context, hash common.Hash) ([]types.Log, error) {
	if f.logsByBlockHash == nil {
		return nil, errors.New("unexpected LogsByBlockHash call")
	}
	return f.logsByBlockHash(ctx, hash)
}
