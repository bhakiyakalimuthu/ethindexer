package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"ethindexer/internal/service"
)

type Server struct {
	QueryService   service.Queries
	Logger         zerolog.Logger
	RequestTimeout time.Duration
}

// NewServer contains only routing, handlers, and HTTP middleware.
func NewServer(dependencies Server) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(dependencies.RequestTimeout))
	router.Get("/v1/blocks/{number}", dependencies.blockByNumber)
	router.Get("/v1/transactions/{hash}", dependencies.transactionByHash)
	router.Get("/v1/addresses/{address}/events", dependencies.eventsByAddress)

	return router
}
