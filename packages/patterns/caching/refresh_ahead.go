package caching

import (
	"context"
	"sync"
	"time"

	"holographic/packages/infra/cache"
)

// RefreshAhead implements the Refresh-Ahead pattern
// Proactively refresh cache before expiration
type RefreshAhead struct {
	cache       cache.Cache
	ttl         cache.TTL
	refreshTime time.Duration // Time before expiration to refresh
	entries     sync.Map      // key -> *refreshEntry
	stopCh      chan struct{}
	wg          sync.WaitGroup
	stopOnce    sync.Once
}

type refreshEntry struct {
	key       string
	loader    func() (string, error)
	expiresAt time.Time
	mu        sync.RWMutex
}

// NewRefreshAhead creates a new RefreshAhead instance
func NewRefreshAhead(c cache.Cache, ttl time.Duration, refreshBefore time.Duration) *RefreshAhead {
	if refreshBefore <= 0 {
		refreshBefore = ttl / 2
	}
	if refreshBefore <= 0 {
		refreshBefore = time.Second
	}
	ra := &RefreshAhead{
		cache:       c,
		ttl:         cache.TTL(ttl),
		refreshTime: refreshBefore,
		stopCh:      make(chan struct{}),
	}

	// Start background refresher
	ra.wg.Add(1)
	go ra.refreshLoop()

	return ra
}

// Get retrieves data with automatic refresh scheduling
func (ra *RefreshAhead) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	val, err := ra.cache.Get(ctx, key)
	if err == nil {
		// Schedule refresh if needed
		ra.scheduleRefresh(key, loader)
		return val, nil
	}

	// Cache miss - load and cache
	data, err := loader()
	if err != nil {
		return "", err
	}

	_ = ra.cache.Set(ctx, key, data, ra.ttl)
	ra.trackEntry(key, loader)
	return data, nil
}

func (ra *RefreshAhead) trackEntry(key string, loader func() (string, error)) {
	entry := &refreshEntry{
		key:       key,
		loader:    loader,
		expiresAt: time.Now().Add(time.Duration(ra.ttl)),
	}
	ra.entries.Store(key, entry)
}

func (ra *RefreshAhead) scheduleRefresh(key string, loader func() (string, error)) {
	val, ok := ra.entries.Load(key)
	if !ok {
		ra.trackEntry(key, loader)
		return
	}

	entry := val.(*refreshEntry)
	entry.mu.RLock()
	shouldRefresh := time.Now().After(entry.expiresAt.Add(-ra.refreshTime))
	entry.mu.RUnlock()

	if shouldRefresh {
		go ra.refreshEntry(context.Background(), entry)
	}
}

func (ra *RefreshAhead) refreshEntry(ctx context.Context, entry *refreshEntry) {
	data, err := entry.loader()
	if err != nil {
		return
	}

	_ = ra.cache.Set(ctx, entry.key, data, ra.ttl)

	entry.mu.Lock()
	entry.expiresAt = time.Now().Add(time.Duration(ra.ttl))
	entry.mu.Unlock()
}

func (ra *RefreshAhead) refreshLoop() {
	defer ra.wg.Done()

	ticker := time.NewTicker(ra.refreshTime / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ra.stopCh:
			return
		case <-ticker.C:
			ra.entries.Range(func(key, value interface{}) bool {
				entry := value.(*refreshEntry)
				ra.scheduleRefresh(entry.key, entry.loader)
				return true
			})
		}
	}
}

// Stop stops the background refresher
func (ra *RefreshAhead) Stop() {
	ra.stopOnce.Do(func() {
		close(ra.stopCh)
	})
	ra.wg.Wait()
}
