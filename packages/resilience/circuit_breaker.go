package resilience

import (
	"context"
	"sync"
	"time"

	"holographic/packages/errs"
	"holographic/packages/log"
)

var (
	// ErrCircuitOpen 熔断器打开错误
	ErrCircuitOpen = errs.New(
		errs.WithCode(530),
		errs.WithMsgf("circuit breaker is open"),
	)
	// ErrTooManyCalls 半开状态下调用过多错误
	ErrTooManyCalls = errs.New(
		errs.WithCode(531),
		errs.WithMsgf("too many calls in half-open state"),
	)
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
}

// NewCircuitBreaker 创建一个新的熔断器
func NewCircuitBreaker(config CircuitBreakerConfig) CircuitBreaker {
	// 使用默认配置填充未设置的字段
	if config.FailureThreshold == 0 {
		config.FailureThreshold = DefaultCircuitBreakerConfig.FailureThreshold
	}
	if config.SuccessThreshold == 0 {
		config.SuccessThreshold = DefaultCircuitBreakerConfig.SuccessThreshold
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultCircuitBreakerConfig.Timeout
	}
	if config.HalfOpenMaxCalls == 0 {
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
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	return cb.currentState()
}

// currentState 返回当前熔断器状态（内部方法，不获取锁）
func (cb *circuitBreaker) currentState() CircuitState {
	if cb.state == StateOpen && time.Now().After(cb.expiry) {
		return StateHalfOpen
	}
	return cb.state
}

// Execute 执行被保护的函数
func (cb *circuitBreaker) Execute(ctx context.Context, fn func() error) error {
	// 检查熔断器状态
	if !cb.allowRequest() {
		state := cb.State()
		log.Infof("Circuit breaker '%s' is %s, rejecting request", cb.config.Name, state)
		// 返回带有上下文的错误
		return errs.New(
			errs.WithCode(530),
			errs.WithMsgf("circuit breaker '%s' is %s", cb.config.Name, state),
			errs.WithCause(ErrCircuitOpen),
		)
	}

	// 执行函数
	err := fn()

	// 根据执行结果更新熔断器状态
	if err != nil {
		cb.onFailure()
		// 返回原始错误，熔断器上下文已记录
		return err
	}

	cb.onSuccess()
	return nil
}

// allowRequest 判断是否允许请求通过
func (cb *circuitBreaker) allowRequest() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	state := cb.currentState()

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		return false
	case StateHalfOpen:
		// 半开状态下限制并发调用数
		if cb.halfOpenCalls >= cb.config.HalfOpenMaxCalls {
			return false
		}
		cb.halfOpenCalls++
		return true
	default:
		return false
	}
}

// onSuccess 处理成功的请求
func (cb *circuitBreaker) onSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	state := cb.currentState()

	switch state {
	case StateClosed:
		// 关闭状态下重置失败计数
		cb.failures = 0
	case StateHalfOpen:
		// 半开状态下增加成功计数
		cb.successes++
		if cb.halfOpenCalls > 0 {
			cb.halfOpenCalls--
		}
		// 如果连续成功次数达到阈值，关闭熔断器
		if cb.successes >= cb.config.SuccessThreshold {
			log.Infof("Circuit breaker '%s' closed after %d successful calls", cb.config.Name, cb.successes)
			cb.state = StateClosed
			cb.failures = 0
			cb.successes = 0
			cb.halfOpenCalls = 0
		}
	}
}

// onFailure 处理失败的请求
func (cb *circuitBreaker) onFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	state := cb.currentState()

	switch state {
	case StateClosed:
		// 关闭状态下增加失败计数
		cb.failures++
		// 如果失败次数超过阈值，打开熔断器
		if cb.failures >= cb.config.FailureThreshold {
			log.Infof("Circuit breaker '%s' opened after %d failures", cb.config.Name, cb.failures)
			cb.state = StateOpen
			cb.expiry = time.Now().Add(cb.config.Timeout)
		}
	case StateHalfOpen:
		// 半开状态下失败，立即打开熔断器
		log.Infof("Circuit breaker '%s' opened after failure in half-open state", cb.config.Name)
		cb.state = StateOpen
		cb.expiry = time.Now().Add(cb.config.Timeout)
		if cb.halfOpenCalls > 0 {
			cb.halfOpenCalls--
		}
		cb.successes = 0
	}
}
