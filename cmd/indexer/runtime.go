package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

type indexerRunner interface {
	Run(context.Context) error
}

type httpRuntime interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

func runServices(
	ctx context.Context,
	indexer indexerRunner,
	httpServer httpRuntime,
	shutdownTimeout time.Duration,
) error {
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		err := indexer.Run(groupCtx)
		if groupCtx.Err() != nil && (err == nil || errors.Is(err, groupCtx.Err())) {
			return nil
		}
		if err == nil {
			return errors.New("indexer stopped unexpectedly")
		}
		return fmt.Errorf("run indexer: %w", err)
	})
	group.Go(func() error {
		err := httpServer.ListenAndServe()
		if groupCtx.Err() != nil && errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server stopped unexpectedly")
		}
		return fmt.Errorf("serve HTTP: %w", err)
	})
	group.Go(func() error {
		<-groupCtx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(groupCtx), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		return nil
	})

	return group.Wait()
}
