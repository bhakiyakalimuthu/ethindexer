package server

import (
	"errors"
	"net/http"

	"ethindexer/internal/service"

	"github.com/go-chi/chi/v5/middleware"
)

type healthResponse struct {
	Status string `json:"status"`
}

type readinessResponse struct {
	Status           string `json:"status"`
	LastIndexedBlock uint64 `json:"last_indexed_block"`
}

func (s Server) health(writer http.ResponseWriter, request *http.Request) {
	s.writeJSON(writer, request, http.StatusOK, healthResponse{Status: "ok"})
}

func (s Server) readiness(writer http.ResponseWriter, request *http.Request) {
	tip, err := s.HealthService.Ready(request.Context())
	if err != nil {
		if !errors.Is(err, service.ErrNotReady) {
			s.Logger.Error().
				Err(err).
				Str("request_id", middleware.GetReqID(request.Context())).
				Msg("readiness check failed")
		}
		s.writeJSON(writer, request, http.StatusServiceUnavailable, healthResponse{Status: "not_ready"})
		return
	}

	s.writeJSON(writer, request, http.StatusOK, readinessResponse{
		Status:           "ready",
		LastIndexedBlock: tip.Number,
	})
}
