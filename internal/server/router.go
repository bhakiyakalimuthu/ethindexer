package server

import (
	"context"
	"net/http"
	"time"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

// Queries is owned by the server package and implemented by service.QueryService.
type Queries interface {
	Block(ctx context.Context, number uint64) (domain.BlockResult, error)
	Transaction(ctx context.Context, hash common.Hash) (domain.TransactionResult, error)
	Events(ctx context.Context, query domain.EventQuery) (domain.EventPage, error)
}

type Dependencies struct {
	Queries        Queries
	Logger         zerolog.Logger
	RequestTimeout time.Duration
}

// NewRouter contains only routing and HTTP middleware. Business endpoints are
// added here when their handlers are implemented.
func NewRouter(dependencies Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(dependencies.RequestTimeout))

	return router
}
