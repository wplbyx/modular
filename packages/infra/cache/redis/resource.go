package redis

import (
	"context"
	"errors"

	goredis "github.com/redis/go-redis/v9"

	"modular/packages/config"
	"modular/packages/core"
)

var _ core.Resource = (*Resource)(nil)

// Resource 将 Redis 客户端纳入 Application 生命周期。
type Resource struct {
	cfg     *config.Redis
	client  goredis.UniversalClient
	connect func(*config.Redis) (goredis.UniversalClient, error)
}

// ResourceOption 配置 Redis Resource。
type ResourceOption func(*Resource)

// WithConnector 覆盖 Redis 建连函数，主要用于测试或自定义客户端创建。
func WithConnector(fn func(*config.Redis) (goredis.UniversalClient, error)) ResourceOption {
	return func(r *Resource) {
		if fn != nil {
			r.connect = fn
		}
	}
}

// NewResource 创建 Redis 生命周期资源。
func NewResource(cfg *config.Redis, opts ...ResourceOption) *Resource {
	r := &Resource{cfg: cfg, connect: NewRedisClient}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

func (r *Resource) Name() string { return "redis" }

func (r *Resource) Setup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.cfg == nil {
		return errors.New("redis config is nil")
	}
	client, err := r.connect(r.cfg)
	if err != nil {
		return err
	}
	r.client = client
	return nil
}

func (r *Resource) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.client == nil {
		return nil
	}
	err := r.client.Close()
	r.client = nil
	return err
}

// Client 返回已初始化的 Redis 客户端。
func (r *Resource) Client() goredis.UniversalClient { return r.client }
