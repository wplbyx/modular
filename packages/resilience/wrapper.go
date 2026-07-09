package resilience

import "context"

// Executor 执行器接口，代表一个可以执行的函数

type Executor func(ctx context.Context) error

// Middleware 中间件函数类型

type Middleware func(Executor) Executor

// CircuitBreakerMiddleware 创建一个熔断器中间件
func CircuitBreakerMiddleware(cb CircuitBreaker) Middleware {
	return func(next Executor) Executor {
		return func(ctx context.Context) error {
			return cb.Execute(ctx, func() error {
				return next(ctx)
			})
		}
	}
}

// RateLimiterMiddleware 创建一个限流器中间件
func RateLimiterMiddleware(rl RateLimiter) Middleware {
	return func(next Executor) Executor {
		return func(ctx context.Context) error {
			if err := rl.Take(ctx); err != nil {
				return err
			}
			return next(ctx)
		}
	}
}

// BulkheadMiddleware 创建一个隔板中间件
func BulkheadMiddleware(bh Bulkhead) Middleware {
	return func(next Executor) Executor {
		return func(ctx context.Context) error {
			return bh.Execute(ctx, func() error {
				return next(ctx)
			})
		}
	}
}

// RetryMiddleware 创建一个重试中间件
func RetryMiddleware(r Retry) Middleware {
	return func(next Executor) Executor {
		return func(ctx context.Context) error {
			return r.Execute(ctx, func() error {
				return next(ctx)
			})
		}
	}
}

// Chain 将多个中间件组合成一个
func Chain(middlewares ...Middleware) Middleware {
	return func(next Executor) Executor {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// ExecuteWithResilience 使用组合的韧性模式执行函数
func ExecuteWithResilience(ctx context.Context, fn Executor, middlewares ...Middleware) error {
	if len(middlewares) == 0 {
		return fn(ctx)
	}

	// 创建中间件链
	middleware := Chain(middlewares...)
	// 执行包装后的函数
	return middleware(fn)(ctx)
}

// NewCompositeResilience 创建一个组合了多种韧性模式的执行器
func NewCompositeResilience(
	circuitBreaker CircuitBreaker,
	rateLimiter RateLimiter,
	bulkhead Bulkhead,
	retry Retry,
) Middleware {
	var middlewares []Middleware

	if circuitBreaker != nil {
		middlewares = append(middlewares, CircuitBreakerMiddleware(circuitBreaker))
	}

	if rateLimiter != nil {
		middlewares = append(middlewares, RateLimiterMiddleware(rateLimiter))
	}

	if bulkhead != nil {
		middlewares = append(middlewares, BulkheadMiddleware(bulkhead))
	}

	if retry != nil {
		middlewares = append(middlewares, RetryMiddleware(retry))
	}

	return Chain(middlewares...)
}
