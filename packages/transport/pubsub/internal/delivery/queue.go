package delivery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wplbyx/modular/packages/transport/pubsub"
)

var ErrClosed = errors.New("consumer is closed")

type task struct {
	ctx     context.Context
	run     func(context.Context) error
	success func()
	retry   bool
}

// Queue 限制处理并发和排队量，独立管理接收停止与处理取消。
type Queue struct {
	mu        sync.Mutex
	closed    bool
	stop      chan struct{}
	done      chan struct{}
	tasks     chan task
	producers sync.WaitGroup
	workers   sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	active    atomic.Int64
	succeeded atomic.Uint64
	failed    atomic.Uint64
	retried   atomic.Uint64
	canceled  atomic.Uint64
}

func NewQueue(workers, capacity int) *Queue {
	if workers <= 0 {
		workers = 8
	}
	if capacity <= 0 {
		capacity = 256
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{stop: make(chan struct{}), done: make(chan struct{}), tasks: make(chan task, capacity), ctx: ctx, cancel: cancel}
	q.workers.Add(workers)
	for i := 0; i < workers; i++ {
		go q.work()
	}
	return q
}

func (q *Queue) Submit(ctx context.Context, run func(context.Context) error, success func(), retry bool) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrClosed
	}
	q.producers.Add(1)
	q.mu.Unlock()
	defer q.producers.Done()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-q.stop:
		return ErrClosed
	case q.tasks <- task{context.WithoutCancel(ctx), run, success, retry}:
		return nil
	}
}

func (q *Queue) Stop() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.stop)
	q.mu.Unlock()
	go func() { q.producers.Wait(); close(q.tasks); q.workers.Wait(); q.cancel(); close(q.done) }()
}

func (q *Queue) Close(ctx context.Context) error {
	q.Stop()
	select {
	case <-q.done:
		return nil
	case <-ctx.Done():
		q.cancel()
		return ctx.Err()
	}
}

func (q *Queue) work() {
	defer q.workers.Done()
	for t := range q.tasks {
		if q.ctx.Err() != nil {
			q.canceled.Add(1)
			continue
		}
		ctx, cancel := context.WithCancel(t.ctx)
		stop := context.AfterFunc(q.ctx, cancel)
		q.active.Add(1)
		attempt := 0
		for {
			err := CallHandler(ctx, func(ctx context.Context, _ pubsub.Message) error { return t.run(ctx) }, pubsub.Message{})
			if ctx.Err() != nil || q.ctx.Err() != nil {
				q.canceled.Add(1)
				break
			}
			if err == nil {
				if t.success != nil {
					t.success()
				}
				q.succeeded.Add(1)
				break
			}
			q.failed.Add(1)
			if !t.retry {
				break
			}
			if !WaitRetry(ctx, 100*time.Millisecond, attempt) {
				q.canceled.Add(1)
				break
			}
			q.retried.Add(1)
			attempt++
		}
		q.active.Add(-1)
		stop()
		cancel()
	}
}

func (q *Queue) Stats() pubsub.DeliveryStats {
	return pubsub.DeliveryStats{Queued: len(q.tasks), Active: q.active.Load(), Succeeded: q.succeeded.Load(), Failed: q.failed.Load(), Retried: q.retried.Load(), Canceled: q.canceled.Load()}
}
