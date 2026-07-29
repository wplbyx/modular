package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	modularlog "github.com/wplbyx/modular/packages/log"
)

func newTestPool(t *testing.T, config Config) *AntsWorkerPool {
	t.Helper()
	workerPool, err := New(config, modularlog.Default())
	require.NoError(t, err)
	require.NoError(t, workerPool.Setup(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = workerPool.Close(ctx)
	})
	return workerPool
}

func TestNewValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "zero capacity", config: Config{}},
		{name: "queue without capacity", config: Config{Capacity: 1, Policy: Queue}},
		{name: "reject with queue", config: Config{Capacity: 1, Policy: Reject, QueueCapacity: 1}},
		{name: "unknown policy", config: Config{Capacity: 1, Policy: Policy(10)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config, modularlog.Default())
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestWorkerPoolRejectsImmediatelyWhenBusy(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 1, Policy: Reject})
	started := make(chan struct{})
	release := make(chan struct{})
	require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
		close(started)
		<-release
		return nil
	}))
	<-started

	start := time.Now()
	err := workerPool.Submit(context.Background(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrOverloaded)
	assert.Less(t, time.Since(start), 50*time.Millisecond)
	assert.Equal(t, uint64(1), workerPool.Stats().Rejected)
	close(release)
}

func TestWorkerPoolBoundsQueueAndReturnsAfterAdmission(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 1, Policy: Queue, QueueCapacity: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
		close(started)
		<-release
		return nil
	}))
	<-started

	secondDone := make(chan struct{})
	require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
		close(secondDone)
		return nil
	}))
	require.Eventually(t, func() bool { return workerPool.Stats().Queued == 1 }, time.Second, time.Millisecond)
	require.ErrorIs(t, workerPool.Submit(context.Background(), func(context.Context) error { return nil }), ErrOverloaded)

	close(release)
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("queued task was not executed")
	}
}

func TestWorkerPoolSkipsCanceledQueuedTask(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 1, Policy: Queue, QueueCapacity: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
		close(started)
		<-release
		return nil
	}))
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	var executed atomic.Bool
	require.NoError(t, workerPool.Submit(ctx, func(context.Context) error {
		executed.Store(true)
		return nil
	}))
	cancel()
	close(release)

	require.Eventually(t, func() bool {
		stats := workerPool.Stats()
		return stats.Completed == 1 && stats.Canceled == 1
	}, time.Second, time.Millisecond)
	assert.False(t, executed.Load())
	assert.Equal(t, uint64(2), workerPool.Stats().Accepted)
	assert.Equal(t, uint64(1), workerPool.Stats().Canceled)
}

func TestWorkerPoolCountsFailuresAndPanics(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 2, Policy: Queue, QueueCapacity: 2})
	require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
		return errors.New("failed")
	}))
	require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
		panic("boom")
	}))

	require.Eventually(t, func() bool { return workerPool.Stats().Completed == 2 }, time.Second, time.Millisecond)
	stats := workerPool.Stats()
	assert.Equal(t, uint64(1), stats.Failed)
	assert.Equal(t, uint64(1), stats.Panicked)
}

func TestWorkerPoolCloseDrainsAcceptedTasksAndRejectsNewOnes(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 2, Policy: Queue, QueueCapacity: 8})
	var completed atomic.Int64
	for range 8 {
		require.NoError(t, workerPool.Submit(context.Background(), func(context.Context) error {
			time.Sleep(time.Millisecond)
			completed.Add(1)
			return nil
		}))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, workerPool.Close(ctx))
	assert.Equal(t, int64(8), completed.Load())
	require.ErrorIs(t, workerPool.Submit(context.Background(), func(context.Context) error { return nil }), ErrClosed)
}

func TestWorkerPoolCloseTimeoutCancelsPoolContext(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 1, Policy: Reject})
	taskDone := make(chan struct{})
	require.NoError(t, workerPool.Submit(context.Background(), func(ctx context.Context) error {
		defer close(taskDone)
		<-ctx.Done()
		return ctx.Err()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, workerPool.Close(ctx), context.DeadlineExceeded)
	select {
	case <-taskDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel task context")
	}
}

func TestWorkerPoolConcurrentSubmitAndClose(t *testing.T) {
	workerPool := newTestPool(t, Config{Capacity: 4, Policy: Queue, QueueCapacity: 64})
	var submitters sync.WaitGroup
	for range 8 {
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			for range 100 {
				err := workerPool.Submit(context.Background(), func(context.Context) error { return nil })
				if err != nil && !errors.Is(err, ErrOverloaded) && !errors.Is(err, ErrClosed) {
					t.Errorf("unexpected submit error: %v", err)
				}
			}
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, workerPool.Close(ctx))
	submitters.Wait()
}

func TestWorkerPoolInstancesAreIndependent(t *testing.T) {
	first := newTestPool(t, Config{Name: "first", Capacity: 1, Policy: Reject})
	second := newTestPool(t, Config{Name: "second", Capacity: 1, Policy: Reject})
	require.NoError(t, first.Close(context.Background()))
	require.NoError(t, second.Submit(context.Background(), func(context.Context) error { return nil }))
	require.Eventually(t, func() bool { return second.Stats().Completed == 1 }, time.Second, time.Millisecond)
}

func TestWorkerPoolRejectsSubmitOutsideLifecycle(t *testing.T) {
	workerPool, err := New(Config{Capacity: 1}, modularlog.Default())
	require.NoError(t, err)
	require.ErrorIs(t, workerPool.Submit(context.Background(), func(context.Context) error { return nil }), ErrNotRunning)
	require.NoError(t, workerPool.Close(context.Background()))
	require.ErrorIs(t, workerPool.Submit(context.Background(), func(context.Context) error { return nil }), ErrClosed)
}

func TestWorkerPoolConcurrentSetupAndClose(t *testing.T) {
	for range 100 {
		workerPool, err := New(Config{Capacity: 1}, modularlog.Default())
		require.NoError(t, err)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			err := workerPool.Setup(context.Background())
			if err != nil && !errors.Is(err, ErrClosed) {
				t.Errorf("unexpected setup error: %v", err)
			}
		}()
		go func() {
			defer group.Done()
			if err := workerPool.Close(context.Background()); err != nil {
				t.Errorf("unexpected close error: %v", err)
			}
		}()
		group.Wait()
		require.ErrorIs(t, workerPool.Submit(context.Background(), func(context.Context) error { return nil }), ErrClosed)
	}
}
