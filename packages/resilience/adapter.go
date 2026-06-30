package resilience

import "context"

// CircuitBreaker 熔断器接口
type CircuitBreaker interface {
	// Execute 执行被保护的函数
	Execute(ctx context.Context, fn func() error) error
	// Name 返回熔断器名称
	Name() string
	// State 返回当前熔断器状态
	State() CircuitState
}

// CircuitState 熔断器状态

type CircuitState int

const (
	// StateClosed 熔断器关闭状态：允许请求通过
	StateClosed CircuitState = iota
	// StateOpen 熔断器打开状态：拒绝请求
	StateOpen
	// StateHalfOpen 熔断器半开状态：允许有限请求通过以探测服务是否恢复
	StateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// RateLimiter 限流器接口
type RateLimiter interface {
	// Allow 判断是否允许请求通过
	Allow(ctx context.Context) bool
	// Take 阻塞等待直到获取到令牌或超时
	Take(ctx context.Context) error
	// Name 返回限流器名称
	Name() string
}

// Bulkhead 隔板模式接口
type Bulkhead interface {
	// Execute 在隔板内执行函数
	Execute(ctx context.Context, fn func() error) error
	// Name 返回隔板名称
	Name() string
	// Running 返回当前运行中的请求数
	Running() int
}

// Retry 重试机制接口
type Retry interface {
	// Execute 执行函数并在失败时重试
	Execute(ctx context.Context, fn func() error) error
	// Name 返回重试策略名称
	Name() string
}
