package resilience

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wplbyx/modular/packages/errs"
)

var (
	circuitOpenMessage  = errs.Define("CIRCUIT_OPEN", errs.Template("service is temporarily unavailable"))
	tooManyCallsMessage = errs.Define("CIRCUIT_TOO_MANY_CALLS", errs.Template("too many requests"))

	// ErrCircuitOpen 熔断器打开错误
	ErrCircuitOpen = errs.ServiceUnavailable(circuitOpenMessage)
	// ErrTooManyCalls 半开状态下调用过多错误
	ErrTooManyCalls = errs.TooManyRequests(tooManyCallsMessage)
)

// 熔断器配置

type CircuitBreakerConfig struct {
	// Name 熔断器名称
	Name string
	// FailureThreshold 失败阈值，超过此值熔断器打开
	FailureThreshold int
	// SuccessThreshold 成功阈值，半开状态下连续成功次数达到此值则关闭熔断器
	SuccessThreshold int
	// Timeout 熔断器打开状态下的超时时间，超时后转为半开状态
	Timeout time.Duration
	// HalfOpenMaxCalls 半开状态下允许的最大并发调用数
	HalfOpenMaxCalls int
}

// 默认熔断器配置

var DefaultCircuitBreakerConfig = CircuitBreakerConfig{
	Name:             "default",
	FailureThreshold: 5,
	SuccessThreshold: 3,
	Timeout:          10 * time.Second,
	HalfOpenMaxCalls: 2,
}

// circuitBreaker 熔断器实现

type circuitBreaker struct {
	config CircuitBreakerConfig

	mutex         sync.RWMutex
	state         CircuitState
	failures      int
	successes     int
	expiry        time.Time
	halfOpenCalls int
	generation    uint64
}

// NewCircuitBreaker 创建一个新的熔断器
func NewCircuitBreaker(config CircuitBreakerConfig) CircuitBreaker {
	// 使用默认配置填充未设置的字段
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = DefaultCircuitBreakerConfig.FailureThreshold
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = DefaultCircuitBreakerConfig.SuccessThreshold
	}
	if config.Timeout <= 0 {
		config.Timeout = DefaultCircuitBreakerConfig.Timeout
	}
	if config.HalfOpenMaxCalls <= 0 {
		config.HalfOpenMaxCalls = DefaultCircuitBreakerConfig.HalfOpenMaxCalls
	}
	if config.Name == "" {
		config.Name = DefaultCircuitBreakerConfig.Name
	}

	return &circuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// Name 返回熔断器名称
func (cb *circuitBreaker) Name() string {
	return cb.config.Name
}

// State 返回当前熔断器状态
func (cb *circuitBreaker) State() CircuitState {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.advance()
	return cb.state
}
func (cb *circuitBreaker) advance() {
	if cb.state == StateOpen && !time.Now().Before(cb.expiry) {
		cb.transition(StateHalfOpen)
	}
}
func (cb *circuitBreaker) transition(state CircuitState) {
	cb.state = state
	cb.generation++
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenCalls = 0
	if state == StateOpen {
		cb.expiry = time.Now().Add(cb.config.Timeout)
	}
}

// Execute 的完成结果仅更新放行时所属的状态代际。
func (cb *circuitBreaker) Execute(ctx context.Context, fn func() error) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("circuit breaker function is nil")
	}
	cb.mutex.Lock()
	cb.advance()
	if cb.state == StateOpen {
		cb.mutex.Unlock()
		return ErrCircuitOpen
	}
	if cb.state == StateHalfOpen && cb.halfOpenCalls >= cb.config.HalfOpenMaxCalls {
		cb.mutex.Unlock()
		return ErrTooManyCalls
	}
	generation := cb.generation
	if cb.state == StateHalfOpen {
		cb.halfOpenCalls++
	}
	cb.mutex.Unlock()
	defer func() {
		p := recover()
		cb.mutex.Lock()
		cb.advance()
		if generation == cb.generation {
			if cb.state == StateHalfOpen {
				cb.halfOpenCalls--
			}
			if err != nil || p != nil {
				if cb.state == StateHalfOpen {
					cb.transition(StateOpen)
				} else {
					cb.failures++
					if cb.failures >= cb.config.FailureThreshold {
						cb.transition(StateOpen)
					}
				}
			} else if cb.state == StateHalfOpen {
				cb.successes++
				if cb.successes >= cb.config.SuccessThreshold {
					cb.transition(StateClosed)
				}
			} else {
				cb.failures = 0
			}
		}
		cb.mutex.Unlock()
		if p != nil {
			panic(p)
		}
	}()
	return fn()
}
