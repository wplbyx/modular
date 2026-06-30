package caching

import (
	"context"
	"time"

	"holographic/packages/infra/cache"
)

// CacheAside implements the Cache-Aside pattern
// Read: Check cache -> If miss, read from DB -> Write to cache -> Return
// Write: Update DB -> Invalidate cache
type CacheAside struct {
	cache cache.Cache
	ttl   cache.TTL
}

// NewCacheAside creates a new CacheAside instance
func NewCacheAside(c cache.Cache, ttl time.Duration) *CacheAside {
	return &CacheAside{
		cache: c,
		ttl:   cache.TTL(ttl),
	}
}

// Get retrieves data using cache-aside pattern
func (ca *CacheAside) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	// Try to get from cache first
	val, err := ca.cache.Get(ctx, key)
	if err == nil {
		return val, nil
	}

	// Cache miss - load from source
	data, err := loader()
	if err != nil {
		return "", err
	}

	// Store in cache
	if err := ca.cache.Set(ctx, key, data, ca.ttl); err != nil {
		// Log error but don't fail the operation
	}

	return data, nil
}

// Set updates both cache and source
func (ca *CacheAside) Set(ctx context.Context, key string, value string, writer func() error) error {
	// Write to source first
	if err := writer(); err != nil {
		return err
	}

	// Invalidate cache
	return ca.cache.Del(ctx, key)
}

// Delete removes from both cache and source
func (ca *CacheAside) Delete(ctx context.Context, key string, deleter func() error) error {
	// Delete from source first
	if err := deleter(); err != nil {
		return err
	}

	// Remove from cache
	return ca.cache.Del(ctx, key)
}
