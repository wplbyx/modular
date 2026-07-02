package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"modular/packages/config"
)

// globalClient 是全局 Redis 客户端，供需要直接访问 Redis 的包在装配阶段使用。
// 由 NewRedisClient 设置，通过 GetClient() 读取。
var globalClient redis.UniversalClient

// GetClient 返回全局 Redis 客户端；若 NewRedisClient 尚未调用则返回 nil。
func GetClient() redis.UniversalClient {
	return globalClient
}

// NewRedisClient 根据配置创建 go-redis 客户端，Ping 探活后存为全局实例并返回。
func NewRedisClient(cfg *config.Redis) (redis.UniversalClient, error) {
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

	globalClient = client
	return client, nil
}
