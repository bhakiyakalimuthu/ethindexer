package server

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
)

func TestBlockByNumberReturnsBlock(t *testing.T) {
	indexedAt := time.Date(2026, time.August, 27, 12, 30, 0, 0, time.FixedZone("test", 2*60*60))
	wantHash := common.HexToHash("0x1234")
	wantTransactionHash := common.HexToHash("0xabcd")
	queries := &queryServiceStub{
		block: func(_ context.Context, number uint64) (domain.BlockResult, error) {
			if number != 123 {
				t.Fatalf("Block() number = %d, want 123", number)
			}
			return domain.BlockResult{
				Block: domain.Block{
					Number:           number,
					Hash:             wantHash,
					ParentHash:       common.HexToHash("0x1222"),
					Timestamp:        1_700_000_000,
					GasLimit:         30_000_000,
					GasUsed:          20_000_000,
					BaseFeePerGas:    big.NewInt(10_000_000_000),
					TransactionCount: 1,
					IndexedAt:        indexedAt,
				},
				TransactionHashes: []common.Hash{wantTransactionHash},
			}, nil
		},
	}

	response := serveBlockRequest(t, queries, "/v1/blocks/123")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}

	var body blockResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Number != 123 || body.Hash != wantHash.Hex() || body.ParentHash != common.HexToHash("0x1222").Hex() {
		t.Fatalf("block identity response = %#v", body)
	}
	if body.BaseFeePerGasWei == nil || *body.BaseFeePerGasWei != "10000000000" {
		t.Fatalf("base fee response = %#v", body.BaseFeePerGasWei)
	}
	if len(body.TransactionHashes) != 1 || body.TransactionHashes[0] != wantTransactionHash.Hex() {
		t.Fatalf("transaction hashes response = %#v", body.TransactionHashes)
	}
	if !body.IndexedAt.Equal(indexedAt.UTC()) {
		t.Fatalf("indexed at = %s, want %s", body.IndexedAt, indexedAt.UTC())
	}
}

func TestBlockByNumberRejectsInvalidPath(t *testing.T) {
	paths := []string{
		"/v1/blocks/-1",
		"/v1/blocks/0x10",
		"/v1/blocks/1.5",
		"/v1/blocks/%2B1",
		"/v1/blocks/18446744073709551616",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			called := false
			queries := &queryServiceStub{
				block: func(context.Context, uint64) (domain.BlockResult, error) {
					called = true
					return domain.BlockResult{}, nil
				},
			}

			response := serveBlockRequest(t, queries, path)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if called {
				t.Fatal("Block() was called for an invalid path")
			}
			body := decodeErrorResponse(t, response)
			if body.Error.Code != "invalid_block_number" || body.Error.RequestID == "" {
				t.Fatalf("error response = %#v", body)
			}
		})
	}
}

func TestBlockByNumberReturnsNotFound(t *testing.T) {
	queries := &queryServiceStub{
		block: func(context.Context, uint64) (domain.BlockResult, error) {
			return domain.BlockResult{}, service.ErrNotFound
		},
	}

	response := serveBlockRequest(t, queries, "/v1/blocks/99")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if body := decodeErrorResponse(t, response); body.Error.Code != "block_not_found" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestBlockByNumberHidesInternalError(t *testing.T) {
	queries := &queryServiceStub{
		block: func(context.Context, uint64) (domain.BlockResult, error) {
			return domain.BlockResult{}, errors.New("database password leaked")
		},
	}

	response := serveBlockRequest(t, queries, "/v1/blocks/99")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	body := decodeErrorResponse(t, response)
	if body.Error.Code != "internal_error" || body.Error.Message != "internal server error" {
		t.Fatalf("error response = %#v", body)
	}
}

func serveBlockRequest(t *testing.T, queries service.Queries, path string) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewServer(Server{
		QueryService:   queries,
		Logger:         zerolog.Nop(),
		RequestTimeout: time.Second,
	})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeErrorResponse(t *testing.T, response *httptest.ResponseRecorder) errorResponse {
	t.Helper()

	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return body
}

type queryServiceStub struct {
	block func(context.Context, uint64) (domain.BlockResult, error)
}

func (stub *queryServiceStub) Block(ctx context.Context, number uint64) (domain.BlockResult, error) {
	return stub.block(ctx, number)
}

func (stub *queryServiceStub) Transaction(context.Context, common.Hash) (domain.TransactionResult, error) {
	return domain.TransactionResult{}, errors.New("unexpected Transaction call")
}

func (stub *queryServiceStub) Events(context.Context, domain.EventQuery) (domain.EventPage, error) {
	return domain.EventPage{}, errors.New("unexpected Events call")
}
