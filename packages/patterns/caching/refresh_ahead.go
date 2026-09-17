package caching

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RefreshAhead bounds refresh concurrency and stops refreshing keys unused for one TTL.
type RefreshAhead struct {
	cache                     KVCache
	ttl, refreshTime, timeout time.Duration
	concurrency               int
	mu                        sync.Mutex
	entries                   map[string]*refreshEntry
	stopped                   bool
	stopCh, done              chan struct{}
	slots                     chan struct{}
	wg                        sync.WaitGroup
	taskCtx                   context.Context
	cancel                    context.CancelFunc
}
type refreshEntry struct {
	key                            string
	loader                         func(context.Context) (string, error)
	expiresAt, lastUsed, nextRetry time.Time
	refreshing                     bool
	failures                       int
}
type RefreshAheadOption func(*RefreshAhead)

func WithRefreshConcurrency(limit int) RefreshAheadOption {
	return func(r *RefreshAhead) {
		if limit > 0 {
			r.concurrency = limit
		}
	}
}
func WithRefreshTimeout(timeout time.Duration) RefreshAheadOption {
	return func(r *RefreshAhead) {
		if timeout > 0 {
			r.timeout = timeout
		}
	}
}
func NewRefreshAhead(cache KVCache, ttl, refreshBefore time.Duration) *RefreshAhead {
	return NewRefreshAheadWithOptions(cache, ttl, refreshBefore)
}
func NewRefreshAheadWithOptions(cache KVCache, ttl, refreshBefore time.Duration, opts ...RefreshAheadOption) *RefreshAhead {
	if refreshBefore <= 0 {
		refreshBefore = ttl / 2
	}
	if refreshBefore <= 0 {
		refreshBefore = time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &RefreshAhead{cache: cache, ttl: ttl, refreshTime: refreshBefore, timeout: 30 * time.Second, concurrency: 8, entries: make(map[string]*refreshEntry), stopCh: make(chan struct{}), done: make(chan struct{}), taskCtx: ctx, cancel: cancel}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	r.slots = make(chan struct{}, r.concurrency)
	r.wg.Add(1)
	go r.refreshLoop()
	return r
}

// Get adapts a legacy loader that cannot be canceled. Use GetContext for cancellable work.
func (r *RefreshAhead) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	if loader == nil {
		return "", errors.New("refresh loader is nil")
	}
	return r.GetContext(ctx, key, func(context.Context) (string, error) { return loader() })
}
func (r *RefreshAhead) GetContext(ctx context.Context, key string, loader func(context.Context) (string, error)) (string, error) {
	if loader == nil {
		return "", errors.New("refresh loader is nil")
	}
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return "", errors.New("refresh-ahead is closed")
	}
	value, err := r.cache.Get(ctx, key)
	if err != nil && !errors.Is(err, ErrCacheMiss) {
		return "", err
	}
	if err != nil {
		value, err = loader(ctx)
		if err != nil {
			return "", err
		}
		if err = r.cache.Set(ctx, key, value, r.ttl); err != nil {
			return value, nil
		}
	}
	if r.ttl > 0 {
		r.mu.Lock()
		if !r.stopped {
			entry := r.entries[key]
			now := time.Now()
			if entry == nil {
				entry = &refreshEntry{key: key, loader: loader, expiresAt: now.Add(r.ttl)}
				r.entries[key] = entry
			}
			entry.lastUsed = now
			entry.loader = loader
			r.scheduleLocked(entry, now)
		}
		r.mu.Unlock()
	}
	return value, nil
}
func (r *RefreshAhead) scheduleLocked(entry *refreshEntry, now time.Time) {
	if r.stopped || entry.refreshing || now.Before(entry.expiresAt.Add(-r.refreshTime)) || now.Before(entry.nextRetry) {
		return
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return
	}
	entry.refreshing = true
	loader := entry.loader
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() { <-r.slots }()
		ctx, cancel := context.WithTimeout(r.taskCtx, r.timeout)
		defer cancel()
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("refresh loader panic: %v", p)
				}
			}()
			data, err := loader(ctx)
			if err != nil {
				return err
			}
			return r.cache.Set(ctx, entry.key, data, r.ttl)
		}()
		r.mu.Lock()
		defer r.mu.Unlock()
		entry.refreshing = false
		if err == nil {
			entry.expiresAt = time.Now().Add(r.ttl)
			entry.failures = 0
			entry.nextRetry = time.Time{}
		} else {
			entry.failures++
			delay := 100 * time.Millisecond
			for i := 1; i < entry.failures && delay < 30*time.Second; i++ {
				delay *= 2
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			entry.nextRetry = time.Now().Add(delay)
		}
	}()
}
func (r *RefreshAhead) refreshLoop() {
	defer r.wg.Done()
	interval := max(r.refreshTime/2, time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case now := <-ticker.C:
			r.mu.Lock()
			for key, entry := range r.entries {
				if now.Sub(entry.lastUsed) >= r.ttl && !entry.refreshing {
					delete(r.entries, key)
					continue
				}
				r.scheduleLocked(entry, now)
			}
			r.mu.Unlock()
		}
	}
}
func (r *RefreshAhead) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.stopCh)
		go func() { r.wg.Wait(); r.cancel(); close(r.done) }()
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		r.cancel()
		return ctx.Err()
	}
}
func (r *RefreshAhead) Stop() { _ = r.Close(context.Background()) }
