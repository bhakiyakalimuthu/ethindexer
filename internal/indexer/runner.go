package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

type syncFunc func(context.Context) (SyncResult, error)

// Run performs an initial synchronization and then polls sequentially until
// the context is canceled. An initial failure is returned to the caller;
// failures after startup are logged and retried on the next poll cycle.
func (s *Syncer) Run(ctx context.Context) error {
	return s.run(ctx, s.SyncOnce)
}

func (s *Syncer) run(ctx context.Context, sync syncFunc) error {
	if err := s.validateRunConfig(); err != nil {
		return err
	}

	result, err := s.syncWithRetry(ctx, sync)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("initial synchronization: %w", err)
	}
	s.logSyncResult(result)

	for {
		if err := s.wait(ctx, s.config.PollInterval); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("wait for next synchronization: %w", err)
		}

		result, err = s.syncWithRetry(ctx, sync)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.logger.Error().Err(err).Msg("Ethereum synchronization cycle failed")
			continue
		}
		s.logSyncResult(result)
	}
}

func (s *Syncer) syncWithRetry(ctx context.Context, sync syncFunc) (SyncResult, error) {
	backoff := s.config.RetryMinBackoff

	for retry := 0; ; retry++ {
		result, err := sync(ctx)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return SyncResult{}, ctx.Err()
		}
		if isPermanentSyncError(err) {
			return SyncResult{}, err
		}
		if retry >= s.config.RetryAttempts {
			return SyncResult{}, fmt.Errorf(
				"%w after %d attempts: %w",
				ErrSyncRetriesExhausted,
				retry+1,
				err,
			)
		}

		s.logger.Warn().
			Err(err).
			Int("retry", retry+1).
			Int("retry_limit", s.config.RetryAttempts).
			Dur("backoff", backoff).
			Msg("Ethereum synchronization failed; retrying")

		if err := s.wait(ctx, backoff); err != nil {
			if ctx.Err() != nil {
				return SyncResult{}, ctx.Err()
			}
			return SyncResult{}, fmt.Errorf("wait before synchronization retry: %w", err)
		}
		backoff = nextRetryBackoff(backoff, s.config.RetryMaxBackoff)
	}
}

func (s *Syncer) validateRunConfig() error {
	if err := s.validateSyncOnceConfig(); err != nil {
		return err
	}

	switch {
	case s.config.PollInterval <= 0:
		return fmt.Errorf("%w: poll interval must be greater than zero", ErrInvalidSyncConfig)
	case s.config.RetryAttempts < 0:
		return fmt.Errorf("%w: retry attempts cannot be negative", ErrInvalidSyncConfig)
	case s.config.RetryMinBackoff <= 0:
		return fmt.Errorf("%w: retry minimum backoff must be greater than zero", ErrInvalidSyncConfig)
	case s.config.RetryMaxBackoff <= 0:
		return fmt.Errorf("%w: retry maximum backoff must be greater than zero", ErrInvalidSyncConfig)
	case s.config.RetryMinBackoff > s.config.RetryMaxBackoff:
		return fmt.Errorf("%w: retry minimum backoff cannot exceed maximum backoff", ErrInvalidSyncConfig)
	default:
		return nil
	}
}

func (s *Syncer) logSyncResult(result SyncResult) {
	level := zerolog.InfoLevel
	if result.BlockCount == 0 {
		level = zerolog.DebugLevel
	}

	s.logger.WithLevel(level).
		Uint64("chain_id", s.config.ChainID).
		Uint64("from_block", result.FromBlock).
		Uint64("replace_from", result.ReplaceFrom).
		Uint64("to_block", result.Head.Number).
		Str("head_hash", result.Head.Hash.Hex()).
		Int("blocks", result.BlockCount).
		Int("transactions", result.TransactionCount).
		Int("events", result.EventCount).
		Time("synced_at", result.SyncedAt).
		Msg("Ethereum block window synchronized")
}

func isPermanentSyncError(err error) bool {
	return errors.Is(err, ErrInvalidSyncConfig) || errors.Is(err, ErrInvalidSyncDependency)
}

func nextRetryBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum-current {
		return maximum
	}
	return current * 2
}

func waitFor(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
