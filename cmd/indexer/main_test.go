package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReturnsStartupErrors(t *testing.T) {
	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")

	err := run(context.Background(), []string{"-config", missingConfig})
	if err == nil || !strings.Contains(err.Error(), "load configuration") {
		t.Fatalf("run() error = %v, want configuration error", err)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	err := run(context.Background(), []string{"unexpected"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("run() error = %v, want unexpected arguments error", err)
	}
}
