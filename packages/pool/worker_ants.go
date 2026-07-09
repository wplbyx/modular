package pool

import (
	"context"
	"fmt"
	"sync"

	"github.com/panjf2000/ants/v2"

	pkglog "github.com/wplbyx/modular/packages/log"
)

var wp *AntsWorkerPool

type AntsWorkerPool struct {
	pool *ants.Pool
	wg   sync.WaitGroup
}

func NewAntsWorkerPool(capacity int) (*AntsWorkerPool, error) {
	p, err := ants.NewPool(capacity, ants.WithOptions(ants.Options{PreAlloc: true}))
	if err != nil {
		return nil, fmt.Errorf("failed to create ants pool: %w", err)
	}

	wp = &AntsWorkerPool{pool: p}
	return wp, nil
}

func Submit(ctx context.Context, task WorkerTask) error {
	if wp == nil {
		return fmt.Errorf("worker pool is not initialized")
	}
	return wp.Submit(ctx, task)
}

func (ap *AntsWorkerPool) Submit(ctx context.Context, task WorkerTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ap.wg.Add(1)

	if err := ap.pool.Submit(func() {
		defer func() {
			ap.wg.Done()
			if r := recover(); r != nil {
				pkglog.Errorf("worker pool: task recovered from panic: %v", r)
			}
		}()

		if err := task(ctx); err != nil {
			pkglog.Errorf("worker pool: task failed: %v", err)
		}
	}); err != nil {
		ap.wg.Done()
		return err
	}

	return nil
}

func (ap *AntsWorkerPool) Close() {
	ap.wg.Wait()
	ap.pool.Release()
}
