package caching

import (
	"context"
	"time"
)

// KVCache 定义缓存模式所需的最小 KV 操作接口。
// 这是一个本地接口，不依赖 infra/cache —— 任何具备 Get/Set/Del 能力的
// 缓存客户端（如 RedisCache）都隐式满足此接口，可直接传入。
type KVCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}
