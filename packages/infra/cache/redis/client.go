package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/wplbyx/modular/packages/config/configitem"
)

// NewRedisClient 根据配置创建并验证 go-redis 客户端。
func NewRedisClient(ctx context.Context, cfg *configitem.Redis) (redis.UniversalClient, error) {
	if cfg == nil {
		return nil, errors.New("redis config is nil")
	}

	addresses := cfg.Urls
	if len(addresses) == 0 {
		addresses = []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)}
	}

	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:                 addresses,
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

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
