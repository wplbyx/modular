package concurrency

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Memoizer caches values with optional TTL and safe concurrent access.
type Memoizer[K comparable, V any] struct {
	mu    sync.RWMutex
	ttl   time.Duration
	now   func() time.Time
	items map[K]memoItem[V]
}

type memoItem[V any] struct {
	value     V
	expiresAt time.Time
}

func NewMemoizer[K comparable, V any](ttl time.Duration) *Memoizer[K, V] {
	return &Memoizer[K, V]{
		ttl:   ttl,
		now:   time.Now,
		items: make(map[K]memoItem[V]),
	}
}

func (m *Memoizer[K, V]) Get(key K) (V, bool) {
	m.mu.RLock()
	item, ok := m.items[key]
	m.mu.RUnlock()

	var zero V
	if !ok {
		return zero, false
	}
	if !item.expiresAt.IsZero() && m.now().After(item.expiresAt) {
		m.Delete(key)
		return zero, false
	}
	return item.value, true
}

func (m *Memoizer[K, V]) Set(key K, value V) {
	expiresAt := time.Time{}
	if m.ttl > 0 {
		expiresAt = m.now().Add(m.ttl)
	}

	m.mu.Lock()
	if m.items == nil {
		m.items = make(map[K]memoItem[V])
	}
	m.items[key] = memoItem[V]{value: value, expiresAt: expiresAt}
	m.mu.Unlock()
}

func (m *Memoizer[K, V]) GetOrLoad(ctx context.Context, key K, loader func(context.Context) (V, error)) (V, error) {
	if val, ok := m.Get(key); ok {
		return val, nil
	}
	var zero V
	if loader == nil {
		return zero, errors.New("memoizer loader is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	val, err := loader(ctx)
	if err != nil {
		return zero, err
	}
	m.Set(key, val)
	return val, nil
}

func (m *Memoizer[K, V]) Delete(key K) {
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
}

func (m *Memoizer[K, V]) Clear() {
	m.mu.Lock()
	m.items = make(map[K]memoItem[V])
	m.mu.Unlock()
}

func (m *Memoizer[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}
