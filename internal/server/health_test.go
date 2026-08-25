package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/service"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
)

func TestHealthReturnsOKWithoutCheckingDependencies(t *testing.T) {
	called := false
	health := healthServiceStub(func(context.Context) (domain.ChainTip, error) {
		called = true
		return domain.ChainTip{}, nil
	})

	response := serveHealthRequest(t, health, "/healthz")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if called {
		t.Fatal("liveness check called a dependency")
	}

	var body healthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("response = %#v", body)
	}
}

func TestReadinessReturnsCanonicalTip(t *testing.T) {
	health := healthServiceStub(func(context.Context) (domain.ChainTip, error) {
		return domain.ChainTip{Number: 42, Hash: common.HexToHash("0x42")}, nil
	})

	response := serveHealthRequest(t, health, "/readyz")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	var body readinessResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ready" || body.LastIndexedBlock != 42 {
		t.Fatalf("response = %#v", body)
	}
}

func TestReadinessReturnsServiceUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "initial sync incomplete", err: service.ErrNotReady},
		{name: "PostgreSQL unavailable", err: errors.New("database unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			health := healthServiceStub(func(context.Context) (domain.ChainTip, error) {
				return domain.ChainTip{}, test.err
			})

			response := serveHealthRequest(t, health, "/readyz")
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
			var body healthResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Status != "not_ready" {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}

func serveHealthRequest(t *testing.T, health service.Health, path string) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewServer(Server{
		HealthService:  health,
		Logger:         zerolog.Nop(),
		RequestTimeout: time.Second,
	})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type healthServiceStub func(context.Context) (domain.ChainTip, error)

func (stub healthServiceStub) Ready(ctx context.Context) (domain.ChainTip, error) {
	return stub(ctx)
}
