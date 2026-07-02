package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"modular/packages/config"
	"modular/packages/infra/cache"
)

// globalClient 是全局 Redis 客户端，供需要直接访问 Redis 的包在装配阶段使用。
var globalClient *RedisCache

// RedisCache 封装 redis.UniversalClient，同时实现 cache.BloomFilter 接口。
// 它保留完整的 Get/Set/Del 等方法供直接调用，但这些方法不再绑定到任何接口 ——
// 消费方可以直接使用，也可以通过 GetClient() 拿到原生客户端。
type RedisCache struct {
	client redis.UniversalClient
	bloom  map[string]bloomConfig
	mu     sync.RWMutex
}

// 确保 RedisCache 实现布隆过滤器接口
var _ cache.BloomFilter = (*RedisCache)(nil)

func GetClient() redis.UniversalClient {
	if globalClient == nil {
		return nil
	}
	return globalClient.client
}

// NewRedisCache creates a new RedisCache instance
func NewRedisCache(cfg *config.Redis) (*RedisCache, error) {
	if cfg == nil {
		return nil, errors.New("redis config is nil")
	}

	address := cfg.Urls
	if len(address) == 0 {
		address = []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)}
	}

	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:                 address,
		Username:              cfg.Username,
		Password:              cfg.Password,
		DB:                    cfg.Database,
		PoolSize:              cfg.PoolSize,
		MinIdleConns:          cfg.MinIdleConn,
		DialTimeout:           cfg.DialTimeout,
		ReadTimeout:           cfg.ReadTimeout,
		WriteTimeout:          cfg.WriteTimeout,
		MaxRetries:            cfg.MaxRetries,
		MinRetryBackoff:       time.Millisecond * time.Duration(cfg.MinRetryBackoff),
		MaxRetryBackoff:       time.Millisecond * time.Duration(cfg.MaxRetryBackoff),
		ContextTimeoutEnabled: true,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	rc := &RedisCache{
		client: client,
		bloom:  make(map[string]bloomConfig),
	}
	globalClient = rc
	return rc, nil
}

// Close closes the underlying Redis client.
func (c *RedisCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// ==================== Basic Cache Operations ====================

// Get retrieves a value from cache by key
func (c *RedisCache) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// Set stores a key-value pair with optional TTL
func (c *RedisCache) Set(ctx context.Context, key string, value string, ttl cache.TTL) error {
	if ttl < cache.TTLNever {
		return fmt.Errorf("ttl must be non-negative")
	}

	if ttl == cache.TTLNever {
		return c.client.Set(ctx, key, value, 0).Err()
	}
	return c.client.Set(ctx, key, value, time.Duration(ttl)).Err()
}

// Del removes a key from cache
func (c *RedisCache) Del(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// Exists checks if a key exists in cache
func (c *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	val, err := c.client.Exists(ctx, key).Result()
	return val > 0, err
}

// Expire sets or updates the TTL for an existing key
func (c *RedisCache) Expire(ctx context.Context, key string, ttl cache.TTL) error {
	if ttl <= cache.TTLNever {
		return fmt.Errorf("ttl must be positive")
	}
	return c.client.Expire(ctx, key, time.Duration(ttl)).Err()
}

// ==================== Hash Operations ====================

// HGet retrieves a single field from a hash
func (c *RedisCache) HGet(ctx context.Context, key, field string) (string, error) {
	return c.client.HGet(ctx, key, field).Result()
}

// HSet sets multiple fields in a hash
func (c *RedisCache) HSet(ctx context.Context, key string, fields ...cache.Field) error {
	if len(fields) == 0 {
		return nil
	}

	values := make([]interface{}, 0, len(fields)*2)
	for _, f := range fields {
		values = append(values, f.Key, f.Value)
	}

	return c.client.HSet(ctx, key, values...).Err()
}

// HDel removes fields from a hash
func (c *RedisCache) HDel(ctx context.Context, key string, fields ...string) error {
	if len(fields) == 0 {
		return nil
	}
	return c.client.HDel(ctx, key, fields...).Err()
}

// HGetAll retrieves all fields from a hash
func (c *RedisCache) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.client.HGetAll(ctx, key).Result()
}

// HExists checks if a field exists in a hash
func (c *RedisCache) HExists(ctx context.Context, key, field string) (bool, error) {
	return c.client.HExists(ctx, key, field).Result()
}

// ==================== Sorted Set Operations ====================

// ZAdd adds members to a sorted set
func (c *RedisCache) ZAdd(ctx context.Context, key string, members ...cache.Member) error {
	if len(members) == 0 {
		return nil
	}

	zMembers := make([]redis.Z, 0, len(members))
	for _, m := range members {
		zMembers = append(zMembers, redis.Z{Score: m.Score, Member: m.Value})
	}

	return c.client.ZAdd(ctx, key, zMembers...).Err()
}

// ZRange returns members in the specified range
func (c *RedisCache) ZRange(ctx context.Context, key string, opts cache.RangeOptions) ([]string, error) {
	start := opts.Offset
	stop := start + opts.Count - 1
	if opts.Count == 0 {
		stop = -1
	}

	if opts.Rev {
		return c.client.ZRevRange(ctx, key, start, stop).Result()
	}
	return c.client.ZRange(ctx, key, start, stop).Result()
}

// ZRangeWithScores returns members with scores in the specified range
func (c *RedisCache) ZRangeWithScores(ctx context.Context, key string, opts cache.RangeOptions) ([]cache.Member, error) {
	start := opts.Offset
	stop := start + opts.Count - 1
	if opts.Count == 0 {
		stop = -1
	}

	var result []redis.Z
	var err error
	if opts.Rev {
		result, err = c.client.ZRevRangeWithScores(ctx, key, start, stop).Result()
	} else {
		result, err = c.client.ZRangeWithScores(ctx, key, start, stop).Result()
	}
	if err != nil {
		return nil, err
	}

	members := make([]cache.Member, 0, len(result))
	for _, z := range result {
		members = append(members, cache.Member{Score: z.Score, Value: z.Member.(string)})
	}
	return members, nil
}

// ZRem removes members from a sorted set
func (c *RedisCache) ZRem(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	// Convert string members to interface{} slice
	ifaceMembers := make([]interface{}, len(members))
	for i, m := range members {
		ifaceMembers[i] = m
	}
	return c.client.ZRem(ctx, key, ifaceMembers...).Err()
}

// ZScore returns the score of a member
func (c *RedisCache) ZScore(ctx context.Context, key, member string) (float64, error) {
	return c.client.ZScore(ctx, key, member).Result()
}

// ZRank returns the rank of a member
func (c *RedisCache) ZRank(ctx context.Context, key, member string) (int64, error) {
	return c.client.ZRank(ctx, key, member).Result()
}

// ZCard returns the number of members in a sorted set
func (c *RedisCache) ZCard(ctx context.Context, key string) (int64, error) {
	return c.client.ZCard(ctx, key).Result()
}

// ==================== Client Access ====================

// GetClient returns the underlying redis.UniversalClient.
func (c *RedisCache) GetClient() redis.UniversalClient {
	return c.client
}
