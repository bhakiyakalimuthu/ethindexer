package service

import (
	"context"
	"errors"
	"testing"

	"ethindexer/internal/domain"

	"github.com/ethereum/go-ethereum/common"
)

func TestHealthServiceReady(t *testing.T) {
	want := domain.ChainTip{Number: 42, Hash: common.HexToHash("0x42")}
	store := &readinessStoreStub{
		canonicalTip: func(context.Context) (*domain.ChainTip, error) {
			return &want, nil
		},
	}

	got, err := NewHealthService(store).Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if got != want {
		t.Fatalf("Ready() = %#v, want %#v", got, want)
	}
}

func TestHealthServiceNotReadyWithoutCanonicalTip(t *testing.T) {
	store := &readinessStoreStub{
		canonicalTip: func(context.Context) (*domain.ChainTip, error) {
			return nil, nil
		},
	}

	_, err := NewHealthService(store).Ready(context.Background())
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("Ready() error = %v, want ErrNotReady", err)
	}
}

func TestHealthServiceNotReadyWhenPostgresPingFails(t *testing.T) {
	wantErr := errors.New("database unavailable")
	canonicalTipCalled := false
	store := &readinessStoreStub{
		ping: func(context.Context) error { return wantErr },
		canonicalTip: func(context.Context) (*domain.ChainTip, error) {
			canonicalTipCalled = true
			return nil, nil
		},
	}

	_, err := NewHealthService(store).Ready(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Ready() error = %v, want %v", err, wantErr)
	}
	if canonicalTipCalled {
		t.Fatal("CanonicalTip() called after PostgreSQL ping failed")
	}
}

type readinessStoreStub struct {
	ping         func(context.Context) error
	canonicalTip func(context.Context) (*domain.ChainTip, error)
}

func (stub *readinessStoreStub) Ping(ctx context.Context) error {
	if stub.ping == nil {
		return nil
	}
	return stub.ping(ctx)
}

func (stub *readinessStoreStub) CanonicalTip(ctx context.Context) (*domain.ChainTip, error) {
	if stub.canonicalTip == nil {
		return nil, errors.New("unexpected CanonicalTip call")
	}
	return stub.canonicalTip(ctx)
}
