package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"ethindexer/internal/domain"
	"ethindexer/internal/store"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog"
)

func TestLogSyncResultIncludesChainDecisionAndReorgDetails(t *testing.T) {
	var output bytes.Buffer
	syncer := &Syncer{
		config: Config{ChainID: 1, HeadMode: domain.HeadLatest},
		logger: zerolog.New(&output),
	}
	storedTip := &domain.ChainTip{Number: 104, Hash: common.HexToHash("0x104")}
	ancestor := &domain.ChainTip{Number: 102, Hash: common.HexToHash("0x102")}

	syncer.logSyncResult(SyncResult{
		Head:             domain.ChainTip{Number: 106, Hash: common.HexToHash("0x106")},
		StoredTip:        storedTip,
		Mode:             SyncModeReorg,
		FromBlock:        102,
		ReplaceFrom:      103,
		BlockCount:       4,
		TransactionCount: 10,
		EventCount:       20,
		SyncedAt:         time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC),
		Reorg: &ReorgResult{
			CommonAncestor:     ancestor,
			ReplacedFrom:       103,
			ReplacedTo:         104,
			ReplacedBlockCount: 2,
		},
	})

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	want := map[string]any{
		"level":                     "warn",
		"head_mode":                 "latest",
		"sync_mode":                 "reorg",
		"stored_tip_present":        true,
		"stored_tip_number":         float64(104),
		"stored_tip_hash":           storedTip.Hash.Hex(),
		"selected_head_number":      float64(106),
		"selected_head_hash":        common.HexToHash("0x106").Hex(),
		"retained_from_block":       float64(102),
		"retained_to_block":         float64(106),
		"retained_blocks":           float64(5),
		"fetched_from_block":        float64(103),
		"fetched_to_block":          float64(106),
		"fetched_blocks":            float64(4),
		"fetched_transactions":      float64(10),
		"fetched_events":            float64(20),
		"reorg_detected":            true,
		"reorg_replaced_from_block": float64(103),
		"reorg_replaced_to_block":   float64(104),
		"reorg_replaced_blocks":     float64(2),
		"common_ancestor_found":     true,
		"common_ancestor_number":    float64(102),
		"common_ancestor_hash":      ancestor.Hash.Hex(),
	}
	for field, wantValue := range want {
		if got := entry[field]; got != wantValue {
			t.Errorf("log field %q = %#v, want %#v", field, got, wantValue)
		}
	}
}

func TestSyncWithRetryUsesBoundedExponentialBackoff(t *testing.T) {
	syncer := &Syncer{
		config: Config{
			RetryAttempts:   4,
			RetryMinBackoff: 10 * time.Millisecond,
			RetryMaxBackoff: 25 * time.Millisecond,
		},
		logger: zerolog.Nop(),
	}
	var waits []time.Duration
	syncer.wait = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}

	calls := 0
	wantResult := SyncResult{BlockCount: 2}
	result, err := syncer.syncWithRetry(context.Background(), func(context.Context) (SyncResult, error) {
		calls++
		if calls <= syncer.config.RetryAttempts {
			return SyncResult{}, errors.New("temporary RPC failure")
		}
		return wantResult, nil
	})
	if err != nil {
		t.Fatalf("syncWithRetry() error = %v", err)
	}
	if result != wantResult {
		t.Fatalf("syncWithRetry() result = %#v, want %#v", result, wantResult)
	}
	if calls != 5 {
		t.Fatalf("sync calls = %d, want 5", calls)
	}
	if want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond, 25 * time.Millisecond}; !slices.Equal(waits, want) {
		t.Fatalf("retry waits = %v, want %v", waits, want)
	}
}

func TestSyncWithRetryDoesNotRetryPermanentErrors(t *testing.T) {
	syncer := &Syncer{
		config: Config{
			RetryAttempts:   3,
			RetryMinBackoff: time.Millisecond,
			RetryMaxBackoff: time.Second,
		},
		logger: zerolog.Nop(),
	}
	waitCalls := 0
	syncer.wait = func(context.Context, time.Duration) error {
		waitCalls++
		return nil
	}

	syncCalls := 0
	_, err := syncer.syncWithRetry(context.Background(), func(context.Context) (SyncResult, error) {
		syncCalls++
		return SyncResult{}, ErrInvalidSyncConfig
	})
	if !errors.Is(err, ErrInvalidSyncConfig) {
		t.Fatalf("syncWithRetry() error = %v, want ErrInvalidSyncConfig", err)
	}
	if syncCalls != 1 || waitCalls != 0 {
		t.Fatalf("sync calls = %d, wait calls = %d, want 1 and 0", syncCalls, waitCalls)
	}
}

func TestSyncWithRetryDoesNotRetryInvalidStoreUpdate(t *testing.T) {
	syncer := &Syncer{
		config: Config{
			RetryAttempts:   3,
			RetryMinBackoff: time.Millisecond,
			RetryMaxBackoff: time.Second,
		},
		logger: zerolog.Nop(),
	}
	waitCalls := 0
	syncer.wait = func(context.Context, time.Duration) error {
		waitCalls++
		return nil
	}

	syncCalls := 0
	_, err := syncer.syncWithRetry(context.Background(), func(context.Context) (SyncResult, error) {
		syncCalls++
		return SyncResult{}, store.ErrInvalidCanonicalUpdate
	})
	if !errors.Is(err, store.ErrInvalidCanonicalUpdate) {
		t.Fatalf("syncWithRetry() error = %v, want store.ErrInvalidCanonicalUpdate", err)
	}
	if syncCalls != 1 || waitCalls != 0 {
		t.Fatalf("sync calls = %d, wait calls = %d, want 1 and 0", syncCalls, waitCalls)
	}
}

