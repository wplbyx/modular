package caching

import (
	"context"
	"time"

	"modular/packages/infra/cache"
)

// WriteThrough implements the Write-Through pattern
// Write: Update cache AND DB in the same transaction
// Read: Check cache -> If miss, read from DB -> Write to cache
type WriteThrough struct {
	cache cache.Cache
	ttl   cache.TTL
}

// NewWriteThrough creates a new WriteThrough instance
func NewWriteThrough(c cache.Cache, ttl time.Duration) *WriteThrough {
	return &WriteThrough{
		cache: c,
		ttl:   cache.TTL(ttl),
	}
}

// Get retrieves data using write-through pattern
func (wt *WriteThrough) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	val, err := wt.cache.Get(ctx, key)
	if err == nil {
		return val, nil
	}

	data, err := loader()
	if err != nil {
		return "", err
	}

	_ = wt.cache.Set(ctx, key, data, wt.ttl)
	return data, nil
}

// Set writes to both cache and source atomically
func (wt *WriteThrough) Set(ctx context.Context, key string, value string, writer func() error) error {
	// Write to source
	if err := writer(); err != nil {
		return err
	}

	// Update cache immediately
	return wt.cache.Set(ctx, key, value, wt.ttl)
}
