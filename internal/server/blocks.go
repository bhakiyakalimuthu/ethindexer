package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type blockResponse struct {
	Number            uint64    `json:"number"`
	Hash              string    `json:"hash"`
	ParentHash        string    `json:"parent_hash"`
	Timestamp         uint64    `json:"timestamp"`
	GasLimit          uint64    `json:"gas_limit"`
	GasUsed           uint64    `json:"gas_used"`
	BaseFeePerGasWei  *string   `json:"base_fee_per_gas_wei"`
	TransactionCount  int       `json:"transaction_count"`
	TransactionHashes []string  `json:"transaction_hashes"`
	IndexedAt         time.Time `json:"indexed_at"`
}

func (s Server) blockByNumber(writer http.ResponseWriter, request *http.Request) {
	rawNumber := chi.URLParam(request, "number")
	number, err := parseBlockNumber(rawNumber)
	if err != nil {
		s.writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_block_number",
			"block number must be an unsigned decimal integer",
		)
		return
	}

	result, err := s.QueryService.Block(request.Context(), number)
	if errors.Is(err, service.ErrNotFound) {
		s.writeError(writer, request, http.StatusNotFound, "block_not_found", "block was not found")
		return
	}
	if err != nil {
		s.Logger.Error().
			Err(err).
			Str("request_id", middleware.GetReqID(request.Context())).
			Uint64("block_number", number).
			Msg("query block")
		s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	s.writeJSON(writer, request, http.StatusOK, newBlockResponse(result))
}

func parseBlockNumber(value string) (uint64, error) {
	if value == "" {
		return 0, strconv.ErrSyntax
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	return strconv.ParseUint(value, 10, 64)
}

func newBlockResponse(result domain.BlockResult) blockResponse {
	transactionHashes := make([]string, len(result.TransactionHashes))
	for index, hash := range result.TransactionHashes {
		transactionHashes[index] = hash.Hex()
	}

	return blockResponse{
		Number:            result.Block.Number,
		Hash:              result.Block.Hash.Hex(),
		ParentHash:        result.Block.ParentHash.Hex(),
		Timestamp:         result.Block.Timestamp,
		GasLimit:          result.Block.GasLimit,
		GasUsed:           result.Block.GasUsed,
		BaseFeePerGasWei:  optionalBigIntString(result.Block.BaseFeePerGas),
		TransactionCount:  result.Block.TransactionCount,
		TransactionHashes: transactionHashes,
		IndexedAt:         result.Block.IndexedAt.UTC(),
	}
}
