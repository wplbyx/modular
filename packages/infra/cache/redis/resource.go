package redis

import (
	"context"
	"errors"

	goredis "github.com/redis/go-redis/v9"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
)

// Resource 是由 core.ManagedResource 管理的 Redis 客户端。
type Resource = core.ManagedResource[goredis.UniversalClient]

type resourceConfig struct {
	connect func(context.Context, *configitem.Redis) (goredis.UniversalClient, error)
}

// ResourceOption 配置 Redis Resource。
type ResourceOption func(*resourceConfig)

// WithConnector 覆盖 Redis 建连函数，主要用于测试或定制客户端创建。
func WithConnector(fn func(context.Context, *configitem.Redis) (goredis.UniversalClient, error)) ResourceOption {
	return func(cfg *resourceConfig) {
		if fn != nil {
			cfg.connect = fn
		}
	}
}

// NewResource 创建 Redis 生命周期资源。
func NewResource(cfg *configitem.Redis, options ...ResourceOption) *Resource {
	resourceCfg := resourceConfig{connect: NewRedisClient}
	for _, option := range options {
		if option != nil {
			option(&resourceCfg)
		}
	}

	return core.NewManagedResource(
		"redis",
		func(ctx context.Context) (goredis.UniversalClient, error) {
			if cfg == nil {
				return nil, errors.New("redis config is nil")
			}
			return resourceCfg.connect(ctx, cfg)
		},
		func(_ context.Context, client goredis.UniversalClient) error {
			return client.Close()
		},
		core.WithResourceCheck(func(ctx context.Context, client goredis.UniversalClient) error {
			return client.Ping(ctx).Err()
		}),
	)
}
