package cache

 import (
 	"context"
 	"time"
 )

 // 本包定义缓存相关的共享类型与高层流程接口。
 // 不封装 Redis 的 get/set/del 等底层命令 -- 底层库（go-redis）已经提供，
 // 消费方可以直接通过 RedisCache.GetClient() 拿到原生客户端做这些操作。
 // 本包只定义真正需要抽象的高层能力：布隆过滤器。

 // TTL 是缓存过期时间的类型别名。
 type TTL time.Duration

 const (
 	TTLNever TTL = 0 // 永不过期
 )

 // Field 代表 Hash 操作的键值对。
 type Field struct {
 	Key   string
 	Value string
 }

 // Member 代表 SortedSet 的分值/成员对。
 type Member struct {
 	Score float64
 	Value string
 }

 // RangeOptions 代表范围查询的选项。
 type RangeOptions struct {
 	Offset int64
 	Count  int64
 	Rev    bool // 是否倒序
 }

 // BloomFilter 布隆过滤器接口（高层流程能力）。
 type BloomFilter interface {
 	// Add 向布隆过滤器添加元素
 	Add(ctx context.Context, key string, item []byte) error
 	// MightContain 检查元素是否可能存在（可能有误判）
 	MightContain(ctx context.Context, key string, item []byte) (bool, error)
 }

 // bloomConfig 是 Redis 布隆过滤器模块的内部配置（不导出）。
 type bloomConfig struct {
 	errorRate float64
 	capacity  int64
 }
 