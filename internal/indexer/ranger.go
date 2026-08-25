package indexer

import (
	"context"
	"errors"
	"ethindexer/internal/domain"
	"fmt"
	"math"
	"sync"
)

var (
	ErrInvalidLowFetchRange   = errors.New("indexer: invalid low fetch range")
	ErrInvalidLargeFetchRange = errors.New("indexer: invalid large fetch range")
)

type fetchJob struct {
	index       int
	blockNumber uint64
}

func (f *Fetcher) FetchRange(ctx context.Context, from uint64, to uint64, concurrency int) ([]domain.BlockBundle, error) {
	blockRange := to - from
	if blockRange > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w block range is too large", ErrInvalidLargeFetchRange)
	}
	blockCount := int(blockRange + 1)
	if blockCount <= 0 {
		return nil, ErrInvalidLowFetchRange
	}
	if concurrency < 1 {
		concurrency = 1
	}

	if concurrency > blockCount {
		concurrency = blockCount
	}

	bundles := make([]domain.BlockBundle, blockCount)
	jobs := make(chan fetchJob, blockCount)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		pWg sync.WaitGroup
		cWg sync.WaitGroup
	)

	pWg.Add(1)
	go func() {
		defer pWg.Done()
		for i := 0; i < blockCount; i++ {
			job := fetchJob{index: i, blockNumber: from + uint64(i)}
			select {
			case jobs <- job:
			case <-ctx.Done():
				return
			}
		}
		close(jobs)
	}()

	for i := 0; i < concurrency; i++ {
		cWg.Add(1)
		go func() {
			defer cWg.Done()
			for job := range jobs {
				block, err := f.FetchBlock(ctx, job.blockNumber)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("failed to fetch block : %d, %w", job.blockNumber, err):
						cancel() // cancel context to notify other workers
					default:
					}
					return
				}
				bundles[job.index] = block
			}
		}()
	}
	pWg.Wait()
	cWg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		return nil, err
	}

	if err := validateFetchedBundles(bundles, from); err != nil {
		return nil, err
	}
	return bundles, nil
}

func validateFetchedBundles(bundles []domain.BlockBundle, from uint64) error {
	for i, bundle := range bundles {
		expectedBlockNum := from + uint64(i)
		if bundle.Block.Number != expectedBlockNum {
			return fmt.Errorf("indexer: %w: range position %d contains block %d, expected %d", ErrInconsistentBlockData, i, bundle.Block.Number, expectedBlockNum)
		}
		if i > 0 && bundle.Block.ParentHash != bundles[i-1].Block.Hash {
			return fmt.Errorf("%w: block %d parent %s does not match block %d hash %s", ErrInconsistentBlockData, bundle.Block.Number, bundle.Block.ParentHash, bundles[i-1].Block.Number, bundles[i-1].Block.Hash)

		}
	}
	return nil
}
