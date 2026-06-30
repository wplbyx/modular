package cache

import (
	"context"
	"time"
)

// TODO: 这里的设计思路错了：不应该封装 get,set... 等等这些命令操作，原来的库里是有的
// 		 应该封装哪些流程的操作： 布隆过滤器，分布式锁，等等这种

// TTL is a type alias for cache expiration durations
type TTL time.Duration

// Common TTL constants
const (
	TTLNever TTL = 0 // Never expire
	// Note: Use time.Duration constants directly when calling Set/Expire
	// Example: cache.Set(ctx, key, value, cache.TTL(time.Minute))
)

// Cache is the basic cache interface for Key-Value operations
type Cache interface {
	// Get retrieves a value from cache by key
	Get(ctx context.Context, key string) (string, error)

	// Set stores a key-value pair with optional TTL
	Set(ctx context.Context, key string, value string, ttl TTL) error

	// Del removes a key from cache
	Del(ctx context.Context, key string) error

	// Exists checks if a key exists in cache
	Exists(ctx context.Context, key string) (bool, error)

	// Expire sets or updates the TTL for an existing key
	Expire(ctx context.Context, key string, ttl TTL) error
}

// Field represents a key-value pair for Hash operations
type Field struct {
	Key   string
	Value string
}

// HashCache is the interface for Hash data structure operations
type HashCache interface {
	// HGet retrieves a single field from a hash
	HGet(ctx context.Context, key, field string) (string, error)

	// HSet sets multiple fields in a hash
	HSet(ctx context.Context, key string, fields ...Field) error

	// HDel removes fields from a hash
	HDel(ctx context.Context, key string, fields ...string) error

	// HGetAll retrieves all fields from a hash
	HGetAll(ctx context.Context, key string) (map[string]string, error)

	// HExists checks if a field exists in a hash
	HExists(ctx context.Context, key, field string) (bool, error)
}

// Member represents a score-value pair for SortedSet operations
type Member struct {
	Score float64
	Value string
}

// RangeOptions represents options for range queries
type RangeOptions struct {
	Offset int64
	Count  int64
	Rev    bool // reverse order
}

// SortedSetCache is the interface for SortedSet (ZSet) operations
type SortedSetCache interface {
	// ZAdd adds members to a sorted set
	ZAdd(ctx context.Context, key string, members ...Member) error

	// ZRange returns members in the specified range
	ZRange(ctx context.Context, key string, opts RangeOptions) ([]string, error)

	// ZRangeWithScores returns members with scores in the specified range
	ZRangeWithScores(ctx context.Context, key string, opts RangeOptions) ([]Member, error)

	// ZRem removes members from a sorted set
	ZRem(ctx context.Context, key string, members ...string) error

	// ZScore returns the score of a member
	ZScore(ctx context.Context, key, member string) (float64, error)

	// ZRank returns the rank of a member
	ZRank(ctx context.Context, key, member string) (int64, error)

	// ZCard returns the number of members in a sorted set
	ZCard(ctx context.Context, key string) (int64, error)
}

// BloomFilter is the interface for bloom filter operations
type BloomFilter interface {
	// Add adds an item to the bloom filter
	Add(ctx context.Context, key string, item []byte) error

	// MightContain checks if an item might be in the bloom filter
	// Returns true if the item might be present (may have false positives)
	// Returns false if the item is definitely not present
	MightContain(ctx context.Context, key string, item []byte) (bool, error)
}

// FullCache combines all cache interfaces
type FullCache interface {
	Cache
	HashCache
	SortedSetCache
}

// FullCacheWithBloom combines all cache interfaces with bloom filter
type FullCacheWithBloom interface {
	FullCache
	BloomFilter
}

// CacheGetter is a minimal read-only cache interface
type CacheGetter interface {
	Get(ctx context.Context, key string) (string, error)
}

// CacheSetter is a minimal write-only cache interface
type CacheSetter interface {
	Set(ctx context.Context, key string, value string, ttl TTL) error
}

// CacheGetterSetter combines read and write operations
type CacheGetterSetter interface {
	CacheGetter
	CacheSetter
}
