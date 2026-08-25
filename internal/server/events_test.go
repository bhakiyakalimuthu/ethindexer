package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
)

func TestEventsByAddressReturnsPage(t *testing.T) {
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	blockHash := common.HexToHash("0x1234")
	transactionHash := common.HexToHash("0xabcd")
	next := &domain.EventCursor{BlockNumber: 123, BlockHash: blockHash, LogIndex: 7}
	queries := &queryServiceStub{
		events: func(_ context.Context, query domain.EventQuery) (domain.EventPage, error) {
			if query.Address != address || query.Limit != defaultEventPageSize || query.Before != nil {
				t.Fatalf("Events() query = %#v", query)
			}
			return domain.EventPage{
				Events: []domain.Event{{
					BlockNumber:      123,
					BlockHash:        blockHash,
					TransactionHash:  transactionHash,
					TransactionIndex: 4,
					LogIndex:         7,
					Address:          address,
					Topics:           []common.Hash{common.HexToHash("0x01")},
					Data:             []byte{0xca, 0xfe},
				}},
				NextCursor: next,
			}, nil
		},
	}

	response := serveEventsRequest(t, queries, "/v1/addresses/"+address.Hex()+"/events")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	var body eventPageResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Events) != 1 || body.Events[0].TransactionHash != transactionHash.Hex() {
		t.Fatalf("events response = %#v", body.Events)
	}
	if body.NextCursor == nil || *body.NextCursor != encodeEventCursor(*next) {
		t.Fatalf("next cursor = %#v", body.NextCursor)
	}
}

func TestEventsByAddressParsesCursorAndLimit(t *testing.T) {
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	wantCursor := domain.EventCursor{
		BlockNumber: 100,
		BlockHash:   common.HexToHash("0x5678"),
		LogIndex:    9,
	}
	queries := &queryServiceStub{
		events: func(_ context.Context, query domain.EventQuery) (domain.EventPage, error) {
			if query.Address != address || query.Limit != 25 || query.Before == nil || *query.Before != wantCursor {
				t.Fatalf("Events() query = %#v", query)
			}
			return domain.EventPage{Events: []domain.Event{}}, nil
		},
	}
	path := "/v1/addresses/" + address.Hex() + "/events?limit=25&cursor=" +
		url.QueryEscape(encodeEventCursor(wantCursor))

	response := serveEventsRequest(t, queries, path)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body eventPageResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Events == nil || len(body.Events) != 0 || body.NextCursor != nil {
		t.Fatalf("empty page response = %#v", body)
	}
}

func TestEventsByAddressRejectsInvalidAddress(t *testing.T) {
	addresses := []string{
		"0x1234",
		strings.Repeat("1", 2*common.AddressLength),
		"0X" + strings.Repeat("1", 2*common.AddressLength),
		"0x" + strings.Repeat("g", 2*common.AddressLength),
	}
	for _, address := range addresses {
		t.Run(address, func(t *testing.T) {
			queries := eventsQueryMustNotBeCalled(t)
			response := serveEventsRequest(t, queries, "/v1/addresses/"+address+"/events")
			assertEventError(t, response, http.StatusBadRequest, "invalid_address")
		})
	}
}

func TestEventsByAddressRejectsInvalidLimit(t *testing.T) {
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	parameters := []string{"", "0", "1001", "-1", "1.5", "1&limit=2"}
	for _, parameter := range parameters {
		t.Run(parameter, func(t *testing.T) {
			queries := eventsQueryMustNotBeCalled(t)
			path := "/v1/addresses/" + address.Hex() + "/events?limit=" + parameter
			response := serveEventsRequest(t, queries, path)
			assertEventError(t, response, http.StatusBadRequest, "invalid_limit")
		})
	}
}

func TestEventsByAddressRejectsInvalidCursor(t *testing.T) {
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	parameters := []string{"", "not-a-cursor", "AA==", "AA&cursor=AA"}
	for _, parameter := range parameters {
		t.Run(parameter, func(t *testing.T) {
			queries := eventsQueryMustNotBeCalled(t)
			path := "/v1/addresses/" + address.Hex() + "/events?cursor=" + parameter
			response := serveEventsRequest(t, queries, path)
			assertEventError(t, response, http.StatusBadRequest, "invalid_cursor")
		})
	}
}

func TestEventsByAddressReturnsConflictForStaleCursor(t *testing.T) {
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	cursor := encodeEventCursor(domain.EventCursor{BlockHash: common.HexToHash("0x1234")})
	queries := &queryServiceStub{
		events: func(context.Context, domain.EventQuery) (domain.EventPage, error) {
			return domain.EventPage{}, service.ErrStaleCursor
		},
	}

	response := serveEventsRequest(
		t,
		queries,
		"/v1/addresses/"+address.Hex()+"/events?cursor="+url.QueryEscape(cursor),
	)
	assertEventError(t, response, http.StatusConflict, "stale_cursor")
}

func TestEventsByAddressHidesInternalError(t *testing.T) {
	address := common.HexToAddress("0x1000000000000000000000000000000000000001")
	queries := &queryServiceStub{
		events: func(context.Context, domain.EventQuery) (domain.EventPage, error) {
			return domain.EventPage{}, errors.New("database password leaked")
		},
	}

	response := serveEventsRequest(t, queries, "/v1/addresses/"+address.Hex()+"/events")
	assertEventError(t, response, http.StatusInternalServerError, "internal_error")
}

func eventsQueryMustNotBeCalled(t *testing.T) *queryServiceStub {
	t.Helper()
	return &queryServiceStub{
		events: func(context.Context, domain.EventQuery) (domain.EventPage, error) {
			t.Fatal("Events() was called for an invalid request")
			return domain.EventPage{}, nil
		},
	}
}

func assertEventError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	if body := decodeErrorResponse(t, response); body.Error.Code != code {
		t.Fatalf("error response = %#v", body)
	}
}

func serveEventsRequest(t *testing.T, queries service.Queries, path string) *httptest.ResponseRecorder {
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
