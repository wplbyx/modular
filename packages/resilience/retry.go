package resilience

import (
	"context"
	"errors"
	"time"

	retrylib "github.com/avast/retry-go"
	"go.uber.org/zap"
	"holographic/packages/errs"
	"holographic/packages/log"
)

// RetryConfig 重试配置
type RetryConfig struct {
	// Name 重试策略名称
	Name string
	// MaxRetries 最大重试次数
	MaxRetries int
	// InitialInterval 初始重试间隔
	InitialInterval time.Duration
	// MaxInterval 最大重试间隔
	MaxInterval time.Duration
	// Multiplier 间隔增长倍数
	Multiplier float64
	// RetryableErrors 可重试的错误类型
	RetryableErrors []error
}

// 默认重试配置
var DefaultRetryConfig = RetryConfig{
	Name:            "default",
	MaxRetries:      3,
	InitialInterval: 100 * time.Millisecond,
	MaxInterval:     2 * time.Second,
	Multiplier:      2.0,
}

// retryImpl 重试机制实现
type retryImpl struct {
	config RetryConfig
}

// NewRetry 创建一个新的重试机制
func NewRetry(config RetryConfig) Retry {
	// 使用默认配置填充未设置的字段
	if config.MaxRetries < 0 {
		config.MaxRetries = DefaultRetryConfig.MaxRetries
	}
	if config.InitialInterval <= 0 {
		config.InitialInterval = DefaultRetryConfig.InitialInterval
	}
	if config.MaxInterval <= 0 {
		config.MaxInterval = DefaultRetryConfig.MaxInterval
	}
	if config.Multiplier <= 1.0 {
		config.Multiplier = DefaultRetryConfig.Multiplier
	}
	if config.Name == "" {
		config.Name = DefaultRetryConfig.Name
	}

	return &retryImpl{
		config: config,
	}
}

// Name 返回重试策略名称
func (r *retryImpl) Name() string {
	return r.config.Name
}

// Execute 执行函数并在失败时重试
func (r *retryImpl) Execute(ctx context.Context, fn func() error) error {
	// 构建retry-go的选项
	opts := []retrylib.Option{
		retrylib.Attempts(uint(r.config.MaxRetries) + 1), // +1 因为包括初始尝试
		retrylib.Delay(r.config.InitialInterval),
		retrylib.MaxDelay(r.config.MaxInterval),
		retrylib.DelayType(retrylib.BackOffDelay),
		retrylib.Context(ctx),
		retrylib.OnRetry(func(attempt uint, err error) {
			log.Infof("Retry '%s' attempt %d/%d for error: %v",
				r.config.Name, attempt, r.config.MaxRetries, err)
		}),
	}

	// 如果配置了可重试错误列表，添加可重试条件
	if len(r.config.RetryableErrors) > 0 {
		opts = append(opts, retrylib.RetryIf(func(err error) bool {
			return r.isRetryableError(err)
		}))
	}

	// 执行重试逻辑
	err := retrylib.Do(fn, opts...)

	// 包装错误并添加上下文信息
	if err != nil {
		// 检查是否是上下文取消错误
		if ctx.Err() != nil {
			wrappedErr := errs.New(
				errs.WithCode(500),
				errs.WithMsgf("context canceled during retry operation"),
				errs.WithCause(ctx.Err()),
			)
			log.Error("retry context canceled", zap.Error(wrappedErr), zap.String("retry_name", r.config.Name))
			return wrappedErr
		}

		// 包装最终错误
		finalErr := errs.New(
			errs.WithCode(500),
			errs.WithMsgf("all retry attempts failed: %v", err),
			errs.WithCause(err),
		)
		log.Error("retry exhausted",
			zap.Error(finalErr),
			zap.String("retry_name", r.config.Name),
			zap.Int("max_retries", r.config.MaxRetries),
		)
		return finalErr
	}

	return nil
}

// isRetryableError 判断错误是否可重试
func (r *retryImpl) isRetryableError(err error) bool {
	// 检查错误是否在可重试列表中
	for _, retryErr := range r.config.RetryableErrors {
		if errors.Is(err, retryErr) {
			return true
		}
	}

	return false
}
