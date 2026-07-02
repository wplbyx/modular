package caching

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWriteBehindSetFlushesDefaultWriter(t *testing.T) {
	c := newMemoryCache()
	var mu sync.Mutex
	writes := make(map[string]string)

	wb := NewWriteBehind(c, time.Minute, 4, WithWriteBehindWriter(func(_ context.Context, key, value string) error {
		mu.Lock()
		writes[key] = value
		mu.Unlock()
		return nil
	}))
	defer wb.Stop()

	if err := wb.Set(context.Background(), "key", "value"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if got, err := c.Get(context.Background(), "key"); err != nil || got != "value" {
		t.Fatalf("cache Get() = %q, %v", got, err)
	}
	if err := wb.FlushContext(context.Background()); err != nil {
		t.Fatalf("FlushContext() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if writes["key"] != "value" {
		t.Fatalf("writer value = %q", writes["key"])
	}
}

func TestWriteBehindRequiresWriter(t *testing.T) {
	c := newMemoryCache()
	wb := NewWriteBehind(c, time.Minute, 1)
	defer wb.Stop()

	if err := wb.Set(context.Background(), "key", "value"); err == nil {
		t.Fatal("Set() error = nil")
	}
	if _, err := c.Get(context.Background(), "key"); err == nil {
		t.Fatal("cache was updated without writer")
	}
}

func TestWriteBehindQueueFull(t *testing.T) {
	c := newMemoryCache()
	started := make(chan struct{})
	release := make(chan struct{})

	wb := NewWriteBehind(c, time.Minute, 1, WithWriteBehindWriter(func(context.Context, string, string) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil
	}))
	defer wb.Stop()

	if err := wb.Set(context.Background(), "first", "1"); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	<-started
	if err := wb.Set(context.Background(), "second", "2"); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}
	if err := wb.Set(context.Background(), "third", "3"); err == nil {
		t.Fatal("third Set() error = nil")
	}

	close(release)
	if err := wb.FlushContext(context.Background()); err != nil {
		t.Fatalf("FlushContext() error = %v", err)
	}
}

type memoryCache struct {
	mu    sync.Mutex
	items map[string]string
}

func newMemoryCache() *memoryCache {
	return &memoryCache{items: make(map[string]string)}
}

func (c *memoryCache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[key]
	if !ok {
		return "", errors.New("cache miss")
	}
	return value, nil
}

func (c *memoryCache) Set(_ context.Context, key string, value string, _ time.Duration) error {
	c.mu.Lock()
	c.items[key] = value
	c.mu.Unlock()
	return nil
}

func (c *memoryCache) Del(_ context.Context, key string) error {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

func (c *memoryCache) Exists(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	return ok, nil
}

func (c *memoryCache) Expire(_ context.Context, _ string, _ time.Duration) error {
	return nil
}