func TestSyncWithRetryStopsDuringBackoffCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	syncer := &Syncer{
		config: Config{
			RetryAttempts:   3,
			RetryMinBackoff: time.Millisecond,
			RetryMaxBackoff: time.Second,
		},
		logger: zerolog.Nop(),
	}
	syncer.wait = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	syncCalls := 0
	_, err := syncer.syncWithRetry(ctx, func(context.Context) (SyncResult, error) {
		syncCalls++
		return SyncResult{}, errors.New("temporary RPC failure")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("syncWithRetry() error = %v, want context.Canceled", err)
	}
	if syncCalls != 1 {
		t.Fatalf("sync calls = %d, want 1", syncCalls)
	}
}

func TestRunSynchronizesImmediatelyAndPollsUntilCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	syncer := newRunTestSyncer(validRunTestConfig())

	var waits []time.Duration
	syncer.wait = func(ctx context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		if len(waits) == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}

	syncCalls := 0
	err := syncer.run(ctx, func(context.Context) (SyncResult, error) {
		syncCalls++
		return SyncResult{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v, want context.Canceled", err)
	}
	if syncCalls != 2 {
		t.Fatalf("sync calls = %d, want 2", syncCalls)
	}
	wantWaits := []time.Duration{syncer.config.PollInterval, syncer.config.PollInterval}
	if !slices.Equal(waits, wantWaits) {
		t.Fatalf("waits = %v, want %v", waits, wantWaits)
	}
}

func TestRunContinuesAfterPostStartupRetriesAreExhausted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	config := validRunTestConfig()
	config.RetryAttempts = 1
	syncer := newRunTestSyncer(config)

	var waits []time.Duration
	syncer.wait = func(ctx context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		if len(waits) == 4 {
			cancel()
			return ctx.Err()
		}
		return nil
	}

	syncCalls := 0
	err := syncer.run(ctx, func(context.Context) (SyncResult, error) {
		syncCalls++
		if syncCalls == 2 || syncCalls == 3 {
			return SyncResult{}, errors.New("temporary RPC failure")
		}
		return SyncResult{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v, want context.Canceled", err)
	}
	if syncCalls != 4 {
		t.Fatalf("sync calls = %d, want 4", syncCalls)
	}
	wantWaits := []time.Duration{
		config.PollInterval,
		config.RetryMinBackoff,
		config.PollInterval,
		config.PollInterval,
	}
	if !slices.Equal(waits, wantWaits) {
		t.Fatalf("waits = %v, want %v", waits, wantWaits)
	}
}

func TestRunReturnsInitialRetryExhaustion(t *testing.T) {
	config := validRunTestConfig()
	config.RetryAttempts = 1
	syncer := newRunTestSyncer(config)
	syncer.wait = func(context.Context, time.Duration) error { return nil }

	syncCalls := 0
	err := syncer.run(context.Background(), func(context.Context) (SyncResult, error) {
		syncCalls++
		return SyncResult{}, errors.New("temporary RPC failure")
	})
	if !errors.Is(err, ErrSyncRetriesExhausted) {
		t.Fatalf("run() error = %v, want ErrSyncRetriesExhausted", err)
	}
	if syncCalls != 2 {
		t.Fatalf("sync calls = %d, want 2", syncCalls)
	}
}

func TestRunRejectsInvalidRunnerConfigBeforeSync(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{name: "poll interval", change: func(config *Config) { config.PollInterval = 0 }},
		{name: "negative retries", change: func(config *Config) { config.RetryAttempts = -1 }},
		{name: "minimum backoff", change: func(config *Config) { config.RetryMinBackoff = 0 }},
		{name: "maximum backoff", change: func(config *Config) { config.RetryMaxBackoff = 0 }},
		{name: "backoff order", change: func(config *Config) { config.RetryMinBackoff = 2 * config.RetryMaxBackoff }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validRunTestConfig()
			test.change(&config)
			syncer := newRunTestSyncer(config)

			syncCalls := 0
			err := syncer.run(context.Background(), func(context.Context) (SyncResult, error) {
				syncCalls++
				return SyncResult{}, nil
			})
			if !errors.Is(err, ErrInvalidSyncConfig) {
				t.Fatalf("run() error = %v, want ErrInvalidSyncConfig", err)
			}
			if syncCalls != 0 {
				t.Fatalf("sync calls = %d, want zero", syncCalls)
			}
		})
	}
}

func validRunTestConfig() Config {
	config := validSyncTestConfig()
	config.PollInterval = time.Second
	config.RetryAttempts = 2
	config.RetryMinBackoff = 10 * time.Millisecond
	config.RetryMaxBackoff = time.Second
	return config
}

func newRunTestSyncer(config Config) *Syncer {
	chain := &syncTestChain{}
	return NewSyncer(NewFetcher(chain, 1), chain, store.NewMemory(), config, zerolog.Nop())
}
