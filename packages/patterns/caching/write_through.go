package caching

import (
	"context"
	"errors"
	"time"
)

// WriteThrough 实现 Write-Through（写穿透）模式。
// 写：更新缓存和源在同一事务中完成
// 读：查缓存 -> 未命中则从源读取 -> 回写缓存
type WriteThrough struct {
	cache KVCache
	ttl   time.Duration
}

// NewWriteThrough 创建 WriteThrough 实例
func NewWriteThrough(c KVCache, ttl time.Duration) *WriteThrough {
	return &WriteThrough{
		cache: c,
		ttl:   ttl,
	}
}

// Get 使用 write-through 模式读取数据
func (wt *WriteThrough) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	val, err := wt.cache.Get(ctx, key)
	if err == nil {
		return val, nil
	}
	if !errors.Is(err, ErrCacheMiss) {
		return "", err
	}

	data, err := loader()
	if err != nil {
		return "", err
	}

	// 缓存回写失败不影响读结果，显式忽略
	_ = wt.cache.Set(ctx, key, data, wt.ttl)
	return data, nil
}

// Set 原子地写入缓存和源
func (wt *WriteThrough) Set(ctx context.Context, key string, value string, writer func() error) error {
	if err := writer(); err != nil {
		return err
	}

	return wt.cache.Set(ctx, key, value, wt.ttl)
}
