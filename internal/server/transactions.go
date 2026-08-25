package server

import (
	"errors"
	"net/http"
	"strings"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var (
	errInvalidTransactionHash  = errors.New("invalid transaction hash")
	errMissingTransactionValue = errors.New("transaction value is missing")
)

type transactionResponse struct {
	Hash                    string          `json:"hash"`
	BlockNumber             uint64          `json:"block_number"`
	BlockHash               string          `json:"block_hash"`
	Index                   uint32          `json:"index"`
	Type                    uint8           `json:"type"`
	Nonce                   uint64          `json:"nonce"`
	Sender                  string          `json:"sender"`
	Recipient               *string         `json:"recipient"`
	ValueWei                string          `json:"value_wei"`
	GasLimit                uint64          `json:"gas_limit"`
	GasPriceWei             *string         `json:"gas_price_wei"`
	MaxFeePerGasWei         *string         `json:"max_fee_per_gas_wei"`
	MaxPriorityFeePerGasWei *string         `json:"max_priority_fee_per_gas_wei"`
	Input                   string          `json:"input"`
	Events                  []eventResponse `json:"events"`
}

func (s Server) transactionByHash(writer http.ResponseWriter, request *http.Request) {
	rawHash := chi.URLParam(request, "hash")
	hash, err := parseTransactionHash(rawHash)
	if err != nil {
		s.writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_transaction_hash",
			"transaction hash must be a 0x-prefixed 32-byte hexadecimal value",
		)
		return
	}

	result, err := s.QueryService.Transaction(request.Context(), hash)
	if errors.Is(err, service.ErrNotFound) {
		s.writeError(writer, request, http.StatusNotFound, "transaction_not_found", "transaction was not found")
		return
	}
	if err != nil {
		s.logTransactionError(request, hash, err)
		s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	response, err := newTransactionResponse(result)
	if err != nil {
		s.logTransactionError(request, hash, err)
		s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.writeJSON(writer, request, http.StatusOK, response)
}

func (s Server) logTransactionError(request *http.Request, hash common.Hash, err error) {
	s.Logger.Error().
		Err(err).
		Str("request_id", middleware.GetReqID(request.Context())).
		Str("transaction_hash", hash.Hex()).
		Msg("query transaction")
}

func parseTransactionHash(value string) (common.Hash, error) {
	if len(value) != 2+2*common.HashLength || !strings.HasPrefix(value, "0x") || !common.IsHexHash(value) {
		return common.Hash{}, errInvalidTransactionHash
	}
	return common.HexToHash(value), nil
}

func newTransactionResponse(result domain.TransactionResult) (transactionResponse, error) {
	transaction := result.Transaction
	if transaction.Value == nil {
		return transactionResponse{}, errMissingTransactionValue
	}

	events := make([]eventResponse, len(result.Events))
	for index, event := range result.Events {
		events[index] = newEventResponse(event)
	}
	return transactionResponse{
		Hash:                    transaction.Hash.Hex(),
		BlockNumber:             transaction.BlockNumber,
		BlockHash:               transaction.BlockHash.Hex(),
		Index:                   transaction.Index,
		Type:                    transaction.Type,
		Nonce:                   transaction.Nonce,
		Sender:                  transaction.Sender.Hex(),
		Recipient:               optionalAddressString(transaction.Recipient),
		ValueWei:                transaction.Value.String(),
		GasLimit:                transaction.GasLimit,
		GasPriceWei:             optionalBigIntString(transaction.GasPrice),
		MaxFeePerGasWei:         optionalBigIntString(transaction.MaxFeePerGas),
		MaxPriorityFeePerGasWei: optionalBigIntString(transaction.MaxPriorityFeePerGas),
		Input:                   hexutil.Encode(transaction.Input),
		Events:                  events,
	}, nil
}
