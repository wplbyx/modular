package resilience

import (
	"context"
	"sync"
	"time"

	"holographic/packages/errs"
	"holographic/packages/log"
)

var (
	// ErrRateLimitExceeded 超过限流阈值错误
	ErrRateLimitExceeded = errs.New(
		errs.WithCode(540),
		errs.WithMsgf("rate limit exceeded"),
	)
)

// RateLimiterConfig 限流器配置

type RateLimiterConfig struct {
	// Name 限流器名称
	Name string
	// Rate 每秒生成的令牌数
	Rate float64
	// Burst 令牌桶容量
	Burst int
}

// 默认限流器配置

var DefaultRateLimiterConfig = RateLimiterConfig{
	Name:  "default",
	Rate:  100.0, // 每秒100个令牌
	Burst: 10,    // 最大突发10个请求
}

// rateLimiter 基于令牌桶算法的限流器实现

type rateLimiter struct {
	config RateLimiterConfig

	mutex     sync.Mutex
	lastTime  time.Time
	available float64
}

// NewRateLimiter 创建一个新的限流器
func NewRateLimiter(config RateLimiterConfig) RateLimiter {
	// 使用默认配置填充未设置的字段
	if config.Rate <= 0 {
		config.Rate = DefaultRateLimiterConfig.Rate
	}
	if config.Burst <= 0 {
		config.Burst = DefaultRateLimiterConfig.Burst
	}
	if config.Name == "" {
		config.Name = DefaultRateLimiterConfig.Name
	}

	return &rateLimiter{
		config:    config,
		lastTime:  time.Now(),
		available: float64(config.Burst),
	}
}

// Name 返回限流器名称
func (rl *rateLimiter) Name() string {
	return rl.config.Name
}

// Allow 判断是否允许请求通过
func (rl *rateLimiter) Allow(ctx context.Context) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	// 计算当前可用令牌数
	now := time.Now()
	elapsed := now.Sub(rl.lastTime).Seconds()
	// 增加可用令牌，但不超过桶容量
	rl.available = min(float64(rl.config.Burst), rl.available+elapsed*rl.config.Rate)
	// 更新最后计算时间
	rl.lastTime = now

	// 检查是否有足够的令牌
	if rl.available >= 1.0 {
		// 消耗一个令牌
		rl.available--
		return true
	}

	// 没有足够的令牌，拒绝请求
	log.Infof("Rate limiter '%s' rejected request, available tokens: %.2f, rate: %.2f/s, burst: %d",
		rl.config.Name, rl.available, rl.config.Rate, rl.config.Burst)
	return false
}

// Take 阻塞等待直到获取到令牌或超时
func (rl *rateLimiter) Take(ctx context.Context) error {
	// 尝试立即获取令牌
	if rl.Allow(ctx) {
		return nil
	}

	// 如果上下文已经取消，直接返回错误
	select {
	case <-ctx.Done():
		// 包装上下文取消错误，添加限流器上下文
		return errs.New(
			errs.WithCode(499),
			errs.WithMsgf("context canceled while waiting for rate limiter '%s'", rl.config.Name),
			errs.WithCause(ctx.Err()),
		)
	default:
	}

	// 计算需要等待的时间
	// 由于令牌每秒生成rl.config.Rate个，所以需要等待的时间大约是 (1 - available) / rate
	rate := rl.config.Rate
	if rate <= 0 {
		return errs.New(
			errs.WithCode(540),
			errs.WithMsgf("invalid rate configuration for rate limiter '%s'", rl.config.Name),
			errs.WithCause(ErrRateLimitExceeded),
		)
	}

	// 计算理论上需要等待的时间
	// 这里简化处理，实际上应该更精确地计算
	waitTime := time.Duration(float64(time.Second) / rate)

	// 创建一个定时器
	timer := time.NewTimer(waitTime)
	defer timer.Stop()

	// 等待定时器或上下文取消
	select {
	case <-timer.C:
		// 再次尝试获取令牌
		if rl.Allow(ctx) {
			return nil
		}
		// 包装限流错误，添加详细上下文
		return errs.New(
			errs.WithCode(540),
			errs.WithMsgf("rate limiter '%s' exceeded after waiting", rl.config.Name),
			errs.WithCause(ErrRateLimitExceeded),
		)
	case <-ctx.Done():
		// 包装上下文取消错误，添加限流器上下文
		return errs.New(
			errs.WithCode(499),
			errs.WithMsgf("context canceled while waiting for rate limiter '%s'", rl.config.Name),
			errs.WithCause(ctx.Err()),
		)
	}
}
