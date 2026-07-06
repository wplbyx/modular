package caching

import (
	"context"
	"time"
)

// CacheAside 实现 Cache-Aside（旁路缓存）模式。
// 读：查缓存 -> 未命中则从源加载 -> 回写缓存 -> 返回
// 写：更新源 -> 失效缓存
type CacheAside struct {
	cache KVCache
	ttl   time.Duration
}

// NewCacheAside 创建 CacheAside 实例
func NewCacheAside(c KVCache, ttl time.Duration) *CacheAside {
	return &CacheAside{
		cache: c,
		ttl:   ttl,
	}
}

// Get 使用 cache-aside 模式读取数据
func (ca *CacheAside) Get(ctx context.Context, key string, loader func() (string, error)) (string, error) {
	val, err := ca.cache.Get(ctx, key)
	if err == nil {
		return val, nil
	}

	data, err := loader()
	if err != nil {
		return "", err
	}

	// 缓存回写失败不影响读结果，但显式忽略而非空 if 块
	_ = ca.cache.Set(ctx, key, data, ca.ttl)

	return data, nil
}

// Set 同时更新缓存和源
func (ca *CacheAside) Set(ctx context.Context, key string, value string, writer func() error) error {
	if err := writer(); err != nil {
		return err
	}

	return ca.cache.Del(ctx, key)
}

// Delete 同时从缓存和源删除
func (ca *CacheAside) Delete(ctx context.Context, key string, deleter func() error) error {
	if err := deleter(); err != nil {
		return err
	}

	return ca.cache.Del(ctx, key)
}
