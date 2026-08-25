package server

import (
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/go-chi/chi/v5/middleware"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func (s Server) writeJSON(writer http.ResponseWriter, request *http.Request, status int, response any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		s.Logger.Error().
			Err(err).
			Str("request_id", middleware.GetReqID(request.Context())).
			Msg("encode HTTP response")
	}
}

func (s Server) writeError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	s.writeJSON(writer, request, status, errorResponse{Error: errorBody{
		Code:      code,
		Message:   message,
		RequestID: middleware.GetReqID(request.Context()),
	}})
}

func optionalBigIntString(value *big.Int) *string {
	if value == nil {
		return nil
	}
	encoded := value.String()
	return &encoded
}

func optionalAddressString(address *common.Address) *string {
	if address == nil {
		return nil
	}
	encoded := address.Hex()
	return &encoded
}
