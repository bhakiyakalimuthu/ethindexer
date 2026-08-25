package server

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	defaultEventPageSize   uint32 = 100
	eventCursorEncodedSize        = 8 + common.HashLength + 4
)

var (
	errInvalidEventAddress = errors.New("invalid event address")
	errInvalidEventLimit   = errors.New("invalid event page limit")
	errInvalidEventCursor  = errors.New("invalid event cursor")
)

type eventResponse struct {
	BlockNumber      uint64   `json:"block_number"`
	BlockHash        string   `json:"block_hash"`
	TransactionHash  string   `json:"transaction_hash"`
	TransactionIndex uint32   `json:"transaction_index"`
	LogIndex         uint32   `json:"log_index"`
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
}

type eventPageResponse struct {
	Events     []eventResponse `json:"events"`
	NextCursor *string         `json:"next_cursor"`
}

func (s Server) eventsByAddress(writer http.ResponseWriter, request *http.Request) {
	address, err := parseEventAddress(chi.URLParam(request, "address"))
	if err != nil {
		s.writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_address",
			"address must be a 0x-prefixed 20-byte hexadecimal value",
		)
		return
	}

	parameters := request.URL.Query()
	limit, err := parseEventLimit(parameters)
	if err != nil {
		s.writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_limit",
			"limit must be an integer from 1 to 1000",
		)
		return
	}
	cursor, err := parseEventCursor(parameters)
	if err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
		return
	}

	page, err := s.QueryService.Events(request.Context(), domain.EventQuery{
		Address: address,
		Limit:   limit,
		Before:  cursor,
	})
	switch {
	case errors.Is(err, service.ErrInvalidQuery):
		s.writeError(writer, request, http.StatusBadRequest, "invalid_event_query", "event query is invalid")
		return
	case errors.Is(err, service.ErrStaleCursor):
		s.writeError(
			writer,
			request,
			http.StatusConflict,
			"stale_cursor",
			"cursor is no longer canonical; restart pagination",
		)
		return
	case err != nil:
		s.Logger.Error().
			Err(err).
			Str("request_id", middleware.GetReqID(request.Context())).
			Str("address", address.Hex()).
			Msg("query events by address")
		s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	s.writeJSON(writer, request, http.StatusOK, newEventPageResponse(page))
}

func newEventResponse(event domain.Event) eventResponse {
	topics := make([]string, len(event.Topics))
	for index, topic := range event.Topics {
		topics[index] = topic.Hex()
	}
	return eventResponse{
		BlockNumber:      event.BlockNumber,
		BlockHash:        event.BlockHash.Hex(),
		TransactionHash:  event.TransactionHash.Hex(),
		TransactionIndex: event.TransactionIndex,
		LogIndex:         event.LogIndex,
		Address:          event.Address.Hex(),
		Topics:           topics,
		Data:             hexutil.Encode(event.Data),
	}
}

func newEventPageResponse(page domain.EventPage) eventPageResponse {
	events := make([]eventResponse, len(page.Events))
	for index, event := range page.Events {
		events[index] = newEventResponse(event)
	}

	var nextCursor *string
	if page.NextCursor != nil {
		encoded := encodeEventCursor(*page.NextCursor)
		nextCursor = &encoded
	}
	return eventPageResponse{Events: events, NextCursor: nextCursor}
}

func parseEventAddress(value string) (common.Address, error) {
	if len(value) != 2+2*common.AddressLength || !strings.HasPrefix(value, "0x") || !common.IsHexAddress(value) {
		return common.Address{}, errInvalidEventAddress
	}
	return common.HexToAddress(value), nil
}

func parseEventLimit(parameters url.Values) (uint32, error) {
	values, exists := parameters["limit"]
	if !exists {
		return defaultEventPageSize, nil
	}
	if len(values) != 1 {
		return 0, errInvalidEventLimit
	}

	limit, err := parseBlockNumber(values[0])
	if err != nil || limit == 0 || limit > uint64(domain.MaxEventPageSize) {
		return 0, errInvalidEventLimit
	}
	return uint32(limit), nil
}

func parseEventCursor(parameters url.Values) (*domain.EventCursor, error) {
	values, exists := parameters["cursor"]
	if !exists {
		return nil, nil
	}
	if len(values) != 1 || values[0] == "" {
		return nil, errInvalidEventCursor
	}

	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil || len(decoded) != eventCursorEncodedSize || base64.RawURLEncoding.EncodeToString(decoded) != values[0] {
		return nil, errInvalidEventCursor
	}
	return &domain.EventCursor{
		BlockNumber: binary.BigEndian.Uint64(decoded[:8]),
		BlockHash:   common.BytesToHash(decoded[8 : 8+common.HashLength]),
		LogIndex:    binary.BigEndian.Uint32(decoded[8+common.HashLength:]),
	}, nil
}

func encodeEventCursor(cursor domain.EventCursor) string {
	encoded := make([]byte, eventCursorEncodedSize)
	binary.BigEndian.PutUint64(encoded[:8], cursor.BlockNumber)
	copy(encoded[8:8+common.HashLength], cursor.BlockHash[:])
	binary.BigEndian.PutUint32(encoded[8+common.HashLength:], cursor.LogIndex)
	return base64.RawURLEncoding.EncodeToString(encoded)
}
