package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRunServicesGracefullyStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan struct{})
	serverStopped := make(chan struct{})
	indexer := indexerRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	httpServer := httpRuntimeStub{
		listen: func() error {
			close(listening)
			<-serverStopped
			return http.ErrServerClosed
		},
		shutdown: func(ctx context.Context) error {
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				t.Error("Shutdown() context has no deadline")
			}
			close(serverStopped)
			return nil
		},
	}
	go func() {
		<-listening
		cancel()
	}()

	if err := runServices(ctx, indexer, httpServer, time.Second); err != nil {
		t.Fatalf("runServices() error = %v", err)
	}
}

func TestRunServicesStopsHTTPWhenIndexerFails(t *testing.T) {
	wantErr := errors.New("initial synchronization failed")
	serverStopped := make(chan struct{})
	indexer := indexerRunnerFunc(func(context.Context) error { return wantErr })
	httpServer := httpRuntimeStub{
		listen: func() error {
			<-serverStopped
			return http.ErrServerClosed
		},
		shutdown: func(context.Context) error {
			close(serverStopped)
			return nil
		},
	}

	err := runServices(context.Background(), indexer, httpServer, time.Second)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runServices() error = %v, want %v", err, wantErr)
	}
}

func TestRunServicesCancelsIndexerWhenHTTPFails(t *testing.T) {
	wantErr := errors.New("address already in use")
	indexerStopped := make(chan struct{})
	indexer := indexerRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		close(indexerStopped)
		return ctx.Err()
	})
	httpServer := httpRuntimeStub{
		listen:   func() error { return wantErr },
		shutdown: func(context.Context) error { return nil },
	}

	err := runServices(context.Background(), indexer, httpServer, time.Second)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runServices() error = %v, want %v", err, wantErr)
	}
	select {
	case <-indexerStopped:
	default:
		t.Fatal("indexer was not canceled")
	}
}

func TestRunServicesReturnsShutdownFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wantErr := errors.New("grace period expired")
	listening := make(chan struct{})
	serverStopped := make(chan struct{})
	indexer := indexerRunnerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	httpServer := httpRuntimeStub{
		listen: func() error {
			close(listening)
			<-serverStopped
			return http.ErrServerClosed
		},
		shutdown: func(context.Context) error {
			close(serverStopped)
			return wantErr
		},
	}
	go func() {
		<-listening
		cancel()
	}()

	err := runServices(ctx, indexer, httpServer, time.Second)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runServices() error = %v, want %v", err, wantErr)
	}
}

type indexerRunnerFunc func(context.Context) error

func (run indexerRunnerFunc) Run(ctx context.Context) error {
	return run(ctx)
}

type httpRuntimeStub struct {
	listen   func() error
	shutdown func(context.Context) error
}

func (stub httpRuntimeStub) ListenAndServe() error {
	return stub.listen()
}

func (stub httpRuntimeStub) Shutdown(ctx context.Context) error {
	return stub.shutdown(ctx)
}
