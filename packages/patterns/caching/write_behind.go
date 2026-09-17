package caching

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// WriteBehind owns a bounded, process-local asynchronous write queue.
// Set reports admission, not durability. FlushContext reports failures up to its admission watermark.
type WriteBehind struct {
	cache         KVCache
	ttl           time.Duration
	queue         chan writeTask
	slots         chan struct{}
	keys          [64]chan struct{}
	mu            sync.Mutex
	stopped       bool
	producers     sync.WaitGroup
	accepted      uint64
	completed     uint64
	failed        uint64
	firstFailure  uint64
	firstErr      error
	changed       chan struct{}
	done          chan struct{}
	taskCtx       context.Context
	cancel        context.CancelFunc
	timeout       time.Duration
	defaultWriter WriteBehindWriter
}
type writeTask struct {
	ctx    context.Context
	seq    uint64
	writer func(context.Context) error
}
type WriteBehindWriter func(context.Context, string, string) error
type WriteBehindOption func(*WriteBehind)
type WriteBehindStats struct {
	Accepted, Completed, Failed uint64
	Queued                      int
	Closed                      bool
}

func WithWriteBehindWriter(writer WriteBehindWriter) WriteBehindOption {
	return func(w *WriteBehind) { w.defaultWriter = writer }
}
func WithWriteBehindTimeout(timeout time.Duration) WriteBehindOption {
	return func(w *WriteBehind) {
		if timeout > 0 {
			w.timeout = timeout
		}
	}
}
func NewWriteBehind(cache KVCache, ttl time.Duration, queueSize int, opts ...WriteBehindOption) *WriteBehind {
	if queueSize <= 0 {
		queueSize = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &WriteBehind{cache: cache, ttl: ttl, queue: make(chan writeTask, queueSize), slots: make(chan struct{}, queueSize), changed: make(chan struct{}), done: make(chan struct{}), taskCtx: ctx, cancel: cancel, timeout: 30 * time.Second}
	for i := range w.keys {
		w.keys[i] = make(chan struct{}, 1)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	go w.processQueue()
	return w
}
func (w *WriteBehind) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	value, err := w.cache.Get(ctx, key)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, ErrCacheMiss) {
		return "", err
	}
	data, err := loader()
	if err != nil {
		return "", err
	}
	_ = w.cache.Set(ctx, key, data, w.ttl)
	return data, nil
}
func (w *WriteBehind) Set(ctx context.Context, key, value string) error {
	if w.defaultWriter == nil {
		return errors.New("write-behind default writer is nil")
	}
	return w.set(ctx, key, value, func(ctx context.Context) error { return w.defaultWriter(ctx, key, value) })
}

// SetWithWriter preserves the legacy callback; callbacks must finish to permit draining.
func (w *WriteBehind) SetWithWriter(ctx context.Context, key, value string, writer func() error) error {
	if writer == nil {
		return errors.New("write-behind writer is nil")
	}
	return w.set(ctx, key, value, func(context.Context) error { return writer() })
}
func (w *WriteBehind) set(ctx context.Context, key, value string, writer func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return errors.New("write-behind is stopped")
	}
	select {
	case w.slots <- struct{}{}:
	default:
		w.mu.Unlock()
		return errors.New("write-behind queue is full")
	}
	w.producers.Add(1)
	w.mu.Unlock()
	defer w.producers.Done()
	queued := false
	defer func() {
		if !queued {
			<-w.slots
		}
	}()
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	lock := w.keys[h.Sum32()%uint32(len(w.keys))]
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-w.taskCtx.Done():
		return w.taskCtx.Err()
	}
	defer func() { <-lock }()
	if err := w.cache.Set(ctx, key, value, w.ttl); err != nil {
		return err
	}
	// Reservation guarantees queue space. Sequence assignment and insertion are atomic to Flush.
	w.mu.Lock()
	w.accepted++
	w.queue <- writeTask{ctx: context.WithoutCancel(ctx), seq: w.accepted, writer: writer}
	queued = true
	w.mu.Unlock()
	return nil
}
func (w *WriteBehind) processQueue() {
	defer close(w.done)
	defer w.cancel()
	for task := range w.queue {
		<-w.slots
		ctx, cancel := context.WithTimeout(task.ctx, w.timeout)
		stop := context.AfterFunc(w.taskCtx, cancel)
		if w.taskCtx.Err() != nil {
			cancel()
		}
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("write-behind writer panic: %v", p)
				}
			}()
			if err = ctx.Err(); err != nil {
				return err
			}
			return task.writer(ctx)
		}()
		stop()
		cancel()
		w.mu.Lock()
		w.completed = task.seq
		if err != nil {
			w.failed++
			if w.firstFailure == 0 {
				w.firstFailure = task.seq
				w.firstErr = err
			}
		}
		close(w.changed)
		w.changed = make(chan struct{})
		w.mu.Unlock()
	}
}

// Close stops admission and drains accepted work. Expiry cancels cooperative writers.
func (w *WriteBehind) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		go func() { w.producers.Wait(); close(w.queue) }()
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.failure(w.accepted)
	case <-ctx.Done():
		w.cancel()
		return ctx.Err()
	}
}

// Stop is the legacy unbounded drain. Use Close to obtain errors and set a budget.
func (w *WriteBehind) Stop() { _ = w.Close(context.Background()) }

// Flush is retained for compatibility; FlushContext also returns failures.
func (w *WriteBehind) Flush(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = w.FlushContext(ctx)
}
func (w *WriteBehind) FlushContext(ctx context.Context) error {
	w.mu.Lock()
	target := w.accepted
	for w.completed < target {
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
		w.mu.Lock()
	}
	defer w.mu.Unlock()
	return w.failure(target)
}

// Store only the first failure and a total count, keeping diagnostics bounded.
func (w *WriteBehind) failure(target uint64) error {
	if w.firstFailure != 0 && w.firstFailure <= target {
		return fmt.Errorf("write-behind failed at sequence %d (flush watermark %d): %w", w.firstFailure, target, w.firstErr)
	}
	return nil
}
func (w *WriteBehind) Stats() WriteBehindStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WriteBehindStats{Accepted: w.accepted, Completed: w.completed, Failed: w.failed, Queued: len(w.queue), Closed: w.stopped}
}
