package caching

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCacheAsidePropagatesBackendGetError(t *testing.T) {
	backendErr := errors.New("redis unavailable")
	cache := &failingGetCache{err: backendErr}
	ca := NewCacheAside(cache, time.Minute)

	loaderCalled := false
	_, err := ca.Get(context.Background(), "key", func() (string, error) {
		loaderCalled = true
		return "source", nil
	})
	if !errors.Is(err, backendErr) {
		t.Fatalf("Get() error = %v, want backendErr", err)
	}
	if loaderCalled {
		t.Fatal("loader called on backend cache error")
	}
}

func TestCacheAsideLoadsOnCacheMiss(t *testing.T) {
	cache := newMemoryCache()
	ca := NewCacheAside(cache, time.Minute)

	got, err := ca.Get(context.Background(), "key", func() (string, error) {
		return "source", nil
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != "source" {
		t.Fatalf("Get() = %q", got)
	}
}

type failingGetCache struct {
	err error
}

func (c *failingGetCache) Get(context.Context, string) (string, error) { return "", c.err }
func (c *failingGetCache) Set(context.Context, string, string, time.Duration) error {
	return nil
}
func (c *failingGetCache) Del(context.Context, string) error { return nil }
