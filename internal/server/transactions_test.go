package server

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
)

func TestTransactionByHashReturnsTransactionAndEvents(t *testing.T) {
	wantHash := common.HexToHash("0x1234")
	blockHash := common.HexToHash("0xabcd")
	recipient := common.HexToAddress("0x1000000000000000000000000000000000000001")
	emitter := common.HexToAddress("0x2000000000000000000000000000000000000002")
	queries := &queryServiceStub{
		transaction: func(_ context.Context, hash common.Hash) (domain.TransactionResult, error) {
			if hash != wantHash {
				t.Fatalf("Transaction() hash = %s, want %s", hash, wantHash)
			}
			return domain.TransactionResult{
				Transaction: domain.Transaction{
					Hash:                 wantHash,
					BlockNumber:          123,
					BlockHash:            blockHash,
					Index:                4,
					Type:                 2,
					Nonce:                9,
					Sender:               common.HexToAddress("0x3000000000000000000000000000000000000003"),
					Recipient:            &recipient,
					Value:                big.NewInt(1_000_000_000_000_000_000),
					GasLimit:             100_000,
					MaxFeePerGas:         big.NewInt(30_000_000_000),
					MaxPriorityFeePerGas: big.NewInt(2_000_000_000),
					Input:                []byte{0xde, 0xad},
				},
				Events: []domain.Event{
					{
						BlockNumber:      123,
						BlockHash:        blockHash,
						TransactionHash:  wantHash,
						TransactionIndex: 4,
						LogIndex:         7,
						Address:          emitter,
						Topics:           []common.Hash{common.HexToHash("0x01"), common.HexToHash("0x02")},
						Data:             []byte{0xca, 0xfe},
					},
				},
			}, nil
		},
	}

	response := serveTransactionRequest(t, queries, "/v1/transactions/"+wantHash.Hex())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	var body transactionResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Hash != wantHash.Hex() || body.BlockHash != blockHash.Hex() || body.BlockNumber != 123 {
		t.Fatalf("transaction identity response = %#v", body)
	}
	if body.ValueWei != "1000000000000000000" || body.Recipient == nil || *body.Recipient != recipient.Hex() {
		t.Fatalf("transaction value/recipient response = %#v", body)
	}
	if body.GasPriceWei != nil || body.MaxFeePerGasWei == nil || *body.MaxFeePerGasWei != "30000000000" {
		t.Fatalf("transaction fee response = %#v", body)
	}
	if body.Input != "0xdead" || len(body.Events) != 1 {
		t.Fatalf("transaction input/events response = %#v", body)
	}
	event := body.Events[0]
	if event.LogIndex != 7 || event.Address != emitter.Hex() || event.Data != "0xcafe" || len(event.Topics) != 2 {
		t.Fatalf("event response = %#v", event)
	}
}

func TestTransactionByHashRejectsInvalidHash(t *testing.T) {
	validWithoutPrefix := strings.Repeat("1", 2*common.HashLength)
	paths := []string{
		"/v1/transactions/0x1234",
		"/v1/transactions/" + validWithoutPrefix,
		"/v1/transactions/0X" + validWithoutPrefix,
		"/v1/transactions/0x" + strings.Repeat("g", 2*common.HashLength),
		"/v1/transactions/0x" + strings.Repeat("1", 2*common.HashLength+2),
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			called := false
			queries := &queryServiceStub{
				transaction: func(context.Context, common.Hash) (domain.TransactionResult, error) {
					called = true
					return domain.TransactionResult{}, nil
				},
			}

			response := serveTransactionRequest(t, queries, path)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if called {
				t.Fatal("Transaction() was called for an invalid hash")
			}
			if body := decodeErrorResponse(t, response); body.Error.Code != "invalid_transaction_hash" {
				t.Fatalf("error response = %#v", body)
			}
		})
	}
}

func TestTransactionByHashReturnsNotFound(t *testing.T) {
	hash := common.HexToHash("0x1234")
	queries := &queryServiceStub{
		transaction: func(context.Context, common.Hash) (domain.TransactionResult, error) {
			return domain.TransactionResult{}, service.ErrNotFound
		},
	}

	response := serveTransactionRequest(t, queries, "/v1/transactions/"+hash.Hex())
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if body := decodeErrorResponse(t, response); body.Error.Code != "transaction_not_found" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestTransactionByHashRejectsMissingStoredValue(t *testing.T) {
	hash := common.HexToHash("0x1234")
	queries := &queryServiceStub{
		transaction: func(context.Context, common.Hash) (domain.TransactionResult, error) {
			return domain.TransactionResult{Transaction: domain.Transaction{Hash: hash}}, nil
		},
	}

	response := serveTransactionRequest(t, queries, "/v1/transactions/"+hash.Hex())
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if body := decodeErrorResponse(t, response); body.Error.Code != "internal_error" {
		t.Fatalf("error response = %#v", body)
	}
}

func serveTransactionRequest(t *testing.T, queries service.Queries, path string) *httptest.ResponseRecorder {
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
