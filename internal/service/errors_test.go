package service

import (
	"errors"
	"testing"

	"ethindexer/internal/store"
)

func TestTranslateStoreError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "not found", err: store.ErrNotFound, want: ErrNotFound},
		{name: "invalid query", err: store.ErrInvalidQuery, want: ErrInvalidQuery},
		{name: "stale cursor", err: store.ErrStaleCursor, want: ErrStaleCursor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := translateStoreError(test.err); !errors.Is(err, test.want) {
				t.Fatalf("translateStoreError() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTranslateStoreErrorPreservesUnexpectedError(t *testing.T) {
	want := errors.New("database unavailable")
	if got := translateStoreError(want); !errors.Is(got, want) {
		t.Fatalf("translateStoreError() error = %v, want original error", got)
	}
}
