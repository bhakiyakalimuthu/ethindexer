package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ethindexer/internal/store"

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
	if result.Reorg != nil {
		level = zerolog.WarnLevel
	} else if result.BlockCount == 0 {
		level = zerolog.DebugLevel
	}

	event := s.logger.WithLevel(level).
		Uint64("chain_id", s.config.ChainID).
		Str("head_mode", string(s.config.HeadMode)).
		Str("sync_mode", string(result.Mode)).
		Bool("stored_tip_present", result.StoredTip != nil).
		Uint64("selected_head_number", result.Head.Number).
		Str("selected_head_hash", result.Head.Hash.Hex()).
		Uint64("retained_from_block", result.FromBlock).
		Uint64("retained_to_block", result.Head.Number).
		Uint64("retained_blocks", result.Head.Number-result.FromBlock+1).
		Int("fetched_blocks", result.BlockCount).
		Int("fetched_transactions", result.TransactionCount).
		Int("fetched_events", result.EventCount).
		Bool("reorg_detected", result.Reorg != nil).
		Time("synced_at", result.SyncedAt)

	if result.StoredTip != nil {
		event = event.
			Uint64("stored_tip_number", result.StoredTip.Number).
			Str("stored_tip_hash", result.StoredTip.Hash.Hex())
	}
	if result.BlockCount > 0 {
		event = event.
			Uint64("fetched_from_block", result.ReplaceFrom).
			Uint64("fetched_to_block", result.Head.Number)
	}
	if result.Reorg != nil {
		event = event.
			Uint64("reorg_replaced_from_block", result.Reorg.ReplacedFrom).
			Uint64("reorg_replaced_to_block", result.Reorg.ReplacedTo).
			Uint64("reorg_replaced_blocks", result.Reorg.ReplacedBlockCount).
			Bool("common_ancestor_found", result.Reorg.CommonAncestor != nil)
		if result.Reorg.CommonAncestor != nil {
			event = event.
				Uint64("common_ancestor_number", result.Reorg.CommonAncestor.Number).
				Str("common_ancestor_hash", result.Reorg.CommonAncestor.Hash.Hex())
		}
	}

	event.Msg("Ethereum block window synchronized")
}

func isPermanentSyncError(err error) bool {
	return errors.Is(err, ErrInvalidSyncConfig) ||
		errors.Is(err, ErrInvalidSyncDependency) ||
		errors.Is(err, store.ErrInvalidCanonicalUpdate) ||
		errors.Is(err, store.ErrInconsistentData)
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
