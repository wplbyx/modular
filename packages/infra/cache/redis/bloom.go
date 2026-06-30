package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
	"github.com/spaolacci/murmur3"
)

const (
	defaultBloomCapacity          = uint(1_000_000)
	defaultBloomFalsePositiveRate = 0.02
	bloomMetaPrefix               = "bloom:meta:"

	scriptSetBloom = `
for _, offset in ipairs(ARGV) do
	redis.call("SETBIT", KEYS[1], offset, 1)
end
return 1
`

	scriptGetBloom = `
for _, offset in ipairs(ARGV) do
	if tonumber(redis.call("GETBIT", KEYS[1], offset)) == 0 then
		return 0
	end
end
return 1
`
)

type bloomConfig struct {
	bits   uint
	hashes uint
}

// InitBloom configures a bitmap-backed Bloom filter for key.
func (c *RedisCache) InitBloom(key string, capacity uint, falsePositiveRate float64) error {
	return c.InitBloomWithContext(context.Background(), key, capacity, falsePositiveRate)
}

func (c *RedisCache) InitBloomWithContext(ctx context.Context, key string, capacity uint, falsePositiveRate float64) error {
	if key == "" {
		return errors.New("bloom key is empty")
	}

	cfg, err := newBloomConfig(capacity, falsePositiveRate)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.bloom[key] = cfg
	c.mu.Unlock()

	if err := c.saveBloomConfig(ctx, key, cfg); err != nil {
		return err
	}
	return nil
}

// Add adds an item to the Bloom filter.
func (c *RedisCache) Add(ctx context.Context, key string, item []byte) error {
	cfg, err := c.bloomConfig(key)
	if err != nil {
		return err
	}

	offsets, err := bloomOffsets(cfg, item)
	if err != nil {
		return err
	}

	if err := goredis.NewScript(scriptSetBloom).Run(ctx, c.client, []string{key}, offsets...).Err(); err != nil {
		return fmt.Errorf("set bloom bits: %w", err)
	}
	return nil
}

// MightContain checks whether an item may be present in the Bloom filter.
func (c *RedisCache) MightContain(ctx context.Context, key string, item []byte) (bool, error) {
	cfg, err := c.bloomConfig(key)
	if err != nil {
		return false, err
	}

	offsets, err := bloomOffsets(cfg, item)
	if err != nil {
		return false, err
	}

	result, err := goredis.NewScript(scriptGetBloom).Run(ctx, c.client, []string{key}, offsets...).Bool()
	if err != nil {
		return false, fmt.Errorf("get bloom bits: %w", err)
	}
	return result, nil
}

func (c *RedisCache) bloomConfig(key string) (bloomConfig, error) {
	if key == "" {
		return bloomConfig{}, errors.New("bloom key is empty")
	}

	c.mu.RLock()
	cfg, ok := c.bloom[key]
	c.mu.RUnlock()
	if ok {
		return cfg, nil
	}

	cfg, err := c.loadBloomConfig(context.Background(), key)
	if err == nil {
		c.mu.Lock()
		if existing, ok := c.bloom[key]; ok {
			c.mu.Unlock()
			return existing, nil
		}
		c.bloom[key] = cfg
		c.mu.Unlock()
		return cfg, nil
	}

	cfg, err = newBloomConfig(defaultBloomCapacity, defaultBloomFalsePositiveRate)
	if err != nil {
		return bloomConfig{}, err
	}

	c.mu.Lock()
	if existing, ok := c.bloom[key]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	c.bloom[key] = cfg
	c.mu.Unlock()
	return cfg, nil
}

func (c *RedisCache) saveBloomConfig(ctx context.Context, key string, cfg bloomConfig) error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.HSet(ctx, bloomMetaPrefix+key,
		"bits", strconv.FormatUint(uint64(cfg.bits), 10),
		"hashes", strconv.FormatUint(uint64(cfg.hashes), 10),
	).Err()
}

func (c *RedisCache) loadBloomConfig(ctx context.Context, key string) (bloomConfig, error) {
	if c == nil || c.client == nil {
		return bloomConfig{}, errors.New("redis client is nil")
	}

	values, err := c.client.HGetAll(ctx, bloomMetaPrefix+key).Result()
	if err != nil {
		return bloomConfig{}, err
	}
	if len(values) == 0 {
		return bloomConfig{}, errors.New("bloom config not found")
	}

	bits, err := strconv.ParseUint(values["bits"], 10, 64)
	if err != nil {
		return bloomConfig{}, fmt.Errorf("parse bloom bits: %w", err)
	}
	hashes, err := strconv.ParseUint(values["hashes"], 10, 64)
	if err != nil {
		return bloomConfig{}, fmt.Errorf("parse bloom hashes: %w", err)
	}
	cfg := bloomConfig{bits: uint(bits), hashes: uint(hashes)}
	if cfg.bits == 0 || cfg.hashes == 0 {
		return bloomConfig{}, errors.New("invalid persisted bloom config")
	}
	return cfg, nil
}

func newBloomConfig(capacity uint, falsePositiveRate float64) (bloomConfig, error) {
	if capacity == 0 {
		return bloomConfig{}, errors.New("bloom capacity must be positive")
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return bloomConfig{}, errors.New("bloom false positive rate must be between 0 and 1")
	}

	bits := uint(math.Ceil(-float64(capacity) * math.Log(falsePositiveRate) / math.Pow(math.Log(2), 2)))
	hashes := uint(math.Round(float64(bits) / float64(capacity) * math.Log(2)))
	if hashes == 0 {
		hashes = 1
	}

	return bloomConfig{bits: bits, hashes: hashes}, nil
}

func bloomOffsets(cfg bloomConfig, item []byte) ([]interface{}, error) {
	if cfg.bits == 0 || cfg.hashes == 0 {
		return nil, errors.New("invalid bloom config")
	}

	result := make([]interface{}, 0, cfg.hashes)
	for i := uint(0); i < cfg.hashes; i++ {
		hash := murmur3.New32WithSeed(uint32(i))
		if _, err := hash.Write(item); err != nil {
			return nil, err
		}
		offset := uint(hash.Sum32()) % cfg.bits
		result = append(result, strconv.FormatUint(uint64(offset), 10))
	}
	return result, nil
}
