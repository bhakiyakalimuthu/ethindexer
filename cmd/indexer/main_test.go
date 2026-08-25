package main

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"ethindexer/internal/store"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestRunReturnsStartupErrors(t *testing.T) {
	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")

	err := run(context.Background(), []string{"-config", missingConfig})
	if err == nil || !strings.Contains(err.Error(), "load configuration") {
		t.Fatalf("run() error = %v, want configuration error", err)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	err := run(context.Background(), []string{"unexpected"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("run() error = %v, want unexpected arguments error", err)
	}
}

func TestVerifyLatestBlockFetchesStoresAndReadsBack(t *testing.T) {
	const blockNumber = uint64(123)
	block := types.NewBlockWithHeader(&types.Header{
		Number:     new(big.Int).SetUint64(blockNumber),
		ParentHash: common.HexToHash("0x122"),
	})
	chain := &verificationChain{block: block}
	memory := store.NewMemory()

	stored, eventCount, err := verifyLatestBlock(context.Background(), chain, memory, 1)
	if err != nil {
		t.Fatalf("verifyLatestBlock() error = %v", err)
	}
	if stored.Block.Number != blockNumber || stored.Block.Hash != block.Hash() {
		t.Fatalf("stored block = %#v", stored.Block)
	}
	if eventCount != 0 {
		t.Fatalf("event count = %d, want zero", eventCount)
	}
	if chain.blockCalls != 1 || chain.logCalls != 1 {
		t.Fatalf("RPC calls: blocks=%d logs=%d", chain.blockCalls, chain.logCalls)
	}
}

type verificationChain struct {
	block      *types.Block
	blockCalls int
	logCalls   int
}

func (c *verificationChain) ChainID(context.Context) (*big.Int, error) {
	return nil, errors.New("unexpected ChainID call")
}

func (c *verificationChain) BlockNumber(context.Context) (uint64, error) {
	return c.block.NumberU64(), nil
}

func (c *verificationChain) BlockByNumber(_ context.Context, number *big.Int) (*types.Block, error) {
	c.blockCalls++
	if !number.IsUint64() || number.Uint64() != c.block.NumberU64() {
		return nil, errors.New("unexpected block number")
	}
	return c.block, nil
}

func (c *verificationChain) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, errors.New("unexpected HeaderByNumber call")
}

func (c *verificationChain) LogsByBlockHash(_ context.Context, hash common.Hash) ([]types.Log, error) {
	c.logCalls++
	if hash != c.block.Hash() {
		return nil, errors.New("unexpected block hash")
	}
	return nil, nil
}
